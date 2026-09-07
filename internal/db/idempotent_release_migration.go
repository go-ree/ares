package db

import (
	"context"
	"fmt"
)

const (
	idempotentReleaseMigrationVersion = "20260907_001_idempotent_releases"
	idempotentReleaseMigrationPayload = "idempotent-releases-v1|app-config-task-target|two-append-only-receipt-tables|atomic-ordered-results|strict-release-receipt-contracts"
	idempotentReleaseDataContractID   = "idempotent-release-receipts-v1"
	idempotentReleaseTaskTargetDDL    = `ALTER TABLE task_record
		ADD COLUMN app_config_id INT NULL AFTER publisher_user_id,
		ADD INDEX idx_task_app_config (app_config_id)`
)

func newIdempotentReleaseSchemaMigration(implementationID string) schemaMigration {
	return schemaMigration{
		epoch: 6, version: idempotentReleaseMigrationVersion,
		description: "建立 AppConfig 发布目标与原子幂等回执", compatibleMin: 6, compatibleMax: 6,
		payload:          idempotentReleaseMigrationPayload,
		implementationID: implementationID,
		preflight:        func(session *migrationSession) error { return session.verifyIdempotentReleaseResumeState() },
		up:               func(session *migrationSession) error { return session.migrateIdempotentRelease() },
		verify:           func(session *migrationSession) error { return session.verifyIdempotentReleasePostconditions() },
	}
}

// migrateIdempotentRelease has exactly three recoverable MySQL DDL boundaries:
// task target metadata, the command receipt table, and its ordered result table.
// MySQL 8.4 makes each statement atomic; the resume preflight admits only the
// complete shapes after each boundary.
func (s *migrationSession) migrateIdempotentRelease() error {
	var taskTargetExists bool
	if err := s.queryScalar(`SELECT EXISTS(
		SELECT 1 FROM information_schema.COLUMNS
		WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'task_record'
			AND COLUMN_NAME = 'app_config_id'
	)`, &taskTargetExists); err != nil {
		return fmt.Errorf("inspect task_record.app_config_id migration boundary: %w", err)
	}
	if !taskTargetExists {
		if err := s.execMigrationStatement(idempotentReleaseTaskTargetDDL); err != nil {
			return err
		}
	}

	for _, statement := range idempotentReleaseTables() {
		if err := s.execMigrationStatement(statement); err != nil {
			return err
		}
	}
	return s.verifyIdempotentReleasePostconditions()
}

func idempotentReleaseTables() []string {
	return []string{
		`CREATE TABLE IF NOT EXISTS release_idempotency_records (
			idempotency_id BIGINT NOT NULL AUTO_INCREMENT,
			actor_user_id BIGINT NOT NULL,
			semantic_operation VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
			key_digest BINARY(32) NOT NULL,
			request_digest BINARY(32) NOT NULL,
			item_count SMALLINT UNSIGNED NOT NULL,
			created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
			PRIMARY KEY (idempotency_id),
			UNIQUE KEY uk_release_idempotency_scope_actor_key (semantic_operation, actor_user_id, key_digest),
			KEY idx_release_idempotency_created (created_at, idempotency_id)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci`,
		`CREATE TABLE IF NOT EXISTS release_idempotency_items (
			record_id BIGINT NOT NULL,
			request_index SMALLINT UNSIGNED NOT NULL,
			config_id INT NOT NULL,
			task_id INT NULL,
			workflow_version_id BIGINT NULL,
			outcome VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
			error_code VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL,
			created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
			PRIMARY KEY (record_id, request_index),
			UNIQUE KEY uk_release_idempotency_items_task (task_id),
			KEY idx_release_idempotency_items_config (config_id)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci`,
	}
}

func (s *migrationSession) verifyIdempotentReleaseResumeState() error {
	if err := s.verifySemanticSchemaStates(
		"幂等发布迁移恢复状态", epoch6ResumeSchemaStates()); err != nil {
		return err
	}
	if err := s.verifyEpochDataContracts(5); err != nil {
		return err
	}
	return s.verifyStoredWorkflowChecksums()
}

func (s *migrationSession) verifyIdempotentReleasePostconditions() error {
	if err := s.verifySemanticSchema(
		"幂等发布迁移后置条件", epoch6SemanticSchemaManifest); err != nil {
		return err
	}
	if err := s.verifyEpochDataContracts(6); err != nil {
		return err
	}
	return s.verifyStoredWorkflowChecksums()
}

func (s *migrationSession) epoch6SchemaDiffs(ctx context.Context) ([]string, error) {
	snapshot, err := readSchemaSnapshot(ctx, s.executor)
	if err != nil {
		return nil, err
	}
	return compareSemanticSchema(snapshot, epoch6SemanticSchemaManifest), nil
}

func initializeEpoch6SemanticSchemaManifest() {
	epoch6SemanticSchemaManifest = cloneSemanticSchemaManifest(epoch5SemanticSchemaManifest)

	task := epoch6SemanticSchemaManifest.tables["task_record"]
	task.columns = append(task.columns, "app_config_id")
	for name, definition := range mustParseFullColumnManifest("task_record",
		"app_config_id|int|YES|<NULL>||") {
		task.critical[name] = definition
	}
	task.indexes = append(task.indexes, regularIndex("app_config_id"))
	epoch6SemanticSchemaManifest.tables["task_record"] = task

	for tableName, definition := range epoch6ReleaseTableCatalog() {
		critical := mustParseFullColumnManifest(tableName, definition.specification)
		for columnName, column := range critical {
			if isCharacterColumnType(column.columnType) {
				column.charset = "ascii"
				column.collation = "ascii_bin"
			}
			critical[columnName] = column
		}
		epoch6SemanticSchemaManifest.tables[tableName] = schemaTableManifest{
			columns:   columnNames(definition.columns),
			critical:  critical,
			indexes:   definition.indexes,
			engine:    "InnoDB",
			charset:   "utf8mb4",
			collation: "utf8mb4_0900_ai_ci",
		}
	}
}

type epoch6ReleaseTableDefinition struct {
	columns       string
	specification string
	indexes       []schemaIndexManifest
}

func epoch6ReleaseTableCatalog() map[string]epoch6ReleaseTableDefinition {
	return map[string]epoch6ReleaseTableDefinition{
		"release_idempotency_records": {
			columns: "idempotency_id actor_user_id semantic_operation key_digest request_digest item_count created_at",
			specification: `
idempotency_id|bigint|NO|<NULL>|auto_increment|
actor_user_id|bigint|NO|<NULL>||
semantic_operation|varchar(64)|NO|<NULL>||
key_digest|binary(32)|NO|<NULL>||
request_digest|binary(32)|NO|<NULL>||
item_count|smallint unsigned|NO|<NULL>||
created_at|datetime(6)|NO|CURRENT_TIMESTAMP(6)||`,
			indexes: []schemaIndexManifest{
				primaryIndex("idempotency_id"),
				uniqueIndex("semantic_operation", "actor_user_id", "key_digest"),
				regularIndex("created_at", "idempotency_id"),
			},
		},
		"release_idempotency_items": {
			columns: "record_id request_index config_id task_id workflow_version_id outcome error_code created_at",
			specification: `
record_id|bigint|NO|<NULL>||
request_index|smallint unsigned|NO|<NULL>||
config_id|int|NO|<NULL>||
task_id|int|YES|<NULL>||
workflow_version_id|bigint|YES|<NULL>||
outcome|varchar(16)|NO|<NULL>||
error_code|varchar(64)|YES|<NULL>||
created_at|datetime(6)|NO|CURRENT_TIMESTAMP(6)||`,
			indexes: []schemaIndexManifest{
				primaryIndex("record_id", "request_index"),
				uniqueIndex("task_id"),
				regularIndex("config_id"),
			},
		},
	}
}

func epoch6ResumeSchemaStates() []semanticSchemaManifest {
	current := cloneSemanticSchemaManifest(epoch5SemanticSchemaManifest)
	states := []semanticSchemaManifest{cloneSemanticSchemaManifest(current)}

	current.tables["task_record"] = cloneSchemaTableManifest(
		epoch6SemanticSchemaManifest.tables["task_record"])
	states = append(states, cloneSemanticSchemaManifest(current))

	for _, tableName := range []string{
		"release_idempotency_records", "release_idempotency_items",
	} {
		current.tables[tableName] = cloneSchemaTableManifest(
			epoch6SemanticSchemaManifest.tables[tableName])
		states = append(states, cloneSemanticSchemaManifest(current))
	}
	return states
}

type idempotentReleaseRowCheck struct {
	problem string
	query   string
}

func idempotentReleaseRowChecks() []idempotentReleaseRowCheck {
	return []idempotentReleaseRowCheck{
		{
			problem: "release idempotency records must use a known operation and actor",
			query: `SELECT COUNT(*)
				FROM release_idempotency_records receipt
				LEFT JOIN auth_users actor ON actor.user_id = receipt.actor_user_id
				WHERE receipt.actor_user_id <= 0 OR actor.user_id IS NULL
					OR BINARY receipt.semantic_operation NOT IN ('release.create@v1', 'release.batch.create@v1')
					OR receipt.item_count < 1 OR receipt.item_count > 100
					OR (BINARY receipt.semantic_operation = 'release.create@v1' AND receipt.item_count <> 1)`,
		},
		{
			problem: "release idempotency records must own a complete contiguous item range",
			query: `SELECT COUNT(*)
				FROM release_idempotency_records receipt
				LEFT JOIN (
					SELECT record_id, COUNT(*) AS actual_count,
						MIN(request_index) AS first_index, MAX(request_index) AS last_index
					FROM release_idempotency_items GROUP BY record_id
				) item ON item.record_id = receipt.idempotency_id
				WHERE receipt.item_count = 0
					OR COALESCE(item.actual_count, 0) <> receipt.item_count
					OR COALESCE(item.first_index, -1) <> 0
					OR COALESCE(item.last_index, -1) <> CAST(receipt.item_count AS SIGNED) - 1`,
		},
		{
			problem: "release idempotency items must resolve to a record",
			query: `SELECT COUNT(*)
				FROM release_idempotency_items item
				LEFT JOIN release_idempotency_records receipt
					ON receipt.idempotency_id = item.record_id
				WHERE receipt.idempotency_id IS NULL OR item.config_id <= 0`,
		},
		{
			problem: "release idempotency item outcome must be accepted or rejected",
			query: `SELECT COUNT(*) FROM release_idempotency_items
				WHERE BINARY outcome NOT IN ('accepted', 'rejected')`,
		},
		{
			problem: "single release idempotency receipt must contain one accepted item",
			query: `SELECT COUNT(*)
				FROM release_idempotency_items item
				JOIN release_idempotency_records receipt
					ON receipt.idempotency_id = item.record_id
				WHERE BINARY receipt.semantic_operation = 'release.create@v1'
					AND BINARY item.outcome <> 'accepted'`,
		},
		{
			problem: "accepted release receipt must resolve to its actor, target, task and workflow",
			query: `SELECT COUNT(*)
				FROM release_idempotency_items item
				JOIN release_idempotency_records receipt
					ON receipt.idempotency_id = item.record_id
				LEFT JOIN task_record task ON task.task_id = item.task_id
				LEFT JOIN app_configs target ON target.config_id = item.config_id
				LEFT JOIN release_workflow_versions workflow_version
					ON workflow_version.version_id = item.workflow_version_id
				WHERE BINARY item.outcome = 'accepted' AND (
					item.task_id IS NULL OR item.workflow_version_id IS NULL
					OR item.error_code IS NOT NULL OR task.task_id IS NULL OR target.config_id IS NULL
					OR workflow_version.version_id IS NULL OR task.engine_version < 2
					OR NOT (task.app_config_id <=> item.config_id)
					OR NOT (task.workflow_version_id <=> item.workflow_version_id)
					OR NOT (task.publisher_user_id <=> receipt.actor_user_id)
				)`,
		},
		{
			problem: "rejected release receipt must contain only a stable error code",
			query: `SELECT COUNT(*) FROM release_idempotency_items
				WHERE BINARY outcome = 'rejected' AND (
					task_id IS NOT NULL OR workflow_version_id IS NOT NULL
					OR error_code IS NULL OR BINARY error_code NOT IN (
						'release_target_not_found', 'environment_disabled',
						'workflow_not_configured', 'workflow_version_changed',
						'workflow_invalid', 'executor_unavailable'
					)
				)`,
		},
	}
}

func (s *migrationSession) verifyIdempotentReleaseRows() error {
	checks := idempotentReleaseRowChecks()
	problems := make([]string, 0, len(checks))
	for _, check := range checks {
		var count int64
		if err := s.queryScalar(check.query, &count); err != nil {
			return fmt.Errorf("verify idempotent release receipts: %w", err)
		}
		if count != 0 {
			problems = append(problems, fmt.Sprintf("%s (rows=%d)", check.problem, count))
		}
	}
	if len(problems) > 0 {
		return &SchemaStateError{Problems: problems}
	}
	return nil
}
