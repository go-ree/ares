package controller

import (
	"bytes"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/go-ree/ares/internal/auth"
)

func TestWriteInternalFailureDoesNotExposeRawError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const secret = "mysql://admin:provider-super-secret@database.internal/ares"

	var logs bytes.Buffer
	previousLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previousLogger) })

	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Set("request_id", "request-1")
	writeInternalFailure(context, http.StatusInternalServerError, "查询失败", "database", "list_apps", errors.New(secret))

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusInternalServerError)
	}
	if body := recorder.Body.String(); strings.Contains(body, secret) || !strings.Contains(body, publicInternalServiceError) {
		t.Fatalf("unsafe response body: %s", body)
	}
	if output := logs.String(); strings.Contains(output, secret) || !strings.Contains(output, "error_class=internal_failure") {
		t.Fatalf("unsafe log output: %s", output)
	}
}

func TestControllerErrorWritersRedactUnknownFailures(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const secret = "driver failure at mysql://admin:provider-super-secret@database.internal/ares"
	tests := []struct {
		name string
		call func(*gin.Context, error)
	}{
		{name: "application creation", call: func(c *gin.Context, err error) { handleAppCreationError(c, err) }},
		{name: "application config", call: func(c *gin.Context, err error) { writeAppConfigError(c, "操作失败", err) }},
		{name: "application domain", call: func(c *gin.Context, err error) { writeAppDomainError(c, "操作失败", err) }},
		{name: "environment", call: func(c *gin.Context, err error) { writeEnvironmentError(c, "操作失败", err) }},
		{name: "publish", call: func(c *gin.Context, err error) {
			writePublishError(c, "操作失败", http.StatusUnprocessableEntity, err)
		}},
		{name: "workflow", call: func(c *gin.Context, err error) { writeWorkflowError(c, "操作失败", err) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			context, _ := gin.CreateTestContext(recorder)
			test.call(context, errors.New(secret))
			if recorder.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d, want %d; body=%s", recorder.Code, http.StatusInternalServerError, recorder.Body.String())
			}
			if body := recorder.Body.String(); strings.Contains(body, secret) || !strings.Contains(body, publicInternalServiceError) {
				t.Fatalf("unsafe response body: %s", body)
			}
		})
	}
}

func TestPublicAuthErrorUsesTypedInputErrors(t *testing.T) {
	if got := publicAuthError(&auth.InputError{Message: "密码长度无效"}); got != "密码长度无效" {
		t.Fatalf("typed input error = %q", got)
	}
	const secret = "密码哈希数据库失败: provider-super-secret"
	if got := publicAuthError(errors.New(secret)); got != "authentication unavailable" {
		t.Fatalf("untyped error was exposed: %q", got)
	}
}
