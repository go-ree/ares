package webserver

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/go-ree/ares/internal/config"
	"github.com/go-ree/ares/internal/db"
	"github.com/go-ree/ares/internal/logger"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const shutdownTimeoutSecond = 10

func Run(ctx context.Context, router func(gin.IRouter)) {
	gin.SetMode(gin.ReleaseMode)

	g := gin.New()

	g.Use(gin.Recovery())

	g.Use(gin.LoggerWithConfig(gin.LoggerConfig{Output: logger.AccessFile}))

	liveness := func(c *gin.Context) {
		c.String(http.StatusOK, "OK")
	}
	g.Any("/health", liveness)
	g.GET("/health/live", liveness)
	g.GET("/health/ready", func(c *gin.Context) {
		if db.Engine == nil {
			c.String(http.StatusServiceUnavailable, "database not initialized")
			return
		}
		pingContext, cancel := context.WithTimeout(c.Request.Context(), time.Second)
		defer cancel()
		if err := db.Engine.PingContext(pingContext); err != nil {
			c.String(http.StatusServiceUnavailable, "database unavailable")
			return
		}
		c.String(http.StatusOK, "OK")
	})
	g.Any("/metrics", func(c *gin.Context) {
		promhttp.Handler().ServeHTTP(c.Writer, c.Request)
	})
	//g.Any("/_/setlevel/:level", func(c *gin.Context) {
	//	level := c.Param("level")
	//	oldLevel := logger.SetLevel(level)
	//	if oldLevel == "" {
	//		c.String(400, "error log level")
	//		return
	//	}
	//	c.String(http.StatusOK, oldLevel)
	//})

	router(&g.RouterGroup)

	server := &http.Server{
		Addr:    config.Main.Web.Address,
		Handler: g,
	}

	go func() {
		slog.Info("应用启动成功", "addr", server.Addr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("listen error", slog.Any("error", err))
			os.Exit(5)
		}
	}()

	<-ctx.Done()

	timeoutContext, cancel := context.WithTimeout(context.Background(), shutdownTimeoutSecond*time.Second)
	defer cancel()
	if err := server.Shutdown(timeoutContext); err != nil {
		slog.Error("Server Shutdown", slog.Any("error", err))
		os.Exit(5)
	}

	slog.Info("Server Shutdown success")
}
