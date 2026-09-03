package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/go-ree/ares/internal/api"
	"github.com/go-ree/ares/internal/cli"
	"github.com/go-ree/ares/internal/config"
	"github.com/go-ree/ares/internal/db"
	"github.com/go-ree/ares/internal/integration"
	"github.com/go-ree/ares/internal/job"
	"github.com/go-ree/ares/internal/logger"
	"github.com/go-ree/ares/internal/webserver"
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

	err = integration.Initialize(config.SettingsEncryptionKey())
	if err != nil {
		os.Exit(5)
	}

	err = job.Init()
	if err != nil {
		os.Exit(5)
	}

	// Ignore errors; 出错自动os.Exit(5)
	webserver.Run(ctx, api.Router)
}
