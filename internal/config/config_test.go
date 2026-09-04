package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestApplyEnvironmentOverrides(t *testing.T) {
	t.Setenv("ARES_WEB_ADDRESS", ":9090")
	t.Setenv("ARES_DB_CONN_STR", "demo:secret@tcp(mysql:3306)/ares")
	t.Setenv("ARES_DB_MIGRATION_CONN_STR", "migrator:secret@tcp(mysql:3306)/ares")
	t.Setenv("ARES_DB_MIGRATION_ADMIN_CONN_STR", "root:secret@tcp(mysql:3306)/ares")
	t.Setenv("ARES_DB_SCHEMA_MIGRATION_TIMEOUT", "4m")
	t.Setenv("ARES_DB_MIGRATION_LOCK_TIMEOUT", "45s")
	t.Setenv("ARES_DEMO_DATA_ENABLED", "true")
	t.Setenv("ARES_SETTINGS_ADMIN_TOKEN", "test-admin-token")
	t.Setenv("ARES_SETTINGS_ENCRYPTION_KEY", "test-encryption-key-with-32-characters")

	cfg := &Config{}
	if err := applyEnvironmentOverrides(cfg); err != nil {
		t.Fatalf("applyEnvironmentOverrides returned error: %v", err)
	}
	if cfg.Web.Address != ":9090" {
		t.Fatalf("unexpected web address: %q", cfg.Web.Address)
	}
	if cfg.DB.ConnStr != "demo:secret@tcp(mysql:3306)/ares" {
		t.Fatalf("unexpected database DSN: %q", cfg.DB.ConnStr)
	}
	if cfg.DB.MigrationConnStr != "migrator:secret@tcp(mysql:3306)/ares" {
		t.Fatalf("unexpected migration database DSN: %q", cfg.DB.MigrationConnStr)
	}
	if cfg.DB.MigrationAdminConnStr != "root:secret@tcp(mysql:3306)/ares" {
		t.Fatalf("unexpected migration admin DSN: %q", cfg.DB.MigrationAdminConnStr)
	}
	if cfg.DB.SchemaMigrationTimeout != "4m" {
		t.Fatalf("unexpected database schema migration timeout: %q", cfg.DB.SchemaMigrationTimeout)
	}
	if cfg.DB.MigrationLockTimeout != "45s" {
		t.Fatalf("unexpected database migration lock timeout: %q", cfg.DB.MigrationLockTimeout)
	}
	if !cfg.DemoData.Enabled {
		t.Fatal("demo data override was not applied")
	}
	if cfg.Settings.AdminToken != "test-admin-token" {
		t.Fatal("settings admin token override was not applied")
	}
	if cfg.Settings.EncryptionKey != "test-encryption-key-with-32-characters" {
		t.Fatal("settings encryption key override was not applied")
	}
}

func TestApplyEnvironmentOverridesRejectsInvalidBoolean(t *testing.T) {
	t.Setenv("ARES_DEMO_DATA_ENABLED", "sometimes")
	if err := applyEnvironmentOverrides(&Config{}); err == nil {
		t.Fatal("expected invalid boolean environment value to fail")
	}
}

func TestApplyEnvironmentOverridesRejectsInvalidMigrationTimeout(t *testing.T) {
	t.Setenv("ARES_DB_SCHEMA_MIGRATION_TIMEOUT", "never")
	if err := applyEnvironmentOverrides(&Config{}); err == nil {
		t.Fatal("expected invalid schema migration timeout to fail")
	}
}

func TestApplyEnvironmentOverridesRejectsInvalidMigrationLockTimeout(t *testing.T) {
	t.Setenv("ARES_DB_MIGRATION_LOCK_TIMEOUT", "immediately")
	if err := applyEnvironmentOverrides(&Config{}); err == nil {
		t.Fatal("expected invalid migration lock timeout to fail")
	}
}

func TestInitReplacesMainOnlyAfterSuccessfulValidation(t *testing.T) {
	original := Main
	t.Cleanup(func() { Main = original })

	previous := &Config{}
	previous.Web.Address = ":old"
	Main = previous

	configPath := writeConfig(t, `
db:
  conn_str: "runtime-from-file"
  migration_conn_str: "migration-from-file"
  schema_migration_timeout: "3m"
  migration_lock_timeout: "40s"
web:
  address: ":8080"
demo_data:
  enabled: false
`)
	t.Setenv("ARES_DB_CONN_STR", "runtime-from-env")
	t.Setenv("ARES_DB_MIGRATION_CONN_STR", "migration-from-env")
	t.Setenv("ARES_DB_SCHEMA_MIGRATION_TIMEOUT", "5m")
	t.Setenv("ARES_DB_MIGRATION_LOCK_TIMEOUT", "55s")
	t.Setenv("ARES_WEB_ADDRESS", ":9090")
	t.Setenv("ARES_DEMO_DATA_ENABLED", "true")

	if err := Init(configPath); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	if Main == previous {
		t.Fatal("Init() did not replace the previous configuration")
	}
	if Main.DB.ConnStr != "runtime-from-env" || Main.DB.MigrationConnStr != "migration-from-env" {
		t.Fatalf("Init() did not apply database environment overrides: %#v", Main.DB)
	}
	if Main.Web.Address != ":9090" || !Main.DemoData.Enabled {
		t.Fatalf("Init() did not apply environment overrides: %#v", Main)
	}
	if got := DBMigrationConnStr(); got != "migration-from-env" {
		t.Fatalf("DBMigrationConnStr() = %q", got)
	}
	if got := DBMigrationLockTimeout(); got != 55*time.Second {
		t.Fatalf("DBMigrationLockTimeout() = %v", got)
	}
}

func TestInitFailurePreservesMain(t *testing.T) {
	original := Main
	t.Cleanup(func() { Main = original })

	tests := []struct {
		name      string
		contents  string
		configure func(*testing.T)
	}{
		{name: "invalid yaml", contents: "db: ["},
		{name: "unknown top-level field", contents: "databse:\n  conn_str: runtime\n"},
		{name: "unknown nested field", contents: "db:\n  migration_admin_conn_string: admin\n"},
		{name: "multiple yaml documents", contents: "web:\n  address: ':8080'\n---\nweb:\n  address: ':9090'\n"},
		{
			name:     "invalid environment override",
			contents: "db:\n  migration_lock_timeout: 30s\n",
			configure: func(t *testing.T) {
				t.Setenv("ARES_DB_MIGRATION_LOCK_TIMEOUT", "invalid")
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			previous := &Config{}
			previous.Web.Address = ":still-active"
			Main = previous
			if test.configure != nil {
				test.configure(t)
			}

			if err := Init(writeConfig(t, test.contents)); err == nil {
				t.Fatal("Init() succeeded, want error")
			}
			if Main != previous || Main.Web.Address != ":still-active" {
				t.Fatalf("Init() changed Main after failure: %#v", Main)
			}
		})
	}
}

func TestInitReadFailurePreservesMain(t *testing.T) {
	original := Main
	t.Cleanup(func() { Main = original })

	previous := &Config{}
	previous.Web.Address = ":still-active"
	Main = previous

	if err := Init(filepath.Join(t.TempDir(), "missing.yaml")); err == nil {
		t.Fatal("Init() succeeded, want read error")
	}
	if Main != previous || Main.Web.Address != ":still-active" {
		t.Fatalf("Init() changed Main after read failure: %#v", Main)
	}
}

func TestMigrationAccessorsNormalizeAndDefault(t *testing.T) {
	original := Main
	t.Cleanup(func() { Main = original })

	Main = &Config{}
	Main.DB.MigrationConnStr = "  migrator:secret@tcp(mysql:3306)/ares  "
	Main.DB.MigrationAdminConnStr = "  root:secret@tcp(mysql:3306)/ares  "
	if got := DBMigrationConnStr(); got != "migrator:secret@tcp(mysql:3306)/ares" {
		t.Fatalf("DBMigrationConnStr() = %q", got)
	}
	if got := DBMigrationAdminConnStr(); got != "root:secret@tcp(mysql:3306)/ares" {
		t.Fatalf("DBMigrationAdminConnStr() = %q", got)
	}
	if got := DBMigrationLockTimeout(); got != 30*time.Second {
		t.Fatalf("DBMigrationLockTimeout() default = %v", got)
	}

	Main.DB.MigrationLockTimeout = "75s"
	if got := DBMigrationLockTimeout(); got != 75*time.Second {
		t.Fatalf("DBMigrationLockTimeout() = %v", got)
	}
}

func writeConfig(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}
