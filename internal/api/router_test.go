package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/go-ree/ares/internal/auth"
	"github.com/go-ree/ares/internal/jenkins"
	"github.com/go-ree/ares/internal/k8s"
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
	service, _, sessions := newAuthBoundary(t)
	router := gin.New()
	RouterWithRuntime(router, Runtime{Auth: service})

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
		request := authenticatedRequest(t, service, sessions[auth.RoleViewer], test.method, test.path, test.body)
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
	service, _, sessions := newAuthBoundary(t)
	router := gin.New()
	RouterWithRuntime(router, Runtime{Auth: service})
	recorder := httptest.NewRecorder()
	request := authenticatedRequest(t, service, sessions[auth.RoleReleaser], http.MethodPost,
		"/api/v1/deploy/publish", `{"app_name":"demo-api","branch":"main","env":"invalid env"}`)
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("publish returned %d, want domain validation response: %s", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(strings.ToLower(recorder.Body.String()), "jenkins") {
		t.Fatalf("publish is still globally gated by Jenkins: %s", recorder.Body.String())
	}
}

func TestRouterWithoutRuntimeFailsClosed(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	Router(router)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/compatible/metadata/relation/all", nil)
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("Router without auth runtime returned %d, want 503: %s", recorder.Code, recorder.Body.String())
	}
}
