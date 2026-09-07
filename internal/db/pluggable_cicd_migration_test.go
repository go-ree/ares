package db

import (
	"strings"
	"testing"
)

func TestPluggableCICDMigrationCreatesRequiredTables(t *testing.T) {
	statements := strings.Join(pluggableCICDTables(), "\n")
	for _, table := range []string{
		"release_workflows",
		"release_workflow_versions",
		"app_config_workflows",
		"task_step_records",
	} {
		if !strings.Contains(statements, "CREATE TABLE IF NOT EXISTS "+table) {
			t.Fatalf("migration does not create %s", table)
		}
	}
	for _, invariant := range []string{
		"UNIQUE KEY uk_workflow_version (workflow_id, version)",
		"UNIQUE KEY uk_app_config_workflow (app_config_id)",
		"UNIQUE KEY uk_task_step_key (task_id, step_key)",
	} {
		if !strings.Contains(statements, invariant) {
			t.Fatalf("migration does not enforce %q", invariant)
		}
	}
}

func TestLegacyWorkflowNameFitsSchemaAndKeepsStableIdentity(t *testing.T) {
	for _, test := range []struct {
		appName string
		env     string
	}{
		{appName: strings.Repeat("a", 255), env: strings.Repeat("e", 63)},
		{appName: strings.Repeat("应用", 100), env: strings.Repeat("环境", 30)},
	} {
		name := legacyWorkflowName(test.appName, test.env, 987654)
		if got := len([]rune(name)); got > 120 {
			t.Fatalf("legacy workflow name has %d runes: %q", got, name)
		}
		if !strings.HasSuffix(name, " #987654") {
			t.Fatalf("truncated workflow name lost its stable suffix: %q", name)
		}
	}
	if got := legacyWorkflowName("demo-api", "dev", 1); got != "demo-api/dev Jenkins 兼容流程" {
		t.Fatalf("short workflow name changed: %q", got)
	}
}

func TestPluggableCICDMigrationRunsAfterNullCleanup(t *testing.T) {
	if len(schemaMigrations) != int(ApplicationSchemaEpoch) {
		t.Fatalf("schemaMigrations has %d entries, want %d", len(schemaMigrations), ApplicationSchemaEpoch)
	}
	if schemaMigrations[0].version != legacyNullStringMigrationVersion {
		t.Fatalf("epoch 1 migration = %q, want %q", schemaMigrations[0].version, legacyNullStringMigrationVersion)
	}
	if schemaMigrations[1].version != pluggableCICDMigrationVersion {
		t.Fatalf("epoch 2 migration = %q, want %q", schemaMigrations[1].version, pluggableCICDMigrationVersion)
	}
	if schemaMigrations[2].version != cicdRuntimeHardeningMigrationVersion {
		t.Fatalf("epoch 3 migration = %q, want %q", schemaMigrations[2].version, cicdRuntimeHardeningMigrationVersion)
	}
	if schemaMigrations[3].version != versionedSchemaMigrationVersion {
		t.Fatalf("epoch 4 migration = %q, want %q", schemaMigrations[3].version, versionedSchemaMigrationVersion)
	}
	if schemaMigrations[4].version != authRBACMigrationVersion {
		t.Fatalf("epoch 5 migration = %q, want %q", schemaMigrations[4].version, authRBACMigrationVersion)
	}
}
