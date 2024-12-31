package main

import (
	"context"
	"gitlab.ttpai.work/sre/pipeline/ares/internal/jenkins"
	"os"
	"os/signal"
	"syscall"

	"gitlab.ttpai.work/sre/pipeline/ares/internal/api"
	"gitlab.ttpai.work/sre/pipeline/ares/internal/cli"
	"gitlab.ttpai.work/sre/pipeline/ares/internal/config"
	"gitlab.ttpai.work/sre/pipeline/ares/internal/db"
	"gitlab.ttpai.work/sre/pipeline/ares/internal/job"
	"gitlab.ttpai.work/sre/pipeline/ares/internal/logger"
	"gitlab.ttpai.work/sre/pipeline/ares/internal/webserver"
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
