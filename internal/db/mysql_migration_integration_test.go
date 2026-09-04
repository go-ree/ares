package db

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/go-sql-driver/mysql"
	"xorm.io/xorm"
)

const migrationProcessHelperEnv = "ARES_TEST_MIGRATION_PROCESS_HELPER"

func TestMigrationProcessHelper(t *testing.T) {
	if os.Getenv(migrationProcessHelperEnv) != "1" {
		return
	}
	dsn := os.Getenv("ARES_TEST_MIGRATION_PROCESS_DSN")
	readyFile := os.Getenv("ARES_TEST_MIGRATION_PROCESS_READY")
	startFile := os.Getenv("ARES_TEST_MIGRATION_PROCESS_START")
	if dsn == "" || readyFile == "" || startFile == "" {
		t.Fatal("migration process helper environment is incomplete")
	}
	database, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(SafeMigrationErrorText(err))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := database.PingContext(ctx); err != nil {
		_ = database.Close()
		t.Fatal(SafeMigrationErrorText(err))
	}
	_ = database.Close()
	if err := os.WriteFile(readyFile, []byte("ready\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(startFile); err == nil {
			break
		} else if !os.IsNotExist(err) {
			t.Fatal(err)
		}
		select {
		case <-ctx.Done():
			t.Fatal("timed out waiting for migration helper start signal")
		case <-time.After(10 * time.Millisecond):
		}
	}
	status, err := MigrateUp(ctx, dsn, "", 45*time.Second, 10*time.Second)
	if err != nil {
		t.Fatal(SafeMigrationErrorText(err))
	}
	if !status.Compatible() {
		t.Fatalf("migration helper returned incompatible status: %s", status.String())
	}
}

const (
	mysqlIntegrationDSNEnv = "ARES_TEST_MYSQL_DSN"
	preW04FixturePath      = "testdata/pre_w04_main_e2cfd2a.sql"
	preW04FixtureSHA256    = "e0fab1c717f2a67793e8ede72950c7da82ef4590bbf0eab9a5364296d02db2d9"
)

func TestPreW04FixtureIsImmutable(t *testing.T) {
	content, err := os.ReadFile(filepath.Clean(preW04FixturePath))
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(content)
	if got := hex.EncodeToString(digest[:]); got != preW04FixtureSHA256 {
		t.Fatalf("pre-W04 fixture checksum = %s, want %s; replace the fixture only through an explicit baseline decision", got, preW04FixtureSHA256)
	}
}

func TestMySQL84Migrations(t *testing.T) {
	harness := newMySQLIntegrationHarness(t)

	t.Run("empty status is read-only and up is idempotent", func(t *testing.T) {
		dsn, databaseName := harness.newDatabase(t)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()

		status, err := InspectSchema(ctx, dsn, 45*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		if status.Initialized || status.Compatible() {
			t.Fatalf("empty database status = %+v, want uninitialized and incompatible", status)
		}
		if got := harness.tableCount(t, databaseName); got != 0 {
			t.Fatalf("read-only status created %d tables in an empty database", got)
		}

		status, err = MigrateUp(ctx, dsn, "", 45*time.Second, 10*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		assertCompatibleStatus(t, status)
		if got := harness.tableCount(t, databaseName); got != len(epoch4SemanticSchemaManifest.tables)+1 {
			t.Fatalf("table count after migrate up = %d, want %d", got, len(epoch4SemanticSchemaManifest.tables)+1)
		}

		database := openIntegrationDatabase(t, dsn)
		before := readLedgerStamps(t, database)
		if len(before) != len(schemaMigrations) {
			t.Fatalf("ledger rows = %d, want %d", len(before), len(schemaMigrations))
		}
		status, err = MigrateUp(ctx, dsn, "", 45*time.Second, 10*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		assertCompatibleStatus(t, status)
		after := readLedgerStamps(t, database)
		if !reflect.DeepEqual(after, before) {
			t.Fatalf("idempotent migrate up changed the ledger\nbefore=%+v\nafter=%+v", before, after)
		}
	})

	t.Run("structural drift needed by data contracts remains a schema-state result", func(t *testing.T) {
		for _, test := range []struct {
			name      string
			statement string
			want      string
		}{
			{name: "missing table", statement: "DROP TABLE apps", want: "apps"},
			{name: "missing column", statement: "ALTER TABLE apps DROP COLUMN app_name_cn", want: "app_name_cn"},
		} {
			t.Run(test.name, func(t *testing.T) {
				dsn, _ := harness.newDatabase(t)
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
				defer cancel()
				if _, err := MigrateUp(ctx, dsn, "", 45*time.Second, 10*time.Second); err != nil {
					t.Fatal(err)
				}
				database := openIntegrationDatabase(t, dsn)
				if _, err := database.Exec(test.statement); err != nil {
					t.Fatal(err)
				}
				before := readLedgerStamps(t, database)

				status, err := InspectSchema(ctx, dsn, 45*time.Second)
				if err != nil {
					t.Fatalf("structural drift was misclassified as an operational error: %v", err)
				}
				if status.Compatible() || !containsProblem(status.ManifestDiffs, test.want) {
					t.Fatalf("structural drift status = %+v, want schema-state diagnostic containing %q", status, test.want)
				}
				after := readLedgerStamps(t, database)
				if !reflect.DeepEqual(after, before) {
					t.Fatalf("read-only structural drift inspection changed the ledger\nbefore=%+v\nafter=%+v", before, after)
				}
			})
		}
	})

	t.Run("demo seed skips a catalog with a soft-deleted demo environment", func(t *testing.T) {
		dsn, _ := harness.newDatabase(t)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		if _, err := MigrateUp(ctx, dsn, "", 45*time.Second, 10*time.Second); err != nil {
			t.Fatal(err)
		}
		database := openIntegrationDatabase(t, dsn)
		if _, err := database.Exec(`INSERT INTO env_configs
			(env, description_cn, enabled, sort_order, deleted_at)
			VALUES ('dev', '已停用的开发环境', 0, 10, NOW())`); err != nil {
			t.Fatal(err)
		}

		engine, err := xorm.NewEngine("mysql", dsn)
		if err != nil {
			t.Fatal(err)
		}
		previousEngine := Engine
		Engine = engine
		defer func() {
			Engine = previousEngine
			_ = engine.Close()
		}()
		if err := InitializeDemoData(); err != nil {
			t.Fatal(err)
		}
		var apps, configs int
		if err := database.QueryRow(`SELECT
			(SELECT COUNT(*) FROM apps),
			(SELECT COUNT(*) FROM app_configs)`).Scan(&apps, &configs); err != nil {
			t.Fatal(err)
		}
		if apps != 0 || configs != 0 {
			t.Fatalf("demo seed reused a soft-deleted environment: apps=%d configs=%d", apps, configs)
		}
		status, err := InspectSchema(ctx, dsn, 45*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		assertCompatibleStatus(t, status)
	})

	t.Run("legacy null cleanup covers zero and negative signed primary keys", func(t *testing.T) {
		dsn, _ := harness.newDatabase(t)
		database := openIntegrationDatabase(t, dsn)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		connection, err := database.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer connection.Close()

		session := &migrationSession{ctx: ctx, executor: connection, operationTimeout: 45 * time.Second}
		if err := session.createMigrationLedger(); err != nil {
			t.Fatal(err)
		}
		if err := session.bootstrapEmptySchema(); err != nil {
			t.Fatal(err)
		}
		if _, err := connection.ExecContext(ctx,
			"SET SESSION sql_mode = CONCAT_WS(',', @@SESSION.sql_mode, 'NO_AUTO_VALUE_ON_ZERO')"); err != nil {
			t.Fatal(err)
		}

		const minimumInt32 = int64(-1 << 31)
		if _, err := connection.ExecContext(ctx, `INSERT INTO apps
			(app_id, app_name, rundeck_app_name, app_name_cn, owner, owner_cn, dev_language, description_cn, git_url)
			VALUES
			(?, ' Negative App ', 'NULL', 'NULL', 'owner', '负责人', 'golang', ' NULL ', 'https://example.invalid/negative'),
			(0, ' Zero App ', ' NULL ', ' NULL ', 'owner', '负责人', 'golang', 'NULL', 'https://example.invalid/zero')`, minimumInt32); err != nil {
			t.Fatal(err)
		}
		if _, err := connection.ExecContext(ctx, `INSERT INTO app_configs
			(config_id, app_id, env, code_package_type, code_package_path, code_package_name, base_image, pre_stop_command)
			VALUES
			(?, ?, 'negative', 'NULL', ' NULL ', 'NULL', ' null ', 'NULL'),
			(0, 0, 'zero', ' NULL ', 'NULL', ' null ', ' NULL ', 'null')`, minimumInt32, minimumInt32); err != nil {
			t.Fatal(err)
		}
		if _, err := connection.ExecContext(ctx, `INSERT INTO task_record
			(task_id, app_name, rundeck_app_name, branch, env, publisher, message, ci_job_name, cd_job_name, products)
			VALUES
			(?, 'negative-app', 'NULL', 'main', 'negative', 'tester', ' NULL ', 'null', ' NULL ', 'NULL'),
			(0, 'zero-app', ' null ', 'main', 'zero', 'tester', 'NULL', ' NULL ', 'null', ' NULL ')`, minimumInt32); err != nil {
			t.Fatal(err)
		}

		if err := session.executeDirtyMigration(schemaMigrations[0], false); err != nil {
			t.Fatalf("migrate signed legacy identifiers: %v", err)
		}
		rows, err := connection.QueryContext(ctx, `SELECT app_id, app_name_cn,
			rundeck_app_name IS NULL, description_cn IS NULL
			FROM apps WHERE app_id IN (?, 0) ORDER BY app_id`, minimumInt32)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		wantNames := []string{"Negative App", "Zero App"}
		index := 0
		for rows.Next() {
			var id int64
			var name string
			var rundeckNull, descriptionNull bool
			if err := rows.Scan(&id, &name, &rundeckNull, &descriptionNull); err != nil {
				t.Fatal(err)
			}
			if index >= len(wantNames) || name != wantNames[index] || !rundeckNull || !descriptionNull {
				t.Fatalf("normalized app row %d = id=%d name=%q rundeck_null=%t description_null=%t",
					index, id, name, rundeckNull, descriptionNull)
			}
			index++
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		if index != len(wantNames) {
			t.Fatalf("normalized app row count = %d, want %d", index, len(wantNames))
		}
		var invalidConfigs, invalidTasks int
		if err := connection.QueryRowContext(ctx, `SELECT COUNT(*) FROM app_configs
			WHERE config_id IN (?, 0) AND
			(code_package_type <> 'golang' OR code_package_path IS NOT NULL OR code_package_name IS NOT NULL OR
			 base_image IS NOT NULL OR pre_stop_command IS NOT NULL)`, minimumInt32).Scan(&invalidConfigs); err != nil {
			t.Fatal(err)
		}
		if err := connection.QueryRowContext(ctx, `SELECT COUNT(*) FROM task_record
			WHERE task_id IN (?, 0) AND
			(rundeck_app_name IS NOT NULL OR message IS NOT NULL OR ci_job_name IS NOT NULL OR
			 cd_job_name IS NOT NULL OR products IS NOT NULL)`, minimumInt32).Scan(&invalidTasks); err != nil {
			t.Fatal(err)
		}
		if invalidConfigs != 0 || invalidTasks != 0 {
			t.Fatalf("signed legacy rows were skipped: invalid_configs=%d invalid_tasks=%d", invalidConfigs, invalidTasks)
		}

		// Exercise the generic batch cursor against the full signed BIGINT
		// domain as well. Managed epoch-one keys are INT today, but this keeps
		// the helper safe if a future migration uses it for a BIGINT table.
		if _, err := connection.ExecContext(ctx, `CREATE TABLE signed_batch_probe (
			id BIGINT NOT NULL PRIMARY KEY, value VARCHAR(32) NULL
		) ENGINE=InnoDB`); err != nil {
			t.Fatal(err)
		}
		if _, err := connection.ExecContext(ctx, `INSERT INTO signed_batch_probe (id, value)
			VALUES (?, 'NULL'), (0, ' NULL ')`, int64(-1<<63)); err != nil {
			t.Fatal(err)
		}
		probeCondition := legacyNonNullTextSQL("`value`")
		if err := session.updateIDsInBatches("signed_batch_probe", "id", probeCondition,
			"`value` = CASE WHEN "+probeCondition+" THEN NULL ELSE `value` END", nil); err != nil {
			t.Fatalf("normalize full signed BIGINT domain: %v", err)
		}
		var normalizedProbeRows int
		if err := connection.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM signed_batch_probe WHERE value IS NULL").Scan(&normalizedProbeRows); err != nil {
			t.Fatal(err)
		}
		if normalizedProbeRows != 2 {
			t.Fatalf("normalized signed BIGINT rows = %d, want 2", normalizedProbeRows)
		}
	})

	t.Run("guarded migration locks the account before executing and drains it afterward", func(t *testing.T) {
		dsn, databaseName := harness.newDatabase(t)
		migrationDSN, username := harness.newGuardedMigrationUser(t, dsn, databaseName)
		started := make(chan struct{})
		proceed := make(chan struct{})
		released := false
		defer func() {
			if !released {
				close(proceed)
			}
		}()

		original := schemaMigrations[0]
		t.Cleanup(func() { schemaMigrations[0] = original })
		schemaMigrations[0].up = func(session *migrationSession) error {
			close(started)
			select {
			case <-proceed:
				return original.up(session)
			case <-session.ctx.Done():
				return session.ctx.Err()
			}
		}

		type guardedResult struct {
			status MigrationStatus
			err    error
		}
		result := make(chan guardedResult, 1)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		go func() {
			status, err := MigrateUpGuarded(ctx, migrationDSN, dsn, "", 45*time.Second, 10*time.Second)
			result <- guardedResult{status: status, err: err}
		}()

		select {
		case <-started:
		case outcome := <-result:
			t.Fatalf("guarded migration returned before execution observation: status=%s error=%v", outcome.status.String(), outcome.err)
		case <-ctx.Done():
			t.Fatal("timed out waiting for guarded migration execution")
		}
		harness.assertGuardedMigrationAccount(t, username, true, 1)
		assertIntegrationDSNCannotConnect(t, migrationDSN)

		close(proceed)
		released = true
		outcome := <-result
		if outcome.err != nil {
			t.Fatal(outcome.err)
		}
		assertCompatibleStatus(t, outcome.status)
		harness.assertGuardedMigrationAccount(t, username, true, 0)
		assertIntegrationDSNCannotConnect(t, migrationDSN)
	})

	t.Run("guarded migration uses the server canonical database name", func(t *testing.T) {
		var lowerCaseTableNames int
		if err := harness.admin.QueryRow("SELECT @@GLOBAL.lower_case_table_names").Scan(&lowerCaseTableNames); err != nil {
			t.Fatal(err)
		}
		if lowerCaseTableNames == 0 {
			t.Skip("server preserves case-sensitive database names")
		}

		dsn, databaseName := harness.newDatabase(t)
		migrationDSN, username := harness.newGuardedMigrationUser(t, dsn, databaseName)
		migrationConfig, err := mysql.ParseDSN(migrationDSN)
		if err != nil {
			t.Fatal(err)
		}
		adminConfig, err := mysql.ParseDSN(dsn)
		if err != nil {
			t.Fatal(err)
		}
		migrationConfig.DBName = strings.ToUpper(databaseName)
		adminConfig.DBName = strings.ToUpper(databaseName)

		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		status, err := MigrateUpGuarded(ctx, migrationConfig.FormatDSN(), adminConfig.FormatDSN(), "", 45*time.Second, 10*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		assertCompatibleStatus(t, status)
		harness.assertGuardedMigrationAccount(t, username, true, 0)
	})

	t.Run("guarded migration failure still locks rotates and drains the account", func(t *testing.T) {
		dsn, databaseName := harness.newDatabase(t)
		migrationDSN, username := harness.newGuardedMigrationUser(t, dsn, databaseName)

		original := schemaMigrations[0]
		t.Cleanup(func() { schemaMigrations[0] = original })
		schemaMigrations[0].up = func(*migrationSession) error {
			return errors.New("password=guarded-failure-secret forced guarded migration failure")
		}

		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		status, err := MigrateUpGuarded(ctx, migrationDSN, dsn, "", 45*time.Second, 10*time.Second)
		if err == nil || errors.Is(err, ErrSchemaState) {
			t.Fatalf("forced guarded migration error = %v, want operational failure", err)
		}
		if strings.Contains(err.Error(), "guarded-failure-secret") {
			t.Fatalf("forced guarded migration error leaked secret: %v", err)
		}
		if status.Dirty == nil || status.Dirty.Epoch != schemaMigrations[0].epoch {
			t.Fatalf("forced guarded migration status = %+v, want dirty epoch %d", status, schemaMigrations[0].epoch)
		}
		harness.assertGuardedMigrationAccount(t, username, true, 0)
		assertIntegrationDSNCannotConnect(t, migrationDSN)
	})

	t.Run("guarded migrations serialize account handoff before killing sessions", func(t *testing.T) {
		dsn, databaseName := harness.newDatabase(t)
		migrationDSN, username := harness.newGuardedMigrationUser(t, dsn, databaseName)
		started := make(chan struct{})
		proceed := make(chan struct{})
		released := false
		defer func() {
			if !released {
				close(proceed)
			}
		}()

		original := schemaMigrations[0]
		t.Cleanup(func() { schemaMigrations[0] = original })
		schemaMigrations[0].up = func(session *migrationSession) error {
			close(started)
			select {
			case <-proceed:
				return original.up(session)
			case <-session.ctx.Done():
				return session.ctx.Err()
			}
		}

		type guardedResult struct {
			status MigrationStatus
			err    error
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		firstResult := make(chan guardedResult, 1)
		secondResult := make(chan guardedResult, 1)
		go func() {
			status, err := MigrateUpGuarded(ctx, migrationDSN, dsn, "", 45*time.Second, 10*time.Second)
			firstResult <- guardedResult{status: status, err: err}
		}()
		select {
		case <-started:
		case outcome := <-firstResult:
			t.Fatalf("first guarded migration returned before serialization test: status=%s error=%v", outcome.status.String(), outcome.err)
		case <-ctx.Done():
			t.Fatal("timed out waiting for first guarded migration")
		}

		go func() {
			status, err := MigrateUpGuarded(ctx, migrationDSN, dsn, "", 45*time.Second, 10*time.Second)
			secondResult <- guardedResult{status: status, err: err}
		}()
		select {
		case outcome := <-secondResult:
			t.Fatalf("second guarded migration bypassed account guard: status=%s error=%v", outcome.status.String(), outcome.err)
		case <-time.After(300 * time.Millisecond):
		}
		harness.assertGuardedMigrationAccount(t, username, true, 1)

		close(proceed)
		released = true
		first := <-firstResult
		if first.err != nil {
			t.Fatalf("first guarded migration: %v", first.err)
		}
		assertCompatibleStatus(t, first.status)
		second := <-secondResult
		if second.err != nil {
			t.Fatalf("second guarded migration: %v", second.err)
		}
		assertCompatibleStatus(t, second.status)
		harness.assertGuardedMigrationAccount(t, username, true, 0)
	})

	t.Run("guarded account lock timeout makes no account or schema writes", func(t *testing.T) {
		dsn, databaseName := harness.newDatabase(t)
		migrationDSN, username := harness.newGuardedMigrationUser(t, dsn, databaseName)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		holder, err := harness.admin.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer holder.Close()
		lockName := guardedMigrationAccountLockName(username)
		var acquired int
		if err := holder.QueryRowContext(ctx, "SELECT GET_LOCK(?, 0)", lockName).Scan(&acquired); err != nil || acquired != 1 {
			t.Fatalf("hold guarded migration account lock: acquired=%d error=%v", acquired, err)
		}
		defer func() {
			var released sql.NullInt64
			if err := holder.QueryRowContext(context.Background(), "SELECT RELEASE_LOCK(?)", lockName).Scan(&released); err != nil {
				t.Errorf("release held guarded migration account lock: %v", err)
			}
		}()

		type accountState struct {
			plugin, authentication, locked string
		}
		readState := func() accountState {
			var state accountState
			if err := harness.admin.QueryRow(`SELECT plugin, authentication_string, account_locked
				FROM mysql.user WHERE BINARY User = BINARY ? AND Host = '%'`, username).
				Scan(&state.plugin, &state.authentication, &state.locked); err != nil {
				t.Fatal(err)
			}
			return state
		}
		before := readState()
		startedAt := time.Now()
		_, err = MigrateUpGuarded(ctx, migrationDSN, dsn, "", 45*time.Second, 200*time.Millisecond)
		if err == nil || !errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ErrSchemaState) {
			t.Fatalf("guard lock timeout error = %v, want operational context deadline", err)
		}
		if elapsed := time.Since(startedAt); elapsed > 3*time.Second {
			t.Fatalf("guard lock timeout returned after %s, want a prompt bounded failure", elapsed)
		}
		if after := readState(); after != before {
			t.Fatalf("guard lock timeout changed account state: before=%+v after=%+v", before, after)
		}
		harness.assertGuardedMigrationAccount(t, username, true, 0)
		if got := harness.tableCount(t, databaseName); got != 0 {
			t.Fatalf("guard lock timeout created %d schema tables", got)
		}
	})

	t.Run("guarded migration refuses locked account with an active session without killing it", func(t *testing.T) {
		dsn, databaseName := harness.newDatabase(t)
		migrationDSN, username := harness.newGuardedMigrationUser(t, dsn, databaseName)
		account := guardedAccountSQL(username)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if _, err := harness.admin.ExecContext(ctx, "ALTER USER "+account+" ACCOUNT UNLOCK"); err != nil {
			t.Fatal(err)
		}
		migrationDatabase, err := sql.Open("mysql", migrationDSN)
		if err != nil {
			t.Fatal(err)
		}
		migrationDatabase.SetMaxOpenConns(1)
		migrationDatabase.SetMaxIdleConns(0)
		defer migrationDatabase.Close()
		activeConn, err := migrationDatabase.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer activeConn.Close()
		if _, err := activeConn.ExecContext(ctx, "SELECT 1"); err != nil {
			t.Fatal(err)
		}
		if _, err := harness.admin.ExecContext(ctx, "ALTER USER "+account+" ACCOUNT LOCK"); err != nil {
			t.Fatal(err)
		}
		harness.assertGuardedMigrationAccount(t, username, true, 1)
		var authenticationBefore string
		if err := harness.admin.QueryRow(`SELECT authentication_string FROM mysql.user
			WHERE BINARY User = BINARY ? AND Host = '%'`, username).Scan(&authenticationBefore); err != nil {
			t.Fatal(err)
		}

		_, err = MigrateUpGuarded(ctx, migrationDSN, dsn, "", 45*time.Second, 10*time.Second)
		if err == nil || errors.Is(err, ErrSchemaState) || !strings.Contains(err.Error(), "unsafe takeover") {
			t.Fatalf("locked active-session error = %v, want guarded operational refusal", err)
		}
		harness.assertGuardedMigrationAccount(t, username, true, 1)
		var authenticationAfter string
		if err := harness.admin.QueryRow(`SELECT authentication_string FROM mysql.user
			WHERE BINARY User = BINARY ? AND Host = '%'`, username).Scan(&authenticationAfter); err != nil {
			t.Fatal(err)
		}
		if authenticationAfter != authenticationBefore {
			t.Fatal("unsafe-takeover refusal rotated the guarded account credential")
		}
		var one int
		if err := activeConn.QueryRowContext(ctx, "SELECT 1").Scan(&one); err != nil || one != 1 {
			t.Fatalf("unsafe-takeover refusal killed the active session: value=%d error=%v", one, err)
		}
		if got := harness.tableCount(t, databaseName); got != 0 {
			t.Fatalf("unsafe-takeover refusal created %d schema tables", got)
		}
	})

	t.Run("guarded migration rejects an administrator without global session visibility", func(t *testing.T) {
		dsn, databaseName := harness.newDatabase(t)
		migrationDSN, username := harness.newGuardedMigrationUser(t, dsn, databaseName)
		account := guardedAccountSQL(username)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		harness.next++
		adminUsername := fmt.Sprintf("aresadm_%x", time.Now().UnixNano()+int64(harness.next))
		adminPassword := fmt.Sprintf("Aa1!AresAdmin%x", time.Now().UnixNano())
		adminAccount := sqlStringLiteral(adminUsername) + "@'%'"
		if _, err := harness.admin.ExecContext(ctx,
			"CREATE USER "+adminAccount+" IDENTIFIED BY "+sqlStringLiteral(adminPassword)); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cleanupCancel()
			if _, err := harness.admin.ExecContext(cleanupCtx, "DROP USER IF EXISTS "+adminAccount); err != nil {
				t.Errorf("drop limited integration administrator %s: %v", adminUsername, err)
			}
		})
		var partialRevokes int
		if err := harness.admin.QueryRowContext(ctx, "SELECT @@GLOBAL.partial_revokes").Scan(&partialRevokes); err != nil {
			t.Fatal(err)
		}
		grantPattern := guardedMigrationDatabaseGrantPattern(databaseName, partialRevokes == 1)
		if _, err := harness.admin.ExecContext(ctx, "GRANT CREATE USER ON *.* TO "+adminAccount); err != nil {
			t.Fatal(err)
		}
		if _, err := harness.admin.ExecContext(ctx, "GRANT SELECT ON mysql.* TO "+adminAccount); err != nil {
			t.Fatal(err)
		}
		if _, err := harness.admin.ExecContext(ctx,
			fmt.Sprintf("GRANT SELECT ON `%s`.* TO %s", grantPattern, adminAccount)); err != nil {
			t.Fatal(err)
		}
		limitedConfig := harness.adminConfig.Clone()
		limitedConfig.User = adminUsername
		limitedConfig.Passwd = adminPassword
		limitedConfig.DBName = databaseName
		limitedConfig.ParseTime = true

		if _, err := harness.admin.ExecContext(ctx, "ALTER USER "+account+" ACCOUNT UNLOCK"); err != nil {
			t.Fatal(err)
		}
		migrationDatabase, err := sql.Open("mysql", migrationDSN)
		if err != nil {
			t.Fatal(err)
		}
		migrationDatabase.SetMaxOpenConns(1)
		migrationDatabase.SetMaxIdleConns(0)
		defer migrationDatabase.Close()
		activeConn, err := migrationDatabase.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer activeConn.Close()
		if _, err := activeConn.ExecContext(ctx, "SELECT 1"); err != nil {
			t.Fatal(err)
		}
		if _, err := harness.admin.ExecContext(ctx, "ALTER USER "+account+" ACCOUNT LOCK"); err != nil {
			t.Fatal(err)
		}
		var authenticationBefore string
		if err := harness.admin.QueryRowContext(ctx, `SELECT authentication_string FROM mysql.user
			WHERE BINARY User = BINARY ? AND Host = '%'`, username).Scan(&authenticationBefore); err != nil {
			t.Fatal(err)
		}

		_, err = MigrateUpGuarded(ctx, migrationDSN, limitedConfig.FormatDSN(), "", 20*time.Second, 5*time.Second)
		if err == nil || errors.Is(err, ErrSchemaState) || !strings.Contains(err.Error(), "PROCESS") {
			t.Fatalf("limited administrator error = %v, want operational PROCESS refusal", err)
		}
		harness.assertGuardedMigrationAccount(t, username, true, 1)
		var authenticationAfter string
		if err := harness.admin.QueryRowContext(ctx, `SELECT authentication_string FROM mysql.user
			WHERE BINARY User = BINARY ? AND Host = '%'`, username).Scan(&authenticationAfter); err != nil {
			t.Fatal(err)
		}
		if authenticationAfter != authenticationBefore {
			t.Fatal("limited-administrator refusal changed the guarded account credential")
		}
		var one int
		if err := activeConn.QueryRowContext(ctx, "SELECT 1").Scan(&one); err != nil || one != 1 {
			t.Fatalf("limited-administrator refusal killed the hidden session: value=%d error=%v", one, err)
		}
		if got := harness.tableCount(t, databaseName); got != 0 {
			t.Fatalf("limited-administrator refusal created %d schema tables", got)
		}
	})

	t.Run("guarded migration rejects an administrator without authoritative metadata visibility", func(t *testing.T) {
		dsn, databaseName := harness.newDatabase(t)
		migrationDSN, username := harness.newGuardedMigrationUser(t, dsn, databaseName)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		harness.next++
		adminUsername := fmt.Sprintf("aresmeta_%x", time.Now().UnixNano()+int64(harness.next))
		adminPassword := fmt.Sprintf("Aa1!AresMeta%x", time.Now().UnixNano())
		adminAccount := sqlStringLiteral(adminUsername) + "@'%'"
		if _, err := harness.admin.ExecContext(ctx,
			"CREATE USER "+adminAccount+" IDENTIFIED BY "+sqlStringLiteral(adminPassword)); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cleanupCancel()
			if _, err := harness.admin.ExecContext(cleanupCtx, "DROP USER IF EXISTS "+adminAccount); err != nil {
				t.Errorf("drop metadata-limited integration administrator %s: %v", adminUsername, err)
			}
		})
		if _, err := harness.admin.ExecContext(ctx,
			"GRANT SELECT, PROCESS, CREATE USER, EVENT, SHOW VIEW ON *.* TO "+adminAccount); err != nil {
			t.Fatal(err)
		}
		if _, err := harness.admin.ExecContext(ctx, "GRANT CONNECTION_ADMIN ON *.* TO "+adminAccount); err != nil {
			t.Fatal(err)
		}
		limitedConfig := harness.adminConfig.Clone()
		limitedConfig.User = adminUsername
		limitedConfig.Passwd = adminPassword
		limitedConfig.DBName = databaseName
		limitedConfig.ParseTime = true

		if _, err := harness.admin.ExecContext(ctx, fmt.Sprintf(
			"CREATE TABLE `%s`.hidden_trigger_source (id BIGINT PRIMARY KEY)", databaseName)); err != nil {
			t.Fatal(err)
		}
		if _, err := harness.admin.ExecContext(ctx, fmt.Sprintf(
			"CREATE DEFINER=%s TRIGGER `%s`.hidden_guarded_trigger BEFORE INSERT ON `%s`.hidden_trigger_source "+
				"FOR EACH ROW SET NEW.id = NEW.id",
			guardedAccountSQL(username), databaseName, databaseName)); err != nil {
			t.Fatal(err)
		}
		limitedDatabase, err := sql.Open("mysql", limitedConfig.FormatDSN())
		if err != nil {
			t.Fatal(err)
		}
		defer limitedDatabase.Close()
		var limitedVisibleTriggers int
		if err := limitedDatabase.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.TRIGGERS
			WHERE BINARY TRIGGER_SCHEMA = BINARY ?`, databaseName).Scan(&limitedVisibleTriggers); err != nil {
			t.Fatal(err)
		}
		if limitedVisibleTriggers != 0 {
			t.Fatalf("metadata-limited administrator unexpectedly sees %d trigger(s)", limitedVisibleTriggers)
		}
		var authenticationBefore string
		if err := harness.admin.QueryRowContext(ctx, `SELECT authentication_string FROM mysql.user
			WHERE BINARY User = BINARY ? AND Host = '%'`, username).Scan(&authenticationBefore); err != nil {
			t.Fatal(err)
		}

		_, err = MigrateUpGuarded(ctx, migrationDSN, limitedConfig.FormatDSN(), "", 20*time.Second, 5*time.Second)
		if err == nil || errors.Is(err, ErrSchemaState) || !strings.Contains(err.Error(), "TRIGGER") {
			t.Fatalf("metadata-limited administrator error = %v, want operational metadata refusal", err)
		}
		harness.assertGuardedMigrationAccount(t, username, true, 0)
		var authenticationAfter string
		if err := harness.admin.QueryRowContext(ctx, `SELECT authentication_string FROM mysql.user
			WHERE BINARY User = BINARY ? AND Host = '%'`, username).Scan(&authenticationAfter); err != nil {
			t.Fatal(err)
		}
		if authenticationAfter != authenticationBefore {
			t.Fatal("metadata-limited refusal changed the guarded account credential")
		}
		var rootVisibleTriggers int
		if err := harness.admin.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.TRIGGERS
			WHERE BINARY TRIGGER_SCHEMA = BINARY ? AND TRIGGER_NAME = 'hidden_guarded_trigger'`, databaseName).
			Scan(&rootVisibleTriggers); err != nil || rootVisibleTriggers != 1 {
			t.Fatalf("hidden trigger changed during refusal: count=%d error=%v", rootVisibleTriggers, err)
		}
	})

	t.Run("guarded migration authoritatively rejects an external inbound foreign key", func(t *testing.T) {
		dsn, databaseName := harness.newDatabase(t)
		// The external owner is cleaned first so its inbound reference cannot
		// prevent cleanup of the Ares schema.
		_, externalDatabase := harness.newDatabase(t)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		if _, err := MigrateUp(ctx, dsn, "", 45*time.Second, 10*time.Second); err != nil {
			t.Fatal(err)
		}
		if _, err := harness.admin.ExecContext(ctx, fmt.Sprintf(`CREATE TABLE %s.inbound_apps (
			id BIGINT NOT NULL PRIMARY KEY,
			app_id INT NOT NULL,
			CONSTRAINT operator_inbound_apps FOREIGN KEY (app_id)
			REFERENCES %s.apps (app_id) ON DELETE RESTRICT ON UPDATE CASCADE
		) ENGINE=InnoDB`, "`"+externalDatabase+"`", "`"+databaseName+"`")); err != nil {
			t.Fatal(err)
		}

		migrationDSN, username := harness.newGuardedMigrationUser(t, dsn, databaseName)
		account := guardedAccountSQL(username)
		// Demonstrate the visibility gap this administrator pass closes: the
		// least-privilege migration account cannot see the external child row.
		if _, err := harness.admin.ExecContext(ctx, "ALTER USER "+account+" ACCOUNT UNLOCK"); err != nil {
			t.Fatal(err)
		}
		migrationDatabase := openIntegrationDatabase(t, migrationDSN)
		var migrationVisible int
		if err := migrationDatabase.QueryRowContext(ctx, `SELECT COUNT(*)
			FROM information_schema.KEY_COLUMN_USAGE
			WHERE BINARY REFERENCED_TABLE_SCHEMA = BINARY ?
				AND BINARY TABLE_SCHEMA = BINARY ?
				AND REFERENCED_TABLE_NAME = 'apps'`, databaseName, externalDatabase).Scan(&migrationVisible); err != nil {
			t.Fatal(err)
		}
		if err := migrationDatabase.Close(); err != nil {
			t.Fatal(err)
		}
		if migrationVisible != 0 {
			t.Fatalf("least-privilege migration account unexpectedly sees %d external inbound foreign key row(s)", migrationVisible)
		}
		if _, err := harness.admin.ExecContext(ctx, "ALTER USER "+account+" ACCOUNT LOCK"); err != nil {
			t.Fatal(err)
		}
		var authenticationBefore string
		if err := harness.admin.QueryRowContext(ctx, `SELECT authentication_string FROM mysql.user
			WHERE BINARY User = BINARY ? AND Host = '%'`, username).Scan(&authenticationBefore); err != nil {
			t.Fatal(err)
		}

		_, err := MigrateUpGuarded(ctx, migrationDSN, dsn, "", 45*time.Second, 10*time.Second)
		if err == nil || !errors.Is(err, ErrSchemaState) || !strings.Contains(err.Error(), "反向引用") {
			t.Fatalf("guarded inbound foreign-key error = %v, want schema-state refusal", err)
		}
		harness.assertGuardedMigrationAccount(t, username, true, 0)
		var authenticationAfter string
		if err := harness.admin.QueryRowContext(ctx, `SELECT authentication_string FROM mysql.user
			WHERE BINARY User = BINARY ? AND Host = '%'`, username).Scan(&authenticationAfter); err != nil {
			t.Fatal(err)
		}
		if authenticationAfter != authenticationBefore {
			t.Fatal("authoritative inbound foreign-key refusal changed the guarded account credential")
		}
	})

	t.Run("guarded migration rejects an overprivileged direct grant", func(t *testing.T) {
		dsn, databaseName := harness.newDatabase(t)
		migrationDSN, username := harness.newGuardedMigrationUser(t, dsn, databaseName)
		account := guardedAccountSQL(username)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		var partialRevokes int
		if err := harness.admin.QueryRowContext(ctx, "SELECT @@GLOBAL.partial_revokes").Scan(&partialRevokes); err != nil {
			t.Fatal(err)
		}
		grantPattern := guardedMigrationDatabaseGrantPattern(databaseName, partialRevokes == 1)
		if _, err := harness.admin.ExecContext(ctx,
			fmt.Sprintf("GRANT DROP ON `%s`.* TO %s", grantPattern, account)); err != nil {
			t.Fatal(err)
		}

		_, err := MigrateUpGuarded(ctx, migrationDSN, dsn, "", 20*time.Second, 5*time.Second)
		if err == nil || errors.Is(err, ErrSchemaState) || !strings.Contains(err.Error(), "unexpected direct schema privilege") {
			t.Fatalf("overprivileged guarded error = %v, want operational privilege refusal", err)
		}
		harness.assertGuardedMigrationAccount(t, username, true, 0)
		if got := harness.tableCount(t, databaseName); got != 0 {
			t.Fatalf("overprivileged guarded refusal created %d schema tables", got)
		}
	})

	t.Run("guarded migration rejects an unescaped wildcard schema grant", func(t *testing.T) {
		dsn, databaseName := harness.newDatabase(t)
		migrationDSN, username := harness.newGuardedMigrationUser(t, dsn, databaseName)
		account := guardedAccountSQL(username)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		var partialRevokes int
		if err := harness.admin.QueryRowContext(ctx, "SELECT @@GLOBAL.partial_revokes").Scan(&partialRevokes); err != nil {
			t.Fatal(err)
		}
		if partialRevokes != 0 {
			t.Skip("unescaped wildcard grants are literal when partial_revokes is enabled")
		}
		if !strings.Contains(databaseName, "_") {
			t.Fatalf("test database %q does not exercise an underscore wildcard", databaseName)
		}
		if _, err := harness.admin.ExecContext(ctx, "REVOKE ALL PRIVILEGES, GRANT OPTION FROM "+account); err != nil {
			t.Fatal(err)
		}
		if _, err := harness.admin.ExecContext(ctx, fmt.Sprintf(
			"GRANT SELECT, INSERT, UPDATE, DELETE, CREATE, ALTER, INDEX, REFERENCES ON `%s`.* TO %s",
			databaseName, account)); err != nil {
			t.Fatal(err)
		}

		_, err := MigrateUpGuarded(ctx, migrationDSN, dsn, "", 20*time.Second, 5*time.Second)
		if err == nil || errors.Is(err, ErrSchemaState) || !strings.Contains(err.Error(), "unique literal pattern") {
			t.Fatalf("wildcard schema grant error = %v, want operational literal-pattern refusal", err)
		}
		harness.assertGuardedMigrationAccount(t, username, true, 0)
		if got := harness.tableCount(t, databaseName); got != 0 {
			t.Fatalf("wildcard grant refusal created %d schema tables", got)
		}
	})

	t.Run("guarded migration cancels and contains after account lock holder disconnects", func(t *testing.T) {
		dsn, databaseName := harness.newDatabase(t)
		migrationDSN, username := harness.newGuardedMigrationUser(t, dsn, databaseName)
		started := make(chan struct{})
		proceed := make(chan struct{})
		released := false
		defer func() {
			if !released {
				close(proceed)
			}
		}()

		original := schemaMigrations[0]
		t.Cleanup(func() { schemaMigrations[0] = original })
		schemaMigrations[0].up = func(session *migrationSession) error {
			close(started)
			select {
			case <-proceed:
				return original.up(session)
			case <-session.ctx.Done():
				return session.ctx.Err()
			}
		}

		type guardedResult struct {
			status MigrationStatus
			err    error
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		result := make(chan guardedResult, 1)
		go func() {
			status, err := MigrateUpGuarded(ctx, migrationDSN, dsn, "", 20*time.Second, 5*time.Second)
			result <- guardedResult{status: status, err: err}
		}()
		select {
		case <-started:
		case outcome := <-result:
			t.Fatalf("guarded migration returned before watchdog test: status=%s error=%v", outcome.status.String(), outcome.err)
		case <-ctx.Done():
			t.Fatal("timed out waiting for guarded watchdog test migration")
		}

		lockName := guardedMigrationAccountLockName(username)
		var holderID sql.NullInt64
		if err := harness.admin.QueryRowContext(ctx, "SELECT IS_USED_LOCK(?)", lockName).Scan(&holderID); err != nil {
			t.Fatal(err)
		}
		if !holderID.Valid {
			t.Fatal("guarded migration account lock has no holder")
		}
		if _, err := harness.admin.ExecContext(ctx, fmt.Sprintf("KILL CONNECTION %d", holderID.Int64)); err != nil {
			t.Fatal(err)
		}
		select {
		case outcome := <-result:
			if outcome.err == nil || errors.Is(outcome.err, ErrSchemaState) || !strings.Contains(outcome.err.Error(), "watchdog") {
				t.Fatalf("guarded watchdog result = %v, want operational watchdog failure", outcome.err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("guarded migration did not cancel after account lock holder disconnect")
		}
		close(proceed)
		released = true
		harness.assertGuardedMigrationAccount(t, username, true, 0)
		assertIntegrationDSNCannotConnect(t, migrationDSN)
	})

	t.Run("legacy ledger and data are adopted", func(t *testing.T) {
		dsn, _ := harness.newDatabase(t)
		loadSQLFixture(t, dsn, preW04FixturePath)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()

		before, err := InspectSchema(ctx, dsn, 45*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		if !before.Initialized || !before.NeedsAdoption || len(before.Applied) != 3 || len(before.Pending) != 1 {
			t.Fatalf("legacy status = %+v, want three adopted candidates and one pending migration", before)
		}

		status, err := MigrateUp(ctx, dsn, "", 45*time.Second, 10*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		assertCompatibleStatus(t, status)

		database := openIntegrationDatabase(t, dsn)
		var adopted, native int
		if err := database.QueryRow(`SELECT
			SUM(epoch <= 3 AND legacy_adopted = 1),
			SUM(epoch = 4 AND legacy_adopted = 0)
			FROM schema_migrations`).Scan(&adopted, &native); err != nil {
			t.Fatal(err)
		}
		if adopted != 3 || native != 1 {
			t.Fatalf("ledger adoption counts = adopted:%d native:%d, want 3 and 1", adopted, native)
		}
		var appName, environment, packagePath string
		if err := database.QueryRow(`SELECT a.app_name, c.env, c.code_package_path
			FROM apps a JOIN app_configs c ON c.app_id = a.app_id
			WHERE a.app_id = 12345 AND c.config_id = 23456`).Scan(&appName, &environment, &packagePath); err != nil {
			t.Fatal(err)
		}
		if appName != "fixture-api" || environment != "staging" || packagePath != "/immutable/fixture" {
			t.Fatalf("fixture sentinel changed during adoption: %q %q %q", appName, environment, packagePath)
		}
	})

	t.Run("epoch two rejects trailing control characters before environment writes", func(t *testing.T) {
		dsn, _ := harness.newDatabase(t)
		database := migrateDatabaseToEpoch(t, dsn, 1)
		for index, environmentCode := range []string{"dev\n", "qa\r", "prod\r\n"} {
			if _, err := database.Exec(`INSERT INTO env_configs
				(env, cluster_name, description_cn, harbor_url, harbor_project_name, node_version, maven_version)
				VALUES (?, '', ?, '', '', '', '')`, environmentCode, fmt.Sprintf("invalid-%d", index)); err != nil {
				t.Fatal(err)
			}
			appResult, err := database.Exec(`INSERT INTO apps
				(app_name, app_name_cn, owner, owner_cn, dev_language, git_url)
				VALUES (?, ?, 'owner', '负责人', 'golang', 'https://example.invalid/environment-control')`,
				fmt.Sprintf("environment-control-%d", index), fmt.Sprintf("环境控制字符 %d", index))
			if err != nil {
				t.Fatal(err)
			}
			appID, err := appResult.LastInsertId()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := database.Exec(`INSERT INTO app_configs (app_id, env, code_package_type)
				VALUES (?, ?, 'golang')`, appID, environmentCode); err != nil {
				t.Fatal(err)
			}
		}
		var catalogBefore, configsBefore string
		if err := database.QueryRow("SELECT GROUP_CONCAT(HEX(env) ORDER BY id) FROM env_configs").Scan(&catalogBefore); err != nil {
			t.Fatal(err)
		}
		if err := database.QueryRow("SELECT GROUP_CONCAT(HEX(env) ORDER BY config_id) FROM app_configs").Scan(&configsBefore); err != nil {
			t.Fatal(err)
		}

		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		_, err := MigrateUp(ctx, dsn, "", 45*time.Second, 10*time.Second)
		if err == nil || !strings.Contains(err.Error(), "active environment codes") {
			t.Fatalf("trailing control-character migration error = %v, want canonical-code refusal", err)
		}
		var catalogAfter, configsAfter string
		if err := database.QueryRow("SELECT GROUP_CONCAT(HEX(env) ORDER BY id) FROM env_configs").Scan(&catalogAfter); err != nil {
			t.Fatal(err)
		}
		if err := database.QueryRow("SELECT GROUP_CONCAT(HEX(env) ORDER BY config_id) FROM app_configs").Scan(&configsAfter); err != nil {
			t.Fatal(err)
		}
		if catalogAfter != catalogBefore || configsAfter != configsBefore {
			t.Fatalf("refused environment migration changed codes: catalog %q -> %q, configs %q -> %q",
				catalogBefore, catalogAfter, configsBefore, configsAfter)
		}
		var workflowTables int
		if err := database.QueryRow(`SELECT COUNT(*) FROM information_schema.TABLES
			WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'release_workflows'`).Scan(&workflowTables); err != nil {
			t.Fatal(err)
		}
		if workflowTables != 0 {
			t.Fatal("refused environment migration continued into workflow schema writes")
		}
	})

	t.Run("epoch two does not backfill malformed historical task environments", func(t *testing.T) {
		dsn, _ := harness.newDatabase(t)
		database := migrateDatabaseToEpoch(t, dsn, 1)
		const historicalEnvironment = "dev\n"
		if _, err := database.Exec(`INSERT INTO task_record (app_name, branch, env, publisher)
			VALUES ('historical-invalid-env', 'main', ?, 'migration-test')`, historicalEnvironment); err != nil {
			t.Fatal(err)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		status, err := MigrateUp(ctx, dsn, "", 45*time.Second, 10*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		assertCompatibleStatus(t, status)
		var catalogRows int
		if err := database.QueryRow("SELECT COUNT(*) FROM env_configs").Scan(&catalogRows); err != nil {
			t.Fatal(err)
		}
		if catalogRows != 0 {
			t.Fatalf("malformed historical task environment created %d catalog row(s)", catalogRows)
		}
		var storedEnvironment string
		if err := database.QueryRow("SELECT env FROM task_record WHERE app_name = 'historical-invalid-env'").Scan(&storedEnvironment); err != nil {
			t.Fatal(err)
		}
		if storedEnvironment != historicalEnvironment {
			t.Fatalf("historical task environment changed to %q", storedEnvironment)
		}
	})

	t.Run("legacy unknown version fails closed without adoption", func(t *testing.T) {
		dsn, _ := harness.newDatabase(t)
		loadSQLFixture(t, dsn, preW04FixturePath)
		database := openIntegrationDatabase(t, dsn)
		if _, err := database.Exec(`INSERT INTO schema_migrations (version, applied_at)
			VALUES ('20990101_001_unknown', '2026-09-03 01:04:04')`); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		status, err := MigrateUp(ctx, dsn, "", 45*time.Second, 10*time.Second)
		assertSchemaStateError(t, err)
		if !status.NeedsAdoption || !containsProblem(status.Problems, "未知版本") {
			t.Fatalf("unknown legacy version status is not fail-closed: %+v", status)
		}
		assertLegacyLedgerUnchanged(t, database, 4)
	})

	t.Run("legacy version gap fails closed without adoption", func(t *testing.T) {
		dsn, _ := harness.newDatabase(t)
		loadSQLFixture(t, dsn, preW04FixturePath)
		database := openIntegrationDatabase(t, dsn)
		if _, err := database.Exec("DELETE FROM schema_migrations WHERE version = ?", pluggableCICDMigrationVersion); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		status, err := MigrateUp(ctx, dsn, "", 45*time.Second, 10*time.Second)
		assertSchemaStateError(t, err)
		if !status.NeedsAdoption || !containsProblem(status.Problems, "连续前缀") {
			t.Fatalf("gapped legacy ledger status is not fail-closed: %+v", status)
		}
		assertLegacyLedgerUnchanged(t, database, 2)
	})

	t.Run("unsupported legacy ledger shape fails closed before writes", func(t *testing.T) {
		dsn, _ := harness.newDatabase(t)
		database := openIntegrationDatabase(t, dsn)
		if _, err := database.Exec(`CREATE TABLE schema_migrations (
			version VARCHAR(128) NOT NULL PRIMARY KEY,
			unexpected_metadata VARCHAR(64) NULL
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`); err != nil {
			t.Fatal(err)
		}
		before := ledgerColumnNames(t, database)
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		status, err := MigrateUp(ctx, dsn, "", 45*time.Second, 10*time.Second)
		assertSchemaStateError(t, err)
		if !containsProblem(status.Problems, "applied_at") || !containsProblem(status.Problems, "不支持的列") {
			t.Fatalf("unsupported ledger shape problems = %v", status.Problems)
		}
		after := ledgerColumnNames(t, database)
		if !reflect.DeepEqual(after, before) {
			t.Fatalf("refused adoption changed ledger columns: before=%v after=%v", before, after)
		}
	})

	t.Run("legacy epoch-one schema drift fails before adoption", func(t *testing.T) {
		dsn, _ := harness.newDatabase(t)
		loadSQLFixture(t, dsn, preW04FixturePath)
		database := openIntegrationDatabase(t, dsn)
		if _, err := database.Exec(`ALTER TABLE apps
			MODIFY COLUMN description_cn VARCHAR(255) NOT NULL`); err != nil {
			t.Fatal(err)
		}
		before := ledgerColumnNames(t, database)

		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		status, err := MigrateUp(ctx, dsn, "", 45*time.Second, 10*time.Second)
		assertSchemaStateError(t, err)
		if !containsProblem(status.Problems, "legacy") && !strings.Contains(err.Error(), "IS_NULLABLE") {
			t.Fatalf("legacy epoch-one drift error = %v, status=%+v", err, status)
		}
		after := ledgerColumnNames(t, database)
		if !reflect.DeepEqual(after, before) {
			t.Fatalf("refused epoch-one drift changed ledger columns: before=%v after=%v", before, after)
		}
	})

	t.Run("legacy epoch-two default drift fails before adoption", func(t *testing.T) {
		dsn, _ := harness.newDatabase(t)
		loadSQLFixture(t, dsn, preW04FixturePath)
		database := openIntegrationDatabase(t, dsn)
		if _, err := database.Exec("ALTER TABLE env_configs ALTER COLUMN enabled SET DEFAULT 0"); err != nil {
			t.Fatal(err)
		}
		before := ledgerColumnNames(t, database)

		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		_, err := MigrateUp(ctx, dsn, "", 45*time.Second, 10*time.Second)
		assertSchemaStateError(t, err)
		if !strings.Contains(err.Error(), "默认值") {
			t.Fatalf("legacy epoch-two default drift error = %v", err)
		}
		after := ledgerColumnNames(t, database)
		if !reflect.DeepEqual(after, before) {
			t.Fatalf("refused epoch-two drift changed ledger columns: before=%v after=%v", before, after)
		}
	})

	t.Run("invalid empty complete ledger fails closed before bootstrap", func(t *testing.T) {
		dsn, databaseName := harness.newDatabase(t)
		database := openIntegrationDatabase(t, dsn)
		session := &migrationSession{
			ctx: context.Background(), executor: database, operationTimeout: 45 * time.Second,
		}
		if err := session.createMigrationLedger(); err != nil {
			t.Fatal(err)
		}
		if _, err := database.Exec(`ALTER TABLE schema_migrations
			CONVERT TO CHARACTER SET latin1 COLLATE latin1_swedish_ci`); err != nil {
			t.Fatal(err)
		}
		beforeColumns := ledgerColumnNames(t, database)

		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		status, err := MigrateUp(ctx, dsn, "", 45*time.Second, 10*time.Second)
		assertSchemaStateError(t, err)
		if !containsProblem(status.Problems, "schema_migrations") {
			t.Fatalf("invalid complete ledger problems = %v", status.Problems)
		}
		if got := harness.tableCount(t, databaseName); got != 1 {
			t.Fatalf("invalid complete ledger triggered bootstrap: table count=%d, want 1", got)
		}
		if afterColumns := ledgerColumnNames(t, database); !reflect.DeepEqual(afterColumns, beforeColumns) {
			t.Fatalf("invalid complete ledger changed columns: before=%v after=%v", beforeColumns, afterColumns)
		}
	})

	t.Run("empty legacy ledger with business data fails before adoption", func(t *testing.T) {
		dsn, databaseName := harness.newDatabase(t)
		database := openIntegrationDatabase(t, dsn)
		if _, err := database.Exec(`CREATE TABLE schema_migrations (
			version VARCHAR(128) NOT NULL PRIMARY KEY,
			applied_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`); err != nil {
			t.Fatal(err)
		}
		if _, err := database.Exec(schemaBootstrapTables[0].statement); err != nil {
			t.Fatal(err)
		}
		if _, err := database.Exec(`INSERT INTO apps
			(app_name, app_name_cn, owner, owner_cn, dev_language, git_url)
			VALUES ('legacy-data', '旧数据', 'owner', '负责人', 'golang', 'https://example.invalid/repo')`); err != nil {
			t.Fatal(err)
		}
		beforeColumns := ledgerColumnNames(t, database)

		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		status, err := MigrateUp(ctx, dsn, "", 45*time.Second, 10*time.Second)
		assertSchemaStateError(t, err)
		if !status.NeedsAdoption || !containsProblem(status.Problems, "包含业务数据") {
			t.Fatalf("empty legacy ledger with data status = %+v", status)
		}
		if got := harness.tableCount(t, databaseName); got != 2 {
			t.Fatalf("refused empty legacy adoption changed table count=%d, want 2", got)
		}
		if afterColumns := ledgerColumnNames(t, database); !reflect.DeepEqual(afterColumns, beforeColumns) {
			t.Fatalf("refused empty legacy adoption changed columns: before=%v after=%v", beforeColumns, afterColumns)
		}
	})

	t.Run("empty legacy ledger with malformed managed table fails before adoption", func(t *testing.T) {
		dsn, databaseName := harness.newDatabase(t)
		database := openIntegrationDatabase(t, dsn)
		if _, err := database.Exec(`CREATE TABLE schema_migrations (
			version VARCHAR(128) NOT NULL PRIMARY KEY,
			applied_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`); err != nil {
			t.Fatal(err)
		}
		if _, err := database.Exec(`CREATE TABLE apps (
			bogus INT NOT NULL PRIMARY KEY
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci`); err != nil {
			t.Fatal(err)
		}
		beforeColumns := ledgerColumnNames(t, database)

		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		status, err := MigrateUp(ctx, dsn, "", 45*time.Second, 10*time.Second)
		assertSchemaStateError(t, err)
		if !status.NeedsAdoption || !containsProblem(status.Problems, "缺少列") {
			t.Fatalf("empty legacy ledger with malformed table status = %+v", status)
		}
		if got := harness.tableCount(t, databaseName); got != 2 {
			t.Fatalf("malformed legacy database changed table count=%d, want 2", got)
		}
		if afterColumns := ledgerColumnNames(t, database); !reflect.DeepEqual(afterColumns, beforeColumns) {
			t.Fatalf("malformed legacy database changed ledger: before=%v after=%v", beforeColumns, afterColumns)
		}
	})

	t.Run("tampered partial adoption metadata is not overwritten", func(t *testing.T) {
		dsn, _ := harness.newDatabase(t)
		loadSQLFixture(t, dsn, preW04FixturePath)
		database := openIntegrationDatabase(t, dsn)
		if _, err := database.Exec(`ALTER TABLE schema_migrations
			ADD COLUMN epoch BIGINT UNSIGNED NULL,
			ADD COLUMN checksum CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL`); err != nil {
			t.Fatal(err)
		}
		const tampered = "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
		if _, err := database.Exec(`UPDATE schema_migrations SET epoch = 1, checksum = ?
			WHERE version = ?`, tampered, schemaMigrations[0].version); err != nil {
			t.Fatal(err)
		}
		before := ledgerColumnNames(t, database)
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		status, err := MigrateUp(ctx, dsn, "", 45*time.Second, 10*time.Second)
		assertSchemaStateError(t, err)
		if !containsProblem(status.Problems, "已有元数据") {
			t.Fatalf("partial adoption tampering problems = %v", status.Problems)
		}
		after := ledgerColumnNames(t, database)
		if !reflect.DeepEqual(after, before) {
			t.Fatalf("refused partial adoption changed columns: before=%v after=%v", before, after)
		}
		var checksum string
		if err := database.QueryRow("SELECT checksum FROM schema_migrations WHERE version = ?",
			schemaMigrations[0].version).Scan(&checksum); err != nil {
			t.Fatal(err)
		}
		if checksum != tampered {
			t.Fatalf("refused partial adoption overwrote checksum: %s", checksum)
		}
	})

	t.Run("interrupted legacy adoption resumes safely", func(t *testing.T) {
		dsn, _ := harness.newDatabase(t)
		loadSQLFixture(t, dsn, preW04FixturePath)
		database := openIntegrationDatabase(t, dsn)
		if _, err := database.Exec(`ALTER TABLE schema_migrations
			ADD COLUMN epoch BIGINT UNSIGNED NULL,
			ADD COLUMN description VARCHAR(255) NULL,
			ADD COLUMN checksum CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL`); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		status, err := MigrateUp(ctx, dsn, "", 45*time.Second, 10*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		assertCompatibleStatus(t, status)
	})

	t.Run("legacy adoption accepts an equivalent epoch index name", func(t *testing.T) {
		dsn, _ := harness.newDatabase(t)
		loadSQLFixture(t, dsn, preW04FixturePath)
		database := openIntegrationDatabase(t, dsn)
		if _, err := database.Exec(`ALTER TABLE schema_migrations
			ADD COLUMN epoch BIGINT UNSIGNED NULL,
			ADD UNIQUE INDEX operator_epoch_unique (epoch)`); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		status, err := MigrateUp(ctx, dsn, "", 45*time.Second, 10*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		assertCompatibleStatus(t, status)
		var equivalentIndexes int
		if err := database.QueryRow(`SELECT COUNT(DISTINCT INDEX_NAME)
			FROM information_schema.STATISTICS
			WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'schema_migrations'
				AND COLUMN_NAME = 'epoch' AND NON_UNIQUE = 0`).Scan(&equivalentIndexes); err != nil {
			t.Fatal(err)
		}
		if equivalentIndexes != 1 {
			t.Fatalf("equivalent epoch indexes = %d, want 1", equivalentIndexes)
		}
	})

	t.Run("pending migration accepts equivalent operator index names", func(t *testing.T) {
		dsn, _ := harness.newDatabase(t)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		if _, err := MigrateUp(ctx, dsn, "", 45*time.Second, 10*time.Second); err != nil {
			t.Fatal(err)
		}
		database := openIntegrationDatabase(t, dsn)
		for _, statement := range []string{
			"DELETE FROM schema_migrations WHERE version = '" + versionedSchemaMigrationVersion + "'",
			"UPDATE schema_migrations SET dirty = 1, finished_at = NULL, last_error = 'simulated interruption' WHERE version = '" + cicdRuntimeHardeningMigrationVersion + "'",
			"ALTER TABLE app_configs RENAME INDEX uk_app_active_env TO operator_app_active_env",
			"ALTER TABLE task_record RENAME INDEX idx_task_workflow_poll TO operator_task_workflow_poll",
			"ALTER TABLE task_step_records RENAME INDEX idx_step_status_uses TO operator_step_status_uses",
		} {
			if _, err := database.Exec(statement); err != nil {
				t.Fatalf("prepare renamed-index fixture with %q: %v", statement, err)
			}
		}

		status, err := MigrateUp(ctx, dsn, cicdRuntimeHardeningMigrationVersion, 45*time.Second, 10*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		assertCompatibleStatus(t, status)
		for _, check := range []struct {
			table   string
			columns string
			unique  int
		}{
			{"app_configs", "app_id,active_env", 0},
			{"task_record", "engine_version,status,deleted_at,updated_at,task_id", 1},
			{"task_step_records", "status,uses,task_id", 1},
		} {
			var count int
			if err := database.QueryRow(`SELECT COUNT(*) FROM (
				SELECT INDEX_NAME
				FROM information_schema.STATISTICS
				WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ? AND NON_UNIQUE = ?
				GROUP BY INDEX_NAME
				HAVING GROUP_CONCAT(COLUMN_NAME ORDER BY SEQ_IN_INDEX SEPARATOR ',') = ?
			) equivalent_indexes`, check.table, check.unique, check.columns).Scan(&count); err != nil {
				t.Fatal(err)
			}
			if count != 1 {
				t.Fatalf("%s equivalent index count = %d, want 1", check.table, count)
			}
		}
	})

	t.Run("legacy epoch prefixes reject future and unknown schema before adoption", func(t *testing.T) {
		cases := []struct {
			name      string
			epoch     uint64
			statement string
			want      string
		}{
			{
				name: "epoch one future column", epoch: 1,
				statement: "ALTER TABLE app_configs ADD COLUMN active_env VARCHAR(100) GENERATED ALWAYS AS (IF(deleted_at IS NULL, env, NULL)) STORED",
				want:      "manifest 外列",
			},
			{
				name: "epoch two future index", epoch: 2,
				statement: "ALTER TABLE task_record ADD INDEX operator_future_poll (engine_version, status, deleted_at, updated_at, task_id)",
				want:      "manifest 外普通索引",
			},
			{
				name: "epoch three unknown table", epoch: 3,
				statement: "CREATE TABLE operator_data (id INT PRIMARY KEY) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci",
				want:      "manifest 外基础表",
			},
		}
		for _, test := range cases {
			t.Run(test.name, func(t *testing.T) {
				dsn, _ := harness.newDatabase(t)
				database := migrateDatabaseToEpoch(t, dsn, test.epoch)
				convertLedgerToLegacy(t, database)
				if _, err := database.Exec(test.statement); err != nil {
					t.Fatal(err)
				}

				ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
				defer cancel()
				_, err := MigrateUp(ctx, dsn, "", 45*time.Second, 10*time.Second)
				assertSchemaStateError(t, err)
				if !strings.Contains(err.Error(), test.want) {
					t.Fatalf("legacy epoch %d drift error = %v, want %q", test.epoch, err, test.want)
				}
				assertLegacyLedgerUnchanged(t, database, int(test.epoch))
			})
		}
	})

	t.Run("clean prefix rejects target objects before creating dirty row", func(t *testing.T) {
		dsn, _ := harness.newDatabase(t)
		database := migrateDatabaseToEpoch(t, dsn, 1)
		if _, err := database.Exec("ALTER TABLE env_configs ADD COLUMN enabled TINYINT(1) NOT NULL DEFAULT 1 AFTER description_cn"); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		_, err := MigrateUp(ctx, dsn, "", 45*time.Second, 10*time.Second)
		assertSchemaStateError(t, err)
		var rows int
		if err := database.QueryRow("SELECT COUNT(*) FROM schema_migrations").Scan(&rows); err != nil {
			t.Fatal(err)
		}
		if rows != 1 {
			t.Fatalf("refused clean-prefix drift wrote ledger rows: %d", rows)
		}
	})

	t.Run("dirty resume accepts exact target intermediate state", func(t *testing.T) {
		dsn, _ := harness.newDatabase(t)
		database := migrateDatabaseToEpoch(t, dsn, 1)
		migration := schemaMigrations[1]
		insertDirtyMigrationRow(t, database, migration)
		if _, err := database.Exec("ALTER TABLE env_configs ADD COLUMN enabled TINYINT(1) NOT NULL DEFAULT 1 AFTER description_cn"); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		status, err := MigrateUp(ctx, dsn, migration.version, 45*time.Second, 10*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		assertCompatibleStatus(t, status)
	})

	t.Run("dirty resume rejects malformed target object before ledger write", func(t *testing.T) {
		dsn, _ := harness.newDatabase(t)
		database := migrateDatabaseToEpoch(t, dsn, 1)
		migration := schemaMigrations[1]
		insertDirtyMigrationRow(t, database, migration)
		if _, err := database.Exec("ALTER TABLE env_configs ADD COLUMN enabled VARCHAR(10) NULL"); err != nil {
			t.Fatal(err)
		}
		var startedAt time.Time
		if err := database.QueryRow("SELECT started_at FROM schema_migrations WHERE epoch=2").Scan(&startedAt); err != nil {
			t.Fatal(err)
		}

		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		status, err := MigrateUp(ctx, dsn, migration.version, 45*time.Second, 10*time.Second)
		assertSchemaStateError(t, err)
		if !containsProblem(status.ManifestDiffs, "enabled") ||
			!containsProblem(status.ManifestDiffs, "允许的精确语句边界状态") {
			t.Fatalf("malformed dirty target problems = %v", status.ManifestDiffs)
		}
		var unchanged time.Time
		if err := database.QueryRow("SELECT started_at FROM schema_migrations WHERE epoch=2").Scan(&unchanged); err != nil {
			t.Fatal(err)
		}
		if !unchanged.Equal(startedAt) {
			t.Fatalf("refused dirty target changed ledger timestamp: %s -> %s", startedAt, unchanged)
		}
	})

	t.Run("dirty resume rejects an impossible skipped statement state", func(t *testing.T) {
		dsn, _ := harness.newDatabase(t)
		database := migrateDatabaseToEpoch(t, dsn, 1)
		migration := schemaMigrations[1]
		insertDirtyMigrationRow(t, database, migration)
		if _, err := database.Exec("ALTER TABLE env_configs ADD COLUMN sort_order INT NOT NULL DEFAULT 0 AFTER description_cn"); err != nil {
			t.Fatal(err)
		}
		var startedAt time.Time
		if err := database.QueryRow("SELECT started_at FROM schema_migrations WHERE epoch=2").Scan(&startedAt); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		status, err := MigrateUp(ctx, dsn, migration.version, 45*time.Second, 10*time.Second)
		assertSchemaStateError(t, err)
		if !containsProblem(status.ManifestDiffs, "允许的精确语句边界状态") {
			t.Fatalf("skipped dirty statement state problems = %v", status.ManifestDiffs)
		}
		var unchanged time.Time
		if err := database.QueryRow("SELECT started_at FROM schema_migrations WHERE epoch=2").Scan(&unchanged); err != nil {
			t.Fatal(err)
		}
		if !unchanged.Equal(startedAt) {
			t.Fatalf("refused skipped state changed ledger timestamp: %s -> %s", startedAt, unchanged)
		}
	})

	t.Run("active environment codes remain canonical after epoch two", func(t *testing.T) {
		dsn, _ := harness.newDatabase(t)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		if _, err := MigrateUp(ctx, dsn, "", 45*time.Second, 10*time.Second); err != nil {
			t.Fatal(err)
		}
		database := openIntegrationDatabase(t, dsn)
		if _, err := database.Exec(`INSERT INTO env_configs (env, description_cn) VALUES ('Preview', 'Preview')`); err != nil {
			t.Fatal(err)
		}
		if _, err := database.Exec(`INSERT INTO apps
			(app_name, app_name_cn, owner, owner_cn, dev_language, git_url)
			VALUES ('env-contract', '环境契约', 'owner', '负责人', 'golang', 'https://example.invalid/env')`); err != nil {
			t.Fatal(err)
		}
		if _, err := database.Exec(`INSERT INTO app_configs (app_id, env, code_package_type)
			SELECT app_id, ' PROD ', 'golang' FROM apps WHERE app_name='env-contract'`); err != nil {
			t.Fatal(err)
		}
		status, err := InspectSchema(ctx, dsn, 45*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		if status.Compatible() || !containsProblem(status.ManifestDiffs, "active environment codes") {
			t.Fatalf("non-canonical active environment codes were accepted: %+v", status)
		}
	})

	t.Run("active app environments must resolve to visible catalog entries", func(t *testing.T) {
		dsn, _ := harness.newDatabase(t)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		if _, err := MigrateUp(ctx, dsn, "", 45*time.Second, 10*time.Second); err != nil {
			t.Fatal(err)
		}
		database := openIntegrationDatabase(t, dsn)
		if _, err := database.Exec(`INSERT INTO env_configs (env, description_cn)
			VALUES ('preview', 'Preview')`); err != nil {
			t.Fatal(err)
		}
		if _, err := database.Exec(`INSERT INTO apps
			(app_name, app_name_cn, owner, owner_cn, dev_language, git_url)
			VALUES ('catalog-contract', '环境目录契约', 'owner', '负责人', 'golang', 'https://example.invalid/catalog')`); err != nil {
			t.Fatal(err)
		}
		if _, err := database.Exec(`INSERT INTO app_configs (app_id, env, code_package_type)
			SELECT app_id, 'preview', 'golang' FROM apps WHERE app_name = 'catalog-contract'`); err != nil {
			t.Fatal(err)
		}
		if _, err := database.Exec("UPDATE env_configs SET deleted_at = CURRENT_TIMESTAMP WHERE env = 'preview'"); err != nil {
			t.Fatal(err)
		}
		status, err := InspectSchema(ctx, dsn, 45*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		if status.Compatible() || !containsProblem(status.ManifestDiffs, "do not resolve to visible catalog entries") {
			t.Fatalf("soft-deleted environment reference was accepted: %+v", status)
		}
	})

	t.Run("index direction and visibility are contractual", func(t *testing.T) {
		for _, test := range []struct {
			name      string
			statement string
			want      string
		}{
			{"invisible", "ALTER TABLE task_record ALTER INDEX idx_task_workflow_poll INVISIBLE", "VISIBLE=NO"},
			{"descending", `ALTER TABLE task_record DROP INDEX idx_task_workflow_poll,
				ADD INDEX operator_desc_poll (engine_version DESC, status, deleted_at, updated_at, task_id)`, "engine_version D"},
		} {
			t.Run(test.name, func(t *testing.T) {
				dsn, _ := harness.newDatabase(t)
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
				defer cancel()
				if _, err := MigrateUp(ctx, dsn, "", 45*time.Second, 10*time.Second); err != nil {
					t.Fatal(err)
				}
				database := openIntegrationDatabase(t, dsn)
				if _, err := database.Exec(test.statement); err != nil {
					t.Fatal(err)
				}
				status, err := InspectSchema(ctx, dsn, 45*time.Second)
				if err != nil {
					t.Fatal(err)
				}
				if status.Compatible() || !containsProblem(status.ManifestDiffs, test.want) {
					t.Fatalf("%s index drift was accepted: %+v", test.name, status)
				}
			})
		}
	})

	t.Run("character column collation is an exact epoch contract", func(t *testing.T) {
		dsn, _ := harness.newDatabase(t)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		if _, err := MigrateUp(ctx, dsn, "", 45*time.Second, 10*time.Second); err != nil {
			t.Fatal(err)
		}
		database := openIntegrationDatabase(t, dsn)
		if _, err := database.Exec(`ALTER TABLE apps MODIFY app_name
			VARCHAR(255) CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci NOT NULL`); err != nil {
			t.Fatal(err)
		}
		status, err := InspectSchema(ctx, dsn, 45*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		if status.Compatible() ||
			!containsProblem(status.ManifestDiffs, "列 apps.app_name 排序规则") ||
			!containsProblem(status.ManifestDiffs, "utf8mb4_0900_ai_ci") {
			t.Fatalf("app_name collation drift was accepted: %+v", status)
		}
	})

	t.Run("epoch four rejects unowned schema objects visible to migrator", func(t *testing.T) {
		dsn, _ := harness.newDatabase(t)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		if _, err := MigrateUp(ctx, dsn, "", 45*time.Second, 10*time.Second); err != nil {
			t.Fatal(err)
		}
		database := openIntegrationDatabase(t, dsn)
		for _, statement := range []string{
			"CREATE VIEW operator_apps AS SELECT app_id FROM apps",
			"ALTER TABLE apps ADD CONSTRAINT operator_owner CHECK (CHAR_LENGTH(owner) > 0)",
			"CREATE TRIGGER operator_apps_trigger BEFORE INSERT ON apps FOR EACH ROW SET NEW.owner = NEW.owner",
			"CREATE EVENT operator_cleanup ON SCHEDULE EVERY 1 DAY DO DELETE FROM apps WHERE 1=0",
			"CREATE PROCEDURE operator_repair() SELECT 1",
		} {
			if _, err := database.Exec(statement); err != nil {
				t.Fatalf("create schema drift %q: %v", statement, err)
			}
		}
		status, err := InspectSchema(ctx, dsn, 45*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		joined := strings.Join(status.ManifestDiffs, "\n")
		for _, want := range []string{"operator_apps", "operator_owner", "operator_apps_trigger", "operator_cleanup", "operator_repair"} {
			if !strings.Contains(joined, want) {
				t.Errorf("schema object %q was not rejected: %s", want, joined)
			}
		}
		if status.Compatible() {
			t.Fatalf("unowned schema objects were accepted: %+v", status)
		}
	})

	t.Run("AUTO_INCREMENT contract cannot be forged through a table comment", func(t *testing.T) {
		dsn, _ := harness.newDatabase(t)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		if _, err := MigrateUp(ctx, dsn, "", 45*time.Second, 10*time.Second); err != nil {
			t.Fatal(err)
		}
		database := openIntegrationDatabase(t, dsn)
		if _, err := database.Exec("ALTER TABLE apps AUTO_INCREMENT=1, COMMENT='AUTO_INCREMENT=10000'"); err != nil {
			t.Fatal(err)
		}
		status, err := InspectSchema(ctx, dsn, 45*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		if status.Compatible() || !containsProblem(status.ManifestDiffs, "AUTO_INCREMENT") {
			t.Fatalf("comment-forged AUTO_INCREMENT was accepted: %+v", status)
		}
		result, err := database.Exec(`INSERT INTO apps
			(app_name, app_name_cn, owner, owner_cn, dev_language, git_url)
			VALUES ('auto-increment-probe', 'probe', 'probe', 'probe', 'golang', 'https://example.invalid/probe')`)
		if err != nil {
			t.Fatal(err)
		}
		insertedID, err := result.LastInsertId()
		if err != nil {
			t.Fatal(err)
		}
		if insertedID >= 10000 {
			t.Fatalf("test did not lower the real AUTO_INCREMENT value: inserted id=%d", insertedID)
		}
	})

	t.Run("schema diagnostics remove terminal control characters from database identifiers", func(t *testing.T) {
		dsn, _ := harness.newDatabase(t)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		if _, err := MigrateUp(ctx, dsn, "", 45*time.Second, 10*time.Second); err != nil {
			t.Fatal(err)
		}
		database := openIntegrationDatabase(t, dsn)
		unsafeTableName := "operator_\x1b[31m\u202e"
		if _, err := database.Exec("CREATE TABLE `" + unsafeTableName + "` (id BIGINT PRIMARY KEY)"); err != nil {
			t.Fatal(err)
		}
		status, err := InspectSchema(ctx, dsn, 45*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		rendered := status.String()
		for _, character := range rendered {
			if unicode.IsControl(character) || unicode.In(character, unicode.Cf) {
				t.Fatalf("status output retains unsafe rune U+%04X: %q", character, rendered)
			}
		}
		if status.Compatible() || !strings.Contains(rendered, "operator_") {
			t.Fatalf("unsafe-name schema drift was not reported safely: %s", rendered)
		}
	})

	t.Run("migration ledger rejects attached check constraints", func(t *testing.T) {
		dsn, _ := harness.newDatabase(t)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		if _, err := MigrateUp(ctx, dsn, "", 45*time.Second, 10*time.Second); err != nil {
			t.Fatal(err)
		}
		database := openIntegrationDatabase(t, dsn)
		if _, err := database.Exec(`ALTER TABLE schema_migrations
			ADD CONSTRAINT operator_dirty_must_be_clean CHECK (dirty = 0)`); err != nil {
			t.Fatal(err)
		}
		status, err := InspectSchema(ctx, dsn, 45*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		if status.Compatible() || !containsProblem(status.Problems, "不支持的 CHECK") {
			t.Fatalf("ledger CHECK drift was accepted: %+v", status)
		}
	})

	t.Run("foreign keys must reference the current schema", func(t *testing.T) {
		_, externalDatabase := harness.newDatabase(t)
		// Register the target database cleanup after the referenced database so
		// LIFO cleanup removes the foreign key owner first.
		dsn, _ := harness.newDatabase(t)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		if _, err := MigrateUp(ctx, dsn, "", 45*time.Second, 10*time.Second); err != nil {
			t.Fatal(err)
		}
		database := openIntegrationDatabase(t, dsn)
		if _, err := database.Exec(fmt.Sprintf(`CREATE TABLE %s.pipelines (
			job_name VARCHAR(100) NOT NULL PRIMARY KEY
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci`,
			"`"+externalDatabase+"`")); err != nil {
			t.Fatal(err)
		}
		if _, err := database.Exec(fmt.Sprintf(`ALTER TABLE pipelines_job_combination
			DROP FOREIGN KEY fk_pipelines_ci_job,
			ADD CONSTRAINT operator_cross_schema_ci FOREIGN KEY (ci_job_name)
			REFERENCES %s.pipelines (job_name) ON DELETE RESTRICT ON UPDATE CASCADE`,
			"`"+externalDatabase+"`")); err != nil {
			t.Fatal(err)
		}
		status, err := InspectSchema(ctx, dsn, 45*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		if status.Compatible() || !containsProblem(status.ManifestDiffs, "EXTERNAL SCHEMA") {
			t.Fatalf("cross-schema foreign key was accepted: %+v", status)
		}
	})

	t.Run("visible external inbound foreign keys to managed tables are rejected", func(t *testing.T) {
		dsn, databaseName := harness.newDatabase(t)
		// LIFO cleanup drops the external child schema before the referenced
		// Ares schema.
		_, externalDatabase := harness.newDatabase(t)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		if _, err := MigrateUp(ctx, dsn, "", 45*time.Second, 10*time.Second); err != nil {
			t.Fatal(err)
		}
		database := openIntegrationDatabase(t, dsn)
		if _, err := database.Exec(fmt.Sprintf(`CREATE TABLE %s.inbound_apps (
			id BIGINT NOT NULL PRIMARY KEY,
			app_id INT NOT NULL,
			CONSTRAINT operator_inbound_apps FOREIGN KEY (app_id)
			REFERENCES %s.apps (app_id) ON DELETE RESTRICT ON UPDATE CASCADE
		) ENGINE=InnoDB`, "`"+externalDatabase+"`", "`"+databaseName+"`")); err != nil {
			t.Fatal(err)
		}
		status, err := InspectSchema(ctx, dsn, 45*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		if status.Compatible() || !containsProblem(status.ManifestDiffs, "反向引用") ||
			!containsProblem(status.ManifestDiffs, externalDatabase) {
			t.Fatalf("external inbound foreign key was accepted: %+v", status)
		}
	})

	t.Run("interrupted bootstrap rejects malformed fixed seed before inserts", func(t *testing.T) {
		dsn, _ := harness.newDatabase(t)
		database := openIntegrationDatabase(t, dsn)
		session := &migrationSession{ctx: context.Background(), executor: database, operationTimeout: 45 * time.Second}
		if err := session.createMigrationLedger(); err != nil {
			t.Fatal(err)
		}
		for _, table := range schemaBootstrapTables {
			if _, err := database.Exec(table.statement); err != nil {
				t.Fatal(err)
			}
		}
		const malformed = `{"allowed":["jar"],"default":"jar"}`
		if _, err := database.Exec("INSERT INTO dev_language_rules (dev_language, rules) VALUES ('java', ?)", malformed); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		_, err := MigrateUp(ctx, dsn, "", 45*time.Second, 10*time.Second)
		assertSchemaStateError(t, err)
		if !strings.Contains(err.Error(), "语义不匹配") {
			t.Fatalf("malformed bootstrap seed error = %v", err)
		}
		var count int
		var rules string
		if err := database.QueryRow("SELECT COUNT(*), MAX(CAST(rules AS CHAR)) FROM dev_language_rules").Scan(&count, &rules); err != nil {
			t.Fatal(err)
		}
		gotCanonical, canonicalErr := canonicalJSON(rules)
		wantCanonical, _ := canonicalJSON(malformed)
		if count != 1 || canonicalErr != nil || gotCanonical != wantCanonical {
			t.Fatalf("refused bootstrap changed seeds: count=%d rules=%s", count, rules)
		}
	})

	t.Run("applied epoch drift is rejected before the next dirty row", func(t *testing.T) {
		dsn, _ := harness.newDatabase(t)
		loadSQLFixture(t, dsn, preW04FixturePath)
		database := openIntegrationDatabase(t, dsn)
		session := &migrationSession{
			ctx: context.Background(), executor: database, operationTimeout: 45 * time.Second,
		}
		if err := session.adoptLegacyLedger(); err != nil {
			t.Fatal(err)
		}
		if _, err := database.Exec("ALTER TABLE task_step_records DROP INDEX idx_step_status_uses"); err != nil {
			t.Fatal(err)
		}

		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		status, err := MigrateUp(ctx, dsn, "", 45*time.Second, 10*time.Second)
		assertSchemaStateError(t, err)
		if !containsProblem(status.ManifestDiffs, "idx_step_status_uses") &&
			!containsProblem(status.ManifestDiffs, "普通索引") {
			t.Fatalf("epoch-3 postcondition drift = %v", status.ManifestDiffs)
		}
		var count int
		if err := database.QueryRow("SELECT COUNT(*) FROM schema_migrations").Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 3 {
			t.Fatalf("drifted epoch-3 schema gained a dirty row: ledger count=%d", count)
		}
	})

	t.Run("earlier applied postcondition drift is rejected before the next dirty row", func(t *testing.T) {
		dsn, _ := harness.newDatabase(t)
		loadSQLFixture(t, dsn, preW04FixturePath)
		database := openIntegrationDatabase(t, dsn)
		session := &migrationSession{
			ctx: context.Background(), executor: database, operationTimeout: 45 * time.Second,
		}
		if err := session.adoptLegacyLedger(); err != nil {
			t.Fatal(err)
		}
		if _, err := database.Exec(`UPDATE apps SET description_cn = 'NULL' WHERE app_id = 12345`); err != nil {
			t.Fatal(err)
		}

		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		status, err := MigrateUp(ctx, dsn, "", 45*time.Second, 10*time.Second)
		assertSchemaStateError(t, err)
		if !containsProblem(status.ManifestDiffs, "non-canonical empty values") {
			t.Fatalf("epoch-1 postcondition drift = %v", status.ManifestDiffs)
		}
		var count int
		if err := database.QueryRow("SELECT COUNT(*) FROM schema_migrations").Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 3 {
			t.Fatalf("earlier postcondition drift gained a dirty row: ledger count=%d", count)
		}
	})

	t.Run("dirty migration requires an exact explicit resume", func(t *testing.T) {
		dsn, _ := harness.newDatabase(t)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		if _, err := MigrateUp(ctx, dsn, "", 45*time.Second, 10*time.Second); err != nil {
			t.Fatal(err)
		}
		database := openIntegrationDatabase(t, dsn)
		if _, err := database.Exec(`UPDATE schema_migrations
			SET dirty = 1, finished_at = NULL, last_error = 'simulated interruption'
			WHERE version = ?`, versionedSchemaMigrationVersion); err != nil {
			t.Fatal(err)
		}
		var startedAt time.Time
		if err := database.QueryRow("SELECT started_at FROM schema_migrations WHERE version = ?", versionedSchemaMigrationVersion).Scan(&startedAt); err != nil {
			t.Fatal(err)
		}

		status, err := MigrateUp(ctx, dsn, "", 45*time.Second, 10*time.Second)
		assertSchemaStateError(t, err)
		if status.Dirty == nil || status.Dirty.Version != versionedSchemaMigrationVersion {
			t.Fatalf("dirty status = %+v, want %s", status.Dirty, versionedSchemaMigrationVersion)
		}
		var unchangedStartedAt time.Time
		if err := database.QueryRow("SELECT started_at FROM schema_migrations WHERE version = ?", versionedSchemaMigrationVersion).Scan(&unchangedStartedAt); err != nil {
			t.Fatal(err)
		}
		if !unchangedStartedAt.Equal(startedAt) {
			t.Fatalf("refusing an implicit resume changed started_at: before=%s after=%s", startedAt, unchangedStartedAt)
		}

		_, err = MigrateUp(ctx, dsn, schemaMigrations[2].version, 45*time.Second, 10*time.Second)
		assertSchemaStateError(t, err)
		status, err = MigrateUp(ctx, dsn, versionedSchemaMigrationVersion, 45*time.Second, 10*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		assertCompatibleStatus(t, status)
		var dirty bool
		var resumedStartedAt time.Time
		var finishedAt sql.NullTime
		var lastError sql.NullString
		if err := database.QueryRow(`SELECT dirty, started_at, finished_at, last_error
			FROM schema_migrations WHERE version = ?`, versionedSchemaMigrationVersion).
			Scan(&dirty, &resumedStartedAt, &finishedAt, &lastError); err != nil {
			t.Fatal(err)
		}
		if dirty || !finishedAt.Valid || lastError.Valid {
			t.Fatalf("resumed ledger state = dirty:%t finished:%v error:%v", dirty, finishedAt, lastError)
		}
		if !resumedStartedAt.Equal(startedAt) {
			t.Fatalf("successful resume changed started_at: before=%s after=%s", startedAt, resumedStartedAt)
		}
	})

	t.Run("dirty resume accepts the initial crash marker with null diagnostics", func(t *testing.T) {
		dsn, _ := harness.newDatabase(t)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		if _, err := MigrateUp(ctx, dsn, "", 45*time.Second, 10*time.Second); err != nil {
			t.Fatal(err)
		}
		database := openIntegrationDatabase(t, dsn)
		if _, err := database.Exec(`UPDATE schema_migrations
			SET dirty = 1, finished_at = NULL, last_error = NULL
			WHERE version = ?`, versionedSchemaMigrationVersion); err != nil {
			t.Fatal(err)
		}

		status, err := MigrateUp(ctx, dsn, versionedSchemaMigrationVersion, 45*time.Second, 10*time.Second)
		if err != nil {
			t.Fatalf("resume initial dirty marker: %v", err)
		}
		assertCompatibleStatus(t, status)
		var dirty bool
		var finishedAt sql.NullTime
		var lastError sql.NullString
		if err := database.QueryRow(`SELECT dirty, finished_at, last_error
			FROM schema_migrations WHERE version = ?`, versionedSchemaMigrationVersion).
			Scan(&dirty, &finishedAt, &lastError); err != nil {
			t.Fatal(err)
		}
		if dirty || !finishedAt.Valid || lastError.Valid {
			t.Fatalf("resumed initial dirty marker = dirty:%t finished:%v error:%v", dirty, finishedAt, lastError)
		}
	})

	t.Run("failed migration returns refreshed dirty status", func(t *testing.T) {
		dsn, _ := harness.newDatabase(t)
		loadSQLFixture(t, dsn, preW04FixturePath)

		original := schemaMigrations[3]
		t.Cleanup(func() { schemaMigrations[3] = original })
		schemaMigrations[3].up = func(*migrationSession) error {
			return errors.New("password=do-not-print forced migration failure")
		}

		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		status, err := MigrateUp(ctx, dsn, "", 45*time.Second, 10*time.Second)
		if err == nil || errors.Is(err, ErrSchemaState) {
			t.Fatalf("forced migration error = %v, want operational failure", err)
		}
		if strings.Contains(err.Error(), "do-not-print") {
			t.Fatalf("forced migration error leaked secret: %v", err)
		}
		if status.Dirty == nil || status.Dirty.Epoch != 4 || status.Dirty.Version != versionedSchemaMigrationVersion {
			t.Fatalf("returned status did not refresh dirty epoch 4: %+v", status)
		}
		if strings.Contains(status.Dirty.Error, "do-not-print") || !strings.Contains(status.Dirty.Error, "password=<redacted>") {
			t.Fatalf("returned dirty error was not sanitized: %q", status.Dirty.Error)
		}
	})

	t.Run("dirty resume validates all completed predecessors before writes", func(t *testing.T) {
		dsn, _ := harness.newDatabase(t)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		database := migrateDatabaseToEpoch(t, dsn, 2)
		if _, err := database.Exec(`UPDATE schema_migrations
			SET dirty = 1, finished_at = NULL, last_error = 'simulated epoch-2 interruption'
			WHERE epoch = 2`); err != nil {
			t.Fatal(err)
		}
		if _, err := database.Exec(`INSERT INTO apps
			(app_id, app_name, app_name_cn, owner, owner_cn, dev_language, description_cn, git_url)
			VALUES (10000, 'dirty-resume', 'Dirty Resume', 'owner', '负责人', 'golang', 'NULL', 'https://example.invalid/repo')`); err != nil {
			t.Fatal(err)
		}
		var startedAt time.Time
		if err := database.QueryRow("SELECT started_at FROM schema_migrations WHERE epoch = 2").Scan(&startedAt); err != nil {
			t.Fatal(err)
		}

		status, err := MigrateUp(ctx, dsn, schemaMigrations[1].version, 45*time.Second, 10*time.Second)
		assertSchemaStateError(t, err)
		if !containsProblem(status.ManifestDiffs, "non-canonical empty values") {
			t.Fatalf("dirty predecessor drift = %v", status.ManifestDiffs)
		}
		var count int
		var dirty bool
		var unchangedStartedAt time.Time
		if err := database.QueryRow(`SELECT COUNT(*),
			MAX(epoch = 2 AND dirty = 1),
			MAX(CASE WHEN epoch = 2 THEN started_at END)
			FROM schema_migrations`).Scan(&count, &dirty, &unchangedStartedAt); err != nil {
			t.Fatal(err)
		}
		if count != 2 || !dirty || !unchangedStartedAt.Equal(startedAt) {
			t.Fatalf("refused dirty resume changed ledger: count=%d dirty=%t started=%s want=%s",
				count, dirty, unchangedStartedAt, startedAt)
		}
	})

	t.Run("checksum mismatch fails closed", func(t *testing.T) {
		dsn, _ := harness.newDatabase(t)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		if _, err := MigrateUp(ctx, dsn, "", 45*time.Second, 10*time.Second); err != nil {
			t.Fatal(err)
		}
		database := openIntegrationDatabase(t, dsn)
		const tamperedChecksum = "0000000000000000000000000000000000000000000000000000000000000000"
		if _, err := database.Exec("UPDATE schema_migrations SET checksum = ? WHERE epoch = 2", tamperedChecksum); err != nil {
			t.Fatal(err)
		}
		status, err := MigrateUp(ctx, dsn, "", 45*time.Second, 10*time.Second)
		assertSchemaStateError(t, err)
		if !containsProblem(status.Problems, "checksum") {
			t.Fatalf("checksum mismatch status does not explain the problem: %+v", status)
		}
		var checksum string
		if err := database.QueryRow("SELECT checksum FROM schema_migrations WHERE epoch = 2").Scan(&checksum); err != nil {
			t.Fatal(err)
		}
		if checksum != tamperedChecksum {
			t.Fatalf("fail-closed migration rewrote a tampered checksum: %s", checksum)
		}
	})

	t.Run("compatibility metadata mismatch fails closed", func(t *testing.T) {
		dsn, _ := harness.newDatabase(t)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		if _, err := MigrateUp(ctx, dsn, "", 45*time.Second, 10*time.Second); err != nil {
			t.Fatal(err)
		}
		database := openIntegrationDatabase(t, dsn)
		if _, err := database.Exec(`UPDATE schema_migrations
			SET compatible_min = 1, compatible_max = 99 WHERE epoch = 4`); err != nil {
			t.Fatal(err)
		}
		status, err := MigrateUp(ctx, dsn, "", 45*time.Second, 10*time.Second)
		assertSchemaStateError(t, err)
		if !containsProblem(status.Problems, "兼容区间不匹配") {
			t.Fatalf("compatibility metadata mismatch is not explained: %+v", status)
		}
		var minimum, maximum uint64
		if err := database.QueryRow(`SELECT compatible_min, compatible_max
			FROM schema_migrations WHERE epoch = 4`).Scan(&minimum, &maximum); err != nil {
			t.Fatal(err)
		}
		if minimum != 1 || maximum != 99 {
			t.Fatalf("fail-closed migration rewrote compatibility metadata: [%d,%d]", minimum, maximum)
		}
	})

	t.Run("latest epoch retains earlier data invariants", func(t *testing.T) {
		dsn, _ := harness.newDatabase(t)
		loadSQLFixture(t, dsn, preW04FixturePath)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		if _, err := MigrateUp(ctx, dsn, "", 45*time.Second, 10*time.Second); err != nil {
			t.Fatal(err)
		}
		database := openIntegrationDatabase(t, dsn)
		if _, err := database.Exec(`UPDATE apps SET description_cn = 'NULL' WHERE app_id = 12345`); err != nil {
			t.Fatal(err)
		}

		status, err := InspectSchema(ctx, dsn, 45*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		if status.Compatible() || !containsProblem(status.ManifestDiffs, "non-canonical empty values") {
			t.Fatalf("latest epoch accepted an earlier data-invariant drift: %+v", status)
		}
	})

	t.Run("unknown epoch fails closed", func(t *testing.T) {
		dsn, _ := harness.newDatabase(t)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		if _, err := MigrateUp(ctx, dsn, "", 45*time.Second, 10*time.Second); err != nil {
			t.Fatal(err)
		}
		database := openIntegrationDatabase(t, dsn)
		if _, err := database.Exec(`INSERT INTO schema_migrations
			(version, epoch, description, checksum, dirty, started_at, finished_at,
			 compatible_min, compatible_max, last_error, legacy_adopted)
			VALUES ('20990101_001_unknown', 5, 'unknown future migration',
			'1111111111111111111111111111111111111111111111111111111111111111',
			0, NOW(6), NOW(6), 5, 5, NULL, 0)`); err != nil {
			t.Fatal(err)
		}
		status, err := MigrateUp(ctx, dsn, "", 45*time.Second, 10*time.Second)
		assertSchemaStateError(t, err)
		if !containsProblem(status.Problems, "未知 epoch") {
			t.Fatalf("unknown epoch status does not explain the problem: %+v", status)
		}
		var count int
		if err := database.QueryRow("SELECT COUNT(*) FROM schema_migrations").Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != len(schemaMigrations)+1 {
			t.Fatalf("fail-closed migration changed unknown ledger rows: got %d", count)
		}
	})

	t.Run("current manifest distinguishes primary keys from unique indexes", func(t *testing.T) {
		dsn, _ := harness.newDatabase(t)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		if _, err := MigrateUp(ctx, dsn, "", 45*time.Second, 10*time.Second); err != nil {
			t.Fatal(err)
		}
		database := openIntegrationDatabase(t, dsn)
		if _, err := database.Exec(`ALTER TABLE apps
			DROP PRIMARY KEY, ADD UNIQUE INDEX operator_app_id_unique (app_id)`); err != nil {
			t.Fatal(err)
		}
		status, err := InspectSchema(ctx, dsn, 45*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		if status.Compatible() || !containsProblem(status.ManifestDiffs, "缺少主键") {
			t.Fatalf("primary-key drift was not rejected: %+v", status)
		}
	})

	t.Run("concurrent migrators converge without duplicate ledger rows", func(t *testing.T) {
		dsn, databaseName := harness.newDatabase(t)
		database := openIntegrationDatabase(t, dsn)
		lockConnection, err := database.Conn(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		defer lockConnection.Close()
		lockName := integrationMigrationLockName(databaseName)
		var acquired int
		if err := lockConnection.QueryRowContext(context.Background(), "SELECT GET_LOCK(?, 0)", lockName).Scan(&acquired); err != nil {
			t.Fatal(err)
		}
		if acquired != 1 {
			t.Fatalf("precondition lock acquisition = %d, want 1", acquired)
		}

		temporaryDirectory := t.TempDir()
		startFile := filepath.Join(temporaryDirectory, "start")
		commands := make([]*exec.Cmd, 0, 2)
		outputs := make([]bytes.Buffer, 2)
		for index := range 2 {
			readyFile := filepath.Join(temporaryDirectory, fmt.Sprintf("ready-%d", index))
			command := exec.Command(os.Args[0], "-test.run=^TestMigrationProcessHelper$")
			command.Env = append(os.Environ(),
				migrationProcessHelperEnv+"=1",
				"ARES_TEST_MIGRATION_PROCESS_DSN="+dsn,
				"ARES_TEST_MIGRATION_PROCESS_READY="+readyFile,
				"ARES_TEST_MIGRATION_PROCESS_START="+startFile,
			)
			command.Stdout = &outputs[index]
			command.Stderr = &outputs[index]
			if err := command.Start(); err != nil {
				t.Fatal(err)
			}
			commands = append(commands, command)
		}
		deadline := time.Now().Add(15 * time.Second)
		for index := range 2 {
			readyFile := filepath.Join(temporaryDirectory, fmt.Sprintf("ready-%d", index))
			for {
				if _, err := os.Stat(readyFile); err == nil {
					break
				} else if !os.IsNotExist(err) {
					t.Fatal(err)
				}
				if time.Now().After(deadline) {
					t.Fatalf("migration helper %d did not become ready: %s", index, outputs[index].String())
				}
				time.Sleep(10 * time.Millisecond)
			}
		}
		if err := os.WriteFile(startFile, []byte("start\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		// Both independent processes have completed driver startup and receive the
		// same start signal while this connection still owns the database lock.
		time.Sleep(250 * time.Millisecond)
		var released int
		if err := lockConnection.QueryRowContext(context.Background(), "SELECT RELEASE_LOCK(?)", lockName).Scan(&released); err != nil {
			t.Fatal(err)
		}
		if released != 1 {
			t.Fatalf("precondition lock release = %d, want 1", released)
		}
		for index, command := range commands {
			if err := command.Wait(); err != nil {
				t.Fatalf("migration helper %d failed: %v\n%s", index, err, outputs[index].String())
			}
		}
		if got := len(readLedgerStamps(t, database)); got != len(schemaMigrations) {
			t.Fatalf("concurrent migration ledger rows = %d, want %d", got, len(schemaMigrations))
		}
	})

	t.Run("lock timeout is operational and writes no ledger", func(t *testing.T) {
		dsn, databaseName := harness.newDatabase(t)
		database := openIntegrationDatabase(t, dsn)
		lockConnection, err := database.Conn(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		defer lockConnection.Close()
		lockName := integrationMigrationLockName(databaseName)
		var acquired int
		if err := lockConnection.QueryRowContext(context.Background(), "SELECT GET_LOCK(?, 0)", lockName).Scan(&acquired); err != nil {
			t.Fatal(err)
		}
		if acquired != 1 {
			t.Fatalf("precondition lock acquisition = %d, want 1", acquired)
		}
		defer func() {
			var released sql.NullInt64
			_ = lockConnection.QueryRowContext(context.Background(), "SELECT RELEASE_LOCK(?)", lockName).Scan(&released)
		}()

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		started := time.Now()
		_, err = MigrateUp(ctx, dsn, "", 45*time.Second, 100*time.Millisecond)
		elapsed := time.Since(started)
		if err == nil || !strings.Contains(err.Error(), "timed out waiting for database migration lock") {
			t.Fatalf("lock timeout error = %v", err)
		}
		if elapsed < 75*time.Millisecond || elapsed > time.Second {
			t.Fatalf("lock timeout elapsed %s, want configured sub-second deadline", elapsed)
		}
		if errors.Is(err, ErrSchemaState) {
			t.Fatalf("lock timeout was classified as a schema state error: %v", err)
		}
		if got := harness.tableCount(t, databaseName); got != 0 {
			t.Fatalf("lock timeout created %d tables, want 0", got)
		}
	})

	t.Run("runtime account can inspect but cannot execute DDL", func(t *testing.T) {
		dsn, databaseName := harness.newDatabase(t)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		if _, err := MigrateUp(ctx, dsn, "", 45*time.Second, 10*time.Second); err != nil {
			t.Fatal(err)
		}

		runtimeDSN := harness.newRuntimeUser(t, dsn, databaseName)
		status, err := InspectSchema(ctx, runtimeDSN, 45*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		assertCompatibleStatus(t, status)
		database := openIntegrationDatabase(t, runtimeDSN)
		if err := CheckRuntimeCompatibility(ctx, database); err != nil {
			t.Fatalf("runtime compatibility check needs only read privileges: %v", err)
		}
		if _, err := database.Exec(`INSERT INTO integration_settings (provider, config_data)
			VALUES ('integration-test', '{"enabled":true}')`); err != nil {
			t.Fatalf("runtime account cannot perform normal business DML: %v", err)
		}
		_, err = database.Exec("CREATE TABLE runtime_must_not_create_tables (id INT PRIMARY KEY)")
		if err == nil {
			t.Fatal("runtime account unexpectedly executed DDL")
		}
		var permissionError *mysql.MySQLError
		if !errors.As(err, &permissionError) || permissionError.Number != 1142 {
			t.Fatalf("runtime DDL error = %v, want MySQL command-denied error 1142", err)
		}
		_, err = database.Exec("UPDATE schema_migrations SET last_error = 'tampered' WHERE epoch = 4")
		if err == nil {
			t.Fatal("runtime account unexpectedly modified the migration ledger")
		}
		if !errors.As(err, &permissionError) || permissionError.Number != 1142 {
			t.Fatalf("runtime ledger write error = %v, want MySQL command-denied error 1142", err)
		}
	})
}

type mysqlIntegrationHarness struct {
	admin       *sql.DB
	adminConfig *mysql.Config
	next        int
}

func newMySQLIntegrationHarness(t *testing.T) *mysqlIntegrationHarness {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv(mysqlIntegrationDSNEnv))
	if dsn == "" {
		t.Skipf("set %s to a MySQL 8.4 administrative DSN to run integration tests", mysqlIntegrationDSNEnv)
	}
	configuration, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatalf("parse %s: %v", mysqlIntegrationDSNEnv, err)
	}
	configuration.ParseTime = true
	admin, err := sql.Open("mysql", configuration.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := admin.PingContext(ctx); err != nil {
		t.Fatalf("connect to MySQL integration server: %v", err)
	}
	var version string
	if err := admin.QueryRowContext(ctx, "SELECT VERSION()").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(version, "8.4.") {
		t.Fatalf("integration server version = %q, want MySQL 8.4.x", version)
	}
	return &mysqlIntegrationHarness{admin: admin, adminConfig: configuration}
}

func (h *mysqlIntegrationHarness) newDatabase(t *testing.T) (string, string) {
	t.Helper()
	h.next++
	databaseName := fmt.Sprintf("ares_w04_it_%d_%d", time.Now().UnixNano(), h.next)
	if !safeSQLIdentifier(databaseName) {
		t.Fatalf("generated unsafe database name %q", databaseName)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := h.admin.ExecContext(ctx, fmt.Sprintf(
		"CREATE DATABASE `%s` CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci", databaseName)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		if _, err := h.admin.ExecContext(cleanupCtx, fmt.Sprintf("DROP DATABASE IF EXISTS `%s`", databaseName)); err != nil {
			t.Errorf("drop integration database %s: %v", databaseName, err)
		}
	})
	target := h.adminConfig.Clone()
	target.DBName = databaseName
	target.ParseTime = true
	return target.FormatDSN(), databaseName
}

func (h *mysqlIntegrationHarness) tableCount(t *testing.T, databaseName string) int {
	t.Helper()
	var count int
	if err := h.admin.QueryRow(`SELECT COUNT(*) FROM information_schema.TABLES
		WHERE TABLE_SCHEMA = ? AND TABLE_TYPE = 'BASE TABLE'`, databaseName).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func (h *mysqlIntegrationHarness) newRuntimeUser(t *testing.T, targetDSN, databaseName string) string {
	t.Helper()
	h.next++
	username := fmt.Sprintf("aresrt_%x", time.Now().UnixNano()+int64(h.next))
	password := fmt.Sprintf("Ares-W04-%x", time.Now().UnixNano())
	account := sqlStringLiteral(username) + "@'%'"
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := h.admin.ExecContext(ctx, "CREATE USER "+account+" IDENTIFIED BY "+sqlStringLiteral(password)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		if _, err := h.admin.ExecContext(cleanupCtx, "DROP USER IF EXISTS "+account); err != nil {
			t.Errorf("drop integration runtime user %s: %v", username, err)
		}
	})
	var partialRevokes int
	if err := h.admin.QueryRowContext(ctx, "SELECT @@GLOBAL.partial_revokes").Scan(&partialRevokes); err != nil {
		t.Fatal(err)
	}
	grantPattern := guardedMigrationDatabaseGrantPattern(databaseName, partialRevokes == 1)
	if _, err := h.admin.ExecContext(ctx, fmt.Sprintf(
		"GRANT SELECT ON `%s`.* TO %s", grantPattern, account)); err != nil {
		t.Fatal(err)
	}
	for _, tableName := range sortedStringKeys(epoch4SemanticSchemaManifest.tables) {
		if _, err := h.admin.ExecContext(ctx, fmt.Sprintf(
			"GRANT INSERT, UPDATE, DELETE ON `%s`.`%s` TO %s", databaseName, tableName, account)); err != nil {
			t.Fatalf("grant runtime DML on %s: %v", tableName, err)
		}
	}
	configuration, err := mysql.ParseDSN(targetDSN)
	if err != nil {
		t.Fatal(err)
	}
	configuration.User = username
	configuration.Passwd = password
	configuration.ParseTime = true
	return configuration.FormatDSN()
}

func (h *mysqlIntegrationHarness) newGuardedMigrationUser(t *testing.T, targetDSN, databaseName string) (string, string) {
	t.Helper()
	h.next++
	username := fmt.Sprintf("aresmig_%x", time.Now().UnixNano()+int64(h.next))
	password := fmt.Sprintf("Aa1!AresW04%x", time.Now().UnixNano())
	account := sqlStringLiteral(username) + "@'%'"
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := h.admin.ExecContext(ctx, "CREATE USER "+account+" IDENTIFIED BY "+sqlStringLiteral(password)+" ACCOUNT LOCK"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		if _, err := h.admin.ExecContext(cleanupCtx, "DROP USER IF EXISTS "+account); err != nil {
			t.Errorf("drop integration migration user %s: %v", username, err)
		}
	})
	var partialRevokes int
	if err := h.admin.QueryRowContext(ctx, "SELECT @@GLOBAL.partial_revokes").Scan(&partialRevokes); err != nil {
		t.Fatal(err)
	}
	grantPattern := guardedMigrationDatabaseGrantPattern(databaseName, partialRevokes == 1)
	if _, err := h.admin.ExecContext(ctx, fmt.Sprintf(
		"GRANT SELECT, INSERT, UPDATE, DELETE, CREATE, ALTER, INDEX, REFERENCES ON `%s`.* TO %s",
		grantPattern, account)); err != nil {
		t.Fatal(err)
	}
	configuration, err := mysql.ParseDSN(targetDSN)
	if err != nil {
		t.Fatal(err)
	}
	configuration.User = username
	configuration.Passwd = password
	configuration.ParseTime = true
	return configuration.FormatDSN(), username
}

func (h *mysqlIntegrationHarness) assertGuardedMigrationAccount(t *testing.T, username string, wantLocked bool, wantSessions int) {
	t.Helper()
	var locked string
	if err := h.admin.QueryRow(
		"SELECT account_locked FROM mysql.user WHERE BINARY User = BINARY ? AND Host = '%'", username).Scan(&locked); err != nil {
		t.Fatal(err)
	}
	wantLockState := "N"
	if wantLocked {
		wantLockState = "Y"
	}
	if locked != wantLockState {
		t.Fatalf("migration account %s lock state = %s, want %s", username, locked, wantLockState)
	}
	var sessions int
	if err := h.admin.QueryRow(
		"SELECT COUNT(*) FROM information_schema.PROCESSLIST WHERE BINARY USER = BINARY ?", username).Scan(&sessions); err != nil {
		t.Fatal(err)
	}
	if sessions != wantSessions {
		t.Fatalf("migration account %s session count = %d, want %d", username, sessions, wantSessions)
	}
}

func assertIntegrationDSNCannotConnect(t *testing.T, dsn string) {
	t.Helper()
	database, err := sql.Open("mysql", dsn)
	if err != nil {
		return
	}
	defer database.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := database.PingContext(ctx); err == nil {
		t.Fatal("guarded migration credential unexpectedly established a new connection")
	}
}

func sqlStringLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func integrationMigrationLockName(databaseName string) string {
	digest := sha256.Sum256([]byte(databaseName))
	return "ares_schema_migration_" + hex.EncodeToString(digest[:16])
}

func openIntegrationDatabase(t *testing.T, dsn string) *sql.DB {
	t.Helper()
	database, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := database.PingContext(ctx); err != nil {
		t.Fatal(err)
	}
	return database
}

func migrateDatabaseToEpoch(t *testing.T, dsn string, epoch uint64) *sql.DB {
	t.Helper()
	if epoch == 0 || epoch > uint64(len(schemaMigrations)) {
		t.Fatalf("invalid test epoch %d", epoch)
	}
	database := openIntegrationDatabase(t, dsn)
	session := &migrationSession{
		ctx: context.Background(), executor: database, operationTimeout: 45 * time.Second,
	}
	if err := session.createMigrationLedger(); err != nil {
		t.Fatal(err)
	}
	if err := session.bootstrapEmptySchema(); err != nil {
		t.Fatal(err)
	}
	for index := uint64(0); index < epoch; index++ {
		if err := session.executeDirtyMigration(schemaMigrations[index], false); err != nil {
			t.Fatalf("migrate test database to epoch %d: %v", index+1, err)
		}
	}
	status, err := inspectSchema(context.Background(), session)
	if err != nil {
		t.Fatal(err)
	}
	if !status.Compatible() && len(status.Pending) == 0 {
		t.Fatalf("test database epoch %d is not a valid prefix: %+v", epoch, status)
	}
	if status.CurrentEpoch != epoch || len(status.ManifestDiffs) > 0 || len(status.Problems) > 0 {
		t.Fatalf("test database epoch %d status = %+v", epoch, status)
	}
	return database
}

func insertDirtyMigrationRow(t *testing.T, database *sql.DB, migration schemaMigration) {
	t.Helper()
	if _, err := database.Exec(`INSERT INTO schema_migrations
		(version, epoch, description, checksum, dirty, started_at, finished_at,
		 compatible_min, compatible_max, last_error, legacy_adopted)
		VALUES (?, ?, ?, ?, 1, NOW(6), NULL, ?, ?, 'simulated interruption', 0)`,
		migration.version, migration.epoch, migration.description, migration.checksum(),
		migration.compatibleMin, migration.compatibleMax); err != nil {
		t.Fatalf("insert dirty migration %s: %v", migration.version, err)
	}
}

func convertLedgerToLegacy(t *testing.T, database *sql.DB) {
	t.Helper()
	statement := `ALTER TABLE schema_migrations
		DROP INDEX uq_schema_migrations_epoch,
		DROP COLUMN epoch,
		DROP COLUMN description,
		DROP COLUMN checksum,
		DROP COLUMN dirty,
		DROP COLUMN started_at,
		DROP COLUMN finished_at,
		DROP COLUMN compatible_min,
		DROP COLUMN compatible_max,
		DROP COLUMN last_error,
		DROP COLUMN legacy_adopted`
	if _, err := database.Exec(statement); err != nil {
		t.Fatalf("convert migration ledger to legacy shape: %v", err)
	}
}

func loadSQLFixture(t *testing.T, dsn, path string) {
	t.Helper()
	content, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		t.Fatal(err)
	}
	configuration, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatal(err)
	}
	configuration.MultiStatements = true
	configuration.ParseTime = true
	database := openIntegrationDatabase(t, configuration.FormatDSN())
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if _, err := database.ExecContext(ctx, string(content)); err != nil {
		t.Fatalf("load fixture %s: %v", path, err)
	}
}

type ledgerStamp struct {
	Version    string
	Checksum   string
	Dirty      bool
	StartedAt  time.Time
	FinishedAt sql.NullTime
	AppliedAt  time.Time
}

func readLedgerStamps(t *testing.T, database *sql.DB) []ledgerStamp {
	t.Helper()
	rows, err := database.Query(`SELECT version, checksum, dirty, started_at, finished_at, applied_at
		FROM schema_migrations ORDER BY epoch`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	result := make([]ledgerStamp, 0, len(schemaMigrations))
	for rows.Next() {
		var stamp ledgerStamp
		if err := rows.Scan(&stamp.Version, &stamp.Checksum, &stamp.Dirty, &stamp.StartedAt, &stamp.FinishedAt, &stamp.AppliedAt); err != nil {
			t.Fatal(err)
		}
		result = append(result, stamp)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}

func ledgerColumnNames(t *testing.T, database *sql.DB) []string {
	t.Helper()
	rows, err := database.Query(`SELECT COLUMN_NAME FROM information_schema.COLUMNS
		WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'schema_migrations'
		ORDER BY ORDINAL_POSITION`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	result := make([]string, 0)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		result = append(result, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}

func assertLegacyLedgerUnchanged(t *testing.T, database *sql.DB, wantRows int) {
	t.Helper()
	var columns, rows int
	if err := database.QueryRow(`SELECT COUNT(*) FROM information_schema.COLUMNS
		WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'schema_migrations'`).Scan(&columns); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow("SELECT COUNT(*) FROM schema_migrations").Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if columns != 2 || rows != wantRows {
		t.Fatalf("refused legacy adoption changed ledger: columns=%d rows=%d, want columns=2 rows=%d", columns, rows, wantRows)
	}
}

func assertCompatibleStatus(t *testing.T, status MigrationStatus) {
	t.Helper()
	if !status.Compatible() || status.CurrentEpoch != ApplicationSchemaEpoch || len(status.Applied) != len(schemaMigrations) {
		t.Fatalf("migration status is not current and compatible: %+v", status)
	}
}

func assertSchemaStateError(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected a fail-closed schema state error")
	}
	if !errors.Is(err, ErrSchemaState) {
		t.Fatalf("error = %v, want ErrSchemaState", err)
	}
}

func containsProblem(problems []string, fragment string) bool {
	for _, problem := range problems {
		if strings.Contains(problem, fragment) {
			return true
		}
	}
	return false
}
