package controller

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/go-ree/ares/internal/integration"
)

func TestIntegrationSettingsFailureDoesNotExposeUpstreamErrors(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name       string
		err        error
		wantStatus int
	}{
		{
			name:       "upstream secret",
			err:        errors.New("connect failed: Authorization: Bearer provider-super-secret"),
			wantStatus: http.StatusUnprocessableEntity,
		},
		{
			name:       "concurrent update",
			err:        integration.ErrSettingsChanged,
			wantStatus: http.StatusConflict,
		},
		{
			name:       "legacy credential",
			err:        integration.ErrCredentialReentryRequired,
			wantStatus: http.StatusUnprocessableEntity,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			context, _ := gin.CreateTestContext(recorder)
			context.Request = httptest.NewRequest(http.MethodPut, "/api/v1/system/integrations/jenkins", nil)
			context.Set("request_id", "integration-redaction-test")

			respondIntegrationSettingsFailure(context, "jenkins", test.err)

			if recorder.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d", recorder.Code, test.wantStatus)
			}
			body := recorder.Body.String()
			for _, forbidden := range []string{"provider-super-secret", "Authorization", test.err.Error()} {
				if strings.Contains(body, forbidden) {
					t.Fatalf("response exposed upstream error %q: %s", forbidden, body)
				}
			}
			if test.name == "legacy credential" && !strings.Contains(body, "saved credential must be re-entered") {
				t.Fatalf("response omitted actionable credential re-entry signal: %s", body)
			}
		})
	}
}
