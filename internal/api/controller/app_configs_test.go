package controller

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-ree/ares/internal/app"
	"github.com/go-ree/ares/internal/environment"

	"github.com/gin-gonic/gin"
)

func TestWriteAppConfigErrorMapsDomainErrors(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name       string
		err        error
		wantStatus int
	}{
		{
			name:       "validation",
			err:        app.NewValidationError("env 格式错误"),
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "environment not found",
			err:        fmt.Errorf("查询环境: %w", environment.ErrNotFound),
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "application not found",
			err:        app.NewAppNotFoundError(10001, ""),
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "app config not found",
			err:        app.NewAppConfigNotFoundErrorByID(42),
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "environment disabled",
			err:        fmt.Errorf("环境不可用于配置: %w", environment.ErrDisabled),
			wantStatus: http.StatusConflict,
		},
		{
			name:       "duplicate app config",
			err:        app.NewDuplicateAppConfigError(10001, "preview"),
			wantStatus: http.StatusConflict,
		},
		{
			name:       "infrastructure error",
			err:        errors.New("database unavailable"),
			wantStatus: http.StatusInternalServerError,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			context, _ := gin.CreateTestContext(recorder)

			writeAppConfigError(context, "操作失败", test.err, "config_id", 42)

			if recorder.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", recorder.Code, test.wantStatus, recorder.Body.String())
			}
			if test.wantStatus == http.StatusInternalServerError && strings.Contains(recorder.Body.String(), "database unavailable") {
				t.Fatalf("infrastructure error leaked in response: %s", recorder.Body.String())
			}
		})
	}
}
