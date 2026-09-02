package jenkins

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestBuildRuntimeRejectsInvalidAddress(t *testing.T) {
	tests := []struct {
		name    string
		address string
		want    string
	}{
		{name: "empty", address: "", want: "valid HTTP or HTTPS URL"},
		{name: "missing scheme", address: "jenkins.example.com", want: "valid HTTP or HTTPS URL"},
		{name: "unsupported scheme", address: "ftp://jenkins.example.com", want: "valid HTTP or HTTPS URL"},
		{name: "missing host", address: "https://", want: "valid HTTP or HTTPS URL"},
		{name: "embedded credentials", address: "https://user:password@jenkins.example.com", want: "must not contain embedded credentials"},
		{name: "query", address: "https://jenkins.example.com?target=other", want: "must not contain a query or fragment"},
		{name: "fragment", address: "https://jenkins.example.com#other", want: "must not contain a query or fragment"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runtime, err := BuildRuntime(context.Background(), RuntimeConfig{Address: test.address})
			if err == nil {
				t.Fatalf("BuildRuntime(%q) unexpectedly succeeded: %#v", test.address, runtime)
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Fatalf("BuildRuntime(%q) error = %q, want it to contain %q", test.address, err, test.want)
			}
		})
	}
}

func TestBuildRuntimeHonorsCancelledContext(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		<-request.Context().Done()
	}))
	t.Cleanup(server.Close)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	started := time.Now()
	_, err := BuildRuntime(ctx, RuntimeConfig{Address: server.URL, Timeout: time.Minute})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("BuildRuntime() error = %v, want context cancellation", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("BuildRuntime() returned after %s, want prompt cancellation", elapsed)
	}
}

func TestBuildRuntimeValidatesConnectionAndNormalizesConfig(t *testing.T) {
	var username, password string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		username, password, _ = request.BasicAuth()
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Jenkins", "test-version")
		_, _ = w.Write([]byte(`{"jobs":[]}`))
	}))
	t.Cleanup(server.Close)

	runtime, err := BuildRuntime(context.Background(), RuntimeConfig{
		Address:  "  " + server.URL + "/  ",
		Username: "  test-user  ",
		Token:    "test-token",
	})
	if err != nil {
		t.Fatalf("BuildRuntime() error = %v", err)
	}
	if runtime == nil || runtime.Client == nil {
		t.Fatal("BuildRuntime() returned an incomplete runtime")
	}
	if runtime.Config.Address != server.URL {
		t.Fatalf("normalized address = %q, want %q", runtime.Config.Address, server.URL)
	}
	if runtime.Config.Username != "test-user" {
		t.Fatalf("normalized username = %q, want %q", runtime.Config.Username, "test-user")
	}
	if runtime.Config.Timeout != defaultTimeout {
		t.Fatalf("default timeout = %s, want %s", runtime.Config.Timeout, defaultTimeout)
	}
	if runtime.Client.Version != "test-version" {
		t.Fatalf("Jenkins version = %q, want %q", runtime.Client.Version, "test-version")
	}
	if username != "test-user" || password != "test-token" {
		t.Fatalf("Jenkins request credentials = (%q, %q), want configured credentials", username, password)
	}
}
