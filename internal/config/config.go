package config

import (
	"log/slog"
	"os"

	"gitlab.ttpai.work/sre/pipeline/ares/internal/cli"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Log struct {
		Level string
	}
	Web struct {
		Address string
	}
	DB struct {
		ConnStr string
	}
	Job map[string]struct {
		Cron string
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

	return nil
}
