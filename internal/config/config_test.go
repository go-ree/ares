package config

import "testing"

func TestOptionalIntegrationsDefaultToEnabled(t *testing.T) {
	original := Main
	t.Cleanup(func() { Main = original })

	Main = &Config{}
	if !JenkinsEnabled() {
		t.Fatal("jenkins should remain enabled when the setting is omitted")
	}
	if !K8sEnabled() {
		t.Fatal("kubernetes should remain enabled when the setting is omitted")
	}

	disabled := false
	Main.Jenkins.Enabled = &disabled
	Main.K8s.Enabled = &disabled
	if JenkinsEnabled() || K8sEnabled() {
		t.Fatal("explicit false should disable both integrations")
	}
}

func TestApplyEnvironmentOverrides(t *testing.T) {
	t.Setenv("ARES_WEB_ADDRESS", ":9090")
	t.Setenv("ARES_DB_CONN_STR", "demo:secret@tcp(mysql:3306)/ares")
	t.Setenv("ARES_JENKINS_ENABLED", "false")
	t.Setenv("ARES_K8S_ENABLED", "0")
	t.Setenv("ARES_DEMO_DATA_ENABLED", "true")
	t.Setenv("ARES_JENKINS_TIMEOUT_SECONDS", "9")
	t.Setenv("ARES_K8S_TIMEOUT_SECONDS", "11")

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
	if cfg.Jenkins.Enabled == nil || *cfg.Jenkins.Enabled {
		t.Fatal("jenkins override was not applied")
	}
	if cfg.K8s.Enabled == nil || *cfg.K8s.Enabled {
		t.Fatal("kubernetes override was not applied")
	}
	if !cfg.DemoData.Enabled {
		t.Fatal("demo data override was not applied")
	}
	if cfg.Jenkins.TimeoutSeconds != 9 || cfg.K8s.TimeoutSeconds != 11 {
		t.Fatalf("unexpected integration timeouts: jenkins=%d k8s=%d", cfg.Jenkins.TimeoutSeconds, cfg.K8s.TimeoutSeconds)
	}
}

func TestIntegrationTimeoutDefaults(t *testing.T) {
	if got := integrationTimeout(0).Seconds(); got != 15 {
		t.Fatalf("unexpected default timeout: %.0f", got)
	}
	if got := integrationTimeout(7).Seconds(); got != 7 {
		t.Fatalf("unexpected configured timeout: %.0f", got)
	}
}

func TestApplyEnvironmentOverridesRejectsInvalidBoolean(t *testing.T) {
	t.Setenv("ARES_JENKINS_ENABLED", "sometimes")
	if err := applyEnvironmentOverrides(&Config{}); err == nil {
		t.Fatal("expected invalid boolean environment value to fail")
	}
}
