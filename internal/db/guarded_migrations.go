package db

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
)

const guardedMigrationCleanupTimeout = 30 * time.Second

var guardedMigrationUserPattern = regexp.MustCompile(`^[A-Za-z0-9_]+$`)

// MigrateUpGuarded uses an administrative connection to make the migration
// login usable only long enough to establish one physical connection. The
// account is locked and its password is replaced before any migration SQL is
// run. Cleanup is deliberately independent of the caller context so timeout
// and cancellation cannot leave the account open.
func MigrateUpGuarded(ctx context.Context, migrationDSN, adminDSN, resumeDirtyVersion string, operationTimeout, lockTimeout time.Duration) (status MigrationStatus, returnErr error) {
	if ctx == nil {
		ctx = context.Background()
	}
	operationTimeout = normalizedOperationTimeout(operationTimeout)
	if lockTimeout <= 0 {
		lockTimeout = 30 * time.Second
	}
	connectionTimeout := operationTimeout
	if lockTimeout > connectionTimeout {
		connectionTimeout = lockTimeout
	}

	migrationConfig, err := parseAresMySQLDSN(migrationDSN)
	if err != nil {
		return MigrationStatus{}, guardedMigrationResultError(err)
	}
	adminConfig, err := parseAresMySQLDSN(adminDSN)
	if err != nil {
		return MigrationStatus{}, guardedMigrationResultError(err)
	}
	if err := validateGuardedMigrationPrincipals(migrationConfig, adminConfig); err != nil {
		return MigrationStatus{}, guardedMigrationResultError(err)
	}
	username := migrationConfig.User

	adminDatabase, err := openSQLDatabase(ctx, adminDSN, connectionTimeout)
	if err != nil {
		return MigrationStatus{}, guardedMigrationResultError(fmt.Errorf("connect migration administrator: %w", err))
	}
	adminDatabase.SetMaxOpenConns(1)
	adminDatabase.SetMaxIdleConns(1)
	adminConn, err := adminDatabase.Conn(ctx)
	if err != nil {
		_ = adminDatabase.Close()
		return MigrationStatus{}, guardedMigrationResultError(fmt.Errorf("acquire migration administrator connection: %w", err))
	}

	var migrationDatabase *sql.DB
	var migrationConn *sql.Conn
	var adminCurrentUser string
	var canonicalDatabaseName string
	var guardedServerUUID string
	var accountGuardLockName string
	accountGuardLockHeld := false
	adminConnClosed := false
	cleanupArmed := false
	defer func() {
		var closeErrors []error
		if migrationConn != nil {
			if err := migrationConn.Close(); err != nil && !errors.Is(err, sql.ErrConnDone) {
				closeErrors = append(closeErrors, fmt.Errorf("close guarded migration connection: %w", err))
			}
		}
		if migrationDatabase != nil {
			if err := migrationDatabase.Close(); err != nil {
				closeErrors = append(closeErrors, fmt.Errorf("close guarded migration pool: %w", err))
			}
		}

		var cleanupErr error
		if cleanupArmed {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), guardedMigrationCleanupTimeout)
			if !accountGuardLockHeld {
				cleanupErr = errors.New("guarded migration account lock ownership was lost")
			} else if err := assertGuardedMigrationAccountLockOwned(cleanupCtx, adminConn, accountGuardLockName); err != nil {
				cleanupErr = err
			} else {
				cleanupErr = secureGuardedMigrationAccount(cleanupCtx, adminConn, username, 0, true, guardedMigrationCleanupTimeout)
			}
			cleanupCancel()
			if cleanupErr != nil {
				// Closing the original administrator connection also releases its
				// account guard. The fallback must reacquire that same guard before
				// retrying cleanup, so it can never kill a later guarded migrator.
				if err := adminConn.Close(); err != nil && !errors.Is(err, sql.ErrConnDone) {
					closeErrors = append(closeErrors, fmt.Errorf("close failed migration administrator connection: %w", err))
				}
				adminConnClosed = true
				accountGuardLockHeld = false
				fallbackCtx, fallbackCancel := context.WithTimeout(context.Background(), guardedMigrationCleanupTimeout)
				fallbackDatabase, openErr := openSQLDatabase(fallbackCtx, adminDSN, guardedMigrationCleanupTimeout)
				if openErr == nil {
					fallbackDatabase.SetMaxOpenConns(1)
					fallbackDatabase.SetMaxIdleConns(1)
					fallbackConn, connErr := fallbackDatabase.Conn(fallbackCtx)
					if connErr == nil {
						fallbackUser, fallbackDatabaseName, fallbackServerUUID, identityErr := guardedConnectionIdentity(
							fallbackCtx, fallbackConn, guardedMigrationCleanupTimeout)
						fallbackUsername, validFallbackIdentity := guardedCurrentUsername(fallbackUser)
						if identityErr != nil {
							cleanupErr = errors.Join(cleanupErr, fmt.Errorf("inspect fallback migration administrator identity: %w", identityErr))
						} else if !validFallbackIdentity || fallbackUser != adminCurrentUser ||
							strings.EqualFold(fallbackUsername, username) ||
							fallbackDatabaseName != canonicalDatabaseName || fallbackServerUUID != guardedServerUUID {
							cleanupErr = errors.Join(cleanupErr, errors.New("fallback migration administrator reached an unexpected identity or database server"))
						} else if serverErr := validateGuardedMigrationServer(
							fallbackCtx, fallbackConn, guardedMigrationCleanupTimeout); serverErr != nil {
							cleanupErr = errors.Join(cleanupErr, serverErr)
						} else if capabilityErr := validateGuardedMigrationAdministratorCapabilities(
							fallbackCtx, fallbackConn, guardedMigrationCleanupTimeout); capabilityErr != nil {
							cleanupErr = errors.Join(cleanupErr, capabilityErr)
						} else {
							lockErr := tryAcquireGuardedMigrationAccountLock(fallbackCtx, fallbackConn, accountGuardLockName)
							if lockErr != nil {
								cleanupErr = errors.Join(cleanupErr, fmt.Errorf("reacquire guarded migration account lock: %w", lockErr))
							} else {
								retryErr := secureGuardedMigrationAccount(fallbackCtx, fallbackConn, username, 0, true, guardedMigrationCleanupTimeout)
								releaseErr := releaseGuardedMigrationAccountLock(fallbackConn, accountGuardLockName)
								if retryErr == nil && releaseErr == nil {
									cleanupErr = nil
								} else {
									cleanupErr = errors.Join(cleanupErr, retryErr, releaseErr)
								}
							}
						}
						_ = fallbackConn.Close()
					} else {
						cleanupErr = errors.Join(cleanupErr, fmt.Errorf("reacquire migration administrator connection: %w", connErr))
					}
					_ = fallbackDatabase.Close()
				} else {
					cleanupErr = errors.Join(cleanupErr, fmt.Errorf("reconnect migration administrator: %w", openErr))
				}
				fallbackCancel()
			}
		}

		if accountGuardLockHeld {
			if err := releaseGuardedMigrationAccountLock(adminConn, accountGuardLockName); err != nil {
				closeErrors = append(closeErrors, err)
			}
			accountGuardLockHeld = false
		}
		if !adminConnClosed {
			if err := adminConn.Close(); err != nil && !errors.Is(err, sql.ErrConnDone) {
				closeErrors = append(closeErrors, fmt.Errorf("close migration administrator connection: %w", err))
			}
		}
		if err := adminDatabase.Close(); err != nil {
			closeErrors = append(closeErrors, fmt.Errorf("close migration administrator pool: %w", err))
		}
		if cleanupErr != nil || len(closeErrors) > 0 {
			// Cleanup failure is an operational failure even when the migration
			// itself produced a schema-state error. Never preserve ErrSchemaState
			// classification when account containment is uncertain.
			returnErr = guardedMigrationOperationalError("guarded migration account cleanup failed", errors.Join(append(closeErrors, cleanupErr)...))
			return
		}
		if returnErr != nil {
			returnErr = guardedMigrationResultError(returnErr)
		}
	}()

	adminCurrentUser, adminDatabaseName, adminServerUUID, err := guardedConnectionIdentity(ctx, adminConn, operationTimeout)
	if err != nil {
		return MigrationStatus{}, fmt.Errorf("inspect migration administrator identity: %w", err)
	}
	canonicalDatabaseName = adminDatabaseName
	adminUsername, validAdminIdentity := guardedCurrentUsername(adminCurrentUser)
	if !validAdminIdentity {
		return MigrationStatus{}, errors.New("migration administrator CURRENT_USER has an invalid form")
	}
	if adminUsername != adminConfig.User {
		return MigrationStatus{}, errors.New("migration administrator CURRENT_USER does not match the configured DSN user")
	}
	if strings.EqualFold(adminUsername, username) {
		return MigrationStatus{}, errors.New("migration administrator must not be the guarded migration account")
	}
	if err := validateGuardedMigrationServer(ctx, adminConn, operationTimeout); err != nil {
		return MigrationStatus{}, err
	}
	guardedServerUUID = adminServerUUID
	accountGuardLockName = guardedMigrationAccountLockName(username)
	if err := acquireGuardedMigrationAccountLock(ctx, adminConn, accountGuardLockName, lockTimeout); err != nil {
		return MigrationStatus{}, fmt.Errorf("acquire guarded migration account lock: %w", err)
	}
	accountGuardLockHeld = true
	if err := validateGuardedMigrationAdministratorCapabilities(ctx, adminConn, operationTimeout); err != nil {
		return MigrationStatus{}, err
	}
	if err := configureGuardedMigrationLockSession(ctx, adminConn, operationTimeout, lockTimeout); err != nil {
		return MigrationStatus{}, err
	}
	if err := validateGuardedMigrationAccountTopology(ctx, adminConn, username, operationTimeout); err != nil {
		return MigrationStatus{}, err
	}
	locked, existingSessions, err := guardedMigrationAccountState(ctx, adminConn, username, operationTimeout)
	if err != nil {
		return MigrationStatus{}, err
	}
	if locked && existingSessions > 0 {
		return MigrationStatus{}, fmt.Errorf("locked guarded migration account has %d existing session(s); refusing an unsafe takeover", existingSessions)
	}
	if err := validateGuardedMigrationAccountDependencies(ctx, adminConn, username, canonicalDatabaseName, operationTimeout); err != nil {
		return MigrationStatus{}, err
	}
	if err := validateGuardedMigrationDirectPrivileges(ctx, adminConn, username, canonicalDatabaseName, operationTimeout); err != nil {
		return MigrationStatus{}, err
	}
	inboundCtx, inboundCancel := context.WithTimeout(ctx, operationTimeout)
	err = validateNoInboundForeignKeys(inboundCtx, adminConn)
	inboundCancel()
	if err != nil {
		return MigrationStatus{}, err
	}

	// From this point onward every return path must leave the account locked,
	// credential-rotated and without authenticated sessions.
	cleanupArmed = true
	if err := secureGuardedMigrationAccount(ctx, adminConn, username, 0, false, operationTimeout); err != nil {
		return MigrationStatus{}, fmt.Errorf("lock and drain migration account before handoff: %w", err)
	}
	// Recheck the read-only preflight after containment. The named lock
	// serializes Ares jobs, while this second pass also detects an administrator
	// changing account topology or grants without participating in that lock.
	if err := validateGuardedMigrationAdministratorCapabilities(ctx, adminConn, operationTimeout); err != nil {
		return MigrationStatus{}, err
	}
	if err := validateGuardedMigrationAccountTopology(ctx, adminConn, username, operationTimeout); err != nil {
		return MigrationStatus{}, err
	}
	if err := validateGuardedMigrationAccountDependencies(ctx, adminConn, username, canonicalDatabaseName, operationTimeout); err != nil {
		return MigrationStatus{}, err
	}
	if err := validateGuardedMigrationDirectPrivileges(ctx, adminConn, username, canonicalDatabaseName, operationTimeout); err != nil {
		return MigrationStatus{}, err
	}
	inboundCtx, inboundCancel = context.WithTimeout(ctx, operationTimeout)
	err = validateNoInboundForeignKeys(inboundCtx, adminConn)
	inboundCancel()
	if err != nil {
		return MigrationStatus{}, err
	}
	ephemeralPassword, err := guardedOneTimePassword()
	if err != nil {
		return MigrationStatus{}, fmt.Errorf("generate migration handoff credential: %w", err)
	}
	if err := replaceGuardedMigrationPassword(ctx, adminConn, username, ephemeralPassword, true, operationTimeout); err != nil {
		return MigrationStatus{}, fmt.Errorf("prepare migration handoff credential: %w", err)
	}

	migrationDatabase, migrationConn, err = openGuardedMigrationConnection(ctx, migrationConfig, ephemeralPassword, connectionTimeout)
	if err != nil {
		return MigrationStatus{}, fmt.Errorf("establish guarded migration connection: %w", err)
	}
	var migrationConnectionID uint64
	var migrationCurrentUser, migrationDatabaseName, migrationServerUUID string
	identityCtx, identityCancel := context.WithTimeout(ctx, operationTimeout)
	err = migrationConn.QueryRowContext(identityCtx,
		"SELECT CONNECTION_ID(), CURRENT_USER(), DATABASE(), @@GLOBAL.server_uuid").
		Scan(&migrationConnectionID, &migrationCurrentUser, &migrationDatabaseName, &migrationServerUUID)
	identityCancel()
	if err != nil {
		return MigrationStatus{}, fmt.Errorf("verify guarded migration connection identity: %w", err)
	}
	if migrationCurrentUser != username+"@%" {
		return MigrationStatus{}, fmt.Errorf("guarded migration CURRENT_USER mismatch: got %q", migrationCurrentUser)
	}
	if migrationDatabaseName != canonicalDatabaseName || migrationServerUUID != adminServerUUID {
		return MigrationStatus{}, errors.New("guarded migration connection reached an unexpected database server")
	}

	if err := secureGuardedMigrationAccount(ctx, adminConn, username, migrationConnectionID, true, operationTimeout); err != nil {
		return MigrationStatus{}, fmt.Errorf("contain migration account after handoff: %w", err)
	}
	roleCtx, roleCancel := context.WithTimeout(ctx, operationTimeout)
	if _, err := migrationConn.ExecContext(roleCtx, "SET ROLE NONE"); err != nil {
		roleCancel()
		return MigrationStatus{}, fmt.Errorf("disable guarded migration roles: %w", err)
	}
	var verifiedConnectionID uint64
	var verifiedCurrentUser, currentRole string
	err = migrationConn.QueryRowContext(roleCtx, "SELECT CONNECTION_ID(), CURRENT_USER(), CURRENT_ROLE()").
		Scan(&verifiedConnectionID, &verifiedCurrentUser, &currentRole)
	roleCancel()
	if err != nil {
		return MigrationStatus{}, fmt.Errorf("reverify guarded migration connection: %w", err)
	}
	if verifiedConnectionID != migrationConnectionID || verifiedCurrentUser != username+"@%" || currentRole != "NONE" {
		return MigrationStatus{}, errors.New("guarded migration connection identity or role changed after containment")
	}

	migrationCtx, cancelMigration := context.WithCancel(ctx)
	watchdogCtx, stopWatchdog := context.WithCancel(ctx)
	watchdogResult := make(chan error, 1)
	go func() {
		watchdogErr := monitorGuardedMigrationAccountLock(watchdogCtx, adminConn, accountGuardLockName)
		if watchdogErr != nil {
			cancelMigration()
		}
		watchdogResult <- watchdogErr
	}()
	status, returnErr = migrateUpOnConnection(migrationCtx, migrationConn, resumeDirtyVersion, operationTimeout, lockTimeout)
	stopWatchdog()
	watchdogErr := <-watchdogResult
	if watchdogErr != nil {
		cancelMigration()
		return status, fmt.Errorf("guarded migration account lock watchdog failed: %w", watchdogErr)
	}
	if returnErr == nil {
		if err := assertGuardedMigrationAccountLockOwned(ctx, adminConn, accountGuardLockName); err != nil {
			cancelMigration()
			return status, err
		}
		if err := validateGuardedMigrationAdministratorCapabilities(ctx, adminConn, operationTimeout); err != nil {
			cancelMigration()
			return status, err
		}
		if err := validateGuardedMigrationAccountTopology(ctx, adminConn, username, operationTimeout); err != nil {
			cancelMigration()
			return status, err
		}
		if err := validateGuardedMigrationAccountDependencies(ctx, adminConn, username, canonicalDatabaseName, operationTimeout); err != nil {
			cancelMigration()
			return status, err
		}
		if err := validateGuardedMigrationDirectPrivileges(ctx, adminConn, username, canonicalDatabaseName, operationTimeout); err != nil {
			cancelMigration()
			return status, err
		}
		inboundCtx, inboundCancel = context.WithTimeout(ctx, operationTimeout)
		err = validateNoInboundForeignKeys(inboundCtx, adminConn)
		inboundCancel()
		if err != nil {
			cancelMigration()
			return status, err
		}
	}
	cancelMigration()
	return status, returnErr
}

func validGuardedMigrationUsername(username string) bool {
	return username != "" && len(username) <= 32 && guardedMigrationUserPattern.MatchString(username)
}

func validateGuardedMigrationPrincipals(migrationConfig, adminConfig *mysql.Config) error {
	if migrationConfig == nil || adminConfig == nil {
		return errors.New("migration and administrator DSNs are required")
	}
	if !validGuardedMigrationUsername(migrationConfig.User) {
		return errors.New("migration DSN user must match ASCII [A-Za-z0-9_]+ and be at most 32 bytes")
	}
	if strings.EqualFold(migrationConfig.User, "root") {
		return errors.New("root cannot be used as the guarded migration account")
	}
	if strings.EqualFold(migrationConfig.User, adminConfig.User) {
		return errors.New("migration and administrator DSNs must use different users")
	}
	return nil
}

func guardedCurrentUsername(currentUser string) (string, bool) {
	separator := strings.LastIndexByte(currentUser, '@')
	if separator <= 0 || separator == len(currentUser)-1 {
		return "", false
	}
	return currentUser[:separator], true
}

func guardedMigrationAccountLockName(username string) string {
	digest := sha256.Sum256([]byte(username))
	return "ares_migration_account_" + hex.EncodeToString(digest[:16])
}

func guardedMigrationDatabaseGrantPattern(databaseName string, partialRevokes bool) string {
	if partialRevokes {
		return databaseName
	}
	// With partial_revokes disabled, MySQL interprets database-level grant
	// names as LIKE patterns even when they are quoted as identifiers. Escape
	// every pattern metacharacter so a grant for e.g. "ares_prod" cannot also
	// authorize "aresXprod". mysql.db retains this escaped representation.
	replacer := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return replacer.Replace(databaseName)
}

func validateGuardedMigrationServer(ctx context.Context, adminConn *sql.Conn, timeout time.Duration) error {
	queryCtx, cancel := context.WithTimeout(ctx, normalizedOperationTimeout(timeout))
	defer cancel()
	var version, comment string
	if err := adminConn.QueryRowContext(queryCtx, "SELECT VERSION(), @@version_comment").Scan(&version, &comment); err != nil {
		return fmt.Errorf("inspect guarded migration database version: %w", err)
	}
	if problem := supportedDatabaseVersionProblem(version, comment); problem != "" {
		return &SchemaStateError{Problems: []string{problem}}
	}
	return nil
}

func validateGuardedMigrationAdministratorCapabilities(ctx context.Context, adminConn *sql.Conn, timeout time.Duration) error {
	queryCtx, cancel := context.WithTimeout(ctx, normalizedOperationTimeout(timeout))
	defer cancel()
	var processPrivilege, superPrivilege, createUserPrivilege string
	var selectPrivilege, triggerPrivilege, eventPrivilege, showViewPrivilege string
	var connectionAdminPrivilege int
	var partialPrivilegeRestrictions int
	var mandatoryRoles string
	if err := adminConn.QueryRowContext(queryCtx, `SELECT
		u.Process_priv,
		u.Super_priv,
		u.Create_user_priv,
		u.Select_priv,
		u.Trigger_priv,
		u.Event_priv,
		u.Show_view_priv,
		EXISTS(
			SELECT 1 FROM mysql.global_grants g
			WHERE BINARY CONCAT(g.USER, '@', g.HOST) = BINARY CURRENT_USER()
				AND UPPER(g.PRIV) = 'CONNECTION_ADMIN'
		),
		COALESCE(JSON_LENGTH(JSON_EXTRACT(u.User_attributes, '$.Restrictions')), 0),
		COALESCE(@@GLOBAL.mandatory_roles, '')
		FROM mysql.user u
		WHERE BINARY CONCAT(u.User, '@', u.Host) = BINARY CURRENT_USER()`).
		Scan(&processPrivilege, &superPrivilege, &createUserPrivilege,
			&selectPrivilege, &triggerPrivilege, &eventPrivilege, &showViewPrivilege,
			&connectionAdminPrivilege, &partialPrivilegeRestrictions, &mandatoryRoles); err != nil {
		return fmt.Errorf("inspect migration administrator capabilities: %w", err)
	}
	if strings.TrimSpace(mandatoryRoles) != "" {
		return errors.New("MySQL mandatory_roles must be empty for guarded migrations")
	}
	if processPrivilege != "Y" {
		return errors.New("migration administrator requires a direct global PROCESS privilege to prove complete session visibility")
	}
	if superPrivilege != "Y" && connectionAdminPrivilege != 1 {
		return errors.New("migration administrator requires a direct global CONNECTION_ADMIN or SUPER privilege to terminate guarded sessions")
	}
	if createUserPrivilege != "Y" {
		return errors.New("migration administrator requires a direct global CREATE USER privilege to contain the migration account")
	}
	if selectPrivilege != "Y" || triggerPrivilege != "Y" || eventPrivilege != "Y" || showViewPrivilege != "Y" {
		return errors.New("migration administrator requires direct global SELECT, TRIGGER, EVENT and SHOW VIEW privileges for authoritative schema metadata")
	}
	if partialPrivilegeRestrictions != 0 {
		return errors.New("migration administrator must not have partial privilege restrictions that can hide schema metadata")
	}
	return nil
}

func acquireGuardedMigrationAccountLock(ctx context.Context, adminConn *sql.Conn, lockName string, timeout time.Duration) error {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	seconds := int64((timeout + time.Second - 1) / time.Second)
	if seconds < 1 {
		seconds = 1
	}
	lockCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var acquired sql.NullInt64
	if err := adminConn.QueryRowContext(lockCtx, "SELECT GET_LOCK(?, ?)", lockName, seconds).Scan(&acquired); err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(lockCtx.Err(), context.DeadlineExceeded) {
			return fmt.Errorf("timed out waiting for guarded migration account lock: %w", context.DeadlineExceeded)
		}
		return fmt.Errorf("acquire guarded migration account lock: %w", err)
	}
	if !acquired.Valid || acquired.Int64 != 1 {
		return errors.New("timed out waiting for guarded migration account lock")
	}
	return nil
}

func tryAcquireGuardedMigrationAccountLock(ctx context.Context, adminConn *sql.Conn, lockName string) error {
	tryCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var acquired sql.NullInt64
	if err := adminConn.QueryRowContext(tryCtx, "SELECT GET_LOCK(?, 0)", lockName).Scan(&acquired); err != nil {
		return fmt.Errorf("try guarded migration account lock: %w", err)
	}
	if !acquired.Valid || acquired.Int64 != 1 {
		return errors.New("guarded migration account lock is owned by a newer operation")
	}
	return nil
}

func releaseGuardedMigrationAccountLock(adminConn *sql.Conn, lockName string) error {
	releaseCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var released sql.NullInt64
	if err := adminConn.QueryRowContext(releaseCtx, "SELECT RELEASE_LOCK(?)", lockName).Scan(&released); err != nil {
		return fmt.Errorf("release guarded migration account lock: %w", err)
	}
	if !released.Valid || released.Int64 != 1 {
		return errors.New("guarded migration account lock was not owned at release")
	}
	return nil
}

func assertGuardedMigrationAccountLockOwned(ctx context.Context, adminConn *sql.Conn, lockName string) error {
	var owner sql.NullInt64
	var connectionID int64
	if err := adminConn.QueryRowContext(ctx, "SELECT IS_USED_LOCK(?), CONNECTION_ID()", lockName).
		Scan(&owner, &connectionID); err != nil {
		return fmt.Errorf("verify guarded migration account lock ownership: %w", err)
	}
	if !owner.Valid || owner.Int64 != connectionID {
		return errors.New("guarded migration account lock is no longer owned by the administrator connection")
	}
	return nil
}

func configureGuardedMigrationLockSession(ctx context.Context, adminConn *sql.Conn, operationTimeout, lockTimeout time.Duration) error {
	waitTimeout := normalizedOperationTimeout(operationTimeout) + lockTimeout + guardedMigrationCleanupTimeout + time.Minute
	if waitTimeout < time.Hour {
		waitTimeout = time.Hour
	}
	if waitTimeout > 365*24*time.Hour {
		waitTimeout = 365 * 24 * time.Hour
	}
	seconds := int64((waitTimeout + time.Second - 1) / time.Second)
	configurationCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if _, err := adminConn.ExecContext(configurationCtx, fmt.Sprintf("SET SESSION wait_timeout = %d", seconds)); err != nil {
		return fmt.Errorf("configure guarded migration account lock session: %w", err)
	}
	return nil
}

func monitorGuardedMigrationAccountLock(ctx context.Context, adminConn *sql.Conn, lockName string) error {
	const interval = 2 * time.Second
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}
		// Do not derive the probe from the stop context: canceling an in-flight
		// go-sql-driver query can close the physical lock-holding connection.
		// A bounded background probe lets normal shutdown preserve the holder
		// until final containment has completed.
		probeCtx, cancel := context.WithTimeout(context.Background(), interval)
		err := assertGuardedMigrationAccountLockOwned(probeCtx, adminConn, lockName)
		cancel()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
	}
}

func guardedOneTimePassword() (string, error) {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return "", err
	}
	// The fixed prefix satisfies common password validation policies while the
	// random suffix provides 256 bits of entropy and contains no SQL quoting
	// characters.
	return "Aa1!" + hex.EncodeToString(secret), nil
}

func guardedAccountSQL(username string) string {
	// username has already passed validGuardedMigrationUsername, so neither
	// component can alter the fixed account literal.
	return "'" + username + "'@'%'"
}

func guardedConnectionIdentity(ctx context.Context, conn *sql.Conn, timeout time.Duration) (currentUser, databaseName, serverUUID string, err error) {
	queryCtx, cancel := context.WithTimeout(ctx, normalizedOperationTimeout(timeout))
	defer cancel()
	err = conn.QueryRowContext(queryCtx, "SELECT CURRENT_USER(), DATABASE(), @@GLOBAL.server_uuid").
		Scan(&currentUser, &databaseName, &serverUUID)
	return
}

func validateGuardedMigrationAccountTopology(ctx context.Context, adminConn *sql.Conn, username string, timeout time.Duration) error {
	queryCtx, cancel := context.WithTimeout(ctx, normalizedOperationTimeout(timeout))
	defer cancel()
	var exactAccounts, shadowAccounts int
	if err := adminConn.QueryRowContext(queryCtx, `SELECT
		(SELECT COUNT(*) FROM mysql.user WHERE BINARY User = BINARY ? AND Host = '%'),
		(SELECT COUNT(*) FROM mysql.user WHERE BINARY User = BINARY ? AND Host <> '%')`,
		username, username).Scan(&exactAccounts, &shadowAccounts); err != nil {
		return fmt.Errorf("inspect guarded migration account topology: %w", err)
	}
	if exactAccounts != 1 {
		return fmt.Errorf("guarded migration account user@%% count is %d, want 1", exactAccounts)
	}
	if shadowAccounts != 0 {
		return fmt.Errorf("guarded migration username has %d non-%% host account(s)", shadowAccounts)
	}
	return nil
}

func guardedMigrationAccountState(ctx context.Context, adminConn *sql.Conn, username string, timeout time.Duration) (locked bool, sessions int, err error) {
	queryCtx, cancel := context.WithTimeout(ctx, normalizedOperationTimeout(timeout))
	defer cancel()
	var lockState string
	err = adminConn.QueryRowContext(queryCtx, `SELECT u.account_locked,
		(SELECT COUNT(*) FROM information_schema.PROCESSLIST p WHERE BINARY p.USER = BINARY ?)
		FROM mysql.user u WHERE BINARY u.User = BINARY ? AND u.Host = '%'`, username, username).
		Scan(&lockState, &sessions)
	if err != nil {
		return false, 0, fmt.Errorf("inspect guarded migration account state: %w", err)
	}
	if lockState != "Y" && lockState != "N" {
		return false, 0, fmt.Errorf("guarded migration account has unknown lock state %q", lockState)
	}
	return lockState == "Y", sessions, nil
}

func validateGuardedMigrationAccountDependencies(ctx context.Context, adminConn *sql.Conn, username, databaseName string, timeout time.Duration) error {
	queryCtx, cancel := context.WithTimeout(ctx, normalizedOperationTimeout(timeout))
	defer cancel()
	var inboundRoleEdges, outboundRoleEdges, proxyGrants, proxiedGrants, definerObjects int
	var schemaExecutableObjects int
	definer := username + "@%"
	if err := adminConn.QueryRowContext(queryCtx, `SELECT
		(SELECT COUNT(*) FROM mysql.role_edges WHERE BINARY TO_USER = BINARY ? AND TO_HOST = '%'),
		(SELECT COUNT(*) FROM mysql.role_edges WHERE BINARY FROM_USER = BINARY ? AND FROM_HOST = '%'),
		(SELECT COUNT(*) FROM mysql.proxies_priv WHERE BINARY User = BINARY ? AND Host = '%'),
		(SELECT COUNT(*) FROM mysql.proxies_priv WHERE BINARY Proxied_user = BINARY ? AND Proxied_host = '%'),
		(SELECT COUNT(*) FROM information_schema.TRIGGERS WHERE BINARY DEFINER = BINARY ?) +
		(SELECT COUNT(*) FROM information_schema.EVENTS WHERE BINARY DEFINER = BINARY ?) +
		(SELECT COUNT(*) FROM information_schema.ROUTINES WHERE BINARY DEFINER = BINARY ?) +
		(SELECT COUNT(*) FROM information_schema.VIEWS WHERE BINARY DEFINER = BINARY ?),
		(SELECT COUNT(*) FROM information_schema.TRIGGERS WHERE BINARY TRIGGER_SCHEMA = BINARY ?) +
		(SELECT COUNT(*) FROM information_schema.EVENTS WHERE BINARY EVENT_SCHEMA = BINARY ?) +
		(SELECT COUNT(*) FROM information_schema.ROUTINES WHERE BINARY ROUTINE_SCHEMA = BINARY ?) +
		(SELECT COUNT(*) FROM information_schema.VIEWS WHERE BINARY TABLE_SCHEMA = BINARY ?)`,
		username, username, username, username,
		definer, definer, definer, definer,
		databaseName, databaseName, databaseName, databaseName).
		Scan(&inboundRoleEdges, &outboundRoleEdges, &proxyGrants, &proxiedGrants,
			&definerObjects, &schemaExecutableObjects); err != nil {
		return fmt.Errorf("inspect guarded migration account dependencies: %w", err)
	}
	if inboundRoleEdges != 0 || outboundRoleEdges != 0 || proxyGrants != 0 || proxiedGrants != 0 {
		return fmt.Errorf("guarded migration account has inbound_roles=%d outbound_roles=%d proxy_grants=%d proxied_grants=%d, want none",
			inboundRoleEdges, outboundRoleEdges, proxyGrants, proxiedGrants)
	}
	if definerObjects != 0 {
		return fmt.Errorf("guarded migration account is DEFINER for %d database object(s)", definerObjects)
	}
	if schemaExecutableObjects != 0 {
		return fmt.Errorf("guarded migration schema contains %d trigger, event, routine or view object(s), want none", schemaExecutableObjects)
	}
	return nil
}

func validateGuardedMigrationDirectPrivileges(ctx context.Context, adminConn *sql.Conn, username, databaseName string, timeout time.Duration) error {
	queryCtx, cancel := context.WithTimeout(ctx, normalizedOperationTimeout(timeout))
	defer cancel()
	grantee := "'" + username + "'@'%'"
	var globalPrivilegeRows, unexpectedGlobalPrivileges int
	if err := adminConn.QueryRowContext(queryCtx, `SELECT COUNT(*),
		COALESCE(SUM(NOT (PRIVILEGE_TYPE = 'USAGE' AND IS_GRANTABLE = 'NO')), 0)
		FROM information_schema.USER_PRIVILEGES WHERE BINARY GRANTEE = BINARY ?`, grantee).
		Scan(&globalPrivilegeRows, &unexpectedGlobalPrivileges); err != nil {
		return fmt.Errorf("inspect guarded migration global privileges: %w", err)
	}
	if globalPrivilegeRows != 1 || unexpectedGlobalPrivileges != 0 {
		return fmt.Errorf("guarded migration account global privilege set is not exactly non-grantable USAGE")
	}

	var partialRevokes int
	if err := adminConn.QueryRowContext(queryCtx, "SELECT @@GLOBAL.partial_revokes").Scan(&partialRevokes); err != nil {
		return fmt.Errorf("inspect MySQL partial_revokes mode: %w", err)
	}
	if partialRevokes != 0 && partialRevokes != 1 {
		return fmt.Errorf("MySQL partial_revokes mode is %d, want 0 or 1", partialRevokes)
	}
	expectedGrantPattern := guardedMigrationDatabaseGrantPattern(databaseName, partialRevokes == 1)
	var schemaGrantRows, matchingSchemaGrantRows int
	if err := adminConn.QueryRowContext(queryCtx, `SELECT COUNT(*),
		COALESCE(SUM(BINARY Db = BINARY ?), 0)
		FROM mysql.db WHERE BINARY User = BINARY ? AND Host = '%'`, expectedGrantPattern, username).
		Scan(&schemaGrantRows, &matchingSchemaGrantRows); err != nil {
		return fmt.Errorf("inspect guarded migration schema grant count: %w", err)
	}
	if schemaGrantRows != 1 || matchingSchemaGrantRows != 1 {
		return fmt.Errorf("guarded migration account schema grant is not the unique literal pattern for the selected database (rows=%d matches=%d)",
			schemaGrantRows, matchingSchemaGrantRows)
	}
	var selectPrivilege, insertPrivilege, updatePrivilege, deletePrivilege string
	var createPrivilege, dropPrivilege, grantPrivilege, referencesPrivilege string
	var indexPrivilege, alterPrivilege, createTemporaryTablePrivilege, lockTablesPrivilege string
	var createViewPrivilege, showViewPrivilege, createRoutinePrivilege, alterRoutinePrivilege string
	var executePrivilege, eventPrivilege, triggerPrivilege string
	if err := adminConn.QueryRowContext(queryCtx, `SELECT
		Select_priv, Insert_priv, Update_priv, Delete_priv, Create_priv, Drop_priv,
		Grant_priv, References_priv, Index_priv, Alter_priv, Create_tmp_table_priv,
		Lock_tables_priv, Create_view_priv, Show_view_priv, Create_routine_priv,
		Alter_routine_priv, Execute_priv, Event_priv, Trigger_priv
		FROM mysql.db WHERE BINARY User = BINARY ? AND Host = '%' AND BINARY Db = BINARY ?`,
		username, expectedGrantPattern).Scan(
		&selectPrivilege, &insertPrivilege, &updatePrivilege, &deletePrivilege,
		&createPrivilege, &dropPrivilege, &grantPrivilege, &referencesPrivilege,
		&indexPrivilege, &alterPrivilege, &createTemporaryTablePrivilege, &lockTablesPrivilege,
		&createViewPrivilege, &showViewPrivilege, &createRoutinePrivilege, &alterRoutinePrivilege,
		&executePrivilege, &eventPrivilege, &triggerPrivilege); err != nil {
		return fmt.Errorf("inspect guarded migration schema privileges: %w", err)
	}
	wantYes := []string{
		selectPrivilege, insertPrivilege, updatePrivilege, deletePrivilege,
		createPrivilege, referencesPrivilege, indexPrivilege, alterPrivilege,
	}
	wantNo := []string{
		dropPrivilege, grantPrivilege, createTemporaryTablePrivilege, lockTablesPrivilege,
		createViewPrivilege, showViewPrivilege, createRoutinePrivilege, alterRoutinePrivilege,
		executePrivilege, eventPrivilege, triggerPrivilege,
	}
	for _, privilege := range wantYes {
		if privilege != "Y" {
			return errors.New("guarded migration account is missing a required direct schema privilege")
		}
	}
	for _, privilege := range wantNo {
		if privilege != "N" {
			return errors.New("guarded migration account has an unexpected direct schema privilege")
		}
	}

	var dynamicPrivileges, tablePrivileges, columnPrivileges, routinePrivileges int
	if err := adminConn.QueryRowContext(queryCtx, `SELECT
		(SELECT COUNT(*) FROM mysql.global_grants WHERE BINARY USER = BINARY ? AND HOST = '%'),
		(SELECT COUNT(*) FROM mysql.tables_priv WHERE BINARY User = BINARY ? AND Host = '%'),
		(SELECT COUNT(*) FROM mysql.columns_priv WHERE BINARY User = BINARY ? AND Host = '%'),
		(SELECT COUNT(*) FROM mysql.procs_priv WHERE BINARY User = BINARY ? AND Host = '%')`,
		username, username, username, username).
		Scan(&dynamicPrivileges, &tablePrivileges, &columnPrivileges, &routinePrivileges); err != nil {
		return fmt.Errorf("inspect guarded migration scoped privileges: %w", err)
	}
	if dynamicPrivileges != 0 || tablePrivileges != 0 || columnPrivileges != 0 || routinePrivileges != 0 {
		return fmt.Errorf("guarded migration account has dynamic=%d table=%d column=%d routine=%d extra privilege row(s), want none",
			dynamicPrivileges, tablePrivileges, columnPrivileges, routinePrivileges)
	}
	return nil
}

func replaceGuardedMigrationPassword(ctx context.Context, adminConn *sql.Conn, username, password string, unlock bool, timeout time.Duration) error {
	account := guardedAccountSQL(username)
	operationCtx, cancel := context.WithTimeout(ctx, normalizedOperationTimeout(timeout))
	defer cancel()
	if _, err := adminConn.ExecContext(operationCtx,
		"ALTER USER "+account+" IDENTIFIED BY '"+password+"' ACCOUNT LOCK"); err != nil {
		return fmt.Errorf("replace guarded migration credential: %w", err)
	}
	if _, err := adminConn.ExecContext(operationCtx, "ALTER USER "+account+" DISCARD OLD PASSWORD"); err != nil {
		return fmt.Errorf("discard guarded migration secondary credential: %w", err)
	}
	if unlock {
		if _, err := adminConn.ExecContext(operationCtx, "ALTER USER "+account+" ACCOUNT UNLOCK"); err != nil {
			return fmt.Errorf("unlock guarded migration account for handoff: %w", err)
		}
	}
	return nil
}

func secureGuardedMigrationAccount(ctx context.Context, adminConn *sql.Conn, username string, retainedConnectionID uint64, rotatePassword bool, timeout time.Duration) error {
	operationCtx, cancel := context.WithTimeout(ctx, normalizedOperationTimeout(timeout))
	defer cancel()
	account := guardedAccountSQL(username)
	var containmentErrors []error
	if _, err := adminConn.ExecContext(operationCtx, "ALTER USER "+account+" ACCOUNT LOCK"); err != nil {
		containmentErrors = append(containmentErrors, fmt.Errorf("lock guarded migration account: %w", err))
	}
	if rotatePassword {
		password, err := guardedOneTimePassword()
		if err != nil {
			containmentErrors = append(containmentErrors, fmt.Errorf("generate guarded migration tombstone credential: %w", err))
		} else if err := replaceGuardedMigrationPassword(operationCtx, adminConn, username, password, false, timeout); err != nil {
			containmentErrors = append(containmentErrors, err)
		}
	}
	if err := killGuardedMigrationSessions(operationCtx, adminConn, username, retainedConnectionID); err != nil {
		containmentErrors = append(containmentErrors, err)
	}
	if err := verifyGuardedMigrationContainment(operationCtx, adminConn, username, retainedConnectionID); err != nil {
		containmentErrors = append(containmentErrors, err)
	}
	return errors.Join(containmentErrors...)
}

func killGuardedMigrationSessions(ctx context.Context, adminConn *sql.Conn, username string, retainedConnectionID uint64) error {
	backoff := 25 * time.Millisecond
	for {
		ids, err := guardedMigrationSessionIDs(ctx, adminConn, username)
		if err != nil {
			return err
		}
		var killIDs []uint64
		for _, id := range ids {
			if retainedConnectionID == 0 || id != retainedConnectionID {
				killIDs = append(killIDs, id)
			}
		}
		if len(killIDs) == 0 {
			return nil
		}
		for _, id := range killIDs {
			if _, err := adminConn.ExecContext(ctx, fmt.Sprintf("KILL CONNECTION %d", id)); err != nil {
				var mysqlError *mysql.MySQLError
				if !errors.As(err, &mysqlError) || mysqlError.Number != 1094 {
					return fmt.Errorf("kill guarded migration session: %w", err)
				}
			}
		}
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("timed out draining guarded migration sessions: %w", ctx.Err())
		case <-timer.C:
		}
		if backoff < 200*time.Millisecond {
			backoff *= 2
			if backoff > 200*time.Millisecond {
				backoff = 200 * time.Millisecond
			}
		}
	}
}

func guardedMigrationSessionIDs(ctx context.Context, adminConn *sql.Conn, username string) ([]uint64, error) {
	rows, err := adminConn.QueryContext(ctx,
		"SELECT ID FROM information_schema.PROCESSLIST WHERE BINARY USER = BINARY ? ORDER BY ID", username)
	if err != nil {
		return nil, fmt.Errorf("list guarded migration sessions: %w", err)
	}
	defer rows.Close()
	var ids []uint64
	for rows.Next() {
		var id uint64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("read guarded migration session: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list guarded migration sessions: %w", err)
	}
	return ids, nil
}

func verifyGuardedMigrationContainment(ctx context.Context, adminConn *sql.Conn, username string, retainedConnectionID uint64) error {
	var locked string
	if err := adminConn.QueryRowContext(ctx,
		"SELECT account_locked FROM mysql.user WHERE BINARY User = BINARY ? AND Host = '%'", username).Scan(&locked); err != nil {
		return fmt.Errorf("verify guarded migration account lock: %w", err)
	}
	if locked != "Y" {
		return errors.New("guarded migration account is not locked")
	}
	ids, err := guardedMigrationSessionIDs(ctx, adminConn, username)
	if err != nil {
		return err
	}
	expected := []uint64(nil)
	if retainedConnectionID != 0 {
		expected = []uint64{retainedConnectionID}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	if len(ids) != len(expected) {
		return fmt.Errorf("guarded migration session count is %d, want %d", len(ids), len(expected))
	}
	for index := range ids {
		if ids[index] != expected[index] {
			return errors.New("guarded migration retained an unexpected session")
		}
	}
	return nil
}

func openGuardedMigrationConnection(ctx context.Context, configuration *mysql.Config, password string, timeout time.Duration) (*sql.DB, *sql.Conn, error) {
	parsed := configuration.Clone()
	parsed.Passwd = password
	parsed.ParseTime = true
	driverTimeout := normalizedOperationTimeout(timeout) + 30*time.Second
	if parsed.Timeout == 0 {
		parsed.Timeout = 5 * time.Second
	}
	if parsed.ReadTimeout == 0 || parsed.ReadTimeout < driverTimeout {
		parsed.ReadTimeout = driverTimeout
	}
	if parsed.WriteTimeout == 0 || parsed.WriteTimeout < driverTimeout {
		parsed.WriteTimeout = driverTimeout
	}
	database, err := sql.Open("mysql", parsed.FormatDSN())
	if err != nil {
		return nil, nil, fmt.Errorf("open guarded migration connection: %w", err)
	}
	database.SetMaxOpenConns(1)
	database.SetMaxIdleConns(0)
	conn, err := database.Conn(ctx)
	if err != nil {
		_ = database.Close()
		return nil, nil, fmt.Errorf("connect guarded migration account: %w", err)
	}
	return database, conn, nil
}

func guardedMigrationResultError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("guarded database migration failed: %w", sanitizedMigrationError{cause: err})
}

func guardedMigrationOperationalError(prefix string, err error) error {
	message := strings.TrimSpace(SafeMigrationErrorText(err))
	if message == "" {
		return errors.New(prefix)
	}
	// Deliberately do not unwrap err: containment uncertainty must retain the
	// operational-error classification even if the migration error was a
	// SchemaStateError.
	return errors.New(prefix + ": " + message)
}
