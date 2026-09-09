package db

import (
	"fmt"
	"strings"
	"testing"
)

func TestWorkerLeaseMigrationOwnsExactSchedulingSchema(t *testing.T) {
	for _, invariant := range []string{
		"next_poll_at DATETIME(6) NULL DEFAULT NULL",
		"lease_owner VARBINARY(64) NULL DEFAULT NULL",
		"lease_expires_at DATETIME(6) NULL DEFAULT NULL",
		"lease_fencing_token BIGINT UNSIGNED NOT NULL DEFAULT 0",
		"poll_failure_count INT UNSIGNED NOT NULL DEFAULT 0",
		"INDEX idx_task_worker_due (engine_version, deleted_at, next_poll_at, task_id)",
	} {
		if !strings.Contains(workerLeaseTaskDDL, invariant) {
			t.Errorf("epoch 7 task migration does not enforce %q", invariant)
		}
	}
	if !strings.Contains(workerLeaseIntegrationDDL,
		"revision BIGINT UNSIGNED NOT NULL DEFAULT 1") {
		t.Fatal("epoch 7 integration revision does not start at one")
	}
	for _, provider := range []string{"'jenkins'", "'kubernetes'", "INSERT IGNORE"} {
		if !strings.Contains(workerLeaseIntegrationDefaultsSQL, provider) {
			t.Errorf("epoch 7 integration fence seed does not contain %q", provider)
		}
	}
	for _, invariant := range []string{
		"SET next_poll_at = UTC_TIMESTAMP(6)",
		"engine_version = 2",
		"deleted_at IS NULL",
		"BINARY status IN ('queued', 'running')",
		"next_poll_at IS NULL",
	} {
		if !strings.Contains(workerLeaseBackfillSQL, invariant) {
			t.Errorf("epoch 7 backfill does not enforce %q", invariant)
		}
	}
}

func TestWorkerLeaseMigrationResumeStatesCoverTwoAtomicDDLBoundaries(t *testing.T) {
	states := epoch7ResumeSchemaStates()
	if got, want := len(states), 3; got != want {
		t.Fatalf("resume state count = %d, want %d", got, want)
	}
	if diffs := compareSemanticSchemaSnapshotManifests(states[0], epoch6SemanticSchemaManifest); len(diffs) != 0 {
		t.Fatalf("first resume state differs from epoch 6: %v", diffs)
	}
	if diffs := compareSemanticSchemaSnapshotManifests(states[len(states)-1], epoch7SemanticSchemaManifest); len(diffs) != 0 {
		t.Fatalf("last resume state differs from epoch 7: %v", diffs)
	}
	for index := 1; index < len(states); index++ {
		if semanticSchemaManifestDigest(states[index-1]) == semanticSchemaManifestDigest(states[index]) {
			t.Fatalf("resume states %d and %d describe the same structural boundary", index-1, index)
		}
	}
	for _, column := range []string{
		"next_poll_at", "lease_owner", "lease_expires_at",
		"lease_fencing_token", "poll_failure_count",
	} {
		if _, exists := states[1].tables["task_record"].critical[column]; !exists {
			t.Errorf("first DDL boundary does not include task_record.%s", column)
		}
	}
	if _, exists := states[1].tables["integration_settings"].critical["revision"]; exists {
		t.Fatal("first DDL boundary unexpectedly includes integration_settings.revision")
	}
	if _, exists := states[2].tables["integration_settings"].critical["revision"]; !exists {
		t.Fatal("second DDL boundary does not include integration_settings.revision")
	}
}

func TestEpoch7DataContractsRetainHistoryAndWorkerLeases(t *testing.T) {
	want := []string{
		canonicalTextValuesDataContractID,
		normalizedEnvironmentCodesDataContractID,
		activeEnvironmentCatalogDataContractID,
		authBootstrapSingletonDataContractID,
		idempotentReleaseDataContractID,
		workerLeaseDataContractID,
	}
	got := epochDataContractIDs(7)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("epoch 7 data contracts = %v, want %v", got, want)
	}
}

func TestWorkerLeaseMigrationMetadataIsEpochSevenOnly(t *testing.T) {
	migration := migrationByVersion(workerLeaseMigrationVersion)
	if migration == nil {
		t.Fatal("epoch 7 worker lease migration is absent")
	}
	if migration.epoch != 7 || migration.compatibleMin != 7 || migration.compatibleMax != 7 {
		t.Fatalf("epoch 7 metadata = epoch:%d compatible:[%d,%d]",
			migration.epoch, migration.compatibleMin, migration.compatibleMax)
	}
}

func TestWorkerLeaseDataContractUsesExactStatusAndNullSafeLeaseTime(t *testing.T) {
	queries := make([]string, 0, len(workerLeaseRowChecks()))
	for _, check := range workerLeaseRowChecks() {
		queries = append(queries, check.query)
	}
	joined := strings.Join(queries, "\n")
	for _, invariant := range []string{
		"BINARY status IN ('queued', 'running')",
		"BINARY status NOT IN ('queued', 'running')",
		"NOT (next_poll_at <=> lease_expires_at)",
		"OCTET_LENGTH(lease_owner) = 0",
		fmt.Sprintf("poll_failure_count > %d", workerLeaseMaxPollFailures),
		"revision = 0",
		"required_provider",
	} {
		if !strings.Contains(joined, invariant) {
			t.Errorf("epoch 7 data contract does not enforce %q", invariant)
		}
	}
}
