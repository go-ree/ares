package webserver

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/go-ree/ares/internal/config"
	"github.com/go-ree/ares/internal/db"
	"github.com/go-ree/ares/internal/logger"

	"github.com/gin-gonic/gin"
)

const (
	shutdownTimeoutSecond = 10
	// RequestIDHeader is the normalized correlation header on every application response.
	RequestIDHeader = "X-Request-ID"
	// RequestIDKey is the Gin context key containing the normalized request ID.
	RequestIDKey       = "request_id"
	maxRequestIDLength = 64
)

type requestIDContextKey struct{}

// PreparedServer owns an already-bound listener. Preparing the HTTP boundary
// separately lets process composition fail before any background worker starts
// acquiring externally visible work.
type PreparedServer struct {
	server   *http.Server
	listener net.Listener
	started  atomic.Bool
}

func Run(ctx context.Context, router func(gin.IRouter)) error {
	if ctx == nil {
		return errors.New("HTTP server context is nil")
	}
	prepared, err := Prepare(router)
	if err != nil {
		return err
	}
	return prepared.Serve(ctx)
}

// Prepare constructs the router and binds the configured listener without
// starting the serving loop.
func Prepare(router func(gin.IRouter)) (*PreparedServer, error) {
	gin.SetMode(gin.ReleaseMode)

	g, err := newEngine(router, logger.AccessFile)
	if err != nil {
		// Config.Init validates these CIDRs before Run. Reaching this branch means
		// the validated configuration was mutated after startup.
		return nil, fmt.Errorf("initialize HTTP router: %w", err)
	}

	server := newHTTPServer(g)
	address := server.Addr
	if address == "" {
		address = ":http"
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return nil, fmt.Errorf("listen HTTP server: %w", err)
	}
	return &PreparedServer{server: server, listener: listener}, nil
}

// Serve runs one prepared server until it fails or ctx requests graceful
// shutdown. A PreparedServer is intentionally single-use.
func (p *PreparedServer) Serve(ctx context.Context) error {
	if p == nil || p.server == nil || p.listener == nil {
		return errors.New("HTTP server is not prepared")
	}
	if ctx == nil {
		return errors.New("HTTP server context is nil")
	}
	if !p.started.CompareAndSwap(false, true) {
		return errors.New("HTTP server can only be served once")
	}
	serveResult := make(chan error, 1)

	go func() {
		slog.Info("应用启动成功", "addr", p.listener.Addr().String())
		serveResult <- p.server.Serve(p.listener)
	}()

	select {
	case serveErr := <-serveResult:
		if errors.Is(serveErr, http.ErrServerClosed) && ctx.Err() != nil {
			return nil
		}
		return fmt.Errorf("serve HTTP requests: %w", serveErr)
	case <-ctx.Done():
	}

	timeoutContext, cancel := context.WithTimeout(context.Background(), shutdownTimeoutSecond*time.Second)
	defer cancel()
	if err := p.server.Shutdown(timeoutContext); err != nil {
		closeErr := p.server.Close()
		if closeErr != nil {
			return fmt.Errorf("shutdown HTTP server: %w", errors.Join(err, closeErr))
		}
		return fmt.Errorf("shutdown HTTP server: %w", err)
	}
	if serveErr := <-serveResult; serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
		return fmt.Errorf("serve HTTP requests: %w", serveErr)
	}

	slog.Info("Server Shutdown success")
	return nil
}

// Close releases a prepared listener when startup cannot proceed. It is safe
// to call after the listener has already been closed.
func (p *PreparedServer) Close() error {
	if p == nil {
		return nil
	}
	var errs []error
	if p.server != nil {
		if err := p.server.Close(); err != nil && !errors.Is(err, net.ErrClosed) && !errors.Is(err, http.ErrServerClosed) {
			errs = append(errs, err)
		}
	}
	if p.listener != nil {
		if err := p.listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func newEngine(router func(gin.IRouter), accessOutput io.Writer) (*gin.Engine, error) {
	g := gin.New()

	// Install request IDs first so every response, including health,
	// not-found, and recovered panic responses, carries the same correlation ID
	// that downstream handlers can read from Gin and request context.
	g.Use(requestIDMiddleware())
	g.Use(frameProtectionHeaders())

	// The access logger is outside recovery so recovered requests retain a 500
	// access record. Neither middleware serializes raw paths, queries, headers,
	// cookies, authorization values, or panic payloads.
	g.Use(routeTemplateAccessLogger(accessOutput))
	g.Use(redactedRecovery())

	trustedProxies := config.WebTrustedProxyCIDRs()
	if len(trustedProxies) == 0 {
		if err := g.SetTrustedProxies(nil); err != nil {
			return nil, err
		}
	} else if err := g.SetTrustedProxies(trustedProxies); err != nil {
		return nil, err
	}

	liveness := func(c *gin.Context) {
		c.String(http.StatusOK, "OK")
	}
	g.GET("/health", liveness)
	g.HEAD("/health", liveness)
	g.GET("/health/live", liveness)
	g.HEAD("/health/live", liveness)
	readiness := newReadinessProbe(func(ctx context.Context) error {
		if db.Engine == nil {
			return errors.New("database not initialized")
		}
		return db.Engine.PingContext(ctx)
	})
	readinessHandler := func(c *gin.Context) {
		if !readiness.status(c.Request.Context()) {
			c.String(http.StatusServiceUnavailable, "database unavailable")
			return
		}
		c.String(http.StatusOK, "OK")
	}
	g.GET("/health/ready", readinessHandler)
	g.HEAD("/health/ready", readinessHandler)
	//g.Any("/_/setlevel/:level", func(c *gin.Context) {
	//	level := c.Param("level")
	//	oldLevel := logger.SetLevel(level)
	//	if oldLevel == "" {
	//		c.String(400, "error log level")
	//		return
	//	}
	//	c.String(http.StatusOK, oldLevel)
	//})

	if router != nil {
		router(&g.RouterGroup)
	}
	return g, nil
}

func frameProtectionHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("X-Frame-Options", "DENY")
		c.Header("Content-Security-Policy", "frame-ancestors 'none'")
		c.Next()
	}
}

func newHTTPServer(handler http.Handler) *http.Server {
	address := ""
	if config.Main != nil {
		address = config.Main.Web.Address
	}
	return &http.Server{
		Addr:              address,
		Handler:           handler,
		ReadHeaderTimeout: config.WebReadHeaderTimeout(),
		ReadTimeout:       config.WebReadTimeout(),
		WriteTimeout:      config.WebWriteTimeout(),
		IdleTimeout:       config.WebIdleTimeout(),
		MaxHeaderBytes:    config.WebMaxHeaderBytes(),
	}
}

func routeTemplateAccessLogger(output io.Writer) gin.HandlerFunc {
	if output == nil {
		output = io.Discard
	}
	accessLogger := slog.New(slog.NewJSONHandler(output, nil))
	return func(c *gin.Context) {
		started := time.Now()
		requestID := c.GetString(RequestIDKey)
		c.Next()
		accessLogger.Info("http_request",
			"request_id", requestID,
			"method", c.Request.Method,
			"route", safeRouteTemplate(c),
			"status", c.Writer.Status(),
			"response_bytes", c.Writer.Size(),
			"duration_ms", time.Since(started).Milliseconds(),
		)
	}
}

func requestIDMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		requestID := clientRequestID(c.Request.Header.Values(RequestIDHeader))
		if requestID == "" {
			requestID = newRequestID()
		}

		c.Set(RequestIDKey, requestID)
		c.Request.Header.Set(RequestIDHeader, requestID)
		c.Header(RequestIDHeader, requestID)
		c.Request = c.Request.WithContext(context.WithValue(
			c.Request.Context(), requestIDContextKey{}, requestID,
		))
		c.Next()
	}
}

// RequestIDFromContext returns the request ID installed by the global HTTP
// middleware. Code with access to a Gin context may alternatively use
// c.GetString(RequestIDKey).
func RequestIDFromContext(ctx context.Context) (string, bool) {
	if ctx == nil {
		return "", false
	}
	requestID, ok := ctx.Value(requestIDContextKey{}).(string)
	return requestID, ok && requestID != ""
}

func clientRequestID(values []string) string {
	if len(values) != 1 || !validRequestID(values[0]) {
		return ""
	}
	return values[0]
}

func validRequestID(requestID string) bool {
	if requestID == "" || !utf8.ValidString(requestID) || utf8.RuneCountInString(requestID) > maxRequestIDLength {
		return false
	}
	for _, r := range requestID {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func newRequestID() string {
	var value [16]byte
	// Go's crypto/rand.Read contract fills the buffer or terminates the process;
	// it does not return recoverable partial entropy.
	_, _ = rand.Read(value[:])
	return hex.EncodeToString(value[:])
}

func redactedRecovery() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if recover() == nil {
				return
			}
			slog.Error("HTTP handler panic",
				"request_id", c.GetString(RequestIDKey),
				"method", c.Request.Method,
				"route", safeRouteTemplate(c),
			)
			c.Abort()
			if !c.Writer.Written() {
				c.Header("Content-Type", "application/json; charset=utf-8")
				c.Writer.WriteHeader(http.StatusInternalServerError)
				_, _ = c.Writer.Write([]byte(`{"code":0,"message":"内部错误","result":null,"error":"internal error","help":""}`))
			}
		}()
		c.Next()
	}
}

func safeRouteTemplate(c *gin.Context) string {
	if route := c.FullPath(); route != "" {
		return route
	}
	return "<unmatched>"
}
