package db

import (
	"strings"
	"testing"
)

func TestAuthRBACMigrationOwnsRequiredTablesAndActorColumns(t *testing.T) {
	statements := strings.Join(authRBACTables(), "\n")
	for _, table := range []string{
		"auth_users", "auth_identities", "auth_sessions", "auth_oidc_flows",
		"auth_bootstrap_state", "audit_events",
	} {
		if !strings.Contains(statements, "CREATE TABLE IF NOT EXISTS "+table) {
			t.Errorf("epoch 5 migration does not create %s", table)
		}
	}
	for _, invariant := range []string{
		"identity_hash BINARY(32) NOT NULL",
		"UNIQUE KEY uk_auth_identities_hash (identity_hash)",
		"session_hash BINARY(32) NOT NULL",
		"state_hash BINARY(32) NOT NULL",
		"nonce_hash BINARY(32) NOT NULL",
		"binding_hash BINARY(32) NOT NULL",
		"verifier_ciphertext TEXT NOT NULL",
		"return_path VARCHAR(512) NOT NULL",
		"PRIMARY KEY (audit_id)",
	} {
		if !strings.Contains(statements, invariant) {
			t.Errorf("epoch 5 migration does not enforce %q", invariant)
		}
	}
	for _, column := range []struct{ table, name string }{
		{"task_record", "publisher_user_id"},
		{"release_workflow_versions", "created_by_user_id"},
	} {
		definition, exists := epoch5SemanticSchemaManifest.tables[column.table].critical[column.name]
		if !exists {
			t.Errorf("epoch 5 manifest lacks %s.%s", column.table, column.name)
			continue
		}
		if definition.columnType != "bigint" || definition.nullable != "YES" {
			t.Errorf("%s.%s = %+v, want nullable BIGINT", column.table, column.name, definition)
		}
	}
}

func TestAuthRBACMigrationResumeStatesCoverEveryDDLBoundary(t *testing.T) {
	states := epoch5ResumeSchemaStates()
	if got, want := len(states), 9; got != want {
		t.Fatalf("resume state count = %d, want %d", got, want)
	}
	if diffs := compareSemanticSchemaSnapshotManifests(states[0], epoch4SemanticSchemaManifest); len(diffs) != 0 {
		t.Fatalf("first resume state differs from epoch 4: %v", diffs)
	}
	if diffs := compareSemanticSchemaSnapshotManifests(states[len(states)-1], epoch5SemanticSchemaManifest); len(diffs) != 0 {
		t.Fatalf("last resume state differs from epoch 5: %v", diffs)
	}
	for index := 1; index < len(states); index++ {
		if semanticSchemaManifestDigest(states[index-1]) == semanticSchemaManifestDigest(states[index]) {
			t.Fatalf("resume states %d and %d describe the same structural boundary", index-1, index)
		}
	}
}

func compareSemanticSchemaSnapshotManifests(left, right semanticSchemaManifest) []string {
	if semanticSchemaManifestDigest(left) == semanticSchemaManifestDigest(right) {
		return nil
	}
	return []string{"manifest digests differ"}
}

func TestEpoch5DataContractsRetainHistoryAndBootstrapSingleton(t *testing.T) {
	want := []string{
		canonicalTextValuesDataContractID,
		normalizedEnvironmentCodesDataContractID,
		activeEnvironmentCatalogDataContractID,
		authBootstrapSingletonDataContractID,
	}
	got := epochDataContractIDs(5)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("epoch 5 data contracts = %v, want %v", got, want)
	}
}

func TestAuthRBACMigrationMetadataIsEpochFiveOnly(t *testing.T) {
	migration := migrationByVersion(authRBACMigrationVersion)
	if migration == nil {
		t.Fatal("epoch 5 auth migration is absent")
	}
	if migration.epoch != 5 || migration.compatibleMin != 5 || migration.compatibleMax != 5 {
		t.Fatalf("epoch 5 metadata = epoch:%d compatible:[%d,%d]",
			migration.epoch, migration.compatibleMin, migration.compatibleMax)
	}
}
