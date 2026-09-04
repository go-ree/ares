package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestBindJSONStrictBoundary(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name        string
		contentType string
		body        string
		limit       int64
		wantStatus  int
	}{
		{name: "valid", contentType: "application/json", body: `{"name":"demo"}`, wantStatus: http.StatusNoContent},
		{name: "vendor json", contentType: "application/problem+json", body: `{"name":"demo"}`, wantStatus: http.StatusNoContent},
		{name: "unknown field", contentType: "application/json", body: `{"name":"demo","role":"admin"}`, wantStatus: http.StatusBadRequest},
		{name: "duplicate field", contentType: "application/json", body: `{"name":"demo","name":"admin"}`, wantStatus: http.StatusBadRequest},
		{name: "nested duplicate field", contentType: "application/json", body: `{"name":"demo","metadata":{"role":"viewer","role":"admin"}}`, wantStatus: http.StatusBadRequest},
		{name: "second value", contentType: "application/json", body: `{"name":"demo"}{"name":"again"}`, wantStatus: http.StatusBadRequest},
		{name: "oversized", contentType: "application/json", body: `{"name":"demo"}`, limit: 4, wantStatus: http.StatusRequestEntityTooLarge},
		{name: "missing content type", body: `{"name":"demo"}`, wantStatus: http.StatusUnsupportedMediaType},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			router := gin.New()
			router.POST("/", func(c *gin.Context) {
				var request struct {
					Name string `json:"name"`
				}
				if BindJSON(c, &request, test.limit) {
					c.Status(http.StatusNoContent)
				}
			})
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(test.body))
			if test.contentType != "" {
				request.Header.Set("Content-Type", test.contentType)
			}
			router.ServeHTTP(recorder, request)
			if recorder.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d: %s", recorder.Code, test.wantStatus, recorder.Body.String())
			}
		})
	}
}

func TestBindJSONDoesNotReflectSensitivePayload(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/", func(c *gin.Context) {
		var request struct {
			Name string `json:"name"`
		}
		_ = BindJSON(c, &request, 1024)
	})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"name":secret-value}`))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)
	if strings.Contains(recorder.Body.String(), "secret-value") {
		t.Fatalf("decoder response reflected sensitive input: %s", recorder.Body.String())
	}
}
