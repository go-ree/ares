package publish

import (
	"context"
	"database/sql"
	"testing"
	"time"

	appservice "github.com/go-ree/ares/internal/app"
	schemadb "github.com/go-ree/ares/internal/db"
)

// TestMySQLDomainMutationsWaitForReleaseParentLock proves the cross-service
// snapshot boundary under READ COMMITTED. The held transaction deliberately
// locks only app_configs, exactly the first phase of release inspection. Every
// supported domain writer must wait even though no app_config_domains row is
// locked, including the empty-range create case that previously admitted a
// phantom insert.
func TestMySQLDomainMutationsWaitForReleaseParentLock(t *testing.T) {
	harness := newReleaseMySQLHarness(t)
	harness.migrate(t)

	setupCtx, setupCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer setupCancel()
	if _, err := harness.database.ExecContext(setupCtx, `INSERT INTO env_configs (env, description_cn, enabled)
		VALUES ('integration', 'Domain lock integration', 1)`); err != nil {
		t.Fatal(err)
	}

	createConfigID := harness.insertAppConfig(t, setupCtx, "w05-domain-lock-create")
	overwriteConfigID := harness.insertAppConfig(t, setupCtx, "w05-domain-lock-overwrite")
	patchConfigID := harness.insertAppConfig(t, setupCtx, "w05-domain-lock-patch")
	deleteConfigID := harness.insertAppConfig(t, setupCtx, "w05-domain-lock-delete")

	overwriteDomainID := insertDomainForLockTest(t, harness.database, overwriteConfigID, "old-overwrite.example.com", "/old")
	patchDomainID := insertDomainForLockTest(t, harness.database, patchConfigID, "old-patch.example.com", "/old")
	deleteDomainID := insertDomainForLockTest(t, harness.database, deleteConfigID, "delete.example.com", "/delete")

	harness.createRuntimeAccount(t)
	engine := harness.openRuntimeEngine(t)
	// A single runtime connection makes the session isolation assertion exact:
	// every ConfigManager transaction below uses this configured connection.
	engine.SetMaxOpenConns(1)
	engine.SetMaxIdleConns(1)
	if _, err := engine.Exec("SET SESSION TRANSACTION ISOLATION LEVEL READ COMMITTED"); err != nil {
		t.Fatalf("set runtime transaction isolation: %v", err)
	}
	var runtimeIsolation string
	if err := engine.DB().DB.QueryRow("SELECT @@transaction_isolation").Scan(&runtimeIsolation); err != nil {
		t.Fatalf("read runtime transaction isolation: %v", err)
	}
	if runtimeIsolation != "READ-COMMITTED" {
		t.Fatalf("runtime transaction isolation = %q, want READ-COMMITTED", runtimeIsolation)
	}

	previousEngine := schemadb.Engine
	schemadb.Engine = engine
	t.Cleanup(func() {
		schemadb.Engine = previousEngine
		if err := engine.Close(); err != nil {
			t.Errorf("close domain mutation engine: %v", err)
		}
	})
	manager := appservice.NewConfigManager()
	patchedHost := "patched.example.com"

	tests := []struct {
		name     string
		configID int
		mutate   func(context.Context) error
		verify   func(*testing.T)
	}{
		{
			name:     "create empty range",
			configID: createConfigID,
			mutate: func(ctx context.Context) error {
				_, err := manager.CreateDomain(ctx, createConfigID, appservice.DomainItem{
					Host: "created.example.com", Path: "/create",
				})
				return err
			},
			verify: func(t *testing.T) {
				assertDomainForLockTest(t, harness.database, createConfigID, "created.example.com", "/create", 1)
			},
		},
		{
			name:     "overwrite existing row",
			configID: overwriteConfigID,
			mutate: func(ctx context.Context) error {
				return manager.OverwriteDomainsByConfigID(ctx, overwriteConfigID, []appservice.DomainItem{
					{Host: "new-overwrite.example.com", Path: "/new"},
				})
			},
			verify: func(t *testing.T) {
				assertDomainForLockTest(t, harness.database, overwriteConfigID, "new-overwrite.example.com", "/new", 1)
				assertDomainIDAbsentForLockTest(t, harness.database, overwriteDomainID)
			},
		},
		{
			name:     "patch existing row",
			configID: patchConfigID,
			mutate: func(ctx context.Context) error {
				_, err := manager.PatchDomainByID(ctx, patchConfigID, patchDomainID, appservice.PatchDomainRequest{
					Host: &patchedHost,
				})
				return err
			},
			verify: func(t *testing.T) {
				assertDomainForLockTest(t, harness.database, patchConfigID, patchedHost, "/old", 1)
			},
		},
		{
			name:     "delete existing row",
			configID: deleteConfigID,
			mutate: func(ctx context.Context) error {
				return manager.DeleteDomainByID(ctx, deleteConfigID, deleteDomainID)
			},
			verify: func(t *testing.T) {
				assertDomainIDAbsentForLockTest(t, harness.database, deleteDomainID)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertDomainMutationWaitsForParentLock(t, harness.database, test.configID, test.mutate)
			test.verify(t)
		})
	}
}

func assertDomainMutationWaitsForParentLock(
	t *testing.T,
	database *sql.DB,
	configID int,
	mutate func(context.Context) error,
) {
	t.Helper()
	lockCtx, lockCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer lockCancel()
	held, err := database.BeginTx(lockCtx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		t.Fatal(err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = held.Rollback()
		}
	}()
	var lockedConfigID int
	if err := held.QueryRowContext(lockCtx, `SELECT config_id FROM app_configs
		WHERE config_id = ? AND deleted_at IS NULL FOR UPDATE`, configID).Scan(&lockedConfigID); err != nil {
		t.Fatalf("lock release parent config %d: %v", configID, err)
	}
	if lockedConfigID != configID {
		t.Fatalf("locked config_id = %d, want %d", lockedConfigID, configID)
	}

	started := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		close(started)
		mutationCtx, mutationCancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer mutationCancel()
		result <- mutate(mutationCtx)
	}()
	<-started

	blockedWindow := time.NewTimer(300 * time.Millisecond)
	select {
	case mutationErr := <-result:
		if !blockedWindow.Stop() {
			<-blockedWindow.C
		}
		t.Fatalf("domain mutation crossed held release parent lock: %v", mutationErr)
	case <-blockedWindow.C:
	}

	if err := held.Commit(); err != nil {
		t.Fatalf("commit held release parent lock: %v", err)
	}
	committed = true
	select {
	case mutationErr := <-result:
		if mutationErr != nil {
			t.Fatalf("domain mutation after parent unlock: %v", mutationErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("domain mutation remained blocked after release parent commit")
	}
}

func insertDomainForLockTest(t *testing.T, database *sql.DB, configID int, host, path string) int64 {
	t.Helper()
	result, err := database.Exec(`INSERT INTO app_config_domains (config_id, host, path) VALUES (?, ?, ?)`, configID, host, path)
	if err != nil {
		t.Fatal(err)
	}
	domainID, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return domainID
}

func assertDomainForLockTest(t *testing.T, database *sql.DB, configID int, host, path string, want int) {
	t.Helper()
	var count int
	if err := database.QueryRow(`SELECT COUNT(*) FROM app_config_domains
		WHERE config_id = ? AND host = ? AND path = ?`, configID, host, path).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != want {
		t.Fatalf("domain count for config=%d host=%s path=%s = %d, want %d", configID, host, path, count, want)
	}
}

func assertDomainIDAbsentForLockTest(t *testing.T, database *sql.DB, domainID int64) {
	t.Helper()
	var count int
	if err := database.QueryRow("SELECT COUNT(*) FROM app_config_domains WHERE id = ?", domainID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("domain id %d still exists", domainID)
	}
}
