package db

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/go-ree/ares/internal/canonicaljson"
)

const (
	authRBACMigrationVersion = "20260904_001_auth_rbac_audit"
	authRBACMigrationPayload = "auth-rbac-audit-v2|6-managed-tables|opaque-session-and-oidc-digests|bootstrap-singleton|append-only-audit|stable-task-and-workflow-actors|canonical-workflow-checksums"
)

func newAuthRBACSchemaMigration(implementationID string) schemaMigration {
	return schemaMigration{
		epoch: 5, version: authRBACMigrationVersion,
		description: "建立认证、RBAC 与只增审计数据边界", compatibleMin: 5, compatibleMax: 5,
		payload:          authRBACMigrationPayload,
		implementationID: implementationID,
		preflight:        func(session *migrationSession) error { return session.verifyAuthRBACResumeState() },
		up:               func(session *migrationSession) error { return session.migrateAuthRBAC() },
		verify:           func(session *migrationSession) error { return session.verifyAuthRBACPostconditions() },
	}
}

func (s *migrationSession) migrateAuthRBAC() error {
	for index, statement := range authRBACTables() {
		if err := s.execMigrationStatement(statement); err != nil {
			return err
		}
		if index == 4 {
			if err := s.ensureAuthBootstrapSingleton(); err != nil {
				return err
			}
		}
	}
	if err := s.ensureMigrationColumn("task_record", "publisher_user_id",
		"BIGINT NULL AFTER `publisher`"); err != nil {
		return err
	}
	if err := s.ensureMigrationColumn("release_workflow_versions", "created_by_user_id",
		"BIGINT NULL AFTER `created_by`"); err != nil {
		return err
	}
	if err := s.normalizeStoredWorkflowChecksums(); err != nil {
		return err
	}
	return s.verifyAuthRBACPostconditions()
}

// authRBACTables returns statements in the same order as
// epoch5ResumeSchemaStates. MySQL 8.4 DDL is atomic at each statement boundary;
// the resume preflight admits exactly those boundaries and no partial shape.
func authRBACTables() []string {
	return []string{
		`CREATE TABLE IF NOT EXISTS auth_users (
			user_id BIGINT NOT NULL AUTO_INCREMENT,
			username VARCHAR(100) NOT NULL,
			display_name VARCHAR(255) NOT NULL,
			email VARCHAR(320) NULL,
			password_hash VARCHAR(255) NULL,
			role VARCHAR(32) NOT NULL DEFAULT 'viewer',
			auth_source VARCHAR(32) NOT NULL,
			enabled TINYINT(1) NOT NULL DEFAULT 1,
			last_login_at DATETIME(6) NULL,
			created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
			updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
			PRIMARY KEY (user_id),
			UNIQUE KEY uk_auth_users_username (username)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci`,
		`CREATE TABLE IF NOT EXISTS auth_identities (
			identity_id BIGINT NOT NULL AUTO_INCREMENT,
			user_id BIGINT NOT NULL,
			issuer VARCHAR(2048) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL,
			subject VARCHAR(255) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL,
			identity_hash BINARY(32) NOT NULL,
			created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
			PRIMARY KEY (identity_id),
			UNIQUE KEY uk_auth_identities_hash (identity_hash),
			KEY idx_auth_identities_user (user_id)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci`,
		`CREATE TABLE IF NOT EXISTS auth_sessions (
			session_hash BINARY(32) NOT NULL,
			user_id BIGINT NOT NULL,
			expires_at DATETIME(6) NOT NULL,
			revoked_at DATETIME(6) NULL,
			last_seen_at DATETIME(6) NOT NULL,
			created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
			PRIMARY KEY (session_hash),
			KEY idx_auth_sessions_user_state (user_id, revoked_at, expires_at),
			KEY idx_auth_sessions_expires (expires_at)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci`,
		`CREATE TABLE IF NOT EXISTS auth_oidc_flows (
			state_hash BINARY(32) NOT NULL,
			nonce_hash BINARY(32) NOT NULL,
			binding_hash BINARY(32) NOT NULL,
			verifier_ciphertext TEXT NOT NULL,
			return_path VARCHAR(512) NOT NULL DEFAULT '/',
			expires_at DATETIME(6) NOT NULL,
			consumed_at DATETIME(6) NULL,
			created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
			PRIMARY KEY (state_hash),
			KEY idx_auth_oidc_flows_expires (expires_at)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci`,
		`CREATE TABLE IF NOT EXISTS auth_bootstrap_state (
			id TINYINT NOT NULL,
			completed_at DATETIME(6) NULL,
			completed_by BIGINT NULL,
			PRIMARY KEY (id)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci`,
		`CREATE TABLE IF NOT EXISTS audit_events (
			audit_id BIGINT NOT NULL AUTO_INCREMENT,
			actor_user_id BIGINT NULL,
			actor_username VARCHAR(100) NOT NULL,
			actor_display_name VARCHAR(255) NOT NULL,
			auth_source VARCHAR(32) NOT NULL,
			action VARCHAR(100) NOT NULL,
			resource_type VARCHAR(100) NOT NULL,
			resource_id VARCHAR(255) NOT NULL,
			result VARCHAR(32) NOT NULL,
			http_status SMALLINT UNSIGNED NOT NULL,
			request_id VARCHAR(64) NOT NULL,
			created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
			PRIMARY KEY (audit_id),
			KEY idx_audit_created (created_at, audit_id),
			KEY idx_audit_actor_time (actor_user_id, created_at, audit_id),
			KEY idx_audit_resource_time (resource_type, resource_id, created_at, audit_id)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci`,
	}
}

func (s *migrationSession) ensureAuthBootstrapSingleton() error {
	if err := s.verifyAuthBootstrapRows(true); err != nil {
		return err
	}
	ctx, cancel := s.operationContext()
	defer cancel()
	if _, err := s.executor.ExecContext(ctx, `INSERT INTO auth_bootstrap_state
		(id, completed_at, completed_by)
		SELECT 1, NULL, NULL
		WHERE NOT EXISTS (SELECT 1 FROM auth_bootstrap_state)`); err != nil {
		return fmt.Errorf("initialize auth bootstrap singleton: %w", err)
	}
	return s.verifyAuthBootstrapRows(false)
}

func (s *migrationSession) verifyAuthRBACResumeState() error {
	if err := s.verifySemanticSchemaStates("认证与审计迁移恢复状态", epoch5ResumeSchemaStates()); err != nil {
		return err
	}
	if err := s.verifyEpochDataContracts(4); err != nil {
		return err
	}

	ctx, cancel := s.operationContext()
	tables, err := s.databaseTables(ctx)
	cancel()
	if err != nil {
		return err
	}
	if _, exists := tables["auth_bootstrap_state"]; !exists {
		return nil
	}
	// The empty form is one legitimate crash boundary immediately after the
	// bootstrap table DDL. Once any later statement exists, the singleton INSERT
	// must already have completed.
	_, auditExists := tables["audit_events"]
	return s.verifyAuthBootstrapRows(!auditExists)
}

func (s *migrationSession) verifyAuthRBACPostconditions() error {
	if err := s.verifySemanticSchema("认证与审计迁移后置条件", epoch5SemanticSchemaManifest); err != nil {
		return err
	}
	if err := s.verifyEpochDataContracts(5); err != nil {
		return err
	}
	return s.verifyStoredWorkflowChecksums()
}

type storedWorkflowChecksum struct {
	versionID int64
	spec      []byte
	checksum  string
}

func (s *migrationSession) readStoredWorkflowChecksums() ([]storedWorkflowChecksum, error) {
	ctx, cancel := s.operationContext()
	defer cancel()
	rows, err := s.executor.QueryContext(ctx,
		`SELECT version_id, spec, checksum FROM release_workflow_versions ORDER BY version_id`)
	if err != nil {
		return nil, fmt.Errorf("read workflow checksums: %w", err)
	}
	defer rows.Close()
	result := make([]storedWorkflowChecksum, 0)
	for rows.Next() {
		var item storedWorkflowChecksum
		if err := rows.Scan(&item.versionID, &item.spec, &item.checksum); err != nil {
			return nil, fmt.Errorf("scan workflow checksum: %w", err)
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate workflow checksums: %w", err)
	}
	return result, nil
}

func canonicalWorkflowChecksum(spec []byte) (string, error) {
	canonical, err := canonicaljson.Canonicalize(spec)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(canonical)
	return hex.EncodeToString(digest[:]), nil
}

func (s *migrationSession) normalizeStoredWorkflowChecksums() error {
	rows, err := s.readStoredWorkflowChecksums()
	if err != nil {
		return err
	}
	for _, row := range rows {
		checksum, err := canonicalWorkflowChecksum(row.spec)
		if err != nil {
			return fmt.Errorf("canonicalize workflow version %d: %w", row.versionID, err)
		}
		if row.checksum == checksum {
			continue
		}
		ctx, cancel := s.operationContext()
		result, updateErr := s.executor.ExecContext(ctx,
			`UPDATE release_workflow_versions SET checksum = ? WHERE version_id = ?`, checksum, row.versionID)
		cancel()
		if updateErr != nil {
			return fmt.Errorf("normalize workflow version %d checksum: %w", row.versionID, updateErr)
		}
		affected, affectedErr := result.RowsAffected()
		if affectedErr != nil {
			return fmt.Errorf("inspect workflow version %d checksum update: %w", row.versionID, affectedErr)
		}
		if affected != 1 {
			return fmt.Errorf("normalize workflow version %d checksum affected %d rows", row.versionID, affected)
		}
	}
	return nil
}

func (s *migrationSession) verifyStoredWorkflowChecksums() error {
	rows, err := s.readStoredWorkflowChecksums()
	if err != nil {
		return err
	}
	for _, row := range rows {
		checksum, err := canonicalWorkflowChecksum(row.spec)
		if err != nil {
			return &SchemaStateError{Problems: []string{fmt.Sprintf(
				"workflow version %d cannot be canonicalized", row.versionID)}}
		}
		if row.checksum != checksum {
			return &SchemaStateError{Problems: []string{fmt.Sprintf(
				"workflow version %d checksum does not match canonical spec", row.versionID)}}
		}
	}
	return nil
}

func (s *migrationSession) verifyAuthBootstrapRows(allowEmpty bool) error {
	ctx, cancel := s.operationContext()
	defer cancel()
	var rows, singletonRows, inconsistentRows, missingCompletedUsers int
	if err := s.executor.QueryRowContext(ctx, `SELECT
		COUNT(*),
		COALESCE(SUM(bootstrap_state.id = 1), 0),
		COALESCE(SUM((bootstrap_state.completed_at IS NULL) <> (bootstrap_state.completed_by IS NULL)), 0),
		COALESCE(SUM(bootstrap_state.completed_by IS NOT NULL AND auth_user.user_id IS NULL), 0)
		FROM auth_bootstrap_state bootstrap_state
		LEFT JOIN auth_users auth_user ON auth_user.user_id = bootstrap_state.completed_by`).Scan(
		&rows, &singletonRows, &inconsistentRows, &missingCompletedUsers); err != nil {
		return fmt.Errorf("verify auth bootstrap singleton: %w", err)
	}
	if allowEmpty && rows == 0 {
		return nil
	}
	problems := make([]string, 0, 3)
	if rows != 1 || singletonRows != 1 {
		problems = append(problems, fmt.Sprintf(
			"auth_bootstrap_state must contain exactly singleton id=1 (rows=%d singleton_rows=%d)",
			rows, singletonRows))
	}
	if inconsistentRows != 0 {
		problems = append(problems, "auth_bootstrap_state completion time and user must transition together")
	}
	if missingCompletedUsers != 0 {
		problems = append(problems, "auth_bootstrap_state completed_by does not resolve to auth_users")
	}
	if len(problems) > 0 {
		return &SchemaStateError{Problems: problems}
	}
	return nil
}

func (s *migrationSession) epoch5SchemaDiffs(ctx context.Context) ([]string, error) {
	snapshot, err := readSchemaSnapshot(ctx, s.executor)
	if err != nil {
		return nil, err
	}
	return compareSemanticSchema(snapshot, epoch5SemanticSchemaManifest), nil
}

func initializeEpoch5SemanticSchemaManifest() {
	epoch5SemanticSchemaManifest = cloneSemanticSchemaManifest(epoch4SemanticSchemaManifest)

	for _, addition := range []struct {
		table, column, specification string
	}{
		{"task_record", "publisher_user_id", "publisher_user_id|bigint|YES|<NULL>||"},
		{"release_workflow_versions", "created_by_user_id", "created_by_user_id|bigint|YES|<NULL>||"},
	} {
		table := epoch5SemanticSchemaManifest.tables[addition.table]
		table.columns = append(table.columns, addition.column)
		for name, definition := range mustParseFullColumnManifest(addition.table, addition.specification) {
			table.critical[name] = definition
		}
		epoch5SemanticSchemaManifest.tables[addition.table] = table
	}

	for tableName, definition := range epoch5AuthTableCatalog() {
		critical := mustParseFullColumnManifest(tableName, definition.specification)
		for columnName, column := range critical {
			if isCharacterColumnType(column.columnType) {
				column.charset = "utf8mb4"
				column.collation = "utf8mb4_0900_ai_ci"
			}
			critical[columnName] = column
		}
		if tableName == "auth_identities" {
			for _, columnName := range []string{"issuer", "subject"} {
				column := critical[columnName]
				column.collation = "utf8mb4_bin"
				critical[columnName] = column
			}
		}
		epoch5SemanticSchemaManifest.tables[tableName] = schemaTableManifest{
			columns:   columnNames(definition.columns),
			critical:  critical,
			indexes:   definition.indexes,
			engine:    "InnoDB",
			charset:   "utf8mb4",
			collation: "utf8mb4_0900_ai_ci",
		}
	}
}

type epoch5AuthTableDefinition struct {
	columns       string
	specification string
	indexes       []schemaIndexManifest
}

func epoch5AuthTableCatalog() map[string]epoch5AuthTableDefinition {
	return map[string]epoch5AuthTableDefinition{
		"auth_users": {
			columns: "user_id username display_name email password_hash role auth_source enabled last_login_at created_at updated_at",
			specification: `
user_id|bigint|NO|<NULL>|auto_increment|
username|varchar(100)|NO|<NULL>||
display_name|varchar(255)|NO|<NULL>||
email|varchar(320)|YES|<NULL>||
password_hash|varchar(255)|YES|<NULL>||
role|varchar(32)|NO|viewer||
auth_source|varchar(32)|NO|<NULL>||
enabled|tinyint(1)|NO|1||
last_login_at|datetime(6)|YES|<NULL>||
created_at|datetime(6)|NO|CURRENT_TIMESTAMP(6)||
updated_at|datetime(6)|NO|CURRENT_TIMESTAMP(6)|on update CURRENT_TIMESTAMP(6)|`,
			indexes: []schemaIndexManifest{
				primaryIndex("user_id"),
				uniqueIndex("username"),
			},
		},
		"auth_identities": {
			columns: "identity_id user_id issuer subject identity_hash created_at",
			specification: `
identity_id|bigint|NO|<NULL>|auto_increment|
user_id|bigint|NO|<NULL>||
issuer|varchar(2048)|NO|<NULL>||
subject|varchar(255)|NO|<NULL>||
identity_hash|binary(32)|NO|<NULL>||
created_at|datetime(6)|NO|CURRENT_TIMESTAMP(6)||`,
			indexes: []schemaIndexManifest{
				primaryIndex("identity_id"),
				uniqueIndex("identity_hash"),
				regularIndex("user_id"),
			},
		},
		"auth_sessions": {
			columns: "session_hash user_id expires_at revoked_at last_seen_at created_at",
			specification: `
session_hash|binary(32)|NO|<NULL>||
user_id|bigint|NO|<NULL>||
expires_at|datetime(6)|NO|<NULL>||
revoked_at|datetime(6)|YES|<NULL>||
last_seen_at|datetime(6)|NO|<NULL>||
created_at|datetime(6)|NO|CURRENT_TIMESTAMP(6)||`,
			indexes: []schemaIndexManifest{
				primaryIndex("session_hash"),
				regularIndex("user_id", "revoked_at", "expires_at"),
				regularIndex("expires_at"),
			},
		},
		"auth_oidc_flows": {
			columns: "state_hash nonce_hash binding_hash verifier_ciphertext return_path expires_at consumed_at created_at",
			specification: `
state_hash|binary(32)|NO|<NULL>||
nonce_hash|binary(32)|NO|<NULL>||
binding_hash|binary(32)|NO|<NULL>||
verifier_ciphertext|text|NO|<NULL>||
return_path|varchar(512)|NO|/||
expires_at|datetime(6)|NO|<NULL>||
consumed_at|datetime(6)|YES|<NULL>||
created_at|datetime(6)|NO|CURRENT_TIMESTAMP(6)||`,
			indexes: []schemaIndexManifest{
				primaryIndex("state_hash"),
				regularIndex("expires_at"),
			},
		},
		"auth_bootstrap_state": {
			columns: "id completed_at completed_by",
			specification: `
id|tinyint|NO|<NULL>||
completed_at|datetime(6)|YES|<NULL>||
completed_by|bigint|YES|<NULL>||`,
			indexes: []schemaIndexManifest{primaryIndex("id")},
		},
		"audit_events": {
			columns: "audit_id actor_user_id actor_username actor_display_name auth_source action resource_type resource_id result http_status request_id created_at",
			specification: `
audit_id|bigint|NO|<NULL>|auto_increment|
actor_user_id|bigint|YES|<NULL>||
actor_username|varchar(100)|NO|<NULL>||
actor_display_name|varchar(255)|NO|<NULL>||
auth_source|varchar(32)|NO|<NULL>||
action|varchar(100)|NO|<NULL>||
resource_type|varchar(100)|NO|<NULL>||
resource_id|varchar(255)|NO|<NULL>||
result|varchar(32)|NO|<NULL>||
http_status|smallint unsigned|NO|<NULL>||
request_id|varchar(64)|NO|<NULL>||
created_at|datetime(6)|NO|CURRENT_TIMESTAMP(6)||`,
			indexes: []schemaIndexManifest{
				primaryIndex("audit_id"),
				regularIndex("created_at", "audit_id"),
				regularIndex("actor_user_id", "created_at", "audit_id"),
				regularIndex("resource_type", "resource_id", "created_at", "audit_id"),
			},
		},
	}
}

func epoch5ResumeSchemaStates() []semanticSchemaManifest {
	current := cloneSemanticSchemaManifest(epoch4SemanticSchemaManifest)
	states := []semanticSchemaManifest{cloneSemanticSchemaManifest(current)}
	for _, tableName := range []string{
		"auth_users",
		"auth_identities",
		"auth_sessions",
		"auth_oidc_flows",
		"auth_bootstrap_state",
		"audit_events",
	} {
		current.tables[tableName] = cloneSchemaTableManifest(epoch5SemanticSchemaManifest.tables[tableName])
		states = append(states, cloneSemanticSchemaManifest(current))
	}
	addTargetManifestColumn(&current, epoch5SemanticSchemaManifest, "task_record", "publisher_user_id")
	states = append(states, cloneSemanticSchemaManifest(current))
	addTargetManifestColumn(&current, epoch5SemanticSchemaManifest,
		"release_workflow_versions", "created_by_user_id")
	states = append(states, cloneSemanticSchemaManifest(current))
	return states
}
