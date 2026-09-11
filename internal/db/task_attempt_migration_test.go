package db

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestAttemptMigrationKeepsPreviousEpochImmutable(t *testing.T) {
	states := epoch8ResumeSchemaStates()
	if len(states) != 3 || len(epoch8SemanticSchemaManifest.tables) != 23 {
		t.Fatal("incorrect epoch 8 boundaries")
	}
	if diffs := compareSemanticSchemaSnapshotManifests(states[0], epoch7SemanticSchemaManifest); len(diffs) != 0 {
		t.Fatal(diffs)
	}
	if _, ok := epoch7SemanticSchemaManifest.tables["task_step_records"].critical["max_attempts"]; ok {
		t.Fatal("mutated epoch 7")
	}
	if _, ok := epoch7SemanticSchemaManifest.tables["task_step_attempts"]; ok {
		t.Fatal("mutated epoch 7 tables")
	}
}

func TestMySQL84TaskAttemptMigration(t *testing.T) {
	harness := newMySQLIntegrationHarness(t)
	for boundary := 0; boundary <= 2; boundary++ {
		t.Run(fmt.Sprintf("dirty-boundary-%d", boundary), func(t *testing.T) {
			dsn, _ := harness.newDatabase(t)
			database := migrateDatabaseToEpoch(t, dsn, 7)
			if _, err := database.Exec(`INSERT INTO task_step_records (task_id, workflow_version_id, step_key, name, uses, position, config, status, attempt, started_at, finished_at)
    VALUES (9000, 0, 'old', 'Old', 'builtin.noop@v1', 0, '{}', 'failed', 3, UTC_TIMESTAMP(), UTC_TIMESTAMP()),
    (9000, 0, 'pending', 'Pending', 'builtin.noop@v1', 1, '{}', 'pending', 1, NULL, NULL),
    (9000, 0, 'skipped', 'Skipped', 'builtin.noop@v1', 2, '{}', 'skipped', 1, NULL, UTC_TIMESTAMP())`); err != nil {
				t.Fatal(err)
			}
			migration := schemaMigrations[7]
			insertDirtyMigrationRow(t, database, migration)
			if boundary >= 1 {
				if _, err := database.Exec(taskAttemptStepDDL); err != nil {
					t.Fatal(err)
				}
			}
			if boundary >= 2 {
				if _, err := database.Exec(taskAttemptTableDDL); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			status, err := MigrateUp(ctx, dsn, migration.version, 45*time.Second, 10*time.Second)
			if err != nil {
				t.Fatal(err)
			}
			assertCompatibleStatus(t, status)
			var count, attempt, max int
			var class, key string
			if err := database.QueryRow(`SELECT COUNT(*), MAX(attempt), MAX(retry_class), MAX(idempotency_key) FROM task_step_attempts`).Scan(&count, &attempt, &class, &key); err != nil {
				t.Fatal(err)
			}
			if count != 1 || attempt != 3 || class != "" || key != "9000/old/3" {
				t.Fatalf("fabricated history: %d %d %q %q", count, attempt, class, key)
			}
			if err := database.QueryRow(`SELECT max_attempts FROM task_step_records WHERE step_key = 'old'`).Scan(&max); err != nil || max != 1 {
				t.Fatalf("unsafe default=%d err=%v", max, err)
			}
			status, err = MigrateUp(ctx, dsn, "", 45*time.Second, 10*time.Second)
			if err != nil {
				t.Fatal(err)
			}
			assertCompatibleStatus(t, status)
		})
	}
}
