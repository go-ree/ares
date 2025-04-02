package config

import (
	"ares/docs"
	"log/slog"
	"os"

	"ares/internal/cli"
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
		ConnStr string `yaml:"conn_str"`
	} `yaml:"db"`
	Job map[string]struct {
		Cron string `yaml:"cron"`
	} `yaml:"job"`
	Jenkins struct {
		Address  string `yaml:"address"`
		Token    string `yaml:"token"`
		UserName string `yaml:"username"`
		Password string `yaml:"password"`
	}
}

var Main = &Config{}

func Init() error {
	yamlData, err := os.ReadFile(cli.ConfigFilePath)
	if err != nil {
		slog.Error("read config file error", slog.Any("error", err))
		return err
	}

	err = yaml.Unmarshal(yamlData, Main)
	if err != nil {
		slog.Error("yaml unmarshal error", slog.Any("error", err))
		return err
	}
	slog.Info("load config successfully", slog.Any("config", Main))
	return nil
}

func InitSwagger() {
	docs.SwaggerInfo.Title = "Ares"
	docs.SwaggerInfo.Version = "v1.x"
	docs.SwaggerInfo.Description = "天天拍车发布引擎"
	docs.SwaggerInfo.Schemes = []string{"http", "https"}
	docs.SwaggerInfo.Host = ""
	docs.SwaggerInfo.BasePath = ""
	slog.Info("swagger config successfully")
}
