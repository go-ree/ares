package db

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/go-ree/ares/internal/config"
	"github.com/go-sql-driver/mysql"
)

const (
	schemaMigrationsTable            = "schema_migrations"
	defaultMigrationOperationTimeout = 2 * time.Minute
	// ApplicationSchemaEpoch is the exact catalog and manifest epoch this
	// binary can prove safe. Unknown future epochs remain fail-closed even if a
	// database row claims a wider compatibility range.
	ApplicationSchemaEpoch uint64 = 5
)

var ErrSchemaState = errors.New("database schema requires migration or operator attention")

var mysql84VersionPattern = regexp.MustCompile(`^8\.4(?:\.|$)`)

// SchemaStateError represents an expected, fail-closed database state. CLI
// callers map this error to exit code 3; connection and SQL failures remain
// operational errors and use exit code 5.
type SchemaStateError struct {
	Problems []string
}

func (e *SchemaStateError) Error() string {
	if e == nil || len(e.Problems) == 0 {
		return ErrSchemaState.Error()
	}
	return ErrSchemaState.Error() + ": " + strings.Join(e.Problems, "; ")
}

func (e *SchemaStateError) Unwrap() error { return ErrSchemaState }

type MigrationInfo struct {
	Epoch         uint64
	Version       string
	Description   string
	Checksum      string
	CompatibleMin uint64
	CompatibleMax uint64
}

type DirtyMigration struct {
	Epoch    uint64
	Version  string
	Checksum string
	Error    string
}

// MigrationStatus retains raw diagnostic fields for programmatic callers.
// Its String representation is the credential-safe CLI/logging boundary.
type MigrationStatus struct {
	Initialized   bool
	NeedsAdoption bool
	CurrentEpoch  uint64
	CompatibleMin uint64
	CompatibleMax uint64
	Applied       []MigrationInfo
	Pending       []MigrationInfo
	Dirty         *DirtyMigration
	ManifestDiffs []string
	Problems      []string
}

func (s MigrationStatus) Compatible() bool {
	return s.Initialized && !s.NeedsAdoption && s.Dirty == nil &&
		len(s.Pending) == 0 && len(s.ManifestDiffs) == 0 && len(s.Problems) == 0 &&
		s.CurrentEpoch == ApplicationSchemaEpoch &&
		ApplicationSchemaEpoch >= s.CompatibleMin && ApplicationSchemaEpoch <= s.CompatibleMax
}

func (s MigrationStatus) String() string {
	parts := make([]string, 0, 8)
	if !s.Initialized {
		parts = append(parts, "数据库尚未初始化")
	} else if s.NeedsAdoption {
		parts = append(parts, "检测到旧版 migration ledger，需执行 migrate up 收养")
	} else {
		parts = append(parts, fmt.Sprintf("当前 schema epoch=%d，应用 epoch=%d", s.CurrentEpoch, ApplicationSchemaEpoch))
	}
	if s.CompatibleMin != 0 || s.CompatibleMax != 0 {
		parts = append(parts, fmt.Sprintf("数据库兼容区间=[%d,%d]", s.CompatibleMin, s.CompatibleMax))
	}
	if len(s.Applied) > 0 {
		current := s.Applied[len(s.Applied)-1]
		parts = append(parts, fmt.Sprintf("当前迁移=%s checksum=%s", current.Version, current.Checksum))
	}
	if len(s.Pending) > 0 {
		versions := make([]string, 0, len(s.Pending))
		for _, migration := range s.Pending {
			versions = append(versions,
				fmt.Sprintf("%s(checksum=%s)", migration.Version, migration.Checksum))
		}
		parts = append(parts, "待执行="+strings.Join(versions, ","))
	}
	if s.Dirty != nil {
		dirty := fmt.Sprintf("dirty=%s checksum=%s", s.Dirty.Version, s.Dirty.Checksum)
		if sanitized := sanitizeMigrationErrorText(s.Dirty.Error); sanitized != "" {
			dirty += " last_error=" + sanitized
		}
		parts = append(parts, dirty)
	}
	if len(s.ManifestDiffs) > 0 {
		parts = append(parts, "schema 漂移="+strings.Join(s.ManifestDiffs, " | "))
	}
	if len(s.Problems) > 0 {
		parts = append(parts, "问题="+strings.Join(s.Problems, " | "))
	}
	if s.Compatible() {
		parts = append(parts, "状态=兼容")
	} else {
		parts = append(parts, "状态=不兼容")
	}
	// Problems and manifest diffs can contain database-controlled names and
	// defaults. Sanitize the complete rendered status, not only last_error, so
	// stdout is held to the same credential boundary as stderr and logs.
	return sanitizeMigrationErrorText(strings.Join(parts, "；"))
}

type sqlExecutor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
	BeginTx(context.Context, *sql.TxOptions) (*sql.Tx, error)
}

type migrationSession struct {
	ctx              context.Context
	executor         sqlExecutor
	operationTimeout time.Duration
}

func (s *migrationSession) operationContext() (context.Context, context.CancelFunc) {
	parent := s.ctx
	if parent == nil {
		parent = context.Background()
	}
	return context.WithTimeout(parent, normalizedOperationTimeout(s.operationTimeout))
}

func normalizedOperationTimeout(timeout time.Duration) time.Duration {
	if timeout <= 0 {
		return defaultMigrationOperationTimeout
	}
	return timeout
}

type schemaMigration struct {
	epoch            uint64
	version          string
	description      string
	compatibleMin    uint64
	compatibleMax    uint64
	payload          string
	implementationID string
	preflight        func(*migrationSession) error
	up               func(*migrationSession) error
	verify           func(*migrationSession) error
}

func (m schemaMigration) checksum() string {
	canonical := fmt.Sprintf("epoch=%d\nversion=%s\ndescription=%s\ncompatible=%d:%d\npayload=%s\nimplementation=%s\n",
		m.epoch, m.version, m.description, m.compatibleMin, m.compatibleMax, m.payload, m.implementationID)
	digest := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(digest[:])
}

func (m schemaMigration) info() MigrationInfo {
	return MigrationInfo{
		Epoch:         m.epoch,
		Version:       m.version,
		Description:   m.description,
		Checksum:      m.checksum(),
		CompatibleMin: m.compatibleMin,
		CompatibleMax: m.compatibleMax,
	}
}

var schemaMigrations = []schemaMigration{
	newLegacyNullStringSchemaMigration("cae7333a1d25ebd1d6cbfe87ad550d9d6e585e8396114223578813b7c3d428be"),
	newPluggableCICDSchemaMigration("d310404d22d42b99b2ada630032ba304a52ac3ca919493cb59558e724365fcda"),
	newCICDRuntimeHardeningSchemaMigration("5a4c4a3efce7318d5f9b46d45dfda56b91ef987f0d603df46d4658ce813ffcaf"),
	newVersionedSchemaMigration("783d880e4482090857c37f55e111e771fce5f30e2308206772d07f9fefe4a187"),
	newAuthRBACSchemaMigration("94c805d77d89ec0a748b85906900b641440922548d612de77ef19df4085ea6bf"),
}

type ledgerRow struct {
	Version       string
	Epoch         sql.NullInt64
	Description   sql.NullString
	Checksum      sql.NullString
	Dirty         sql.NullInt64
	StartedAt     sql.NullTime
	FinishedAt    sql.NullTime
	CompatibleMin sql.NullInt64
	CompatibleMax sql.NullInt64
	LastError     sql.NullString
	LegacyAdopted sql.NullInt64
	AppliedAt     time.Time
}

var completeLedgerColumns = []string{
	"version", "epoch", "description", "checksum", "dirty", "started_at",
	"finished_at", "compatible_min", "compatible_max", "last_error",
	"legacy_adopted", "applied_at",
}

// SchemaMigrationStatus uses the runtime DSN and never writes to the database.
func SchemaMigrationStatus(ctx context.Context) (MigrationStatus, error) {
	return InspectSchema(ctx, config.Main.DB.ConnStr, config.DBSchemaMigrationTimeout())
}

// RunSchemaMigrations runs the explicit migrator. A migration DSN is required;
// falling back to the runtime DSN would defeat the least-privilege boundary.
func RunSchemaMigrations(ctx context.Context, resumeDirtyVersion string) (MigrationStatus, error) {
	dsn := config.DBMigrationConnStr()
	if dsn == "" {
		return MigrationStatus{}, fmt.Errorf("db.migration_conn_str / ARES_DB_MIGRATION_CONN_STR is required for migrate up")
	}
	if adminDSN := config.DBMigrationAdminConnStr(); adminDSN != "" {
		return MigrateUpGuarded(ctx, dsn, adminDSN, resumeDirtyVersion, config.DBSchemaMigrationTimeout(), config.DBMigrationLockTimeout())
	}
	return MigrateUp(ctx, dsn, resumeDirtyVersion, config.DBSchemaMigrationTimeout(), config.DBMigrationLockTimeout())
}

// InspectSchema performs a strictly read-only inspection. Incompatible schema
// state is returned in MigrationStatus rather than as a query error.
func InspectSchema(ctx context.Context, dsn string, operationTimeout time.Duration) (MigrationStatus, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	operationTimeout = normalizedOperationTimeout(operationTimeout)
	database, err := openSQLDatabase(ctx, dsn, operationTimeout)
	if err != nil {
		return MigrationStatus{}, err
	}
	defer database.Close()
	conn, err := database.Conn(ctx)
	if err != nil {
		return MigrationStatus{}, fmt.Errorf("acquire schema inspection connection: %w", err)
	}
	defer conn.Close()
	return inspectSchema(ctx, &migrationSession{ctx: ctx, executor: conn, operationTimeout: operationTimeout})
}

// CheckRuntimeCompatibility validates an already-open runtime pool without any
// DDL or seed writes.
func CheckRuntimeCompatibility(ctx context.Context, database *sql.DB) error {
	if ctx == nil {
		ctx = context.Background()
	}
	conn, err := database.Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquire runtime schema inspection connection: %w", err)
	}
	defer conn.Close()
	status, err := inspectSchema(ctx, &migrationSession{
		ctx:              ctx,
		executor:         conn,
		operationTimeout: config.DBSchemaMigrationTimeout(),
	})
	if err != nil {
		return err
	}
	if !status.Compatible() {
		return &SchemaStateError{Problems: []string{status.String()}}
	}
	return nil
}

func openSQLDatabase(ctx context.Context, dsn string, operationTimeout time.Duration) (*sql.DB, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	operationTimeout = normalizedOperationTimeout(operationTimeout)
	parsed, err := parseAresMySQLDSN(dsn)
	if err != nil {
		return nil, err
	}
	driverTimeout := operationTimeout + 30*time.Second
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
		return nil, fmt.Errorf("open database connection: %w", err)
	}
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := database.PingContext(pingCtx); err != nil {
		_ = database.Close()
		return nil, fmt.Errorf("connect database: %w", err)
	}
	return database, nil
}

func parseAresMySQLDSN(dsn string) (*mysql.Config, error) {
	parsed, err := mysql.ParseDSN(strings.TrimSpace(dsn))
	if err != nil {
		return nil, fmt.Errorf("parse database DSN: %w", err)
	}
	if strings.TrimSpace(parsed.DBName) == "" {
		return nil, fmt.Errorf("database DSN must select a database")
	}
	// Ledger timestamps and persistent entities are represented as time.Time.
	// Normalize this driver option instead of making every deployment remember
	// an otherwise undocumented parseTime=true query parameter.
	parsed.ParseTime = true
	return parsed, nil
}

func normalizedAresMySQLDSN(dsn string) (string, error) {
	parsed, err := parseAresMySQLDSN(dsn)
	if err != nil {
		return "", err
	}
	return parsed.FormatDSN(), nil
}

func MigrateUp(ctx context.Context, dsn, resumeDirtyVersion string, operationTimeout, lockTimeout time.Duration) (MigrationStatus, error) {
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
	database, err := openSQLDatabase(ctx, dsn, connectionTimeout)
	if err != nil {
		return MigrationStatus{}, err
	}
	defer database.Close()
	database.SetMaxOpenConns(1)
	database.SetMaxIdleConns(1)

	conn, err := database.Conn(ctx)
	if err != nil {
		return MigrationStatus{}, fmt.Errorf("acquire migration connection: %w", err)
	}
	defer conn.Close()
	return migrateUpOnConnection(ctx, conn, resumeDirtyVersion, operationTimeout, lockTimeout)
}

// migrateUpOnConnection runs the complete migration state machine on one
// caller-owned physical MySQL connection. Keeping connection ownership with
// the caller lets guarded migration lock the login account while the already
// authenticated session continues to apply and verify migrations.
func migrateUpOnConnection(ctx context.Context, conn *sql.Conn, resumeDirtyVersion string, operationTimeout, lockTimeout time.Duration) (MigrationStatus, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	operationTimeout = normalizedOperationTimeout(operationTimeout)
	if lockTimeout <= 0 {
		lockTimeout = 30 * time.Second
	}
	session := &migrationSession{ctx: ctx, executor: conn, operationTimeout: operationTimeout}

	release, err := acquireMigrationLock(ctx, conn, lockTimeout)
	if err != nil {
		return MigrationStatus{}, err
	}
	defer release()

	status, err := inspectSchema(ctx, session)
	if err != nil {
		return MigrationStatus{}, err
	}
	if !status.Initialized {
		if len(status.Problems) > 0 {
			return status, &SchemaStateError{Problems: status.Problems}
		}
		if err := session.createMigrationLedger(); err != nil {
			return MigrationStatus{}, err
		}
		status = MigrationStatus{Initialized: true, Pending: migrationInfos(schemaMigrations)}
	}
	if status.NeedsAdoption {
		if err := schemaStateBlocker(status); err != nil {
			return status, err
		}
		// An empty legacy ledger is indistinguishable from an interrupted old
		// initialization. Prove that the database is still bootstrap-eligible
		// before ALTERing the ledger, otherwise fail without any writes.
		if len(status.Applied) == 0 {
			_, problems, err := session.inspectBootstrapState()
			if err != nil {
				return MigrationStatus{}, err
			}
			if len(problems) > 0 {
				status.Problems = append(status.Problems, problems...)
				return status, &SchemaStateError{Problems: status.Problems}
			}
		}
		if err := session.adoptLegacyLedger(); err != nil {
			return MigrationStatus{}, err
		}
		status, err = inspectSchema(ctx, session)
		if err != nil {
			return MigrationStatus{}, err
		}
	}
	// Never bootstrap or write a new dirty row when the existing ledger or
	// applied schema is already known to be unsupported.
	if err := schemaStateBlocker(status); err != nil {
		return status, err
	}

	if len(status.Applied) == 0 && status.Dirty == nil {
		if err := session.bootstrapEmptySchema(); err != nil {
			return status, err
		}
		status, err = inspectSchema(ctx, session)
		if err != nil {
			return MigrationStatus{}, err
		}
	}

	if err := schemaStateBlocker(status); err != nil {
		return status, err
	}
	if status.Dirty != nil {
		if resumeDirtyVersion == "" {
			return status, &SchemaStateError{Problems: []string{
				fmt.Sprintf("migration %s is dirty; resume with --resume-dirty %s", status.Dirty.Version, status.Dirty.Version),
			}}
		}
		if resumeDirtyVersion != status.Dirty.Version {
			return status, &SchemaStateError{Problems: []string{
				fmt.Sprintf("dirty migration is %s, not %s", status.Dirty.Version, resumeDirtyVersion),
			}}
		}
		migration := migrationByVersion(status.Dirty.Version)
		if migration == nil || migration.checksum() != status.Dirty.Checksum {
			return status, &SchemaStateError{Problems: []string{"dirty migration checksum does not match the compiled catalog"}}
		}
		if err := session.executeDirtyMigration(*migration, true); err != nil {
			return refreshedMigrationStatus(ctx, session, status), err
		}
	} else if resumeDirtyVersion != "" {
		return status, &SchemaStateError{Problems: []string{"--resume-dirty was provided but the database has no dirty migration"}}
	}

	status, err = inspectSchema(ctx, session)
	if err != nil {
		return MigrationStatus{}, err
	}
	if err := schemaStateBlocker(status); err != nil {
		return status, err
	}
	for _, pending := range status.Pending {
		migration := migrationByVersion(pending.Version)
		if migration == nil {
			return status, &SchemaStateError{Problems: []string{"pending migration is absent from the compiled catalog"}}
		}
		if err := session.executeDirtyMigration(*migration, false); err != nil {
			return refreshedMigrationStatus(ctx, session, status), err
		}
	}

	status, err = inspectSchema(ctx, session)
	if err != nil {
		return MigrationStatus{}, err
	}
	if !status.Compatible() {
		return status, &SchemaStateError{Problems: []string{status.String()}}
	}
	return status, nil
}

// refreshedMigrationStatus reports the ledger and schema state left by a
// failed migration attempt. The original migration error remains authoritative;
// a best-effort diagnostic read must never hide it.
func refreshedMigrationStatus(ctx context.Context, session *migrationSession, fallback MigrationStatus) MigrationStatus {
	status, err := inspectSchema(ctx, session)
	if err != nil {
		slog.Warn("failed to refresh database migration status after error", "error", SafeMigrationErrorText(err))
		return fallback
	}
	return status
}

func schemaStateBlocker(status MigrationStatus) error {
	blocking := append([]string(nil), status.Problems...)
	blocking = append(blocking, status.ManifestDiffs...)
	if len(blocking) == 0 {
		return nil
	}
	return &SchemaStateError{Problems: blocking}
}

func migrationInfos(migrations []schemaMigration) []MigrationInfo {
	result := make([]MigrationInfo, 0, len(migrations))
	for _, migration := range migrations {
		result = append(result, migration.info())
	}
	return result
}

func migrationByVersion(version string) *schemaMigration {
	for index := range schemaMigrations {
		if schemaMigrations[index].version == version {
			return &schemaMigrations[index]
		}
	}
	return nil
}

func acquireMigrationLock(ctx context.Context, conn *sql.Conn, timeout time.Duration) (func(), error) {
	var databaseName string
	queryCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	err := conn.QueryRowContext(queryCtx, "SELECT DATABASE()").Scan(&databaseName)
	cancel()
	if err != nil {
		return nil, fmt.Errorf("resolve database name for migration lock: %w", err)
	}
	digest := sha256.Sum256([]byte(databaseName))
	lockName := "ares_schema_migration_" + hex.EncodeToString(digest[:16])
	// GET_LOCK takes whole seconds on the supported MySQL server. Round its
	// server-side guard upward, while the context keeps the externally visible
	// deadline equal to the configured duration (including sub-second values).
	seconds := int64((timeout + time.Second - 1) / time.Second)
	if seconds < 1 {
		seconds = 1
	}
	lockCtx, lockCancel := context.WithTimeout(ctx, timeout)
	defer lockCancel()
	var acquired sql.NullInt64
	if err := conn.QueryRowContext(lockCtx, "SELECT GET_LOCK(?, ?)", lockName, seconds).Scan(&acquired); err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(lockCtx.Err(), context.DeadlineExceeded) {
			return nil, fmt.Errorf("timed out waiting for database migration lock: %w", context.DeadlineExceeded)
		}
		return nil, fmt.Errorf("acquire database migration lock: %w", err)
	}
	if !acquired.Valid || acquired.Int64 != 1 {
		return nil, fmt.Errorf("timed out waiting for database migration lock")
	}
	return func() {
		releaseCtx, releaseCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer releaseCancel()
		var released sql.NullInt64
		if err := conn.QueryRowContext(releaseCtx, "SELECT RELEASE_LOCK(?)", lockName).Scan(&released); err != nil {
			slog.Warn("failed to release database migration lock", "error", SafeMigrationErrorText(err))
		}
	}, nil
}

func inspectSchema(ctx context.Context, session *migrationSession) (MigrationStatus, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	operationTimeout := normalizedOperationTimeout(session.operationTimeout)
	inspectCtx, cancel := context.WithTimeout(ctx, operationTimeout)
	defer cancel()
	ctx = inspectCtx

	status := MigrationStatus{Pending: migrationInfos(schemaMigrations)}
	var databaseVersion, versionComment string
	if err := session.executor.QueryRowContext(ctx, "SELECT VERSION(), @@version_comment").Scan(&databaseVersion, &versionComment); err != nil {
		return MigrationStatus{}, fmt.Errorf("inspect database version: %w", err)
	}
	if problem := supportedDatabaseVersionProblem(databaseVersion, versionComment); problem != "" {
		status.Problems = append(status.Problems, problem)
	}
	versionProblems := append([]string(nil), status.Problems...)
	tables, err := session.databaseTables(ctx)
	if err != nil {
		return MigrationStatus{}, err
	}
	if _, exists := tables[schemaMigrationsTable]; !exists {
		if len(tables) > 0 {
			names := sortedKeys(tables)
			status.Problems = append(status.Problems,
				"schema_migrations 不存在，但数据库包含用户表: "+strings.Join(names, ","))
		}
		return status, nil
	}
	status.Initialized = true

	columns, err := session.tableColumns(ctx, schemaMigrationsTable)
	if err != nil {
		return MigrationStatus{}, err
	}
	needsAdoption := !containsAll(columns, completeLedgerColumns)
	if !needsAdoption {
		needsAdoption, err = session.ledgerNeedsFinalization(ctx)
		if err != nil {
			return MigrationStatus{}, err
		}
	}
	ledgerColumns, err := session.readMigrationLedgerColumns(ctx)
	if err != nil {
		return MigrationStatus{}, err
	}
	if needsAdoption {
		status.NeedsAdoption = true
		definitionProblems, err := session.migrationLedgerDefinitionProblems(ctx, ledgerColumns, true)
		if err != nil {
			return MigrationStatus{}, err
		}
		if len(definitionProblems) > 0 {
			status.Problems = append(status.Problems, definitionProblems...)
			return status, nil
		}
		versions, err := session.readLegacyVersions(ctx)
		if err != nil {
			return MigrationStatus{}, err
		}
		var prefixProblems []string
		status.Applied, status.Pending, prefixProblems = validateLegacyPrefix(versions)
		status.Problems = append(status.Problems, prefixProblems...)
		if len(prefixProblems) > 0 {
			return status, nil
		}
		metadataProblems, err := session.adoptionMetadataProblems(ctx, ledgerColumns, status.Applied)
		if err != nil {
			return MigrationStatus{}, err
		}
		status.Problems = append(status.Problems, metadataProblems...)
		return status, nil
	}
	definitionProblems, err := session.migrationLedgerDefinitionProblems(ctx, ledgerColumns, false)
	if err != nil {
		return MigrationStatus{}, err
	}
	if len(definitionProblems) > 0 {
		status.Problems = append(status.Problems, definitionProblems...)
		return status, nil
	}

	rows, err := session.readLedgerRows(ctx)
	if err != nil {
		return MigrationStatus{}, err
	}
	if ledgerRowsNeedAdoption(rows) {
		status.NeedsAdoption = true
		versions := make([]string, 0, len(rows))
		for _, row := range rows {
			versions = append(versions, row.Version)
		}
		var prefixProblems []string
		status.Applied, status.Pending, prefixProblems = validateLegacyPrefix(versions)
		status.Problems = append(status.Problems, prefixProblems...)
		return status, nil
	}
	status = validateLedgerRows(rows)
	status.Problems = append(versionProblems, status.Problems...)
	status.Initialized = true
	if status.CurrentEpoch > 0 && status.CurrentEpoch <= uint64(len(schemaMigrations)) &&
		len(status.Problems) == 0 {
		// Each epoch owns an immutable, complete postcondition for that exact
		// schema state. Only the latest applied epoch is evaluated: older schema
		// snapshots are not cumulative constraints on a later migration.
		migration := schemaMigrations[status.CurrentEpoch-1]
		verify := migration.verify
		phase := "postconditions"
		if status.Dirty != nil {
			verify = migration.preflight
			phase = "resume preflight"
		}
		if verify != nil {
			if err := verify(session); err != nil {
				var stateErr *SchemaStateError
				if errors.As(err, &stateErr) {
					status.ManifestDiffs = append(status.ManifestDiffs, stateErr.Problems...)
				} else {
					return MigrationStatus{}, fmt.Errorf(
						"verify migration %s %s: %w", migration.version, phase, err)
				}
			}
		}
	}
	return status, nil
}

func supportedDatabaseVersionProblem(version, comment string) string {
	if !mysql84VersionPattern.MatchString(strings.TrimSpace(version)) ||
		strings.Contains(strings.ToLower(version+" "+comment), "mariadb") {
		return fmt.Sprintf("仅支持 MySQL 8.4，当前数据库版本=%q", strings.TrimSpace(version))
	}
	return ""
}

func (s *migrationSession) verifyEpochPostconditions(epoch uint64) error {
	if epoch == 0 || epoch > uint64(len(schemaMigrations)) {
		return &SchemaStateError{Problems: []string{
			fmt.Sprintf("cannot verify unknown schema epoch %d", epoch),
		}}
	}
	return schemaMigrations[epoch-1].verify(s)
}

func (s *migrationSession) ledgerNeedsFinalization(ctx context.Context) (bool, error) {
	var count int
	err := s.executor.QueryRowContext(ctx, `SELECT COUNT(*)
		FROM information_schema.COLUMNS
		WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'schema_migrations'
			AND COLUMN_NAME IN ('epoch','description','checksum','dirty','started_at',
				'compatible_min','compatible_max','legacy_adopted')
			AND IS_NULLABLE <> 'NO'`).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("inspect migration ledger constraints: %w", err)
	}
	return count > 0, nil
}

func ledgerRowsNeedAdoption(rows []ledgerRow) bool {
	for _, row := range rows {
		if !row.Epoch.Valid || !row.Description.Valid || !row.Checksum.Valid ||
			!row.Dirty.Valid || !row.StartedAt.Valid || !row.CompatibleMin.Valid ||
			!row.CompatibleMax.Valid || !row.LegacyAdopted.Valid {
			return true
		}
	}
	return false
}

func (s *migrationSession) databaseTables(ctx context.Context) (map[string]struct{}, error) {
	rows, err := s.executor.QueryContext(ctx, `SELECT TABLE_NAME
		FROM information_schema.TABLES
		WHERE TABLE_SCHEMA = DATABASE()`)
	if err != nil {
		return nil, fmt.Errorf("inspect database tables: %w", err)
	}
	defer rows.Close()
	result := make(map[string]struct{})
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("scan database table: %w", err)
		}
		result[name] = struct{}{}
	}
	return result, rows.Err()
}

func (s *migrationSession) tableColumns(ctx context.Context, table string) (map[string]struct{}, error) {
	rows, err := s.executor.QueryContext(ctx, `SELECT COLUMN_NAME
		FROM information_schema.COLUMNS
		WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ?`, table)
	if err != nil {
		return nil, fmt.Errorf("inspect %s columns: %w", table, err)
	}
	defer rows.Close()
	result := make(map[string]struct{})
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("scan %s column: %w", table, err)
		}
		result[name] = struct{}{}
	}
	return result, rows.Err()
}

func (s *migrationSession) readLegacyVersions(ctx context.Context) ([]string, error) {
	rows, err := s.executor.QueryContext(ctx, "SELECT version FROM schema_migrations")
	if err != nil {
		return nil, fmt.Errorf("read legacy migration ledger: %w", err)
	}
	defer rows.Close()
	versions := make([]string, 0)
	for rows.Next() {
		var version string
		if err := rows.Scan(&version); err != nil {
			return nil, fmt.Errorf("scan legacy migration ledger: %w", err)
		}
		versions = append(versions, version)
	}
	return versions, rows.Err()
}

func (s *migrationSession) readLedgerRows(ctx context.Context) ([]ledgerRow, error) {
	rows, err := s.executor.QueryContext(ctx, `SELECT version, epoch, description, checksum,
		dirty, started_at, finished_at, compatible_min, compatible_max,
		last_error, legacy_adopted, applied_at
		FROM schema_migrations ORDER BY epoch, version`)
	if err != nil {
		return nil, fmt.Errorf("read migration ledger: %w", err)
	}
	defer rows.Close()
	result := make([]ledgerRow, 0)
	for rows.Next() {
		var row ledgerRow
		var startedAt, finishedAt, appliedAt mysql.NullTime
		if err := rows.Scan(&row.Version, &row.Epoch, &row.Description, &row.Checksum,
			&row.Dirty, &startedAt, &finishedAt, &row.CompatibleMin,
			&row.CompatibleMax, &row.LastError, &row.LegacyAdopted, &appliedAt); err != nil {
			return nil, fmt.Errorf("scan migration ledger: %w", err)
		}
		row.StartedAt = sql.NullTime(startedAt)
		row.FinishedAt = sql.NullTime(finishedAt)
		row.AppliedAt = appliedAt.Time
		result = append(result, row)
	}
	return result, rows.Err()
}

func validateLegacyPrefix(versions []string) ([]MigrationInfo, []MigrationInfo, []string) {
	epochs := make([]int, 0, len(versions))
	problems := make([]string, 0)
	for _, version := range versions {
		migration := migrationByVersion(version)
		if migration == nil || migration.epoch > 3 {
			problems = append(problems, "旧 ledger 包含未知版本 "+version)
			continue
		}
		epochs = append(epochs, int(migration.epoch))
	}
	sort.Ints(epochs)
	applied := make([]MigrationInfo, 0, len(epochs))
	for index, epoch := range epochs {
		if epoch != index+1 {
			problems = append(problems, fmt.Sprintf("旧 ledger 不是从 epoch 1 开始的连续前缀: %v", epochs))
			break
		}
		applied = append(applied, schemaMigrations[epoch-1].info())
	}
	pending := migrationInfos(schemaMigrations[len(applied):])
	return applied, pending, problems
}

func validateLedgerRows(rows []ledgerRow) MigrationStatus {
	status := MigrationStatus{}
	seenNativeMigration := false
	for index, row := range rows {
		expectedEpoch := uint64(index + 1)
		if !row.Epoch.Valid || row.Epoch.Int64 <= 0 || uint64(row.Epoch.Int64) != expectedEpoch {
			status.Problems = append(status.Problems, fmt.Sprintf("ledger epoch 不连续，位置 %d 的值为 %v", index+1, row.Epoch))
			continue
		}
		if index >= len(schemaMigrations) {
			status.Problems = append(status.Problems, fmt.Sprintf("ledger 包含未知 epoch %d (%s)", expectedEpoch, row.Version))
			continue
		}
		migration := schemaMigrations[index]
		if row.Version != migration.version {
			status.Problems = append(status.Problems, fmt.Sprintf("epoch %d 版本不匹配: %s", expectedEpoch, row.Version))
		}
		if !row.Checksum.Valid || row.Checksum.String != migration.checksum() {
			status.Problems = append(status.Problems, fmt.Sprintf("migration %s checksum 不匹配", row.Version))
		}
		if !row.Description.Valid || row.Description.String != migration.description {
			status.Problems = append(status.Problems, fmt.Sprintf("migration %s 描述不匹配", row.Version))
		}
		if !row.CompatibleMin.Valid || !row.CompatibleMax.Valid ||
			row.CompatibleMin.Int64 < 0 || row.CompatibleMax.Int64 < row.CompatibleMin.Int64 {
			status.Problems = append(status.Problems, fmt.Sprintf("migration %s 兼容区间非法", row.Version))
		} else if uint64(row.CompatibleMin.Int64) != migration.compatibleMin ||
			uint64(row.CompatibleMax.Int64) != migration.compatibleMax {
			status.Problems = append(status.Problems, fmt.Sprintf(
				"migration %s 兼容区间不匹配: ledger=[%d,%d] catalog=[%d,%d]",
				row.Version, row.CompatibleMin.Int64, row.CompatibleMax.Int64,
				migration.compatibleMin, migration.compatibleMax))
		}
		dirtyValid := row.Dirty.Valid && (row.Dirty.Int64 == 0 || row.Dirty.Int64 == 1)
		if !dirtyValid {
			status.Problems = append(status.Problems, fmt.Sprintf("migration %s dirty 必须为 0 或 1", row.Version))
		}
		if !row.StartedAt.Valid {
			status.Problems = append(status.Problems, fmt.Sprintf("migration %s 缺少 started_at", row.Version))
		}
		legacyAdoptedValid := row.LegacyAdopted.Valid &&
			(row.LegacyAdopted.Int64 == 0 || row.LegacyAdopted.Int64 == 1)
		if !legacyAdoptedValid {
			status.Problems = append(status.Problems,
				fmt.Sprintf("migration %s legacy_adopted 必须为 0 或 1", row.Version))
		} else {
			if row.LegacyAdopted.Int64 == 1 {
				if seenNativeMigration || expectedEpoch > 3 {
					status.Problems = append(status.Problems,
						fmt.Sprintf("migration %s legacy_adopted 顺序非法", row.Version))
				}
			} else {
				seenNativeMigration = true
			}
		}
		info := migration.info()
		status.Applied = append(status.Applied, info)
		if dirtyValid && row.Dirty.Int64 == 1 {
			if status.Dirty != nil {
				status.Problems = append(status.Problems, "ledger 包含多条 dirty migration")
			}
			status.Dirty = &DirtyMigration{
				Epoch: expectedEpoch, Version: row.Version, Checksum: row.Checksum.String,
				Error: row.LastError.String,
			}
			if row.FinishedAt.Valid {
				status.Problems = append(status.Problems, fmt.Sprintf("dirty migration %s 不应有 finished_at", row.Version))
			}
		} else if dirtyValid {
			if !row.FinishedAt.Valid {
				status.Problems = append(status.Problems, fmt.Sprintf("已完成 migration %s 缺少 finished_at", row.Version))
			} else if row.FinishedAt.Time.Before(row.StartedAt.Time) {
				status.Problems = append(status.Problems, fmt.Sprintf("migration %s finished_at 早于 started_at", row.Version))
			}
			if row.LastError.Valid {
				status.Problems = append(status.Problems, fmt.Sprintf("已完成 migration %s 不应保留 last_error", row.Version))
			}
		}
		status.CurrentEpoch = expectedEpoch
		if row.CompatibleMin.Valid && row.CompatibleMin.Int64 >= 0 {
			status.CompatibleMin = uint64(row.CompatibleMin.Int64)
		}
		if row.CompatibleMax.Valid && row.CompatibleMax.Int64 >= 0 {
			status.CompatibleMax = uint64(row.CompatibleMax.Int64)
		}
	}
	if status.Dirty != nil && status.Dirty.Epoch != uint64(len(rows)) {
		status.Problems = append(status.Problems, "dirty migration 后存在其他 ledger 记录")
	}
	if len(rows) < len(schemaMigrations) {
		status.Pending = migrationInfos(schemaMigrations[len(rows):])
	}
	return status
}

func (s *migrationSession) createMigrationLedger() error {
	ctx, cancel := s.operationContext()
	defer cancel()
	_, err := s.executor.ExecContext(ctx, `CREATE TABLE schema_migrations (
		version VARCHAR(128) NOT NULL PRIMARY KEY,
		epoch BIGINT UNSIGNED NOT NULL,
		description VARCHAR(255) NOT NULL,
		checksum CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
		dirty TINYINT(1) NOT NULL,
		started_at DATETIME(6) NOT NULL,
		finished_at DATETIME(6) NULL,
		compatible_min BIGINT UNSIGNED NOT NULL,
		compatible_max BIGINT UNSIGNED NOT NULL,
		last_error TEXT NULL,
		legacy_adopted TINYINT(1) NOT NULL DEFAULT 0,
		applied_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
		UNIQUE KEY uq_schema_migrations_epoch (epoch)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`)
	if err != nil {
		return fmt.Errorf("create migration ledger: %w", err)
	}
	return nil
}

func (s *migrationSession) adoptLegacyLedger() error {
	ctx, cancel := s.operationContext()
	ledgerColumns, err := s.readMigrationLedgerColumns(ctx)
	if err == nil {
		var definitionProblems []string
		definitionProblems, err = s.migrationLedgerDefinitionProblems(ctx, ledgerColumns, true)
		if len(definitionProblems) > 0 {
			cancel()
			return &SchemaStateError{Problems: definitionProblems}
		}
	}
	cancel()
	if err != nil {
		return err
	}
	ctx, cancel = s.operationContext()
	versions, err := s.readLegacyVersions(ctx)
	cancel()
	if err != nil {
		return err
	}
	applied, _, problems := validateLegacyPrefix(versions)
	if len(problems) > 0 {
		return &SchemaStateError{Problems: problems}
	}
	ctx, cancel = s.operationContext()
	problems, err = s.adoptionMetadataProblems(ctx, ledgerColumns, applied)
	cancel()
	if err != nil {
		return err
	}
	if len(problems) > 0 {
		return &SchemaStateError{Problems: problems}
	}
	if len(applied) > 0 {
		migration := migrationByVersion(applied[len(applied)-1].Version)
		if migration == nil {
			return &SchemaStateError{Problems: []string{
				"cannot adopt unknown migration " + applied[len(applied)-1].Version,
			}}
		}
		if err := s.verifyEpochPostconditions(migration.epoch); err != nil {
			if errors.Is(err, ErrSchemaState) {
				return &SchemaStateError{Problems: []string{
					fmt.Sprintf("legacy migration %s postcondition failed: %v", migration.version, err),
				}}
			}
			return fmt.Errorf("verify legacy migration %s before adoption: %w", migration.version, err)
		}
	}

	ctx, cancel = s.operationContext()
	columns, err := s.tableColumns(ctx, schemaMigrationsTable)
	cancel()
	if err != nil {
		return err
	}
	additions := []struct{ name, definition string }{
		{"epoch", "BIGINT UNSIGNED NULL"},
		{"description", "VARCHAR(255) NULL"},
		{"checksum", "CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL"},
		{"dirty", "TINYINT(1) NULL"},
		{"started_at", "DATETIME(6) NULL"},
		{"finished_at", "DATETIME(6) NULL"},
		{"compatible_min", "BIGINT UNSIGNED NULL"},
		{"compatible_max", "BIGINT UNSIGNED NULL"},
		{"last_error", "TEXT NULL"},
		{"legacy_adopted", "TINYINT(1) NULL"},
	}
	for _, column := range additions {
		if _, exists := columns[column.name]; exists {
			continue
		}
		ctx, cancel = s.operationContext()
		_, err = s.executor.ExecContext(ctx,
			fmt.Sprintf("ALTER TABLE schema_migrations ADD COLUMN `%s` %s", column.name, column.definition))
		cancel()
		if err != nil {
			return fmt.Errorf("add migration ledger column %s: %w", column.name, err)
		}
	}
	for _, info := range applied {
		ctx, cancel = s.operationContext()
		_, err := s.executor.ExecContext(ctx, `UPDATE schema_migrations SET
			epoch = ?, description = ?, checksum = ?, dirty = 0,
			started_at = applied_at, finished_at = applied_at,
			compatible_min = ?, compatible_max = ?, last_error = NULL,
			legacy_adopted = 1 WHERE version = ?`,
			info.Epoch, info.Description, info.Checksum, info.CompatibleMin, info.CompatibleMax, info.Version)
		cancel()
		if err != nil {
			return fmt.Errorf("adopt legacy migration %s: %w", info.Version, err)
		}
	}
	ctx, cancel = s.operationContext()
	indexSnapshot, err := readSchemaSnapshot(ctx, s.executor)
	cancel()
	if err != nil {
		return fmt.Errorf("inspect migration epoch index: %w", err)
	}
	indexExists := len(compareIndexDefinitions(schemaMigrationsTable,
		indexSnapshot.indexes[schemaMigrationsTable],
		[]schemaIndexManifest{uniqueIndex("epoch")}, false)) == 0
	if !indexExists {
		ctx, cancel = s.operationContext()
		_, err = s.executor.ExecContext(ctx,
			"ALTER TABLE schema_migrations ADD UNIQUE INDEX uq_schema_migrations_epoch (epoch)")
		cancel()
		if err != nil {
			return fmt.Errorf("add migration epoch index: %w", err)
		}
	}
	ctx, cancel = s.operationContext()
	_, err = s.executor.ExecContext(ctx, `ALTER TABLE schema_migrations
		MODIFY COLUMN epoch BIGINT UNSIGNED NOT NULL,
		MODIFY COLUMN description VARCHAR(255) NOT NULL,
		MODIFY COLUMN checksum CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
		MODIFY COLUMN dirty TINYINT(1) NOT NULL,
		MODIFY COLUMN started_at DATETIME(6) NOT NULL,
		MODIFY COLUMN compatible_min BIGINT UNSIGNED NOT NULL,
		MODIFY COLUMN compatible_max BIGINT UNSIGNED NOT NULL,
		MODIFY COLUMN legacy_adopted TINYINT(1) NOT NULL DEFAULT 0`)
	cancel()
	if err != nil {
		return fmt.Errorf("finalize migration ledger metadata: %w", err)
	}
	return nil
}

func (s *migrationSession) executeDirtyMigration(migration schemaMigration, resume bool) error {
	checksum := migration.checksum()
	ctx, cancel := s.operationContext()
	if !resume {
		_, err := s.executor.ExecContext(ctx, `INSERT INTO schema_migrations
			(version, epoch, description, checksum, dirty, started_at, finished_at,
			 compatible_min, compatible_max, last_error, legacy_adopted)
			VALUES (?, ?, ?, ?, 1, NOW(6), NULL, ?, ?, NULL, 0)`,
			migration.version, migration.epoch, migration.description, checksum,
			migration.compatibleMin, migration.compatibleMax)
		cancel()
		if err != nil {
			return fmt.Errorf("mark migration %s dirty: %w", migration.version, err)
		}
	} else {
		result, err := s.executor.ExecContext(ctx, `UPDATE schema_migrations
			SET finished_at = NULL, last_error = NULL
			WHERE version = ? AND checksum = ? AND dirty = 1`, migration.version, checksum)
		cancel()
		if err != nil {
			return fmt.Errorf("prepare dirty migration %s for resume: %w", migration.version, err)
		}
		affected, affectedErr := result.RowsAffected()
		if affectedErr == nil && affected > 1 {
			return &SchemaStateError{Problems: []string{"dirty migration changed while preparing resume"}}
		}
		if affectedErr != nil || affected == 0 {
			// MySQL reports changed rows by default. Immediately after the dirty
			// marker is inserted, finished_at and last_error are already NULL, so
			// a legitimate crash-resume UPDATE can be a no-op. The migration lock
			// and single physical session serialize this read-back check.
			verifyCtx, verifyCancel := s.operationContext()
			var matches int
			verifyErr := s.executor.QueryRowContext(verifyCtx, `SELECT COUNT(*) FROM schema_migrations
				WHERE version = ? AND checksum = ? AND dirty = 1
					AND finished_at IS NULL AND last_error IS NULL`, migration.version, checksum).Scan(&matches)
			verifyCancel()
			if verifyErr != nil {
				return fmt.Errorf("verify dirty migration %s resume state: %w", migration.version, verifyErr)
			}
			if matches != 1 {
				return &SchemaStateError{Problems: []string{"dirty migration changed while preparing resume"}}
			}
		}
	}

	slog.Info("applying database migration", "epoch", migration.epoch, "version", migration.version, "resume", resume)
	if err := migration.up(s); err != nil {
		s.recordMigrationError(migration.version, checksum, err)
		return fmt.Errorf("apply database migration %s: %w", migration.version, sanitizedMigrationError{cause: err})
	}
	// The migration's verifier is the immutable, complete contract for this
	// exact epoch. Historical epoch manifests are snapshots and must not reject
	// a legitimate future expand/contract migration.
	if err := s.verifyEpochPostconditions(migration.epoch); err != nil {
		s.recordMigrationError(migration.version, checksum, err)
		return fmt.Errorf("verify database migration %s: %w", migration.version, sanitizedMigrationError{cause: err})
	}
	finishCtx, finishCancel := s.operationContext()
	result, err := s.executor.ExecContext(finishCtx, `UPDATE schema_migrations
		SET dirty = 0, finished_at = NOW(6), last_error = NULL, applied_at = CURRENT_TIMESTAMP
		WHERE version = ? AND checksum = ? AND dirty = 1`, migration.version, checksum)
	finishCancel()
	if err != nil {
		return fmt.Errorf("finish database migration %s: %w", migration.version, err)
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return fmt.Errorf("finish database migration %s: ledger compare-and-set affected %d rows", migration.version, affected)
	}
	slog.Info("database migration applied", "epoch", migration.epoch, "version", migration.version)
	return nil
}

func (s *migrationSession) recordMigrationError(version, checksum string, migrationErr error) {
	message := sanitizeMigrationError(migrationErr)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := s.executor.ExecContext(ctx, `UPDATE schema_migrations
		SET last_error = ? WHERE version = ? AND checksum = ? AND dirty = 1`,
		message, version, checksum); err != nil {
		slog.Warn("failed to record migration error", "version", version, "error", SafeMigrationErrorText(err))
	}
}

func sanitizeMigrationError(err error) string {
	if err == nil {
		return ""
	}
	return sanitizeMigrationErrorText(err.Error())
}

// SafeMigrationErrorText is the CLI/logging trust boundary for errors that may
// contain driver messages, DSNs, trigger text or other operator-controlled
// values. Callers still use the original error for errors.Is/errors.As.
func SafeMigrationErrorText(err error) string {
	return sanitizeMigrationError(err)
}

type sanitizedMigrationError struct {
	cause error
}

func (e sanitizedMigrationError) Error() string { return sanitizeMigrationError(e.cause) }
func (e sanitizedMigrationError) Unwrap() error { return e.cause }

var migrationSecretPatterns = []struct {
	pattern     *regexp.Regexp
	replacement string
}{
	{regexp.MustCompile(`(?i)(https?://)[^/@\s:]+:[^/@\s]+@`), `${1}<redacted>@`},
	{regexp.MustCompile(`(?i)\b[^\s:/@]+:\S+@(tcp|unix)\(`), `<redacted>@${1}(`},
	{regexp.MustCompile(`(?i)\b[^\s:/@]+:\S+@/`), `<redacted>@/`},
	{regexp.MustCompile(`(?i)\bauthorization\s*[:=]\s*(?:bearer|basic|digest)\s+[^\s,;]+`), `Authorization=<redacted>`},
	{regexp.MustCompile(`(?i)\bbearer\s+[^\s,;]+`), `Bearer <redacted>`},
	{regexp.MustCompile(`(?i)\bidentified\s+by\s+("[^"]*"|'[^']*'|[^\s,;]+)`), `IDENTIFIED BY <redacted>`},
	{regexp.MustCompile(`(?i)\b(password|passwd|pwd|token|secret|api[_-]?key|authorization)\s*[:=]\s*("[^"]*"|'[^']*'|[^\s,;]+)`), `${1}=<redacted>`},
}

func sanitizeMigrationErrorText(value string) string {
	message := strings.Map(func(character rune) rune {
		switch character {
		case '\n', '\r', '\t':
			return ' '
		}
		if unicode.IsControl(character) || unicode.In(character, unicode.Cf) {
			return '\uFFFD'
		}
		return character
	}, value)
	message = strings.Join(strings.Fields(message), " ")
	for _, secret := range migrationSecretPatterns {
		message = secret.pattern.ReplaceAllString(message, secret.replacement)
	}
	const limit = 2000
	runes := []rune(message)
	if len(runes) > limit {
		message = string(runes[:limit])
	}
	return message
}

func containsAll(values map[string]struct{}, required []string) bool {
	for _, value := range required {
		if _, exists := values[value]; !exists {
			return false
		}
	}
	return true
}

func sortedKeys(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
