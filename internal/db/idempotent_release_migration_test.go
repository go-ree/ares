package db

import (
	"strings"
	"testing"
)

func TestIdempotentReleaseMigrationOwnsExactReceiptSchema(t *testing.T) {
	statements := strings.Join(idempotentReleaseTables(), "\n")
	for _, table := range []string{
		"release_idempotency_records", "release_idempotency_items",
	} {
		if !strings.Contains(statements, "CREATE TABLE IF NOT EXISTS "+table) {
			t.Errorf("epoch 6 migration does not create %s", table)
		}
	}
	for _, invariant := range []string{
		"semantic_operation VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL",
		"key_digest BINARY(32) NOT NULL",
		"request_digest BINARY(32) NOT NULL",
		"UNIQUE KEY uk_release_idempotency_scope_actor_key (semantic_operation, actor_user_id, key_digest)",
		"PRIMARY KEY (record_id, request_index)",
		"UNIQUE KEY uk_release_idempotency_items_task (task_id)",
		"outcome VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL",
		"error_code VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL",
	} {
		if !strings.Contains(statements, invariant) {
			t.Errorf("epoch 6 migration does not enforce %q", invariant)
		}
	}
	if strings.Contains(strings.ToUpper(statements), "FOREIGN KEY") {
		t.Fatal("release receipt tables must retain deleted and rejected history without foreign keys")
	}
}

func TestIdempotentReleaseMigrationResumeStatesCoverThreeAtomicDDLBoundaries(t *testing.T) {
	states := epoch6ResumeSchemaStates()
	if got, want := len(states), 4; got != want {
		t.Fatalf("resume state count = %d, want %d", got, want)
	}
	if diffs := compareSemanticSchemaSnapshotManifests(states[0], epoch5SemanticSchemaManifest); len(diffs) != 0 {
		t.Fatalf("first resume state differs from epoch 5: %v", diffs)
	}
	if diffs := compareSemanticSchemaSnapshotManifests(states[len(states)-1], epoch6SemanticSchemaManifest); len(diffs) != 0 {
		t.Fatalf("last resume state differs from epoch 6: %v", diffs)
	}
	for index := 1; index < len(states); index++ {
		if semanticSchemaManifestDigest(states[index-1]) == semanticSchemaManifestDigest(states[index]) {
			t.Fatalf("resume states %d and %d describe the same structural boundary", index-1, index)
		}
	}

	taskBoundary := states[1].tables["task_record"]
	if _, exists := taskBoundary.critical["app_config_id"]; !exists {
		t.Fatal("first DDL boundary does not include task_record.app_config_id")
	}
	if _, exists := states[1].tables["release_idempotency_records"]; exists {
		t.Fatal("first DDL boundary unexpectedly includes receipt tables")
	}
	if _, exists := states[2].tables["release_idempotency_records"]; !exists {
		t.Fatal("second DDL boundary does not include release_idempotency_records")
	}
	if _, exists := states[2].tables["release_idempotency_items"]; exists {
		t.Fatal("second DDL boundary unexpectedly includes receipt items")
	}
}

func TestEpoch6DataContractsRetainHistoryAndReleaseReceipts(t *testing.T) {
	want := []string{
		canonicalTextValuesDataContractID,
		normalizedEnvironmentCodesDataContractID,
		activeEnvironmentCatalogDataContractID,
		authBootstrapSingletonDataContractID,
		idempotentReleaseDataContractID,
	}
	got := epochDataContractIDs(6)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("epoch 6 data contracts = %v, want %v", got, want)
	}
}

func TestIdempotentReleaseMigrationMetadataIsEpochSixOnly(t *testing.T) {
	migration := migrationByVersion(idempotentReleaseMigrationVersion)
	if migration == nil {
		t.Fatal("epoch 6 idempotent release migration is absent")
	}
	if migration.epoch != 6 || migration.compatibleMin != 6 || migration.compatibleMax != 6 {
		t.Fatalf("epoch 6 metadata = epoch:%d compatible:[%d,%d]",
			migration.epoch, migration.compatibleMin, migration.compatibleMax)
	}
}

func TestIdempotentReleaseAcceptedRelationUsesNullSafeComparisons(t *testing.T) {
	var query string
	for _, check := range idempotentReleaseRowChecks() {
		if strings.Contains(check.problem, "accepted release receipt") {
			query = check.query
			break
		}
	}
	if query == "" {
		t.Fatal("accepted receipt relation data contract is absent")
	}
	for _, relation := range []string{
		"NOT (task.app_config_id <=> item.config_id)",
		"NOT (task.workflow_version_id <=> item.workflow_version_id)",
		"NOT (task.publisher_user_id <=> receipt.actor_user_id)",
	} {
		if !strings.Contains(query, relation) {
			t.Errorf("accepted receipt relation does not use NULL-safe comparison %q", relation)
		}
	}
}
