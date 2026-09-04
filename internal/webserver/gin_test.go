package webserver

import (
	"bytes"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-ree/ares/internal/config"
)

func TestHTTPServerUsesFiniteConfiguredBoundaries(t *testing.T) {
	original := config.Main
	t.Cleanup(func() { config.Main = original })
	config.Main = &config.Config{}
	config.Main.Web.Address = ":9090"
	config.Main.Web.ReadHeaderTimeout = "4s"
	config.Main.Web.ReadTimeout = "14s"
	config.Main.Web.WriteTimeout = "24s"
	config.Main.Web.IdleTimeout = "54s"
	config.Main.Web.MaxHeaderBytes = 32768

	server := newHTTPServer(http.NewServeMux())
	if server.Addr != ":9090" {
		t.Fatalf("Addr = %q", server.Addr)
	}
	if server.ReadHeaderTimeout != 4*time.Second || server.ReadTimeout != 14*time.Second ||
		server.WriteTimeout != 24*time.Second || server.IdleTimeout != 54*time.Second {
		t.Fatalf("unexpected timeouts: %#v", server)
	}
	if server.MaxHeaderBytes != 32768 {
		t.Fatalf("MaxHeaderBytes = %d", server.MaxHeaderBytes)
	}
}

func TestAccessLoggerUsesRouteTemplateAndRedactsQuery(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var output bytes.Buffer
	engine := gin.New()
	engine.Use(requestIDMiddleware(), routeTemplateAccessLogger(&output))
	engine.GET("/api/v1/auth/oidc/callback", func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet,
		"/api/v1/auth/oidc/callback?code=super-secret-code&state=super-secret-state", nil)
	request.Header.Set("Authorization", "Bearer super-secret-token")
	request.Header.Set("Cookie", "ares_session=super-secret-cookie")
	engine.ServeHTTP(recorder, request)

	logLine := output.String()
	for _, forbidden := range []string{
		"super-secret-code", "super-secret-state", "super-secret-token", "super-secret-cookie",
		"?code=", "Authorization", "Cookie",
	} {
		if strings.Contains(logLine, forbidden) {
			t.Fatalf("access log leaked %q: %s", forbidden, logLine)
		}
	}
	if !strings.Contains(logLine, `"route":"/api/v1/auth/oidc/callback"`) {
		t.Fatalf("access log omitted route template: %s", logLine)
	}
	if !strings.Contains(logLine, `"request_id":"`) {
		t.Fatalf("access log omitted request ID: %s", logLine)
	}
}

func TestAccessLoggerDoesNotRecordUnmatchedRawPath(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var output bytes.Buffer
	engine := gin.New()
	engine.Use(routeTemplateAccessLogger(&output))

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/secret-path-value?token=secret-query-value", nil)
	engine.ServeHTTP(recorder, request)

	if strings.Contains(output.String(), "secret-path-value") || strings.Contains(output.String(), "secret-query-value") {
		t.Fatalf("unmatched request leaked raw URL: %s", output.String())
	}
	if !strings.Contains(output.String(), `"route":"<unmatched>"`) {
		t.Fatalf("unmatched route marker missing: %s", output.String())
	}
}

func TestRecoveryDoesNotReflectOrLogPanicPayload(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var accessOutput bytes.Buffer
	engine := gin.New()
	engine.Use(requestIDMiddleware(), routeTemplateAccessLogger(&accessOutput), redactedRecovery())
	engine.GET("/panic", func(*gin.Context) {
		panic("super-secret-panic-value")
	})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/panic?token=super-secret-query", nil)
	engine.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	assertGeneratedRequestID(t, recorder.Header().Get(RequestIDHeader))
	combined := recorder.Body.String() + accessOutput.String()
	if strings.Contains(combined, "super-secret-panic-value") || strings.Contains(combined, "super-secret-query") {
		t.Fatalf("panic response or access log leaked sensitive data: %s", combined)
	}
}

func TestRequestIDMiddlewarePreservesValidClientValueInAllContexts(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const supplied = "trace-请求-123"
	engine := gin.New()
	engine.Use(requestIDMiddleware())
	engine.GET("/inspect", func(c *gin.Context) {
		contextValue, ok := RequestIDFromContext(c.Request.Context())
		if !ok {
			t.Error("request ID missing from request context")
		}
		if got := c.GetString(RequestIDKey); got != supplied {
			t.Errorf("Gin request ID = %q", got)
		}
		if contextValue != supplied {
			t.Errorf("context request ID = %q", contextValue)
		}
		if got := c.Request.Header.Get(RequestIDHeader); got != supplied {
			t.Errorf("normalized request header = %q", got)
		}
		c.Status(http.StatusNoContent)
	})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/inspect", nil)
	request.Header.Set(RequestIDHeader, supplied)
	engine.ServeHTTP(recorder, request)

	if got := recorder.Header().Get(RequestIDHeader); got != supplied {
		t.Fatalf("response request ID = %q", got)
	}
}

func TestRequestIDMiddlewareReplacesInvalidOrAmbiguousClientValues(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name   string
		values []string
	}{
		{name: "absent"},
		{name: "empty", values: []string{""}},
		{name: "too long", values: []string{strings.Repeat("a", maxRequestIDLength+1)}},
		{name: "duplicate", values: []string{"first", "second"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			engine := gin.New()
			engine.Use(requestIDMiddleware())
			engine.GET("/inspect", func(c *gin.Context) {
				assertGeneratedRequestID(t, c.GetString(RequestIDKey))
				contextValue, ok := RequestIDFromContext(c.Request.Context())
				if !ok {
					t.Error("request ID missing from request context")
				}
				assertGeneratedRequestID(t, contextValue)
				if got := c.Request.Header.Values(RequestIDHeader); len(got) != 1 || got[0] != contextValue {
					t.Errorf("normalized request headers = %#v", got)
				}
				c.Status(http.StatusNoContent)
			})

			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, "/inspect", nil)
			for _, value := range test.values {
				request.Header.Add(RequestIDHeader, value)
			}
			engine.ServeHTTP(recorder, request)
			assertGeneratedRequestID(t, recorder.Header().Get(RequestIDHeader))
		})
	}
}

func TestRequestIDValidationRejectsControlAndInvalidUTF8(t *testing.T) {
	for _, value := range []string{"line\nbreak", "tab\tvalue", "delete\x7f", string([]byte{0xff})} {
		if validRequestID(value) {
			t.Errorf("validRequestID(%q) = true", value)
		}
	}
	if value := strings.Repeat("界", maxRequestIDLength); !validRequestID(value) {
		t.Error("64 Unicode characters should be valid")
	}
}

func TestNewEngineAddsRequestIDToBuiltInAndUnmatchedResponses(t *testing.T) {
	gin.SetMode(gin.TestMode)
	original := config.Main
	t.Cleanup(func() { config.Main = original })
	config.Main = &config.Config{}

	engine, err := newEngine(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/health", "/health/live", "/health/ready", "/metrics", "/not-found"} {
		t.Run(path, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
			assertGeneratedRequestID(t, recorder.Header().Get(RequestIDHeader))
			if recorder.Header().Get("X-Frame-Options") != "DENY" ||
				recorder.Header().Get("Content-Security-Policy") != "frame-ancestors 'none'" {
				t.Fatalf("frame protection headers missing: %v", recorder.Header())
			}
		})
	}
}

func TestMetricsAreNotPubliclyRegistered(t *testing.T) {
	gin.SetMode(gin.TestMode)
	original := config.Main
	t.Cleanup(func() { config.Main = original })
	config.Main = &config.Config{}

	engine, err := newEngine(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("public metrics status = %d, want 404", recorder.Code)
	}
	if strings.Contains(recorder.Body.String(), "go_goroutines") ||
		strings.Contains(recorder.Body.String(), "process_open_fds") {
		t.Fatal("uncredentialed response exposed process metrics")
	}
}

func assertGeneratedRequestID(t *testing.T, requestID string) {
	t.Helper()
	if len(requestID) != 32 {
		t.Fatalf("generated request ID length = %d (%q)", len(requestID), requestID)
	}
	decoded, err := hex.DecodeString(requestID)
	if err != nil || len(decoded) != 16 {
		t.Fatalf("generated request ID %q is not 16-byte hex: %v", requestID, err)
	}
}

func TestNewEngineDoesNotTrustForwardedAddressesByDefault(t *testing.T) {
	gin.SetMode(gin.TestMode)
	original := config.Main
	t.Cleanup(func() { config.Main = original })
	config.Main = &config.Config{}

	var observed string
	engine, err := newEngine(func(router gin.IRouter) {
		router.GET("/client-ip", func(c *gin.Context) {
			observed = c.ClientIP()
			c.Status(http.StatusNoContent)
		})
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/client-ip", nil)
	request.RemoteAddr = "192.0.2.10:1234"
	request.Header.Set("X-Forwarded-For", "203.0.113.7")
	engine.ServeHTTP(recorder, request)
	if observed != "192.0.2.10" {
		t.Fatalf("ClientIP() = %q, trusted forged forwarding header", observed)
	}
}
