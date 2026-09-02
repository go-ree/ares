package config

import "testing"

func TestApplyEnvironmentOverrides(t *testing.T) {
	t.Setenv("ARES_WEB_ADDRESS", ":9090")
	t.Setenv("ARES_DB_CONN_STR", "demo:secret@tcp(mysql:3306)/ares")
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
