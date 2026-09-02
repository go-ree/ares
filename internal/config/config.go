package config

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"ares/internal/cli"
	"ares/internal/swagger"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Log struct {
		Level          string `yaml:"level"`
		AccessLogfile  string `yaml:"accessLogfile"`
		RuntimeLogfile string `yaml:"runtimeLogfile"`
	} `yaml:"log"`
	Web struct {
		Address string `yaml:"address"`
	} `yaml:"web"`
	DB struct {
		ConnStr                string `yaml:"conn_str"`
		SchemaMigrationTimeout string `yaml:"schema_migration_timeout"`
	} `yaml:"db"`
	Job map[string]struct {
		Cron string `yaml:"cron"`
	} `yaml:"job"`
	DemoData struct {
		Enabled bool `yaml:"enabled"`
	} `yaml:"demo_data"`
	Settings struct {
		AdminToken    string `yaml:"admin_token"`
		EncryptionKey string `yaml:"encryption_key"`
	} `yaml:"settings"`
}

var Main = &Config{}

func Init() error {
	configPath := cli.ConfigFilePath
	slog.Info("config load start", "path", configPath)
	yamlData, err := os.ReadFile(configPath)
	if err != nil {
		slog.Error("read config file error", "path", configPath, slog.Any("error", err))
		return err
	}

	err = yaml.Unmarshal(yamlData, Main)
	if err != nil {
		slog.Error("yaml unmarshal error", "path", configPath, slog.Any("error", err))
		return err
	}
	if err := applyEnvironmentOverrides(Main); err != nil {
		slog.Error("apply environment overrides error", slog.Any("error", err))
		return err
	}

	// 注意：不要把完整配置打印到日志（包含 token/密码等敏感信息）
	slog.Info("load config successfully",
		"path", configPath,
		"web.address", Main.Web.Address,
	)
	return nil
}

func applyEnvironmentOverrides(cfg *Config) error {
	if cfg == nil {
		return fmt.Errorf("config is nil")
	}

	overrideString("ARES_WEB_ADDRESS", &cfg.Web.Address)
	overrideString("ARES_DB_CONN_STR", &cfg.DB.ConnStr)
	overrideString("ARES_DB_SCHEMA_MIGRATION_TIMEOUT", &cfg.DB.SchemaMigrationTimeout)
	overrideString("ARES_LOG_LEVEL", &cfg.Log.Level)
	overrideString("ARES_LOG_ACCESS_FILE", &cfg.Log.AccessLogfile)
	overrideString("ARES_LOG_RUNTIME_FILE", &cfg.Log.RuntimeLogfile)
	overrideString("ARES_SETTINGS_ADMIN_TOKEN", &cfg.Settings.AdminToken)
	overrideString("ARES_SETTINGS_ENCRYPTION_KEY", &cfg.Settings.EncryptionKey)
	if err := overrideOptionalBool("ARES_DEMO_DATA_ENABLED", &cfg.DemoData.Enabled); err != nil {
		return err
	}
	if _, err := parsePositiveDuration("db.schema_migration_timeout", cfg.DB.SchemaMigrationTimeout, 2*time.Minute); err != nil {
		return err
	}
	return nil
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

func SettingsAdminToken() string {
	return strings.TrimSpace(Main.Settings.AdminToken)
}

func SettingsEncryptionKey() string {
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
