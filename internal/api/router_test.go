package api

import (
	"github.com/go-ree/ares/internal/jenkins"
	"github.com/go-ree/ares/internal/k8s"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestRouterRegistersWithoutPanic(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()

	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("router registration panicked: %v", recovered)
		}
	}()
	Router(router)

	foundAppsQuery := false
	foundCompatibilityRoute := false
	for _, route := range router.Routes() {
		if route.Method == "POST" && route.Path == "/api/v1/apps/query" {
			foundAppsQuery = true
		}
		if route.Method == "GET" && route.Path == "/api/v1/compatible/metadata/relation/all" {
			foundCompatibilityRoute = true
		}
	}
	if !foundAppsQuery {
		t.Fatal("expected apps query route to be registered")
	}
	if !foundCompatibilityRoute {
		t.Fatal("expected historical compatibility route to be registered")
	}
}

func TestDisabledIntegrationsReturnServiceUnavailable(t *testing.T) {
	originalJenkins := jenkins.Current()
	t.Cleanup(func() { jenkins.Activate(originalJenkins) })
	jenkins.Disable()
	k8s.Disable()

	gin.SetMode(gin.TestMode)
	router := gin.New()
	Router(router)

	tests := []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodGet, "/api/v1/status/nodes", ""},
		{http.MethodGet, "/api/v1/job/stream/log?task_id=1&log_type=ci", ""},
		{http.MethodGet, "/api/v1/k8s/pod/list?env=dev", ""},
	}
	for _, test := range tests {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(test.method, test.path, strings.NewReader(test.body))
		if test.body != "" {
			request.Header.Set("Content-Type", "application/json")
		}
		router.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s %s returned %d, expected 503: %s", test.method, test.path, recorder.Code, recorder.Body.String())
		}
	}
}

func TestPublishIsNotGloballyGatedByJenkins(t *testing.T) {
	originalJenkins := jenkins.Current()
	t.Cleanup(func() { jenkins.Activate(originalJenkins) })
	jenkins.Disable()

	gin.SetMode(gin.TestMode)
	router := gin.New()
	Router(router)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/deploy/publish",
		strings.NewReader(`{"app_name":"demo-api","branch":"main","env":"invalid env","publisher":"demo"}`))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("publish returned %d, want domain validation response: %s", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(strings.ToLower(recorder.Body.String()), "jenkins") {
		t.Fatalf("publish is still globally gated by Jenkins: %s", recorder.Body.String())
	}
}
