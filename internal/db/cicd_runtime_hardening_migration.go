package db

import "fmt"

const cicdRuntimeHardeningMigrationVersion = "20260903_002_cicd_runtime_hardening"

// migrateCICDRuntimeHardening adds the columns and indexes required by the
// workflow runtime. Historical Jenkins build references deliberately remain
// unbound: the legacy schema did not persist the owning Jenkins address, so no
// database-only heuristic can establish that relationship safely.
func migrateCICDRuntimeHardening() error {
	if err := ensureMigrationColumn("task_record", "jenkins_address", "TEXT NULL AFTER `cd_job_name`"); err != nil {
		return err
	}
	return ensureCICDSchemaPostconditions()
}

func ensureCICDSchemaPostconditions() error {
	for _, index := range []struct {
		table, name, columns string
	}{
		{"task_record", "idx_task_workflow_poll", "(`engine_version`, `status`, `deleted_at`, `updated_at`, `task_id`)"},
		{"task_step_records", "idx_step_status_uses", "(`status`, `uses`, `task_id`)"},
	} {
		if err := ensureMigrationIndex(index.table, index.name, index.columns); err != nil {
			return err
		}
	}
	return ensureActiveAppEnvironmentUniqueness()
}

func ensureMigrationIndex(table, index, columns string) error {
	if !safeSQLIdentifier(table) || !safeSQLIdentifier(index) {
		return fmt.Errorf("unsafe migration index identifier %s.%s", table, index)
	}
	var exists bool
	if err := queryScalar(`SELECT EXISTS(
		SELECT 1 FROM information_schema.STATISTICS
		WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ? AND INDEX_NAME = ?
	)`, &exists, table, index); err != nil {
		return fmt.Errorf("inspect migration index %s.%s: %w", table, index, err)
	}
	if exists {
		return nil
	}
	return execMigrationStatement(fmt.Sprintf("ALTER TABLE `%s` ADD INDEX `%s` %s", table, index, columns))
}
