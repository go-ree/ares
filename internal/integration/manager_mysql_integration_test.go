package integration

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	schemadb "github.com/go-ree/ares/internal/db"
	"github.com/go-ree/ares/internal/workflow"
	"github.com/go-sql-driver/mysql"
	"xorm.io/xorm"
)

const jenkinsFenceMySQLIntegrationDSNEnv = "ARES_TEST_MYSQL_DSN"

// TestMySQLJenkinsSettingsFence is opt-in because its assertions depend on
// InnoDB row locks and READ COMMITTED visibility. Each run owns a fresh schema
// and never changes the database named by ARES_TEST_MYSQL_DSN.
func TestMySQLJenkinsSettingsFence(t *testing.T) {
	harness := newJenkinsFenceMySQLHarness(t)

	t.Run("active runs reject the CAS and roll back its transaction", func(t *testing.T) {
		harness.retireActiveRuns(t)
		expected := harness.setJenkinsGeneration(t, true)
		harness.insertActiveLegacyRun(t)
		harness.insertActiveWorkflowRun(t)

		_, err := saveJenkinsProviderCAS(context.Background(), storedJenkinsConfig{
			Enabled: true, Address: "https://next-jenkins.example.test", Username: "builder", TimeoutSeconds: 5,
		}, expected, true)
		if !errors.Is(err, ErrActiveJenkinsRuns) {
			t.Fatalf("guarded CAS error = %v, want ErrActiveJenkinsRuns", err)
		}
		if errors.Is(err, ErrSettingsUnavailable) {
			t.Fatalf("active-run rejection was sanitized as settings unavailable: %v", err)
		}
		for _, detail := range []string{"旧版任务 1 个", "工作流步骤 1 个"} {
			if !strings.Contains(err.Error(), detail) {
				t.Errorf("active-run error %q does not contain %q", err, detail)
			}
		}
		harness.assertJenkinsRecord(t, expected)

		// A rejected transaction must release the row lock and leave the same
		// expected revision usable after the active work has drained.
		harness.retireActiveRuns(t)
		revision, err := saveJenkinsProviderCAS(context.Background(), storedJenkinsConfig{
			Enabled: true, Address: "https://next-jenkins.example.test", Username: "builder", TimeoutSeconds: 5,
		}, expected, true)
		if err != nil {
			t.Fatalf("drained guarded CAS: %v", err)
		}
		if revision != expected.revision+1 {
			t.Fatalf("committed revision = %d, want %d", revision, expected.revision+1)
		}
	})

	t.Run("CAS conflict is classified before the activity guard", func(t *testing.T) {
		harness.retireActiveRuns(t)
		expected := harness.setJenkinsGeneration(t, true)
		if _, err := harness.database.Exec(`UPDATE integration_settings
			SET config_data = '{"enabled":true,"address":"https://concurrent.example.test","timeout_seconds":7}',
				revision = revision + 1
			WHERE provider = 'jenkins'`); err != nil {
			t.Fatal(err)
		}
		harness.insertActiveWorkflowRun(t)

		_, err := saveJenkinsProviderCAS(context.Background(), storedJenkinsConfig{
			Enabled: true, Address: "https://stale-writer.example.test", Username: "builder", TimeoutSeconds: 5,
		}, expected, true)
		if !errors.Is(err, ErrSettingsChanged) {
			t.Fatalf("stale guarded CAS error = %v, want ErrSettingsChanged", err)
		}
		if errors.Is(err, ErrActiveJenkinsRuns) {
			t.Fatalf("stale writer reached the activity guard: %v", err)
		}
	})

	t.Run("a disabled generation can recover despite active work", func(t *testing.T) {
		harness.retireActiveRuns(t)
		expected := harness.setJenkinsGeneration(t, false)
		harness.insertActiveWorkflowRun(t)

		revision, err := saveJenkinsProviderCAS(context.Background(), storedJenkinsConfig{
			Enabled: true, Address: "https://recovered-jenkins.example.test", Username: "builder", TimeoutSeconds: 9,
		}, expected, false)
		if err != nil {
			t.Fatalf("disabled-generation recovery CAS: %v", err)
		}
		if revision != expected.revision+1 {
			t.Fatalf("recovery revision = %d, want %d", revision, expected.revision+1)
		}
	})

	t.Run("a missing disabled generation can recover despite active work", func(t *testing.T) {
		harness.retireActiveRuns(t)
		if _, err := harness.database.Exec(`DELETE FROM integration_settings WHERE provider = 'jenkins'`); err != nil {
			t.Fatal(err)
		}
		expected := providerRecord{}
		harness.insertActiveLegacyRun(t)

		revision, err := saveJenkinsProviderCAS(context.Background(), storedJenkinsConfig{
			Enabled: true, Address: "https://created-jenkins.example.test", Username: "builder", TimeoutSeconds: 5,
		}, expected, false)
		if err != nil {
			t.Fatalf("missing-row recovery CAS: %v", err)
		}
		if revision != 1 {
			t.Fatalf("created revision = %d, want 1", revision)
		}
	})

	t.Run("a claimed step linearizes before a concurrent settings CAS", func(t *testing.T) {
		harness.retireActiveRuns(t)
		harness.setJenkinsGeneration(t, true)
		taskID, stepID := harness.insertClaimableJenkinsStep(t)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		leases, err := harness.store.AcquireTaskLeases(ctx, "jenkins-fence-claim", 1, 30*time.Second)
		if err != nil || len(leases) != 1 || leases[0].TaskID != taskID {
			t.Fatalf("AcquireTaskLeases() = %#v, %v; want task %d", leases, err, taskID)
		}

		// Holding the step row makes ClaimStep pause after taking the task lock
		// and the integration_settings shared locks but before it commits the
		// pending -> running transition.
		blocker, err := harness.database.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
		if err != nil {
			t.Fatal(err)
		}
		defer blocker.Rollback()
		var lockedStepID int64
		if err := blocker.QueryRowContext(ctx, `SELECT step_record_id FROM task_step_records
			WHERE step_record_id = ? FOR UPDATE`, stepID).Scan(&lockedStepID); err != nil {
			t.Fatal(err)
		}

		type claimResult struct {
			claimed bool
			err     error
		}
		claimDone := make(chan claimResult, 1)
		go func() {
			claimed, claimErr := harness.store.ClaimStep(ctx, leases[0], stepID)
			claimDone <- claimResult{claimed: claimed, err: claimErr}
		}()
		harness.waitForJenkinsSharedLock(t, ctx)

		expected := harness.jenkinsRecord(t)
		updateDone := make(chan error, 1)
		go func() {
			_, updateErr := saveJenkinsProviderCAS(ctx, storedJenkinsConfig{
				Enabled: true, Address: "https://fenced-jenkins.example.test", Username: "builder", TimeoutSeconds: 5,
			}, expected, true)
			updateDone <- updateErr
		}()
		select {
		case updateErr := <-updateDone:
			t.Fatalf("settings CAS crossed ClaimStep's shared fence early: %v", updateErr)
		case <-time.After(150 * time.Millisecond):
		}

		if err := blocker.Rollback(); err != nil {
			t.Fatal(err)
		}
		result := <-claimDone
		if result.err != nil || !result.claimed {
			t.Fatalf("ClaimStep() = %t, %v", result.claimed, result.err)
		}
		if updateErr := <-updateDone; !errors.Is(updateErr, ErrActiveJenkinsRuns) {
			t.Fatalf("settings CAS after committed claim = %v, want ErrActiveJenkinsRuns", updateErr)
		}
		harness.assertJenkinsRecord(t, expected)
	})
}

type jenkinsFenceMySQLHarness struct {
	admin        *sql.DB
	database     *sql.DB
	engine       *xorm.Engine
	databaseName string
	store        *workflow.XORMStore
}

func newJenkinsFenceMySQLHarness(t *testing.T) *jenkinsFenceMySQLHarness {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv(jenkinsFenceMySQLIntegrationDSNEnv))
	if dsn == "" {
		t.Skipf("set %s to a MySQL 8.4 administrative DSN to run integration tests",
			jenkinsFenceMySQLIntegrationDSNEnv)
	}
	configuration, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatalf("parse %s: %v", jenkinsFenceMySQLIntegrationDSNEnv, err)
	}
	configuration.ParseTime = true
	configuration.Loc = time.UTC
	admin, err := sql.Open("mysql", configuration.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := admin.PingContext(ctx); err != nil {
		_ = admin.Close()
		t.Fatalf("connect to MySQL integration server: %v", err)
	}
	var version string
	if err := admin.QueryRowContext(ctx, "SELECT VERSION()").Scan(&version); err != nil {
		_ = admin.Close()
		t.Fatal(err)
	}
	if !strings.HasPrefix(version, "8.4.") {
		_ = admin.Close()
		t.Fatalf("integration server version = %q, want MySQL 8.4.x", version)
	}

	randomSuffix := make([]byte, 8)
	if _, err := rand.Read(randomSuffix); err != nil {
		_ = admin.Close()
		t.Fatal(err)
	}
	databaseName := "aresw06jenkinsit" + hex.EncodeToString(randomSuffix)
	if _, err := admin.ExecContext(ctx, fmt.Sprintf(
		"CREATE DATABASE `%s` CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci", databaseName)); err != nil {
		_ = admin.Close()
		t.Fatal(err)
	}

	harness := &jenkinsFenceMySQLHarness{admin: admin, databaseName: databaseName}
	previousEngine := schemadb.Engine
	previousStore := providerSettingsStore
	t.Cleanup(func() {
		schemadb.Engine = previousEngine
		providerSettingsStore = previousStore
		if harness.engine != nil {
			_ = harness.engine.Close()
		}
		if harness.database != nil {
			_ = harness.database.Close()
		}
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		if _, err := harness.admin.ExecContext(cleanupCtx,
			fmt.Sprintf("DROP DATABASE IF EXISTS `%s`", harness.databaseName)); err != nil {
			t.Errorf("drop Jenkins fence integration database %s: %v", harness.databaseName, err)
		}
		_ = harness.admin.Close()
	})

	target := configuration.Clone()
	target.DBName = databaseName
	target.ParseTime = true
	target.Loc = time.UTC
	targetDSN := target.FormatDSN()
	migrateCtx, migrateCancel := context.WithTimeout(context.Background(), 3*time.Minute)
	status, err := schemadb.MigrateUp(migrateCtx, targetDSN, "", 45*time.Second, 10*time.Second)
	migrateCancel()
	if err != nil {
		t.Fatalf("migrate Jenkins fence integration database: %s", schemadb.SafeMigrationErrorText(err))
	}
	if !status.Compatible() {
		t.Fatalf("Jenkins fence integration schema is incompatible: %s", status.String())
	}
	harness.database, err = sql.Open("mysql", targetDSN)
	if err != nil {
		t.Fatal(err)
	}
	if err := harness.database.PingContext(ctx); err != nil {
		t.Fatal(err)
	}
	harness.engine, err = xorm.NewEngine("mysql", targetDSN)
	if err != nil {
		t.Fatal(err)
	}
	harness.engine.SetMaxOpenConns(8)
	harness.engine.SetMaxIdleConns(4)
	if err := harness.engine.PingContext(ctx); err != nil {
		t.Fatal(err)
	}
	harness.store = workflow.NewXORMStore(harness.engine)
	schemadb.Engine = harness.engine
	providerSettingsStore = databaseSettingsStore{}
	return harness
}

func (h *jenkinsFenceMySQLHarness) jenkinsRecord(t *testing.T) providerRecord {
	t.Helper()
	record, err := (databaseSettingsStore{}).load(context.Background(), providerJenkins)
	if err != nil {
		t.Fatal(err)
	}
	return record
}

func (h *jenkinsFenceMySQLHarness) assertJenkinsRecord(t *testing.T, want providerRecord) {
	t.Helper()
	got := h.jenkinsRecord(t)
	if got != want {
		t.Fatalf("Jenkins record = %#v, want %#v", got, want)
	}
}

func (h *jenkinsFenceMySQLHarness) setJenkinsGeneration(t *testing.T, enabled bool) providerRecord {
	t.Helper()
	expected := h.jenkinsRecord(t)
	config := storedJenkinsConfig{Enabled: enabled, TimeoutSeconds: 5}
	if enabled {
		config.Address = "https://current-jenkins.example.test"
		config.Username = "builder"
	}
	if _, err := saveJenkinsProviderCAS(context.Background(), config, expected, false); err != nil {
		t.Fatalf("set Jenkins generation enabled=%t: %v", enabled, err)
	}
	return h.jenkinsRecord(t)
}

func (h *jenkinsFenceMySQLHarness) retireActiveRuns(t *testing.T) {
	t.Helper()
	if _, err := h.database.Exec(`UPDATE task_step_records SET status = 'failed'
		WHERE status = 'running'`); err != nil {
		t.Fatal(err)
	}
	if _, err := h.database.Exec(`UPDATE task_record
		SET status = 'failed', auto_deploy = 0, next_poll_at = NULL,
			lease_owner = NULL, lease_expires_at = NULL, poll_failure_count = 0
		WHERE deleted_at IS NULL AND status IN ('queued', 'running', 'packaging', 'packaged', 'deploying')`); err != nil {
		t.Fatal(err)
	}
}

func (h *jenkinsFenceMySQLHarness) insertActiveLegacyRun(t *testing.T) {
	t.Helper()
	if _, err := h.database.Exec(`INSERT INTO task_record
		(app_name, branch, env, publisher, status, engine_version, auto_deploy)
		VALUES ('jenkins-fence-legacy', 'main', 'integration', 'integration-test', 'packaging', 1, 0)`); err != nil {
		t.Fatal(err)
	}
}

func (h *jenkinsFenceMySQLHarness) insertActiveWorkflowRun(t *testing.T) {
	t.Helper()
	result, err := h.database.Exec(`INSERT INTO task_record
		(app_name, branch, env, publisher, pipeline_param, status, engine_version,
			workflow_version_id, next_poll_at)
		VALUES ('jenkins-fence-v2', 'main', 'integration', 'integration-test', '{}',
			'running', 2, 0, UTC_TIMESTAMP(6))`)
	if err != nil {
		t.Fatal(err)
	}
	taskID, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.database.Exec(`INSERT INTO task_step_records
		(task_id, workflow_version_id, step_key, name, uses, position, config,
			timeout_seconds, on_failure, status, attempt, external_ref, started_at)
		VALUES (?, 0, 'jenkins', 'Jenkins', 'jenkins.job@v1', 0, '{}', 30, 'stop',
			'running', 1, '{"provider_id":"active-build"}', UTC_TIMESTAMP(6))`, taskID); err != nil {
		t.Fatal(err)
	}
}

func (h *jenkinsFenceMySQLHarness) insertClaimableJenkinsStep(t *testing.T) (int, int64) {
	t.Helper()
	result, err := h.database.Exec(`INSERT INTO task_record
		(app_name, branch, env, publisher, pipeline_param, status, engine_version,
			workflow_version_id, next_poll_at)
		VALUES ('jenkins-fence-claim', 'main', 'integration', 'integration-test', '{}',
			'queued', 2, 0, TIMESTAMPADD(SECOND, -1, UTC_TIMESTAMP(6)))`)
	if err != nil {
		t.Fatal(err)
	}
	taskID, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	result, err = h.database.Exec(`INSERT INTO task_step_records
		(task_id, workflow_version_id, step_key, name, uses, position, config,
			timeout_seconds, on_failure, status, attempt)
		VALUES (?, 0, 'jenkins', 'Jenkins', 'jenkins.job@v1', 0, '{}', 30, 'stop', 'pending', 1)`, taskID)
	if err != nil {
		t.Fatal(err)
	}
	stepID, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return int(taskID), stepID
}

func (h *jenkinsFenceMySQLHarness) waitForJenkinsSharedLock(t *testing.T, ctx context.Context) {
	t.Helper()
	for {
		probe, err := h.database.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
		if err != nil {
			t.Fatal(err)
		}
		var revision uint64
		probeErr := probe.QueryRowContext(ctx, `SELECT revision FROM integration_settings
			WHERE provider = 'jenkins' FOR UPDATE NOWAIT`).Scan(&revision)
		_ = probe.Rollback()
		var mysqlErr *mysql.MySQLError
		if errors.As(probeErr, &mysqlErr) && mysqlErr.Number == 3572 {
			return
		}
		if probeErr != nil {
			t.Fatalf("probe Jenkins settings row lock: %v", probeErr)
		}
		select {
		case <-ctx.Done():
			t.Fatalf("ClaimStep did not acquire the Jenkins shared fence: %v", ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
}
