package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-ree/ares/internal/api/util"
	"github.com/go-ree/ares/internal/config"

	"github.com/gin-gonic/gin"
)

func TestIntegrationSettingsAdminToken(t *testing.T) {
	originalToken := config.Main.Settings.AdminToken
	t.Cleanup(func() { config.Main.Settings.AdminToken = originalToken })

	gin.SetMode(gin.TestMode)
	router := gin.New()
	Router(router)

	tests := []struct {
		name           string
		configured     string
		provided       string
		wantHTTPCode   int
		wantResultCode int
	}{
		{
			name:           "endpoint disabled without server token",
			wantHTTPCode:   http.StatusServiceUnavailable,
			wantResultCode: 0,
		},
		{
			name:           "missing client token",
			configured:     "server-side-admin-token",
			wantHTTPCode:   http.StatusUnauthorized,
			wantResultCode: 0,
		},
		{
			name:           "wrong client token of equal length",
			configured:     "server-side-admin-token",
			provided:       "xxxxxxxxxxxxxxxxxxxxxxx",
			wantHTTPCode:   http.StatusUnauthorized,
			wantResultCode: 0,
		},
		{
			name:           "valid client token",
			configured:     "server-side-admin-token",
			provided:       "server-side-admin-token",
			wantHTTPCode:   http.StatusOK,
			wantResultCode: 1,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config.Main.Settings.AdminToken = test.configured
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, "/api/v1/system/integrations", nil)
			if test.provided != "" {
				request.Header.Set("X-Ares-Admin-Token", test.provided)
			}

			router.ServeHTTP(recorder, request)
			if recorder.Code != test.wantHTTPCode {
				t.Fatalf("GET integration settings returned %d, want %d: %s", recorder.Code, test.wantHTTPCode, recorder.Body.String())
			}

			var response util.ResponseTemplate
			if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
				t.Fatalf("decode response: %v; body = %s", err, recorder.Body.String())
			}
			if response.Code != test.wantResultCode {
				t.Fatalf("response code = %d, want %d", response.Code, test.wantResultCode)
			}
		})
	}
}

func TestIntegrationSettingsRejectsOversizedRequest(t *testing.T) {
	originalToken := config.Main.Settings.AdminToken
	config.Main.Settings.AdminToken = "server-side-admin-token"
	t.Cleanup(func() { config.Main.Settings.AdminToken = originalToken })

	gin.SetMode(gin.TestMode)
	router := gin.New()
	Router(router)

	body := `{"enabled":false,"token":"` + strings.Repeat("x", 256*1024) + `"}`
	request := httptest.NewRequest(http.MethodPut, "/api/v1/system/integrations/jenkins", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Ares-Admin-Token", "server-side-admin-token")
	recorder := httptest.NewRecorder()

	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized settings request returned %d, want %d: %s", recorder.Code, http.StatusRequestEntityTooLarge, recorder.Body.String())
	}
}

func TestWorkflowSettingsRoutesRequireAdminToken(t *testing.T) {
	originalToken := config.Main.Settings.AdminToken
	config.Main.Settings.AdminToken = "server-side-admin-token"
	t.Cleanup(func() { config.Main.Settings.AdminToken = originalToken })

	gin.SetMode(gin.TestMode)
	router := gin.New()
	Router(router)

	tests := []struct {
		method string
		path   string
		body   string
	}{
		{method: http.MethodGet, path: "/api/v1/app-configs/1/workflow"},
		{method: http.MethodPut, path: "/api/v1/app-configs/1/workflow", body: `{"revision":0,"spec":{"schema_version":1,"name":"demo","steps":[]}}`},
	}
	for _, test := range tests {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(test.method, test.path, strings.NewReader(test.body))
		if test.body != "" {
			request.Header.Set("Content-Type", "application/json")
		}
		router.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s returned %d, want %d: %s", test.method, test.path, recorder.Code, http.StatusUnauthorized, recorder.Body.String())
		}
	}
}
