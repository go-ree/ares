package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestApplyEnvironmentOverrides(t *testing.T) {
	t.Setenv("ARES_WEB_ADDRESS", ":9090")
	t.Setenv("ARES_WEB_PUBLIC_URL", "http://localhost:9090/")
	t.Setenv("ARES_WEB_TRUSTED_PROXY_CIDRS", "127.0.0.1/32, 10.0.0.0/8")
	t.Setenv("ARES_WEB_READ_HEADER_TIMEOUT", "6s")
	t.Setenv("ARES_WEB_READ_TIMEOUT", "16s")
	t.Setenv("ARES_WEB_WRITE_TIMEOUT", "31s")
	t.Setenv("ARES_WEB_IDLE_TIMEOUT", "61s")
	t.Setenv("ARES_WEB_MAX_HEADER_BYTES", "32768")
	t.Setenv("ARES_WEB_MAX_JSON_BODY_BYTES", "2097152")
	t.Setenv("ARES_WEB_SSE_HEARTBEAT_INTERVAL", "10s")
	t.Setenv("ARES_WEB_SSE_REAUTH_INTERVAL", "20s")
	t.Setenv("ARES_WEB_SSE_WRITE_TIMEOUT", "7s")
	t.Setenv("ARES_WEB_SSE_IDLE_TIMEOUT", "90s")
	t.Setenv("ARES_WEB_SSE_MAX_DURATION", "12m")
	t.Setenv("ARES_DB_CONN_STR", "demo:secret@tcp(mysql:3306)/ares")
	t.Setenv("ARES_DB_MIGRATION_CONN_STR", "migrator:secret@tcp(mysql:3306)/ares")
	t.Setenv("ARES_DB_MIGRATION_ADMIN_CONN_STR", "root:secret@tcp(mysql:3306)/ares")
	t.Setenv("ARES_DB_SCHEMA_MIGRATION_TIMEOUT", "4m")
	t.Setenv("ARES_DB_MIGRATION_LOCK_TIMEOUT", "45s")
	t.Setenv("ARES_DEMO_DATA_ENABLED", "true")
	t.Setenv("ARES_SETTINGS_ADMIN_TOKEN", "test-admin-token")
	t.Setenv("ARES_SETTINGS_ENCRYPTION_KEY", "test-encryption-key-with-32-characters")
	t.Setenv("ARES_AUTH_ROOT_KEY", strings.Repeat("r", 32))
	t.Setenv("ARES_AUTH_OIDC_CLIENT_SECRET", "oidc-secret")
	t.Setenv("ARES_AUTH_OIDC_SCOPES", "openid, profile")
	t.Setenv("ARES_AUTH_OIDC_ALLOWED_SIGNING_ALGORITHMS", "RS256,ES256")
	t.Setenv("ARES_AUTH_SESSION_IDLE_TIMEOUT", "20m")
	t.Setenv("ARES_AUTH_SESSION_ABSOLUTE_TIMEOUT", "6h")
	t.Setenv("ARES_AUTH_SESSION_TOUCH_INTERVAL", "2m")
	t.Setenv("ARES_AUTH_BOOTSTRAP_TOKEN", strings.Repeat("b", 32))
	t.Setenv("ARES_AUTH_OIDC_HTTP_TIMEOUT", "8s")
	t.Setenv("ARES_AUTH_OIDC_MAX_CLOCK_SKEW", "45s")

	cfg := &Config{}
	if err := applyEnvironmentOverrides(cfg); err != nil {
		t.Fatalf("applyEnvironmentOverrides returned error: %v", err)
	}
	if cfg.Web.Address != ":9090" {
		t.Fatalf("unexpected web address: %q", cfg.Web.Address)
	}
	if cfg.Web.PublicURL != "http://localhost:9090" {
		t.Fatalf("unexpected public URL: %q", cfg.Web.PublicURL)
	}
	if got := cfg.Web.TrustedProxyCIDRs; len(got) != 2 || got[0] != "127.0.0.1/32" || got[1] != "10.0.0.0/8" {
		t.Fatalf("unexpected trusted proxy CIDRs: %#v", got)
	}
	if cfg.Web.ReadHeaderTimeout != "6s" || cfg.Web.ReadTimeout != "16s" ||
		cfg.Web.WriteTimeout != "31s" || cfg.Web.IdleTimeout != "61s" {
		t.Fatalf("unexpected web timeouts: %#v", cfg.Web)
	}
	if cfg.Web.MaxHeaderBytes != 32768 || cfg.Web.MaxJSONBodyBytes != 2097152 {
		t.Fatalf("unexpected web byte limits: %#v", cfg.Web)
	}
	if cfg.Web.SSE.HeartbeatInterval != "10s" || cfg.Web.SSE.ReauthInterval != "20s" ||
		cfg.Web.SSE.WriteTimeout != "7s" || cfg.Web.SSE.IdleTimeout != "90s" ||
		cfg.Web.SSE.MaxDuration != "12m" {
		t.Fatalf("unexpected SSE configuration: %#v", cfg.Web.SSE)
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
	if cfg.Auth.RootKey != strings.Repeat("r", 32) || cfg.Auth.OIDC.ClientSecret != "oidc-secret" {
		t.Fatal("authentication secret overrides were not applied")
	}
	if got := cfg.Auth.OIDC.Scopes; len(got) != 2 || got[0] != "openid" || got[1] != "profile" {
		t.Fatalf("unexpected OIDC scopes: %#v", got)
	}
	if got := cfg.Auth.OIDC.AllowedSigningAlgorithms; len(got) != 2 || got[1] != "ES256" {
		t.Fatalf("unexpected OIDC signing algorithms: %#v", got)
	}
	if cfg.Auth.OIDC.HTTPTimeout != "8s" || cfg.Auth.OIDC.MaxClockSkew != "45s" {
		t.Fatalf("unexpected OIDC safety timeouts: %#v", cfg.Auth.OIDC)
	}
}

func TestSecretFilesAreResolvedOnceAndTrimOneLineEnding(t *testing.T) {
	original := Main
	t.Cleanup(func() { Main = original })

	secretDirectory := t.TempDir()
	writeSecret := func(name, value string) string {
		t.Helper()
		path := filepath.Join(secretDirectory, name)
		if err := os.WriteFile(path, []byte(value+"\r\n"), 0o600); err != nil {
			t.Fatalf("write secret %s: %v", name, err)
		}
		return path
	}
	rootKey := strings.Repeat("r", 32)
	bootstrapToken := strings.Repeat("b", 32)
	configPath := writeConfig(t, fmt.Sprintf(`
web:
  address: ":8080"
  public_url: "http://localhost:8080"
settings:
  admin_token_file: %q
  encryption_key_file: %q
auth:
  root_key_file: %q
  local_login:
    enabled: true
  bootstrap:
    enabled: true
    token_file: %q
`, writeSecret("admin", "admin-from-file"), writeSecret("encryption", strings.Repeat("e", 32)),
		writeSecret("root", rootKey), writeSecret("bootstrap", bootstrapToken)))

	if err := Init(configPath); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	if got := SettingsAdminToken(); got != "admin-from-file" {
		t.Fatalf("SettingsAdminToken() = %q", got)
	}
	if got := SettingsEncryptionKey(); got != strings.Repeat("e", 32) {
		t.Fatalf("SettingsEncryptionKey() length = %d", len(got))
	}
	if got := AuthRootKey(); got != rootKey {
		t.Fatalf("AuthRootKey() length = %d", len(got))
	}
	if got := BootstrapToken(); got != bootstrapToken {
		t.Fatalf("BootstrapToken() length = %d", len(got))
	}

	// Accessors use the value loaded during Init rather than silently accepting
	// a later replacement of the mounted secret file.
	if err := os.WriteFile(Main.Auth.RootKeyFile, []byte("replacement"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := AuthRootKey(); got != rootKey {
		t.Fatal("AuthRootKey() re-read a changed secret file")
	}
}

func TestSecretEnvironmentValueAndFileAreMutuallyExclusive(t *testing.T) {
	direct := "do-not-reflect-direct-secret"
	file := filepath.Join(t.TempDir(), "root-key")
	if err := os.WriteFile(file, []byte(strings.Repeat("f", 32)), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ARES_AUTH_ROOT_KEY", direct)
	t.Setenv("ARES_AUTH_ROOT_KEY_FILE", file)
	err := applyEnvironmentOverrides(&Config{})
	if err == nil {
		t.Fatal("direct and _FILE secret sources were accepted together")
	}
	if strings.Contains(err.Error(), direct) {
		t.Fatalf("configuration error reflected a secret: %v", err)
	}
}

func TestAuthenticationConfigurationFailsClosed(t *testing.T) {
	validRootKey := strings.Repeat("r", 32)
	tests := []struct {
		name      string
		configure func(*Config)
	}{
		{
			name: "missing public URL",
			configure: func(cfg *Config) {
				cfg.Auth.LocalLogin.Enabled = true
				cfg.Auth.RootKey = validRootKey
			},
		},
		{
			name: "non-loopback HTTP public URL",
			configure: func(cfg *Config) {
				cfg.Web.PublicURL = "http://ares.example.com"
			},
		},
		{
			name: "missing root key",
			configure: func(cfg *Config) {
				cfg.Web.PublicURL = "https://ares.example.com"
				cfg.Auth.LocalLogin.Enabled = true
			},
		},
		{
			name: "short bootstrap token",
			configure: func(cfg *Config) {
				cfg.Web.PublicURL = "https://ares.example.com"
				cfg.Auth.RootKey = validRootKey
				cfg.Auth.Bootstrap.Enabled = true
				cfg.Auth.Bootstrap.Token = "short"
			},
		},
		{
			name: "symmetric OIDC signing algorithm",
			configure: func(cfg *Config) {
				cfg.Web.PublicURL = "https://ares.example.com"
				cfg.Auth.RootKey = validRootKey
				cfg.Auth.OIDC.Enabled = true
				cfg.Auth.OIDC.IssuerURL = "https://idp.example.com"
				cfg.Auth.OIDC.ClientID = "ares"
				cfg.Auth.OIDC.ClientSecret = "secret"
				cfg.Auth.OIDC.Scopes = []string{"openid"}
				cfg.Auth.OIDC.AllowedSigningAlgorithms = []string{"HS256"}
			},
		},
		{
			name: "OIDC flow lifetime longer than service bound",
			configure: func(cfg *Config) {
				cfg.Web.PublicURL = "https://ares.example.com"
				cfg.Auth.RootKey = validRootKey
				cfg.Auth.OIDC.Enabled = true
				cfg.Auth.OIDC.IssuerURL = "https://idp.example.com"
				cfg.Auth.OIDC.ClientID = "ares"
				cfg.Auth.OIDC.ClientSecret = "secret"
				cfg.Auth.OIDC.FlowTTL = "31m"
			},
		},
		{
			name: "invalid trusted proxy",
			configure: func(cfg *Config) {
				cfg.Web.TrustedProxyCIDRs = []string{"all-proxies"}
			},
		},
		{
			name: "SSE idle shorter than upstream heartbeat",
			configure: func(cfg *Config) {
				cfg.Web.SSE.HeartbeatInterval = "1s"
				cfg.Web.SSE.IdleTimeout = "10s"
			},
		},
		{
			name: "SSE reauthentication exceeds revocation bound",
			configure: func(cfg *Config) {
				cfg.Web.SSE.ReauthInterval = "61s"
			},
		},
		{
			name: "SSE reauthentication exceeds session idle timeout",
			configure: func(cfg *Config) {
				cfg.Web.SSE.ReauthInterval = "30s"
				cfg.Auth.Session.IdleTimeout = "20s"
				cfg.Auth.Session.TouchInterval = "5s"
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := &Config{}
			test.configure(cfg)
			if err := normalizeAndValidateSecurityConfig(cfg); err == nil {
				t.Fatal("configuration was accepted")
			}
		})
	}
}

func TestSecurityDefaultsAndAccessors(t *testing.T) {
	original := Main
	t.Cleanup(func() { Main = original })
	Main = &Config{}

	if got := WebReadHeaderTimeout(); got != 5*time.Second {
		t.Fatalf("WebReadHeaderTimeout() = %v", got)
	}
	if got := WebReadTimeout(); got != 15*time.Second {
		t.Fatalf("WebReadTimeout() = %v", got)
	}
	if got := WebWriteTimeout(); got != 30*time.Second {
		t.Fatalf("WebWriteTimeout() = %v", got)
	}
	if got := WebIdleTimeout(); got != time.Minute {
		t.Fatalf("WebIdleTimeout() = %v", got)
	}
	if got := WebMaxHeaderBytes(); got != 64*1024 {
		t.Fatalf("WebMaxHeaderBytes() = %d", got)
	}
	if got := SSEHeartbeatInterval(); got != 15*time.Second {
		t.Fatalf("SSEHeartbeatInterval() = %v", got)
	}
	if got := SSEMaxDuration(); got != 15*time.Minute {
		t.Fatalf("SSEMaxDuration() = %v", got)
	}
	if got := OIDCHTTPTimeout(); got != 10*time.Second {
		t.Fatalf("OIDCHTTPTimeout() = %v", got)
	}
}

func TestPublicURLIsCanonicalizedForOriginChecksAndRedirects(t *testing.T) {
	original := Main
	t.Cleanup(func() { Main = original })
	Main = &Config{}
	Main.Web.PublicURL = "HTTPS://ARES.EXAMPLE.COM:443/"
	if err := normalizeAndValidateSecurityConfig(Main); err != nil {
		t.Fatal(err)
	}
	if got := WebPublicURL(); got != "https://ares.example.com" {
		t.Fatalf("WebPublicURL() = %q", got)
	}
	if !AuthCookieSecure() {
		t.Fatal("HTTPS public URL did not enable secure cookies")
	}
	if got := OIDCRedirectURL(); got != "https://ares.example.com/api/v1/auth/oidc/callback" {
		t.Fatalf("OIDCRedirectURL() = %q", got)
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
