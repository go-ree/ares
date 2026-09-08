package db

import "fmt"

const (
	workerLeaseMigrationVersion = "20260908_001_worker_leases"
	workerLeaseMigrationPayload = "multi-replica-worker-leases-v1|database-clock-due-scheduling|task-owner-expiry-fencing|persistent-poll-backoff|integration-revision-cas|provider-fence-singletons"
	workerLeaseDataContractID   = "multi-replica-worker-leases-v1"
	workerLeaseMaxPollFailures  = 31
	workerLeaseTaskDDL          = `ALTER TABLE task_record
		ADD COLUMN next_poll_at DATETIME(6) NULL DEFAULT NULL,
		ADD COLUMN lease_owner VARBINARY(64) NULL DEFAULT NULL,
		ADD COLUMN lease_expires_at DATETIME(6) NULL DEFAULT NULL,
		ADD COLUMN lease_fencing_token BIGINT UNSIGNED NOT NULL DEFAULT 0,
		ADD COLUMN poll_failure_count INT UNSIGNED NOT NULL DEFAULT 0,
		ADD INDEX idx_task_worker_due (engine_version, deleted_at, next_poll_at, task_id)`
	workerLeaseIntegrationDDL = `ALTER TABLE integration_settings
		ADD COLUMN revision BIGINT UNSIGNED NOT NULL DEFAULT 1`
	workerLeaseIntegrationDefaultsSQL = `INSERT IGNORE INTO integration_settings
		(provider, config_data, revision) VALUES
		('jenkins', '{"enabled":false,"address":"","username":"","timeout_seconds":15}', 1),
		('kubernetes', '{"enabled":false,"timeout_seconds":15,"clusters":[]}', 1)`
	workerLeaseBackfillSQL = `UPDATE task_record
		SET next_poll_at = UTC_TIMESTAMP(6)
		WHERE engine_version = 2 AND deleted_at IS NULL
			AND BINARY status IN ('queued', 'running')
			AND next_poll_at IS NULL`
)

func newWorkerLeaseSchemaMigration(implementationID string) schemaMigration {
	return schemaMigration{
		epoch: 7, version: workerLeaseMigrationVersion,
		description: "建立多副本 Worker 租约、fencing 与集成配置版本", compatibleMin: 7, compatibleMax: 7,
		payload:          workerLeaseMigrationPayload,
		implementationID: implementationID,
		preflight:        func(session *migrationSession) error { return session.verifyWorkerLeaseResumeState() },
		up:               func(session *migrationSession) error { return session.migrateWorkerLeases() },
		verify:           func(session *migrationSession) error { return session.verifyWorkerLeasePostconditions() },
	}
}

// migrateWorkerLeases has two recoverable MySQL DDL boundaries: the task
// scheduling/lease shape and the integration revision. The default provider
// rows and final UPDATE are deliberately idempotent so a dirty migration can
// safely repeat them after an interruption without overwriting Web settings or
// changing v1, terminal, or soft-deleted tasks.
func (s *migrationSession) migrateWorkerLeases() error {
	var taskShapeExists bool
	if err := s.queryScalar(`SELECT EXISTS(
		SELECT 1 FROM information_schema.COLUMNS
		WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'task_record'
			AND COLUMN_NAME = 'next_poll_at'
	)`, &taskShapeExists); err != nil {
		return fmt.Errorf("inspect task_record worker lease migration boundary: %w", err)
	}
	if !taskShapeExists {
		if err := s.execMigrationStatement(workerLeaseTaskDDL); err != nil {
			return err
		}
	}

	var integrationRevisionExists bool
	if err := s.queryScalar(`SELECT EXISTS(
		SELECT 1 FROM information_schema.COLUMNS
		WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'integration_settings'
			AND COLUMN_NAME = 'revision'
	)`, &integrationRevisionExists); err != nil {
		return fmt.Errorf("inspect integration_settings.revision migration boundary: %w", err)
	}
	if !integrationRevisionExists {
		if err := s.execMigrationStatement(workerLeaseIntegrationDDL); err != nil {
			return err
		}
	}
	if err := s.execMigrationStatement(workerLeaseIntegrationDefaultsSQL); err != nil {
		return err
	}

	if err := s.execMigrationStatement(workerLeaseBackfillSQL); err != nil {
		return err
	}
	return s.verifyWorkerLeasePostconditions()
}

func (s *migrationSession) verifyWorkerLeaseResumeState() error {
	if err := s.verifySemanticSchemaStates(
		"Worker 租约迁移恢复状态", epoch7ResumeSchemaStates()); err != nil {
		return err
	}
	if err := s.verifyEpochDataContracts(6); err != nil {
		return err
	}
	return s.verifyStoredWorkflowChecksums()
}

func (s *migrationSession) verifyWorkerLeasePostconditions() error {
	if err := s.verifySemanticSchema(
		"Worker 租约迁移后置条件", epoch7SemanticSchemaManifest); err != nil {
		return err
	}
	if err := s.verifyEpochDataContracts(7); err != nil {
		return err
	}
	return s.verifyStoredWorkflowChecksums()
}

func initializeEpoch7SemanticSchemaManifest() {
	epoch7SemanticSchemaManifest = cloneSemanticSchemaManifest(epoch6SemanticSchemaManifest)

	task := epoch7SemanticSchemaManifest.tables["task_record"]
	task.columns = append(task.columns,
		"next_poll_at", "lease_owner", "lease_expires_at",
		"lease_fencing_token", "poll_failure_count")
	for name, definition := range mustParseFullColumnManifest("task_record", `
next_poll_at|datetime(6)|YES|<NULL>||
lease_owner|varbinary(64)|YES|<NULL>||
lease_expires_at|datetime(6)|YES|<NULL>||
lease_fencing_token|bigint unsigned|NO|0||
poll_failure_count|int unsigned|NO|0||`) {
		task.critical[name] = definition
	}
	task.indexes = append(task.indexes,
		regularIndex("engine_version", "deleted_at", "next_poll_at", "task_id"))
	epoch7SemanticSchemaManifest.tables["task_record"] = task

	integration := epoch7SemanticSchemaManifest.tables["integration_settings"]
	integration.columns = append(integration.columns, "revision")
	for name, definition := range mustParseFullColumnManifest("integration_settings",
		"revision|bigint unsigned|NO|1||") {
		integration.critical[name] = definition
	}
	epoch7SemanticSchemaManifest.tables["integration_settings"] = integration
}

func epoch7ResumeSchemaStates() []semanticSchemaManifest {
	current := cloneSemanticSchemaManifest(epoch6SemanticSchemaManifest)
	states := []semanticSchemaManifest{cloneSemanticSchemaManifest(current)}

	current.tables["task_record"] = cloneSchemaTableManifest(
		epoch7SemanticSchemaManifest.tables["task_record"])
	states = append(states, cloneSemanticSchemaManifest(current))

	current.tables["integration_settings"] = cloneSchemaTableManifest(
		epoch7SemanticSchemaManifest.tables["integration_settings"])
	states = append(states, cloneSemanticSchemaManifest(current))
	return states
}

type workerLeaseRowCheck struct {
	problem string
	query   string
}

func workerLeaseRowChecks() []workerLeaseRowCheck {
	return []workerLeaseRowCheck{
		{
			problem: "active v2 tasks must have a next poll time",
			query: `SELECT COUNT(*) FROM task_record
				WHERE engine_version = 2 AND deleted_at IS NULL
					AND BINARY status IN ('queued', 'running')
					AND next_poll_at IS NULL`,
		},
		{
			problem: "v1, terminal, unknown-status, and soft-deleted tasks must not be scheduled or leased",
			query: `SELECT COUNT(*) FROM task_record
				WHERE (engine_version <> 2 OR deleted_at IS NOT NULL OR status IS NULL
					OR BINARY status NOT IN ('queued', 'running'))
					AND (next_poll_at IS NOT NULL OR lease_owner IS NOT NULL
						OR lease_expires_at IS NOT NULL)`,
		},
		{
			problem: "worker lease owner and expiry must both be null or both be present",
			query: `SELECT COUNT(*) FROM task_record
				WHERE (lease_owner IS NULL AND lease_expires_at IS NOT NULL)
					OR (lease_owner IS NOT NULL AND lease_expires_at IS NULL)`,
		},
		{
			problem: "held worker leases must have an owner, fencing token, and matching next poll time",
			query: `SELECT COUNT(*) FROM task_record
				WHERE lease_owner IS NOT NULL AND (
					OCTET_LENGTH(lease_owner) = 0 OR lease_fencing_token = 0
					OR NOT (next_poll_at <=> lease_expires_at)
				)`,
		},
		{
			problem: "worker poll failure count exceeds its saturation limit",
			query: fmt.Sprintf(`SELECT COUNT(*) FROM task_record
				WHERE poll_failure_count > %d`, workerLeaseMaxPollFailures),
		},
		{
			problem: "integration setting revisions must start at one",
			query:   `SELECT COUNT(*) FROM integration_settings WHERE revision = 0`,
		},
		{
			problem: "built-in integration provider fence rows must exist",
			query: `SELECT COUNT(*) FROM (
					SELECT 'jenkins' AS provider UNION ALL SELECT 'kubernetes'
				) required_provider
				LEFT JOIN integration_settings configured
					ON configured.provider = required_provider.provider
				WHERE configured.provider IS NULL`,
		},
	}
}

func (s *migrationSession) verifyWorkerLeaseRows() error {
	checks := workerLeaseRowChecks()
	problems := make([]string, 0, len(checks))
	for _, check := range checks {
		var count int64
		if err := s.queryScalar(check.query, &count); err != nil {
			return fmt.Errorf("verify worker lease data: %w", err)
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
