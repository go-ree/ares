package db

import (
	"fmt"
	"strings"
)

const cicdRuntimeHardeningMigrationVersion = "20260903_002_cicd_runtime_hardening"

func newCICDRuntimeHardeningSchemaMigration(implementationID string) schemaMigration {
	return schemaMigration{
		epoch: 3, version: cicdRuntimeHardeningMigrationVersion,
		description: "收口 CI/CD 运行时结构", compatibleMin: 3, compatibleMax: 3,
		payload:          "cicd-runtime-hardening-v1|jenkins-address|poll-and-step-indexes",
		implementationID: implementationID,
		preflight:        func(session *migrationSession) error { return session.verifyCICDRuntimeResumeState() },
		up:               func(session *migrationSession) error { return session.migrateCICDRuntimeHardening() },
		verify:           func(session *migrationSession) error { return session.verifyCICDRuntimePostconditions() },
	}
}

// migrateCICDRuntimeHardening adds the columns and indexes required by the
// workflow runtime. Historical Jenkins build references deliberately remain
// unbound: the legacy schema did not persist the owning Jenkins address, so no
// database-only heuristic can establish that relationship safely.
func (s *migrationSession) migrateCICDRuntimeHardening() error {
	if err := s.ensureMigrationColumn("task_record", "jenkins_address", "TEXT NULL AFTER `cd_job_name`"); err != nil {
		return err
	}
	return s.ensureCICDSchemaPostconditions()
}

func (s *migrationSession) verifyCICDRuntimeResumeState() error {
	if err := s.verifySemanticSchemaStates("CI/CD 运行时迁移恢复状态", epoch3ResumeSchemaStates()); err != nil {
		return err
	}
	return s.verifyEpochDataContracts(2)
}

// verifyCICDRuntimePostconditions is the immutable, complete epoch-3 contract.
// Retained data invariants are composed explicitly and the structural snapshot
// is independent from both adjacent epochs.
func (s *migrationSession) verifyCICDRuntimePostconditions() error {
	if err := s.verifySemanticSchema("CI/CD 运行时迁移后置条件", epoch3SemanticSchemaManifest); err != nil {
		return err
	}
	return s.verifyEpochDataContracts(3)
}

func (s *migrationSession) ensureCICDSchemaPostconditions() error {
	for _, index := range []struct {
		table, name string
		columns     []string
	}{
		{"task_record", "idx_task_workflow_poll", []string{"engine_version", "status", "deleted_at", "updated_at", "task_id"}},
		{"task_step_records", "idx_step_status_uses", []string{"status", "uses", "task_id"}},
	} {
		if err := s.ensureMigrationIndex(index.table, index.name, index.columns); err != nil {
			return err
		}
	}
	return s.ensureActiveAppEnvironmentUniqueness()
}

func (s *migrationSession) ensureMigrationIndex(table, index string, columns []string) error {
	if !safeSQLIdentifier(table) || !safeSQLIdentifier(index) {
		return fmt.Errorf("unsafe migration index identifier %s.%s", table, index)
	}
	for _, column := range columns {
		if !safeSQLIdentifier(column) {
			return fmt.Errorf("unsafe migration index column identifier %s.%s", table, column)
		}
	}
	exists, err := s.hasEquivalentMigrationIndex(table, regularIndex(columns...))
	if err != nil {
		return fmt.Errorf("inspect migration index %s.%s: %w", table, index, err)
	}
	if exists {
		return nil
	}
	quotedColumns := make([]string, 0, len(columns))
	for _, column := range columns {
		quotedColumns = append(quotedColumns, "`"+column+"`")
	}
	return s.execMigrationStatement(fmt.Sprintf(
		"ALTER TABLE `%s` ADD INDEX `%s` (%s)", table, index, strings.Join(quotedColumns, ", ")))
}
