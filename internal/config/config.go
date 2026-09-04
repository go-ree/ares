package config

import (
	"bytes"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/go-ree/ares/internal/swagger"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Log struct {
		Level          string `yaml:"level"`
		AccessLogfile  string `yaml:"accessLogfile"`
		RuntimeLogfile string `yaml:"runtimeLogfile"`
	} `yaml:"log"`
	Web WebConfig `yaml:"web"`
	DB  struct {
		ConnStr                string `yaml:"conn_str"`
		MigrationConnStr       string `yaml:"migration_conn_str"`
		MigrationAdminConnStr  string `yaml:"migration_admin_conn_str"`
		SchemaMigrationTimeout string `yaml:"schema_migration_timeout"`
		MigrationLockTimeout   string `yaml:"migration_lock_timeout"`
	} `yaml:"db"`
	Job map[string]struct {
		Cron string `yaml:"cron"`
	} `yaml:"job"`
	DemoData struct {
		Enabled bool `yaml:"enabled"`
	} `yaml:"demo_data"`
	Settings struct {
		AdminToken        string `yaml:"admin_token"`
		AdminTokenFile    string `yaml:"admin_token_file"`
		EncryptionKey     string `yaml:"encryption_key"`
		EncryptionKeyFile string `yaml:"encryption_key_file"`
	} `yaml:"settings"`
	Auth AuthConfig `yaml:"auth"`

	resolvedSecrets resolvedSecrets
}

var Main = &Config{}

func Init(configPath string) error {
	slog.Info("config load start", "path", configPath)
	yamlData, err := os.ReadFile(configPath)
	if err != nil {
		slog.Error("read config file error", "path", configPath, slog.Any("error", err))
		return err
	}

	loaded := &Config{}
	decoder := yaml.NewDecoder(bytes.NewReader(yamlData))
	decoder.KnownFields(true)
	if err := decoder.Decode(loaded); err != nil {
		slog.Error("yaml decode error", "path", configPath, slog.Any("error", err))
		return err
	}
	var extraDocument any
	if err := decoder.Decode(&extraDocument); err != io.EOF {
		if err == nil {
			err = fmt.Errorf("configuration must contain exactly one YAML document")
		}
		slog.Error("yaml document validation error", "path", configPath, slog.Any("error", err))
		return err
	}
	if err := applyEnvironmentOverrides(loaded); err != nil {
		slog.Error("apply environment overrides error", slog.Any("error", err))
		return err
	}

	// Only replace the active configuration after the complete candidate has
	// been parsed and validated. This prevents callers from observing a partial
	// configuration after a failed reload.
	Main = loaded

	// 注意：不要把完整配置打印到日志（包含 token/密码等敏感信息）
	slog.Info("load config successfully",
		"path", configPath,
		"web.address", loaded.Web.Address,
	)
	return nil
}

func applyEnvironmentOverrides(cfg *Config) error {
	if cfg == nil {
		return fmt.Errorf("config is nil")
	}

	overrideString("ARES_WEB_ADDRESS", &cfg.Web.Address)
	overrideString("ARES_WEB_PUBLIC_URL", &cfg.Web.PublicURL)
	if value, ok := os.LookupEnv("ARES_WEB_TRUSTED_PROXY_CIDRS"); ok {
		cfg.Web.TrustedProxyCIDRs = splitCommaSeparated(value)
	}
	overrideString("ARES_WEB_READ_HEADER_TIMEOUT", &cfg.Web.ReadHeaderTimeout)
	overrideString("ARES_WEB_READ_TIMEOUT", &cfg.Web.ReadTimeout)
	overrideString("ARES_WEB_WRITE_TIMEOUT", &cfg.Web.WriteTimeout)
	overrideString("ARES_WEB_IDLE_TIMEOUT", &cfg.Web.IdleTimeout)
	if err := overrideOptionalInt("ARES_WEB_MAX_HEADER_BYTES", &cfg.Web.MaxHeaderBytes); err != nil {
		return err
	}
	if err := overrideOptionalInt64("ARES_WEB_MAX_JSON_BODY_BYTES", &cfg.Web.MaxJSONBodyBytes); err != nil {
		return err
	}
	overrideString("ARES_WEB_SSE_HEARTBEAT_INTERVAL", &cfg.Web.SSE.HeartbeatInterval)
	overrideString("ARES_WEB_SSE_REAUTH_INTERVAL", &cfg.Web.SSE.ReauthInterval)
	overrideString("ARES_WEB_SSE_WRITE_TIMEOUT", &cfg.Web.SSE.WriteTimeout)
	overrideString("ARES_WEB_SSE_IDLE_TIMEOUT", &cfg.Web.SSE.IdleTimeout)
	overrideString("ARES_WEB_SSE_MAX_DURATION", &cfg.Web.SSE.MaxDuration)
	overrideString("ARES_DB_CONN_STR", &cfg.DB.ConnStr)
	overrideString("ARES_DB_MIGRATION_CONN_STR", &cfg.DB.MigrationConnStr)
	overrideString("ARES_DB_MIGRATION_ADMIN_CONN_STR", &cfg.DB.MigrationAdminConnStr)
	overrideString("ARES_DB_SCHEMA_MIGRATION_TIMEOUT", &cfg.DB.SchemaMigrationTimeout)
	overrideString("ARES_DB_MIGRATION_LOCK_TIMEOUT", &cfg.DB.MigrationLockTimeout)
	overrideString("ARES_LOG_LEVEL", &cfg.Log.Level)
	overrideString("ARES_LOG_ACCESS_FILE", &cfg.Log.AccessLogfile)
	overrideString("ARES_LOG_RUNTIME_FILE", &cfg.Log.RuntimeLogfile)
	if err := overrideSecretSource(
		"ARES_SETTINGS_ADMIN_TOKEN", "ARES_SETTINGS_ADMIN_TOKEN_FILE",
		&cfg.Settings.AdminToken, &cfg.Settings.AdminTokenFile,
	); err != nil {
		return err
	}
	if err := overrideSecretSource(
		"ARES_SETTINGS_ENCRYPTION_KEY", "ARES_SETTINGS_ENCRYPTION_KEY_FILE",
		&cfg.Settings.EncryptionKey, &cfg.Settings.EncryptionKeyFile,
	); err != nil {
		return err
	}
	if err := overrideSecretSource(
		"ARES_AUTH_ROOT_KEY", "ARES_AUTH_ROOT_KEY_FILE",
		&cfg.Auth.RootKey, &cfg.Auth.RootKeyFile,
	); err != nil {
		return err
	}
	if err := overrideOptionalBool("ARES_AUTH_OIDC_ENABLED", &cfg.Auth.OIDC.Enabled); err != nil {
		return err
	}
	overrideString("ARES_AUTH_OIDC_ISSUER_URL", &cfg.Auth.OIDC.IssuerURL)
	overrideString("ARES_AUTH_OIDC_CLIENT_ID", &cfg.Auth.OIDC.ClientID)
	if err := overrideSecretSource(
		"ARES_AUTH_OIDC_CLIENT_SECRET", "ARES_AUTH_OIDC_CLIENT_SECRET_FILE",
		&cfg.Auth.OIDC.ClientSecret, &cfg.Auth.OIDC.ClientSecretFile,
	); err != nil {
		return err
	}
	if value, ok := os.LookupEnv("ARES_AUTH_OIDC_SCOPES"); ok {
		cfg.Auth.OIDC.Scopes = splitCommaSeparated(value)
	}
	if value, ok := os.LookupEnv("ARES_AUTH_OIDC_ALLOWED_SIGNING_ALGORITHMS"); ok {
		cfg.Auth.OIDC.AllowedSigningAlgorithms = splitCommaSeparated(value)
	}
	if err := overrideOptionalBool("ARES_AUTH_OIDC_REQUIRE_VERIFIED_EMAIL", &cfg.Auth.OIDC.RequireVerifiedEmail); err != nil {
		return err
	}
	if err := overrideOptionalBool("ARES_AUTH_OIDC_AUTO_PROVISION", &cfg.Auth.OIDC.AutoProvision); err != nil {
		return err
	}
	overrideString("ARES_AUTH_OIDC_FLOW_TTL", &cfg.Auth.OIDC.FlowTTL)
	overrideString("ARES_AUTH_OIDC_HTTP_TIMEOUT", &cfg.Auth.OIDC.HTTPTimeout)
	overrideString("ARES_AUTH_OIDC_MAX_CLOCK_SKEW", &cfg.Auth.OIDC.MaxClockSkew)
	overrideString("ARES_AUTH_SESSION_IDLE_TIMEOUT", &cfg.Auth.Session.IdleTimeout)
	overrideString("ARES_AUTH_SESSION_ABSOLUTE_TIMEOUT", &cfg.Auth.Session.AbsoluteTimeout)
	overrideString("ARES_AUTH_SESSION_TOUCH_INTERVAL", &cfg.Auth.Session.TouchInterval)
	if err := overrideOptionalBool("ARES_AUTH_LOCAL_LOGIN_ENABLED", &cfg.Auth.LocalLogin.Enabled); err != nil {
		return err
	}
	if err := overrideOptionalBool("ARES_AUTH_BOOTSTRAP_ENABLED", &cfg.Auth.Bootstrap.Enabled); err != nil {
		return err
	}
	if err := overrideSecretSource(
		"ARES_AUTH_BOOTSTRAP_TOKEN", "ARES_AUTH_BOOTSTRAP_TOKEN_FILE",
		&cfg.Auth.Bootstrap.Token, &cfg.Auth.Bootstrap.TokenFile,
	); err != nil {
		return err
	}
	if err := overrideOptionalBool("ARES_AUTH_LEGACY_ADMIN_TOKEN_ENABLED", &cfg.Auth.LegacyAdminToken.Enabled); err != nil {
		return err
	}
	if err := overrideSecretSource(
		"ARES_AUTH_LEGACY_ADMIN_TOKEN", "ARES_AUTH_LEGACY_ADMIN_TOKEN_FILE",
		&cfg.Auth.LegacyAdminToken.Token, &cfg.Auth.LegacyAdminToken.TokenFile,
	); err != nil {
		return err
	}
	overrideString("ARES_AUTH_LEGACY_ADMIN_TOKEN_SUNSET_AT", &cfg.Auth.LegacyAdminToken.SunsetAt)
	if err := overrideOptionalBool("ARES_DEMO_DATA_ENABLED", &cfg.DemoData.Enabled); err != nil {
		return err
	}
	if _, err := parsePositiveDuration("db.schema_migration_timeout", cfg.DB.SchemaMigrationTimeout, 2*time.Minute); err != nil {
		return err
	}
	if _, err := parsePositiveDuration("db.migration_lock_timeout", cfg.DB.MigrationLockTimeout, 30*time.Second); err != nil {
		return err
	}
	return normalizeAndValidateSecurityConfig(cfg)
}

func overrideString(name string, target *string) {
	if value, ok := os.LookupEnv(name); ok {
		*target = value
	}
}

func overrideOptionalBool(name string, target *bool) error {
	value, ok := os.LookupEnv(name)
	if !ok {
		return nil
	}
	parsed, err := strconv.ParseBool(strings.TrimSpace(value))
	if err != nil {
		return fmt.Errorf("invalid boolean value for %s: %w", name, err)
	}
	*target = parsed
	return nil
}

func overrideOptionalInt(name string, target *int) error {
	value, ok := os.LookupEnv(name)
	if !ok {
		return nil
	}
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return fmt.Errorf("invalid integer value for %s: %w", name, err)
	}
	*target = parsed
	return nil
}

func overrideOptionalInt64(name string, target *int64) error {
	value, ok := os.LookupEnv(name)
	if !ok {
		return nil
	}
	parsed, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil {
		return fmt.Errorf("invalid integer value for %s: %w", name, err)
	}
	*target = parsed
	return nil
}

func parsePositiveDuration(name, value string, fallback time.Duration) (time.Duration, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("invalid positive duration for %s: %q", name, value)
	}
	return parsed, nil
}

func DBSchemaMigrationTimeout() time.Duration {
	timeout, err := parsePositiveDuration("db.schema_migration_timeout", Main.DB.SchemaMigrationTimeout, 2*time.Minute)
	if err != nil {
		return 2 * time.Minute
	}
	return timeout
}

func DBMigrationConnStr() string {
	return strings.TrimSpace(Main.DB.MigrationConnStr)
}

func DBMigrationAdminConnStr() string {
	return strings.TrimSpace(Main.DB.MigrationAdminConnStr)
}

func DBMigrationLockTimeout() time.Duration {
	timeout, err := parsePositiveDuration("db.migration_lock_timeout", Main.DB.MigrationLockTimeout, 30*time.Second)
	if err != nil {
		return 30 * time.Second
	}
	return timeout
}

func SettingsAdminToken() string {
	if Main == nil {
		return ""
	}
	if Main.resolvedSecrets.settingsAdminToken != "" {
		return strings.TrimSpace(Main.resolvedSecrets.settingsAdminToken)
	}
	return strings.TrimSpace(Main.Settings.AdminToken)
}

func SettingsEncryptionKey() string {
	if Main == nil {
		return ""
	}
	if Main.resolvedSecrets.settingsEncryptionKey != "" {
		return strings.TrimSpace(Main.resolvedSecrets.settingsEncryptionKey)
	}
	return strings.TrimSpace(Main.Settings.EncryptionKey)
}

func InitSwagger() {
	swagger.SwaggerInfo.Title = "Ares"
	swagger.SwaggerInfo.Version = "v1.x"
	swagger.SwaggerInfo.Description = "天天拍车发布引擎"
	swagger.SwaggerInfo.Schemes = []string{"http", "https"}
	swagger.SwaggerInfo.Host = ""
	swagger.SwaggerInfo.BasePath = ""
	slog.Info("swagger config successfully")
}
