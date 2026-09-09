package controller

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestJenkinsAPISettingsRefreshFailureFailsClosed(t *testing.T) {
	previous := ensureJenkinsRuntimeCurrent
	t.Cleanup(func() { ensureJenkinsRuntimeCurrent = previous })
	secretStoreError := errors.New("settings-db-secret-value")
	refreshCalls := 0
	ensureJenkinsRuntimeCurrent = func(context.Context) error {
		refreshCalls++
		return secretStoreError
	}

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/status/nodes", nil)
	GetJenkinsNodeStatus(c)

	if refreshCalls != 1 {
		t.Fatalf("settings refresh calls = %d, want 1", refreshCalls)
	}
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusServiceUnavailable)
	}
	if strings.Contains(recorder.Body.String(), secretStoreError.Error()) {
		t.Fatalf("response exposed settings error: %s", recorder.Body.String())
	}
}

func TestKubernetesAPISettingsRefreshFailureFailsClosed(t *testing.T) {
	previous := ensureKubernetesRuntimeCurrent
	t.Cleanup(func() { ensureKubernetesRuntimeCurrent = previous })
	secretStoreError := errors.New("kube-settings-secret-value")
	refreshCalls := 0
	ensureKubernetesRuntimeCurrent = func(context.Context) error {
		refreshCalls++
		return secretStoreError
	}

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/k8s/debug", nil)
	NewPodController().GetK8sDebugInfo(c)

	if refreshCalls != 1 {
		t.Fatalf("settings refresh calls = %d, want 1", refreshCalls)
	}
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusServiceUnavailable)
	}
	if strings.Contains(recorder.Body.String(), secretStoreError.Error()) {
		t.Fatalf("response exposed settings error: %s", recorder.Body.String())
	}
}
