package main

import (
	"context"
	"errors"
	"fmt"
	"io"
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

const (
	exitSuccess     = 0
	exitUsage       = 2
	exitSchemaState = 3
	exitOperational = 5
)

func main() {
	os.Exit(realMain(os.Args[1:], os.Stdout, os.Stderr))
}

func realMain(args []string, stdout, stderr io.Writer) int {
	options, err := cli.Parse(args)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "参数错误：%v\n\n%s", err, cli.Usage())
		return exitUsage
	}
	if options.Action == cli.ActionHelp {
		_, _ = io.WriteString(stdout, cli.Usage())
		return exitSuccess
	}

	if err := logger.Init(); err != nil {
		_, _ = fmt.Fprintf(stderr, "初始化日志失败：%v\n", err)
		return exitOperational
	}
	if err := config.Init(options.ConfigPath); err != nil {
		_, _ = fmt.Fprintf(stderr, "加载配置失败：%v\n", err)
		return exitOperational
	}
	if err := logger.Init2(config.Main.Log.Level); err != nil {
		_, _ = fmt.Fprintf(stderr, "初始化运行日志失败：%v\n", err)
		return exitOperational
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	switch options.Action {
	case cli.ActionMigrateStatus:
		status, err := db.SchemaMigrationStatus(ctx)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "检查数据库迁移状态失败：%s\n", db.SafeMigrationErrorText(err))
			return exitOperational
		}
		_, _ = fmt.Fprintln(stdout, status.String())
		if !status.Compatible() {
			return exitSchemaState
		}
		return exitSuccess
	case cli.ActionMigrateUp:
		status, err := db.RunSchemaMigrations(ctx, options.ResumeDirtyVersion)
		if err != nil {
			if status.Initialized || len(status.Problems) > 0 {
				_, _ = fmt.Fprintln(stdout, status.String())
			}
			_, _ = fmt.Fprintf(stderr, "执行数据库迁移失败：%s\n", db.SafeMigrationErrorText(err))
			if errors.Is(err, db.ErrSchemaState) {
				return exitSchemaState
			}
			return exitOperational
		}
		_, _ = fmt.Fprintln(stdout, status.String())
		return exitSuccess
	case cli.ActionServe:
		return runServer(ctx, stderr)
	default:
		_, _ = fmt.Fprintf(stderr, "参数错误：不支持的操作 %q\n", options.Action)
		return exitUsage
	}
}

func runServer(ctx context.Context, stderr io.Writer) int {
	config.InitSwagger()
	if err := db.Init(); err != nil {
		_, _ = fmt.Fprintf(stderr, "初始化数据库失败：%s\n", db.SafeMigrationErrorText(err))
		if errors.Is(err, db.ErrSchemaState) {
			return exitSchemaState
		}
		return exitOperational
	}
	if err := integration.Initialize(config.SettingsEncryptionKey()); err != nil {
		_, _ = fmt.Fprintf(stderr, "初始化外部集成失败：%v\n", err)
		return exitOperational
	}
	if err := job.Init(); err != nil {
		_, _ = fmt.Fprintf(stderr, "初始化后台任务失败：%v\n", err)
		return exitOperational
	}
	webserver.Run(ctx, api.Router)
	return exitSuccess
}
