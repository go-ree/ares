package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"gitlab.ttpai.work/sre/pipeline/ares/internal/api"
	"gitlab.ttpai.work/sre/pipeline/ares/internal/cli"
	"gitlab.ttpai.work/sre/pipeline/ares/internal/config"
	"gitlab.ttpai.work/sre/pipeline/ares/internal/db"
	"gitlab.ttpai.work/sre/pipeline/ares/internal/job"
	"gitlab.ttpai.work/sre/pipeline/ares/internal/logger"
	"gitlab.ttpai.work/sre/pipeline/ares/internal/web"
)

func main() {

	ctx, _ := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)

	logger.Init()

	cli.Init()

	err := config.Init()
	if err != nil {
		os.Exit(5)
	}

	oldLevel := logger.SetLevel(config.Main.Log.Level)
	if oldLevel == "" {
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

	// Ignore errors; 出错自动os.Exit(5)
	web.Run(ctx, api.Router)
}
