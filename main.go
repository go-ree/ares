package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-ree/ares/internal/api"
	"github.com/go-ree/ares/internal/auth"
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
	authRuntime, err := initializeAuthRuntime(ctx)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "初始化身份与权限服务失败：%v\n", err)
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
	webserver.Run(ctx, func(router gin.IRouter) {
		api.RouterWithRuntime(router, authRuntime)
	})
	return exitSuccess
}

func initializeAuthRuntime(ctx context.Context) (api.Runtime, error) {
	if config.Main == nil || (!config.Main.Auth.OIDC.Enabled &&
		!config.Main.Auth.LocalLogin.Enabled && !config.Main.Auth.Bootstrap.Enabled) {
		return api.Runtime{}, errors.New("至少启用 OIDC、本地登录或一次性 bootstrap 中的一种认证方式")
	}
	if db.Engine == nil {
		return api.Runtime{}, errors.New("数据库尚未初始化")
	}
	store, err := auth.NewSQLStore(db.Engine.DB().DB)
	if err != nil {
		return api.Runtime{}, err
	}
	var oidcClient auth.OIDCClient
	if config.Main.Auth.OIDC.Enabled {
		oidcClient, err = auth.NewOIDCClient(auth.OIDCConfig{
			IssuerURL: config.Main.Auth.OIDC.IssuerURL, ClientID: config.Main.Auth.OIDC.ClientID,
			ClientSecret: config.OIDCClientSecret(), RedirectURL: config.OIDCRedirectURL(),
			Scopes:                   append([]string(nil), config.Main.Auth.OIDC.Scopes...),
			RequireVerifiedEmail:     config.Main.Auth.OIDC.RequireVerifiedEmail,
			AllowedSigningAlgorithms: append([]string(nil), config.Main.Auth.OIDC.AllowedSigningAlgorithms...),
			MaxClockSkew:             config.OIDCMaxClockSkew(), HTTPTimeout: config.OIDCHTTPTimeout(),
		})
		if err != nil {
			return api.Runtime{}, err
		}
	}
	service, err := auth.NewService(store, auth.Config{
		RootKey: config.AuthRootKey(), BootstrapToken: config.BootstrapToken(),
		PublicURL: config.WebPublicURL(), CookieSecure: config.AuthCookieSecure(),
		LocalLoginEnabled:      config.Main.Auth.LocalLogin.Enabled,
		BootstrapEnabled:       config.Main.Auth.Bootstrap.Enabled,
		OIDCAutoProvision:      config.Main.Auth.OIDC.AutoProvision,
		SessionAbsoluteTimeout: config.SessionAbsoluteTimeout(),
		SessionIdleTimeout:     config.SessionIdleTimeout(), SessionTouchInterval: config.SessionTouchInterval(),
		OIDCFlowTTL: config.OIDCFlowTTL(),
	}, oidcClient)
	if err != nil {
		return api.Runtime{}, err
	}
	adminCheckContext, cancelAdminCheck := context.WithTimeout(ctx, 5*time.Second)
	defer cancelAdminCheck()
	if err := service.EnsureAdministrativeAccess(adminCheckContext); err != nil {
		return api.Runtime{}, fmt.Errorf("管理员访问边界不可用: %w", err)
	}
	return api.Runtime{
		Auth: service, LegacyAdminTokenEnabled: config.Main.Auth.LegacyAdminToken.Enabled,
		LegacyAdminToken:       config.LegacyAdminToken(),
		LegacyAdminTokenSunset: config.Main.Auth.LegacyAdminToken.SunsetAt,
	}, nil
}
