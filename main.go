package main

import (
	"ares/internal/jenkins"
	"ares/internal/k8s"
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"ares/internal/api"
	"ares/internal/cli"
	"ares/internal/config"
	"ares/internal/db"
	"ares/internal/job"
	"ares/internal/logger"
	"ares/internal/webserver"
)

func main() {

	ctx, _ := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)

	err := logger.Init()
	if err != nil {
		os.Exit(5)
	}

	cli.Init()

	err = config.Init()
	if err != nil {
		os.Exit(5)
	}
	config.InitSwagger()

	err = logger.Init2(config.Main.Log.Level)
	if err != nil {
		os.Exit(5)
	}

	err = db.Init()
	if err != nil {
		os.Exit(5)
	}

	if config.JenkinsEnabled() {
		err = jenkins.Init()
		if err != nil {
			os.Exit(5)
		}

		err = job.Init()
		if err != nil {
			os.Exit(5)
		}
	} else {
		slog.Warn("jenkins integration disabled; publishing actions are unavailable")
	}

	if config.K8sEnabled() {
		// 初始化K8s客户端管理器
		err = k8s.Init()
		if err != nil {
			os.Exit(5)
		}
	} else {
		slog.Warn("kubernetes integration disabled; cluster queries are unavailable")
	}

	// Ignore errors; 出错自动os.Exit(5)
	webserver.Run(ctx, api.Router)
}
