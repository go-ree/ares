package controller

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

const validCITemplate = `{"schema_version":1,"kind":"ci","name":"Java","application_type":"java","steps":[{"key":"build","uses":"example.build@v1","outputs":{"jar":{"kind":"file","media_type":"application/java-archive","simulated":true}}}],"outputs":{"package":{"from":"steps.build.outputs.jar","type":{"kind":"file","media_type":"application/java-archive","simulated":true}}}}`

func TestTemplateValidationHTTPBoundary(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/validate", ValidatePipelineTemplate)
	for _, tc := range []struct {
		name, body, contentType string
		status                  int
	}{
		{"valid", validCITemplate, "application/json", 200},
		{"null", "null", "application/json", 422},
		{"unknown", strings.Replace(validCITemplate, `"kind":"ci"`, `"kind":"ci","force":true`, 1), "application/json", 400},
		{"duplicate", strings.Replace(validCITemplate, `"kind":"ci"`, `"kind":"ci","kind":"cd"`, 1), "application/json", 400},
		{"nested unknown", strings.Replace(validCITemplate, `"key":"build"`, `"key":"build","shell":"no"`, 1), "application/json", 400},
		{"sensitive", strings.Replace(validCITemplate, `"key":"build"`, `"key":"build","with":{"token":"NEVER-ECHO-ME"}`, 1), "application/json", 422},
		{"oversized", strings.Repeat(" ", 65537), "application/json", 413},
		{"trailing", validCITemplate + " {}", "application/json", 400},
		{"media", validCITemplate, "text/plain", 415},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			req := httptest.NewRequest("POST", "/validate", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", tc.contentType)
			r.ServeHTTP(w, req)
			if w.Code != tc.status {
				t.Fatalf("%d %s", w.Code, w.Body.String())
			}
			if strings.Contains(w.Body.String(), "NEVER-ECHO-ME") || strings.Contains(w.Body.String(), `"with"`) {
				t.Fatal("echoed config")
			}
			if w.Code == 200 && (!strings.Contains(w.Body.String(), `"executable":false`) || !strings.Contains(w.Body.String(), `"validation_scope":"structure_only"`)) {
				t.Fatal("missing structural-only warning")
			}
		})
	}
}
