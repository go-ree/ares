package auth

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-sql-driver/mysql"
)

type SQLStore struct {
	database *sql.DB
}

const enabledLoginCapableAdminPredicate = `BINARY u.role = BINARY ? AND u.enabled = 1 AND (
	(? = 1 AND BINARY u.auth_source = BINARY 'bootstrap' AND u.password_hash IS NOT NULL AND u.password_hash <> '')
		OR (? <> '' AND BINARY u.auth_source = BINARY 'oidc' AND EXISTS (
			SELECT 1 FROM auth_identities i WHERE i.user_id = u.user_id AND BINARY i.issuer = BINARY ?
		))
)`

const (
	maxActiveSessionsPerUser = 20
	expiredSessionRetention  = 24 * time.Hour
	sessionPruneBatch        = 1000
)

func NewSQLStore(database *sql.DB) (*SQLStore, error) {
	if database == nil {
		return nil, errors.New("auth database is required")
	}
	return &SQLStore{database: database}, nil
}

func (s *SQLStore) BootstrapAvailable(ctx context.Context) (bool, error) {
	var completed sql.NullTime
	if err := s.database.QueryRowContext(ctx,
		"SELECT completed_at FROM auth_bootstrap_state WHERE id = 1").Scan(&completed); err != nil {
		return false, fmt.Errorf("read bootstrap state: %w", err)
	}
	return !completed.Valid, nil
}

// HasEnabledAdmin only counts administrators that can authenticate through a
// login method enabled by the current process. OIDC administrators must also
// retain an identity for the exact configured issuer. Merely retaining an
// enabled admin row is insufficient: disabling or replacing its sole identity
// source would otherwise let the service start in an unrecoverable lockout.
func (s *SQLStore) HasEnabledAdmin(ctx context.Context, localLoginEnabled bool, oidcIssuer string) (bool, error) {
	var exists bool
	query := "SELECT EXISTS(SELECT 1 FROM auth_users u WHERE " + enabledLoginCapableAdminPredicate + ")"
	if err := s.database.QueryRowContext(ctx, query,
		RoleAdmin, localLoginEnabled, oidcIssuer, oidcIssuer).Scan(&exists); err != nil {
		return false, fmt.Errorf("check enabled administrator: %w", err)
	}
	return exists, nil
}

func (s *SQLStore) CreateBootstrapAdmin(ctx context.Context, input BootstrapUser, audit AuditEvent, now time.Time) (User, error) {
	transaction, err := s.database.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return User{}, fmt.Errorf("begin bootstrap transaction: %w", err)
	}
	defer transaction.Rollback()

	var completed sql.NullTime
	if err := transaction.QueryRowContext(ctx,
		"SELECT completed_at FROM auth_bootstrap_state WHERE id = 1 FOR UPDATE").Scan(&completed); err != nil {
		return User{}, fmt.Errorf("lock bootstrap state: %w", err)
	}
	if completed.Valid {
		return User{}, ErrBootstrapUnavailable
	}
	// OIDC users may arrive before the operator completes bootstrap. They must
	// not be able to consume or deny the one-time administrator ceremony; the
	// locked singleton remains the sole authority for that transition.
	result, err := transaction.ExecContext(ctx, `INSERT INTO auth_users
		(username, display_name, email, password_hash, role, auth_source, enabled, last_login_at, created_at, updated_at)
		VALUES (?, ?, NULL, ?, ?, 'bootstrap', 1, ?, ?, ?)`,
		input.Username, input.DisplayName, input.PasswordHash, RoleAdmin, now, now, now)
	if err != nil {
		return User{}, fmt.Errorf("create bootstrap administrator: %w", err)
	}
	userID, err := result.LastInsertId()
	if err != nil {
		return User{}, fmt.Errorf("read bootstrap administrator id: %w", err)
	}
	updated, err := transaction.ExecContext(ctx, `UPDATE auth_bootstrap_state
		SET completed_at = ?, completed_by = ? WHERE id = 1 AND completed_at IS NULL`, now, userID)
	if err != nil {
		return User{}, fmt.Errorf("complete bootstrap state: %w", err)
	}
	if count, err := updated.RowsAffected(); err != nil || count != 1 {
		return User{}, ErrBootstrapUnavailable
	}
	audit.ActorUserID = &userID
	audit.ActorUsername = input.Username
	audit.ActorDisplayName = input.DisplayName
	audit.AuthSource = "bootstrap"
	audit.CreatedAt = now
	if _, err := transaction.ExecContext(ctx, `INSERT INTO audit_events
		(actor_user_id, actor_username, actor_display_name, auth_source, action,
		 resource_type, resource_id, result, http_status, request_id, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		userID, audit.ActorUsername, audit.ActorDisplayName, audit.AuthSource,
		audit.Action, audit.ResourceType, audit.ResourceID, audit.Result,
		audit.HTTPStatus, audit.RequestID, audit.CreatedAt); err != nil {
		return User{}, fmt.Errorf("append bootstrap audit event: %w", err)
	}
	if err := transaction.Commit(); err != nil {
		return User{}, fmt.Errorf("commit bootstrap administrator: %w", err)
	}
	login := now
	return User{
		ID: userID, Username: input.Username, DisplayName: input.DisplayName,
		PasswordHash: input.PasswordHash, Role: RoleAdmin, AuthSource: "bootstrap",
		Enabled: true, LastLoginAt: &login, CreatedAt: now, UpdatedAt: now,
	}, nil
}

func (s *SQLStore) FindLocalUser(ctx context.Context, username string) (User, error) {
	row := s.database.QueryRowContext(ctx, userSelect+`
		WHERE auth_source = 'bootstrap' AND username = ?`, username)
	user, err := scanUser(row)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrUserNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("find local user: %w", err)
	}
	return user, nil
}

func (s *SQLStore) UpsertOIDCUser(ctx context.Context, input OIDCUser, now time.Time, autoProvision bool) (User, error) {
	for attempt := 0; attempt < 2; attempt++ {
		user, found, err := s.upsertOIDCUserAttempt(ctx, input, now, autoProvision)
		if err == nil {
			return user, nil
		}
		if !isDuplicateKey(err) || attempt == 1 {
			return User{}, err
		}
		if found {
			return user, nil
		}
	}
	return User{}, errors.New("could not provision OIDC user")
}

func (s *SQLStore) upsertOIDCUserAttempt(ctx context.Context, input OIDCUser, now time.Time, autoProvision bool) (User, bool, error) {
	transaction, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return User{}, false, fmt.Errorf("begin OIDC identity transaction: %w", err)
	}
	defer transaction.Rollback()

	row := transaction.QueryRowContext(ctx, userSelect+`
		JOIN auth_identities i ON i.user_id = u.user_id
		WHERE i.identity_hash = ? FOR UPDATE`, input.IdentityHash)
	user, scanErr := scanUser(row)
	if scanErr == nil {
		var issuer, subject string
		if err := transaction.QueryRowContext(ctx,
			"SELECT issuer, subject FROM auth_identities WHERE identity_hash = ?", input.IdentityHash).
			Scan(&issuer, &subject); err != nil {
			return User{}, false, fmt.Errorf("read OIDC identity: %w", err)
		}
		if issuer != input.Issuer || subject != input.Subject {
			return User{}, false, errors.New("OIDC identity digest collision")
		}
		if !user.Enabled {
			return user, true, nil
		}
		displayName := oidcDisplayName(input)
		if _, err := transaction.ExecContext(ctx, `UPDATE auth_users
			SET display_name = ?, email = ?, last_login_at = ?, updated_at = ? WHERE user_id = ?`,
			displayName, nullableString(input.Email), now, now, user.ID); err != nil {
			return User{}, false, fmt.Errorf("update OIDC user profile: %w", err)
		}
		if err := transaction.Commit(); err != nil {
			return User{}, false, fmt.Errorf("commit OIDC user profile: %w", err)
		}
		user.DisplayName, user.Email, user.LastLoginAt, user.UpdatedAt = displayName, input.Email, &now, now
		return user, true, nil
	}
	if !errors.Is(scanErr, sql.ErrNoRows) {
		return User{}, false, fmt.Errorf("find OIDC identity: %w", scanErr)
	}
	if !autoProvision {
		return User{}, false, ErrUserNotFound
	}

	username := "oidc-" + hex.EncodeToString(input.IdentityHash[:12])
	displayName := oidcDisplayName(input)
	result, err := transaction.ExecContext(ctx, `INSERT INTO auth_users
		(username, display_name, email, password_hash, role, auth_source, enabled, last_login_at, created_at, updated_at)
		VALUES (?, ?, ?, NULL, ?, 'oidc', 1, ?, ?, ?)`,
		username, displayName, nullableString(input.Email), RoleViewer, now, now, now)
	if err != nil {
		return User{}, false, fmt.Errorf("create OIDC user: %w", err)
	}
	userID, err := result.LastInsertId()
	if err != nil {
		return User{}, false, fmt.Errorf("read OIDC user id: %w", err)
	}
	if _, err := transaction.ExecContext(ctx, `INSERT INTO auth_identities
		(user_id, issuer, subject, identity_hash, created_at) VALUES (?, ?, ?, ?, ?)`,
		userID, input.Issuer, input.Subject, input.IdentityHash, now); err != nil {
		return User{}, false, fmt.Errorf("create OIDC identity: %w", err)
	}
	if err := transaction.Commit(); err != nil {
		return User{}, false, fmt.Errorf("commit OIDC identity: %w", err)
	}
	login := now
	return User{
		ID: userID, Username: username, DisplayName: displayName, Email: input.Email,
		Role: RoleViewer, AuthSource: "oidc", Enabled: true, LastLoginAt: &login,
		CreatedAt: now, UpdatedAt: now,
	}, false, nil
}

func oidcDisplayName(input OIDCUser) string {
	for _, candidate := range []string{input.DisplayName, input.PreferredUsername, input.Email, "OIDC user"} {
		if value := strings.TrimSpace(candidate); value != "" {
			return truncateUTF8Bytes(value, 255)
		}
	}
	return "OIDC user"
}

func truncateUTF8Bytes(value string, maximum int) string {
	if len(value) <= maximum {
		return value
	}
	value = value[:maximum]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}

func nullableString(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func isDuplicateKey(err error) bool {
	var mysqlError *mysql.MySQLError
	return errors.As(err, &mysqlError) && mysqlError.Number == 1062
}

func (s *SQLStore) CreateSession(ctx context.Context, hash []byte, userID int64, expiresAt, now time.Time) error {
	// Revocation also brings expires_at forward, so this global retention query
	// is a bounded range scan on idx_auth_sessions_expires. Keep it outside the
	// singleton transaction: cleanup must not lengthen the critical section used
	// for user-state and per-user active-session invariants.
	if err := s.pruneExpiredSessions(ctx, now); err != nil {
		return err
	}
	transaction, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin auth session transaction: %w", err)
	}
	defer transaction.Rollback()

	// Serialize session capacity with the permanent singleton. User mutations
	// take the same lock first, so disabling an account cannot race a successful
	// login into creating a fresh usable session.
	var bootstrapID int
	if err := transaction.QueryRowContext(ctx,
		"SELECT id FROM auth_bootstrap_state WHERE id = 1 FOR UPDATE").Scan(&bootstrapID); err != nil {
		return fmt.Errorf("lock auth session capacity: %w", err)
	}
	var enabled bool
	if err := transaction.QueryRowContext(ctx,
		"SELECT enabled FROM auth_users WHERE user_id = ? FOR UPDATE", userID).Scan(&enabled); errors.Is(err, sql.ErrNoRows) {
		return ErrUserNotFound
	} else if err != nil {
		return fmt.Errorf("lock auth session user: %w", err)
	}
	if !enabled {
		return ErrUnauthenticated
	}
	if err := insertBoundedSession(ctx, transaction, hash, userID, expiresAt, now); err != nil {
		return err
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit auth session: %w", err)
	}
	return nil
}

// CreateLocalSession atomically rechecks the password hash observed during
// Argon2 verification, records the login and rotates the browser session. A
// concurrent password change therefore cannot be followed by a stale login
// creating a fresh session with the old password.
func (s *SQLStore) CreateLocalSession(
	ctx context.Context,
	userID int64,
	expectedPasswordHash string,
	sessionHash []byte,
	previousSessionHash []byte,
	expiresAt time.Time,
	now time.Time,
) (User, error) {
	if err := s.pruneExpiredSessions(ctx, now); err != nil {
		return User{}, err
	}
	transaction, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return User{}, fmt.Errorf("begin local login transaction: %w", err)
	}
	defer transaction.Rollback()

	// Use the same singleton -> user lock order as account mutations and
	// password changes so all credential/session state transitions serialize.
	var bootstrapID int
	if err := transaction.QueryRowContext(ctx,
		"SELECT id FROM auth_bootstrap_state WHERE id = 1 FOR UPDATE").Scan(&bootstrapID); err != nil {
		return User{}, fmt.Errorf("lock local login invariant: %w", err)
	}
	user, err := scanUser(transaction.QueryRowContext(ctx, userSelect+`
		WHERE u.user_id = ? FOR UPDATE`, userID))
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrInvalidCredentials
	}
	if err != nil {
		return User{}, fmt.Errorf("lock local login user: %w", err)
	}
	if !user.Enabled || user.AuthSource != "bootstrap" || user.PasswordHash == "" ||
		len(user.PasswordHash) != len(expectedPasswordHash) ||
		subtle.ConstantTimeCompare([]byte(user.PasswordHash), []byte(expectedPasswordHash)) != 1 {
		return User{}, ErrInvalidCredentials
	}
	if _, err := transaction.ExecContext(ctx,
		"UPDATE auth_users SET last_login_at = ?, updated_at = ? WHERE user_id = ?", now, now, userID); err != nil {
		return User{}, fmt.Errorf("record local login: %w", err)
	}
	if len(previousSessionHash) > 0 {
		if _, err := transaction.ExecContext(ctx, `UPDATE auth_sessions
			SET revoked_at = ?, expires_at = LEAST(expires_at, ?)
			WHERE session_hash = ? AND revoked_at IS NULL`, now, now, previousSessionHash); err != nil {
			return User{}, fmt.Errorf("revoke previous local login session: %w", err)
		}
	}
	if err := insertBoundedSession(ctx, transaction, sessionHash, userID, expiresAt, now); err != nil {
		return User{}, err
	}
	if err := transaction.Commit(); err != nil {
		return User{}, fmt.Errorf("commit local login: %w", err)
	}
	login := now
	user.LastLoginAt = &login
	user.UpdatedAt = now
	return user, nil
}

func (s *SQLStore) pruneExpiredSessions(ctx context.Context, now time.Time) error {
	if _, err := s.database.ExecContext(ctx, `DELETE FROM auth_sessions
		WHERE expires_at <= ? ORDER BY expires_at ASC LIMIT ?`,
		now.Add(-expiredSessionRetention), sessionPruneBatch); err != nil {
		return fmt.Errorf("prune expired auth sessions: %w", err)
	}
	return nil
}

func insertBoundedSession(
	ctx context.Context,
	transaction *sql.Tx,
	hash []byte,
	userID int64,
	expiresAt time.Time,
	now time.Time,
) error {
	var active int
	if err := transaction.QueryRowContext(ctx, `SELECT COUNT(*) FROM auth_sessions
		WHERE user_id = ? AND revoked_at IS NULL AND expires_at > ?`, userID, now).Scan(&active); err != nil {
		return fmt.Errorf("count active auth sessions: %w", err)
	}
	if active >= maxActiveSessionsPerUser {
		revokeCount := active - maxActiveSessionsPerUser + 1
		if _, err := transaction.ExecContext(ctx, `UPDATE auth_sessions
			SET revoked_at = ?, expires_at = LEAST(expires_at, ?)
			WHERE user_id = ? AND revoked_at IS NULL AND expires_at > ?
			ORDER BY last_seen_at ASC, created_at ASC LIMIT ?`,
			now, now, userID, now, revokeCount); err != nil {
			return fmt.Errorf("enforce active auth session capacity: %w", err)
		}
	}
	if _, err := transaction.ExecContext(ctx, `INSERT INTO auth_sessions
		(session_hash, user_id, expires_at, revoked_at, last_seen_at, created_at)
		VALUES (?, ?, ?, NULL, ?, ?)`, hash, userID, expiresAt, now, now); err != nil {
		return fmt.Errorf("create auth session: %w", err)
	}
	return nil
}

func (s *SQLStore) FindSession(ctx context.Context, hash []byte) (Session, error) {
	row := s.database.QueryRowContext(ctx, `SELECT
		s.session_hash, s.expires_at, s.revoked_at, s.last_seen_at, s.created_at,
		u.user_id, u.username, u.display_name, u.email, u.password_hash,
		u.role, u.auth_source, u.enabled, u.last_login_at, u.created_at, u.updated_at
		FROM auth_sessions s JOIN auth_users u ON u.user_id = s.user_id
		WHERE s.session_hash = ?`, hash)
	var session Session
	var email, password sql.NullString
	var revoked, lastLogin sql.NullTime
	var role string
	if err := row.Scan(
		&session.Hash, &session.ExpiresAt, &revoked, &session.LastSeenAt, &session.CreatedAt,
		&session.User.ID, &session.User.Username, &session.User.DisplayName, &email, &password,
		&role, &session.User.AuthSource, &session.User.Enabled, &lastLogin,
		&session.User.CreatedAt, &session.User.UpdatedAt,
	); errors.Is(err, sql.ErrNoRows) {
		return Session{}, ErrSessionNotFound
	} else if err != nil {
		return Session{}, fmt.Errorf("find auth session: %w", err)
	}
	session.User.Email, session.User.PasswordHash = email.String, password.String
	session.User.Role = Role(role)
	if revoked.Valid {
		session.RevokedAt = &revoked.Time
	}
	if lastLogin.Valid {
		session.User.LastLoginAt = &lastLogin.Time
	}
	return session, nil
}

func (s *SQLStore) TouchSession(ctx context.Context, hash []byte, now time.Time) error {
	result, err := s.database.ExecContext(ctx,
		"UPDATE auth_sessions SET last_seen_at = ? WHERE session_hash = ? AND revoked_at IS NULL", now, hash)
	if err != nil {
		return fmt.Errorf("touch auth session: %w", err)
	}
	if count, _ := result.RowsAffected(); count != 1 {
		return ErrSessionNotFound
	}
	return nil
}

func (s *SQLStore) RevokeSession(ctx context.Context, hash []byte, now time.Time) error {
	_, err := s.database.ExecContext(ctx, `UPDATE auth_sessions
		SET revoked_at = ?, expires_at = LEAST(expires_at, ?)
		WHERE session_hash = ? AND revoked_at IS NULL`, now, now, hash)
	return err
}

func (s *SQLStore) ChangeLocalPassword(
	ctx context.Context,
	userID int64,
	expectedPasswordHash string,
	newPasswordHash string,
	now time.Time,
) error {
	transaction, err := s.database.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return fmt.Errorf("begin password change transaction: %w", err)
	}
	defer transaction.Rollback()

	// Keep the same lock order as role/enable mutations before taking the user
	// row, preventing deadlocks while also making disable-vs-rotation atomic.
	var bootstrapID int
	if err := transaction.QueryRowContext(ctx,
		"SELECT id FROM auth_bootstrap_state WHERE id = 1 FOR UPDATE").Scan(&bootstrapID); err != nil {
		return fmt.Errorf("lock password change invariant: %w", err)
	}
	var storedHash sql.NullString
	var authSource string
	var enabled bool
	if err := transaction.QueryRowContext(ctx, `SELECT password_hash, auth_source, enabled
		FROM auth_users WHERE user_id = ? FOR UPDATE`, userID).
		Scan(&storedHash, &authSource, &enabled); errors.Is(err, sql.ErrNoRows) {
		return ErrUnauthenticated
	} else if err != nil {
		return fmt.Errorf("lock password change user: %w", err)
	}
	if !enabled || authSource != "bootstrap" || !storedHash.Valid {
		return ErrPasswordChangeUnsupported
	}
	if len(storedHash.String) != len(expectedPasswordHash) ||
		subtle.ConstantTimeCompare([]byte(storedHash.String), []byte(expectedPasswordHash)) != 1 {
		return ErrInvalidCredentials
	}
	if _, err := transaction.ExecContext(ctx,
		"UPDATE auth_users SET password_hash = ?, updated_at = ? WHERE user_id = ?",
		newPasswordHash, now, userID); err != nil {
		return fmt.Errorf("update local password: %w", err)
	}
	if _, err := transaction.ExecContext(ctx, `UPDATE auth_sessions
		SET revoked_at = ?, expires_at = LEAST(expires_at, ?)
		WHERE user_id = ? AND revoked_at IS NULL`, now, now, userID); err != nil {
		return fmt.Errorf("revoke sessions after password change: %w", err)
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit password change: %w", err)
	}
	return nil
}

func (s *SQLStore) CreateOIDCFlow(ctx context.Context, flow OIDCFlow) error {
	transaction, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin OIDC flow transaction: %w", err)
	}
	defer transaction.Rollback()
	// Serialize cleanup/capacity checks with a permanent singleton so anonymous
	// OIDC starts cannot race past the bounded active-flow limit.
	var bootstrapID int
	if err := transaction.QueryRowContext(ctx,
		"SELECT id FROM auth_bootstrap_state WHERE id = 1 FOR UPDATE").Scan(&bootstrapID); err != nil {
		return fmt.Errorf("lock OIDC flow capacity: %w", err)
	}
	if _, err := transaction.ExecContext(ctx, `DELETE FROM auth_oidc_flows
		WHERE consumed_at IS NOT NULL OR expires_at <= ?`, flow.CreatedAt); err != nil {
		return fmt.Errorf("prune OIDC flows: %w", err)
	}
	var active int
	if err := transaction.QueryRowContext(ctx, `SELECT COUNT(*) FROM auth_oidc_flows
		WHERE consumed_at IS NULL AND expires_at > ?`, flow.CreatedAt).Scan(&active); err != nil {
		return fmt.Errorf("count active OIDC flows: %w", err)
	}
	if active >= 4096 {
		return ErrOIDCFlowCapacity
	}
	if _, err := transaction.ExecContext(ctx, `INSERT INTO auth_oidc_flows
		(state_hash, nonce_hash, binding_hash, verifier_ciphertext, return_path, expires_at, consumed_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?, NULL, ?)`,
		flow.StateHash, flow.NonceHash, flow.BindingHash, flow.VerifierCiphertext,
		flow.ReturnPath, flow.ExpiresAt, flow.CreatedAt); err != nil {
		return fmt.Errorf("create OIDC flow: %w", err)
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit OIDC flow: %w", err)
	}
	return nil
}

func (s *SQLStore) ConsumeOIDCFlow(ctx context.Context, stateHash, bindingHash []byte, now time.Time) (OIDCFlow, error) {
	transaction, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return OIDCFlow{}, fmt.Errorf("begin OIDC flow transaction: %w", err)
	}
	defer transaction.Rollback()
	var flow OIDCFlow
	var consumed sql.NullTime
	if err := transaction.QueryRowContext(ctx, `SELECT state_hash, nonce_hash, binding_hash,
		verifier_ciphertext, return_path, expires_at, consumed_at, created_at
		FROM auth_oidc_flows WHERE state_hash = ? FOR UPDATE`, stateHash).Scan(
		&flow.StateHash, &flow.NonceHash, &flow.BindingHash, &flow.VerifierCiphertext,
		&flow.ReturnPath, &flow.ExpiresAt, &consumed, &flow.CreatedAt,
	); errors.Is(err, sql.ErrNoRows) {
		return OIDCFlow{}, ErrInvalidOIDCFlow
	} else if err != nil {
		return OIDCFlow{}, fmt.Errorf("lock OIDC flow: %w", err)
	}
	if consumed.Valid || !now.Before(flow.ExpiresAt) || len(flow.BindingHash) != len(bindingHash) ||
		subtle.ConstantTimeCompare(flow.BindingHash, bindingHash) != 1 {
		return OIDCFlow{}, ErrInvalidOIDCFlow
	}
	result, err := transaction.ExecContext(ctx, `UPDATE auth_oidc_flows SET consumed_at = ?
		WHERE state_hash = ? AND consumed_at IS NULL`, now, stateHash)
	if err != nil {
		return OIDCFlow{}, fmt.Errorf("consume OIDC flow: %w", err)
	}
	if count, _ := result.RowsAffected(); count != 1 {
		return OIDCFlow{}, ErrInvalidOIDCFlow
	}
	if err := transaction.Commit(); err != nil {
		return OIDCFlow{}, fmt.Errorf("commit OIDC flow: %w", err)
	}
	flow.ConsumedAt = &now
	return flow, nil
}

func (s *SQLStore) AppendAudit(ctx context.Context, event AuditEvent) error {
	_, err := s.database.ExecContext(ctx, `INSERT INTO audit_events
		(actor_user_id, actor_username, actor_display_name, auth_source, action,
		 resource_type, resource_id, result, http_status, request_id, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		nullableInt64(event.ActorUserID), event.ActorUsername, event.ActorDisplayName,
		event.AuthSource, event.Action, event.ResourceType, event.ResourceID,
		event.Result, event.HTTPStatus, event.RequestID, event.CreatedAt)
	if err != nil {
		return fmt.Errorf("append audit event: %w", err)
	}
	return nil
}

func nullableInt64(value *int64) any {
	if value == nil {
		return nil
	}
	return *value
}

func (s *SQLStore) LatestAuditID(ctx context.Context) (int64, error) {
	var latest int64
	if err := s.database.QueryRowContext(ctx,
		"SELECT COALESCE(MAX(audit_id), 0) FROM audit_events").Scan(&latest); err != nil {
		return 0, fmt.Errorf("read latest audit event id: %w", err)
	}
	return latest, nil
}

func (s *SQLStore) ListAudit(ctx context.Context, afterID, throughID int64, limit int) ([]AuditEvent, error) {
	if limit <= 0 || limit > 201 {
		limit = 100
	}
	rows, err := s.database.QueryContext(ctx, `SELECT audit_id, actor_user_id, actor_username,
		actor_display_name, auth_source, action, resource_type, resource_id, result,
		http_status, request_id, created_at FROM audit_events
		WHERE audit_id > ? AND audit_id <= ? ORDER BY audit_id ASC LIMIT ?`, afterID, throughID, limit)
	if err != nil {
		return nil, fmt.Errorf("list audit events: %w", err)
	}
	defer rows.Close()
	events := make([]AuditEvent, 0)
	for rows.Next() {
		var event AuditEvent
		var actorID sql.NullInt64
		if err := rows.Scan(&event.ID, &actorID, &event.ActorUsername, &event.ActorDisplayName,
			&event.AuthSource, &event.Action, &event.ResourceType, &event.ResourceID,
			&event.Result, &event.HTTPStatus, &event.RequestID, &event.CreatedAt); err != nil {
			return nil, err
		}
		if actorID.Valid {
			event.ActorUserID = &actorID.Int64
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

func (s *SQLStore) ListUsers(ctx context.Context, offset, limit int) ([]User, error) {
	if offset < 0 {
		offset = 0
	}
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	rows, err := s.database.QueryContext(ctx, userSelect+" ORDER BY u.user_id ASC LIMIT ? OFFSET ?", limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list auth users: %w", err)
	}
	defer rows.Close()
	users := make([]User, 0)
	for rows.Next() {
		user, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		users = append(users, user)
	}
	return users, rows.Err()
}

func (s *SQLStore) UpdateUser(
	ctx context.Context,
	userID int64,
	patch UserPatch,
	now time.Time,
	localLoginEnabled bool,
	oidcIssuer string,
) (User, error) {
	transaction, err := s.database.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return User{}, err
	}
	defer transaction.Rollback()
	// The singleton row serializes all role/enable changes so two concurrent
	// requests cannot both observe another administrator and remove the last one.
	var ignored sql.NullTime
	if err := transaction.QueryRowContext(ctx,
		"SELECT completed_at FROM auth_bootstrap_state WHERE id = 1 FOR UPDATE").Scan(&ignored); err != nil {
		return User{}, fmt.Errorf("lock administrator invariant: %w", err)
	}
	user, err := scanUser(transaction.QueryRowContext(ctx, userSelect+" WHERE u.user_id = ? FOR UPDATE", userID))
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrUserNotFound
	}
	if err != nil {
		return User{}, err
	}
	nextRole, nextEnabled := user.Role, user.Enabled
	if patch.Role != nil {
		nextRole = *patch.Role
	}
	if patch.Enabled != nil {
		nextEnabled = *patch.Enabled
	}
	if user.Role == RoleAdmin && user.Enabled && (nextRole != RoleAdmin || !nextEnabled) {
		var remainingAdministrators int
		query := "SELECT COUNT(*) FROM auth_users u WHERE u.user_id <> ? AND " +
			enabledLoginCapableAdminPredicate
		if err := transaction.QueryRowContext(ctx, query, userID,
			RoleAdmin, localLoginEnabled, oidcIssuer, oidcIssuer).Scan(&remainingAdministrators); err != nil {
			return User{}, err
		}
		if remainingAdministrators == 0 {
			return User{}, ErrLastAdmin
		}
	}
	if _, err := transaction.ExecContext(ctx,
		"UPDATE auth_users SET role = ?, enabled = ?, updated_at = ? WHERE user_id = ?",
		nextRole, nextEnabled, now, userID); err != nil {
		return User{}, err
	}
	if patch.Role != nil || (patch.Enabled != nil && !nextEnabled) {
		if _, err := transaction.ExecContext(ctx, `UPDATE auth_sessions
			SET revoked_at = ?, expires_at = LEAST(expires_at, ?)
			WHERE user_id = ? AND revoked_at IS NULL`, now, now, userID); err != nil {
			return User{}, fmt.Errorf("revoke changed user sessions: %w", err)
		}
	}
	if err := transaction.Commit(); err != nil {
		return User{}, err
	}
	user.Role, user.Enabled, user.UpdatedAt = nextRole, nextEnabled, now
	return user, nil
}

const userSelect = `SELECT u.user_id, u.username, u.display_name, u.email,
	u.password_hash, u.role, u.auth_source, u.enabled, u.last_login_at,
	u.created_at, u.updated_at FROM auth_users u `

type scanner interface {
	Scan(...any) error
}

func scanUser(row scanner) (User, error) {
	var user User
	var email, password sql.NullString
	var lastLogin sql.NullTime
	var role string
	if err := row.Scan(
		&user.ID, &user.Username, &user.DisplayName, &email, &password,
		&role, &user.AuthSource, &user.Enabled, &lastLogin,
		&user.CreatedAt, &user.UpdatedAt,
	); err != nil {
		return User{}, err
	}
	user.Email, user.PasswordHash, user.Role = email.String, password.String, Role(role)
	if lastLogin.Valid {
		user.LastLoginAt = &lastLogin.Time
	}
	return user, nil
}
