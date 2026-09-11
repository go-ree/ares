package db

const taskAttemptMigrationVersion = "20260911_001_task_attempts"

const taskAttemptStepDDL = `ALTER TABLE task_step_records
 ADD COLUMN max_attempts INT NOT NULL DEFAULT 1,
 ADD COLUMN retry_delay_seconds INT NOT NULL DEFAULT 1,
 ADD COLUMN retry_max_delay_seconds INT NOT NULL DEFAULT 60,
 ADD COLUMN retry_mode VARCHAR(16) NOT NULL DEFAULT 'automatic',
 ADD COLUMN retry_class VARCHAR(32) NOT NULL DEFAULT '',
 ADD COLUMN retry_at DATETIME(6) NULL DEFAULT NULL`

const taskAttemptTableDDL = `CREATE TABLE IF NOT EXISTS task_step_attempts (
 step_record_id BIGINT NOT NULL,
 attempt INT NOT NULL,
 status VARCHAR(32) NOT NULL,
 idempotency_key VARCHAR(128) NOT NULL,
 retry_class VARCHAR(32) NOT NULL DEFAULT '',
 external_ref JSON NULL,
 output JSON NULL,
 message VARCHAR(1000) NULL,
 started_at DATETIME(6) NULL,
 finished_at DATETIME(6) NULL,
 PRIMARY KEY (step_record_id, attempt)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`

const taskAttemptBackfillSQL = `INSERT IGNORE INTO task_step_attempts
 (step_record_id, attempt, status, idempotency_key, retry_class, external_ref, output, message, started_at, finished_at)
 SELECT step_record_id, attempt, status, CONCAT(task_id, '/', step_key, '/', attempt), '', external_ref, output, message, started_at, finished_at
 FROM task_step_records WHERE status <> 'pending' AND status <> 'skipped'
 AND (started_at IS NOT NULL OR status = 'running' OR finished_at IS NOT NULL)`

func newTaskAttemptSchemaMigration(implementationID string) schemaMigration {
	return schemaMigration{epoch: 8, version: taskAttemptMigrationVersion,
		description: "建立步骤尝试历史与有界重试策略", compatibleMin: 8, compatibleMax: 8,
		payload:          "task-attempts-v1|immutable-finished-history|bounded-retry-policy|database-retry-clock|conservative-backfill",
		implementationID: implementationID,
		preflight: func(s *migrationSession) error {
			if err := s.verifySemanticSchemaStates("尝试历史迁移恢复状态", epoch8ResumeSchemaStates()); err != nil {
				return err
			}
			return s.verifyEpochDataContracts(7)
		},
		up: func(s *migrationSession) error {
			var exists bool
			if err := s.queryScalar(`SELECT EXISTS(SELECT 1 FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'task_step_records' AND COLUMN_NAME = 'max_attempts')`, &exists); err != nil {
				return err
			}
			if !exists {
				if err := s.execMigrationStatement(taskAttemptStepDDL); err != nil {
					return err
				}
			}
			if err := s.execMigrationStatement(taskAttemptTableDDL); err != nil {
				return err
			}
			return s.execMigrationStatement(taskAttemptBackfillSQL)
		},
		verify: func(s *migrationSession) error {
			if err := s.verifySemanticSchema("尝试历史迁移后置条件", epoch8SemanticSchemaManifest); err != nil {
				return err
			}
			if err := s.verifyEpochDataContracts(8); err != nil {
				return err
			}
			return s.verifyStoredWorkflowChecksums()
		},
	}
}

func initializeEpoch8SemanticSchemaManifest() {
	epoch8SemanticSchemaManifest = cloneSemanticSchemaManifest(epoch7SemanticSchemaManifest)
	steps := epoch8SemanticSchemaManifest.tables["task_step_records"]
	for name, column := range mustParseFullColumnManifest("task_step_records", `
max_attempts|int|NO|1||
retry_delay_seconds|int|NO|1||
retry_max_delay_seconds|int|NO|60||
retry_mode|varchar(16)|NO|automatic||
retry_class|varchar(32)|NO|||
retry_at|datetime(6)|YES|<NULL>||`) {
		if isCharacterColumnType(column.columnType) {
			column.charset = "utf8mb4"
			column.collation = "utf8mb4_unicode_ci"
		}
		steps.critical[name] = column
	}
	steps.columns = append(steps.columns, "max_attempts", "retry_delay_seconds", "retry_max_delay_seconds", "retry_mode", "retry_class", "retry_at")
	epoch8SemanticSchemaManifest.tables["task_step_records"] = steps
	critical := mustParseFullColumnManifest("task_step_attempts", `
step_record_id|bigint|NO|<NULL>||
attempt|int|NO|<NULL>||
status|varchar(32)|NO|<NULL>||
idempotency_key|varchar(128)|NO|<NULL>||
retry_class|varchar(32)|NO|||
external_ref|json|YES|<NULL>||
output|json|YES|<NULL>||
message|varchar(1000)|YES|<NULL>||
started_at|datetime(6)|YES|<NULL>||
finished_at|datetime(6)|YES|<NULL>||`)
	for name, column := range critical {
		if isCharacterColumnType(column.columnType) {
			column.charset = "utf8mb4"
			column.collation = "utf8mb4_unicode_ci"
		}
		critical[name] = column
	}
	epoch8SemanticSchemaManifest.tables["task_step_attempts"] = schemaTableManifest{
		columns:  columnNames("step_record_id attempt status idempotency_key retry_class external_ref output message started_at finished_at"),
		critical: critical, indexes: []schemaIndexManifest{primaryIndex("step_record_id", "attempt")},
		engine: "InnoDB", charset: "utf8mb4", collation: "utf8mb4_unicode_ci",
	}
}

func epoch8ResumeSchemaStates() []semanticSchemaManifest {
	current := cloneSemanticSchemaManifest(epoch7SemanticSchemaManifest)
	states := []semanticSchemaManifest{cloneSemanticSchemaManifest(current)}
	current.tables["task_step_records"] = cloneSchemaTableManifest(epoch8SemanticSchemaManifest.tables["task_step_records"])
	states = append(states, cloneSemanticSchemaManifest(current))
	return append(states, cloneSemanticSchemaManifest(epoch8SemanticSchemaManifest))
}

func (s *migrationSession) verifyTaskAttemptRows() error {
	var invalid int64
	if err := s.queryScalar(`SELECT COUNT(*) FROM task_step_records WHERE max_attempts NOT BETWEEN 1 AND 5
 OR retry_delay_seconds NOT BETWEEN 1 AND 3600 OR retry_max_delay_seconds NOT BETWEEN retry_delay_seconds AND 3600
 OR BINARY retry_mode NOT IN ('automatic', 'manual')
 OR BINARY retry_class NOT IN ('', 'no_side_effect', 'completed_safe')
 OR (BINARY status = 'retry_wait' AND (retry_at IS NULL OR attempt >= max_attempts))`, &invalid); err != nil {
		return err
	}
	if invalid != 0 {
		return &SchemaStateError{Problems: []string{"步骤重试策略或到期时间无效"}}
	}
	return nil
}
