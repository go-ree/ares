package main

import (
	"ares/internal/jenkins"
	"context"
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

	err = job.Init()
	if err != nil {
		os.Exit(5)
	}

	err = jenkins.Init()
	if err != nil {
		os.Exit(5)
	}

	// Ignore errors; 出错自动os.Exit(5)
	webserver.Run(ctx, api.Router)
}
