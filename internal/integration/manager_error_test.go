package integration

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestSafeErrorNeverRetainsUntrustedProviderText(t *testing.T) {
	const secret = "Bearer provider-super-secret"
	got := safeError(errors.New(`upstream Status.message reflected ` + secret))
	if got != "外部集成暂不可用" || strings.Contains(got, secret) {
		t.Fatalf("safeError() = %q", got)
	}
	if got := safeError(context.DeadlineExceeded); got != "外部集成连接超时" {
		t.Fatalf("deadline class = %q", got)
	}
	if got := safeError(context.Canceled); got != "外部集成连接已取消" {
		t.Fatalf("cancellation class = %q", got)
	}
	if got := safeError(fmt.Errorf("load credential: %w", ErrCredentialReentryRequired)); got != "已保存的旧版凭据无法安全迁移，请重新录入" {
		t.Fatalf("credential re-entry class = %q", got)
	}
	if got := safeError(nil); got != "" {
		t.Fatalf("nil error = %q", got)
	}
}

func TestSnapshotMarksLegacyCredentialsForReentry(t *testing.T) {
	runtimeSettings.Lock()
	previousJenkins := runtimeSettings.jenkins
	previousKubernetes := runtimeSettings.kubernetes
	previousJenkinsError := runtimeSettings.jenkinsError
	previousKubernetesError := runtimeSettings.kubernetesError
	runtimeSettings.jenkins = storedJenkinsConfig{
		Enabled: true, Address: "https://jenkins.example.test", Username: "builder",
		TokenCiphertext: "v1:legacy-token",
	}
	runtimeSettings.kubernetes = storedKubernetesConfig{Enabled: true, Clusters: []storedKubernetesCluster{{
		Environment: "production", Name: "primary", KubeconfigCiphertext: "v1:legacy-kubeconfig",
	}}}
	runtimeSettings.jenkinsError = ""
	runtimeSettings.kubernetesError = ""
	runtimeSettings.Unlock()
	t.Cleanup(func() {
		runtimeSettings.Lock()
		runtimeSettings.jenkins = previousJenkins
		runtimeSettings.kubernetes = previousKubernetes
		runtimeSettings.jenkinsError = previousJenkinsError
		runtimeSettings.kubernetesError = previousKubernetesError
		runtimeSettings.Unlock()
	})

	view := SnapshotView()
	if !view.Jenkins.CredentialReentryRequired || view.Jenkins.LastError == "" {
		t.Fatalf("Jenkins legacy credential view = %#v", view.Jenkins)
	}
	if len(view.Kubernetes.Clusters) != 1 ||
		!view.Kubernetes.Clusters[0].CredentialReentryRequired || view.Kubernetes.LastError == "" {
		t.Fatalf("Kubernetes legacy credential view = %#v", view.Kubernetes)
	}
}
