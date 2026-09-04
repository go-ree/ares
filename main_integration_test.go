package main

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
)

const cliMySQLIntegrationDSNEnv = "ARES_TEST_MYSQL_DSN"

func TestMigrationCLIExitCodesAndSafeOutput(t *testing.T) {
	adminDSN := strings.TrimSpace(os.Getenv(cliMySQLIntegrationDSNEnv))
	if adminDSN == "" {
		t.Skipf("set %s to a MySQL 8.4 administrative DSN to run CLI integration tests", cliMySQLIntegrationDSNEnv)
	}
	parsed, err := mysql.ParseDSN(adminDSN)
	if err != nil {
		t.Fatal(err)
	}
	parsed.ParseTime = true
	admin, err := sql.Open("mysql", parsed.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := admin.PingContext(ctx); err != nil {
		t.Fatal(err)
	}

	databaseName := fmt.Sprintf("ares_w04_cli_%d", time.Now().UnixNano())
	if _, err := admin.ExecContext(ctx, "CREATE DATABASE `"+databaseName+"` CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		_, _ = admin.ExecContext(cleanupCtx, "DROP DATABASE IF EXISTS `"+databaseName+"`")
	})
	target := parsed.Clone()
	target.DBName = databaseName
	target.ParseTime = false // The application must normalize this internally.
	targetDSN := target.FormatDSN()
	t.Setenv("ARES_DB_CONN_STR", targetDSN)
	t.Setenv("ARES_DB_MIGRATION_CONN_STR", targetDSN)
	t.Setenv("ARES_DB_MIGRATION_ADMIN_CONN_STR", "")
	t.Setenv("ARES_DEMO_DATA_ENABLED", "false")

	configPath := "config/default.yaml"
	run := func(args ...string) (int, string, string) {
		t.Helper()
		var stdout, stderr bytes.Buffer
		code := realMain(append(args, "--config", configPath), &stdout, &stderr)
		return code, stdout.String(), stderr.String()
	}

	code, stdout, stderr := run("migrate", "status")
	if code != exitSchemaState || stderr != "" || !strings.Contains(stdout, "数据库尚未初始化") {
		t.Fatalf("empty status = code:%d stdout:%q stderr:%q", code, stdout, stderr)
	}
	if got := strings.Count(stdout, "checksum="); got != 4 {
		t.Fatalf("empty status listed %d pending checksums, want 4: %s", got, stdout)
	}

	code, stdout, stderr = run("migrate", "up")
	if code != exitSuccess || stderr != "" || !strings.Contains(stdout, "状态=兼容") {
		t.Fatalf("migrate up = code:%d stdout:%q stderr:%q", code, stdout, stderr)
	}
	code, stdout, stderr = run("migrate", "status")
	if code != exitSuccess || stderr != "" || !strings.Contains(stdout, "状态=兼容") {
		t.Fatalf("compatible status = code:%d stdout:%q stderr:%q", code, stdout, stderr)
	}

	target.ParseTime = true
	database, err := sql.Open("mysql", target.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.ExecContext(ctx, `ALTER TABLE apps MODIFY description_cn
		VARCHAR(255) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci NULL
		DEFAULT 'password=CLISecret'`); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr = run("migrate", "status")
	if code != exitSchemaState || stderr != "" || strings.Contains(stdout, "CLISecret") ||
		!strings.Contains(stdout, "password=<redacted>") {
		t.Fatalf("drift status = code:%d stdout:%q stderr:%q", code, stdout, stderr)
	}

	code, _, _ = run("migrate", "unknown")
	if code != exitUsage {
		t.Fatalf("invalid command exit code = %d, want %d", code, exitUsage)
	}

	t.Setenv("ARES_DB_CONN_STR", "user:password@tcp(127.0.0.1:1)/ares?timeout=100ms")
	code, stdout, stderr = run("migrate", "status")
	if code != exitOperational || stdout != "" || stderr == "" || strings.Contains(stderr, "password") {
		t.Fatalf("connection failure = code:%d stdout:%q stderr:%q", code, stdout, stderr)
	}
}

func TestServeRejectsEmptySchemaBeforeStartingRuntime(t *testing.T) {
	adminDSN := strings.TrimSpace(os.Getenv(cliMySQLIntegrationDSNEnv))
	if adminDSN == "" {
		t.Skipf("set %s to a MySQL 8.4 administrative DSN to run CLI integration tests", cliMySQLIntegrationDSNEnv)
	}
	parsed, err := mysql.ParseDSN(adminDSN)
	if err != nil {
		t.Fatal(err)
	}
	parsed.ParseTime = true
	admin, err := sql.Open("mysql", parsed.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	databaseName := fmt.Sprintf("ares_w04_serve_%d", time.Now().UnixNano())
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := admin.ExecContext(ctx, "CREATE DATABASE `"+databaseName+"` CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		_, _ = admin.ExecContext(cleanupCtx, "DROP DATABASE IF EXISTS `"+databaseName+"`")
	})
	target := parsed.Clone()
	target.DBName = databaseName
	target.ParseTime = false
	t.Setenv("ARES_DB_CONN_STR", target.FormatDSN())
	t.Setenv("ARES_DEMO_DATA_ENABLED", "false")

	var stdout, stderr bytes.Buffer
	code := realMain([]string{"serve", "--config", "config/default.yaml"}, &stdout, &stderr)
	if code != exitSchemaState || stdout.Len() != 0 || !strings.Contains(stderr.String(), "状态=不兼容") {
		t.Fatalf("serve on empty schema = code:%d stdout:%q stderr:%q", code, stdout.String(), stderr.String())
	}
	var tableCount int
	if err := admin.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.TABLES
		WHERE TABLE_SCHEMA = ?`, databaseName).Scan(&tableCount); err != nil {
		t.Fatal(err)
	}
	if tableCount != 0 {
		t.Fatalf("serve created %d tables before rejecting an empty schema", tableCount)
	}
}
