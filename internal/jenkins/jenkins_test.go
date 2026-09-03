package jenkins

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
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

func TestCommitRuntimeWaitsForPinnedExecutorOperation(t *testing.T) {
	Activate(&Runtime{Config: RuntimeConfig{Address: "https://jenkins-old.example"}})
	t.Cleanup(Disable)

	snapshot, release := AcquireForOperation()
	released := false
	defer func() {
		if !released {
			release()
		}
	}()
	if snapshot == nil || snapshot.Address() != "https://jenkins-old.example" {
		t.Fatalf("pinned snapshot = %#v", snapshot)
	}
	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		close(started)
		done <- CommitRuntime(func() (*Runtime, error) {
			return &Runtime{Config: RuntimeConfig{Address: "https://jenkins-new.example"}}, nil
		})
	}()
	<-started
	select {
	case err := <-done:
		t.Fatalf("runtime commit completed before the pinned operation was released: %v", err)
	case <-time.After(20 * time.Millisecond):
	}

	release()
	released = true
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("runtime commit did not resume after releasing the operation")
	}
	if snapshot := Acquire(); snapshot == nil || snapshot.Address() != "https://jenkins-new.example" {
		t.Fatalf("active runtime was not replaced: %#v", snapshot)
	}
}

func TestQueueBuildTaskContextUsesCrumbAndParameterizedEndpoint(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/json":
			response.Header().Set("Content-Type", "application/json")
			response.Header().Set("X-Jenkins", "test-version")
			_, _ = response.Write([]byte(`{"jobs":[]}`))
		case "/crumbIssuer/api/json":
			username, password, _ := request.BasicAuth()
			if username != "test-user" || password != "test-token" {
				t.Errorf("crumb credentials = (%q, %q)", username, password)
			}
			http.SetCookie(response, &http.Cookie{Name: "JSESSIONID", Value: "session-1", Path: "/"})
			response.Header().Set("Content-Type", "application/json")
			_, _ = response.Write([]byte(`{"crumbRequestField":"Jenkins-Crumb","crumb":"crumb-1"}`))
		case "/job/folder/job/demo/buildWithParameters":
			if request.Method != http.MethodPost {
				t.Errorf("build method = %s, want POST", request.Method)
			}
			username, password, _ := request.BasicAuth()
			if username != "test-user" || password != "test-token" {
				t.Errorf("build credentials = (%q, %q)", username, password)
			}
			if got := request.Header.Get("Jenkins-Crumb"); got != "crumb-1" {
				t.Errorf("crumb header = %q", got)
			}
			cookie, err := request.Cookie("JSESSIONID")
			if err != nil || cookie.Value != "session-1" {
				t.Errorf("crumb cookie = %#v, %v", cookie, err)
			}
			if err := request.ParseForm(); err != nil {
				t.Errorf("parse build form: %v", err)
			}
			if request.Form.Get("branch") != "main" || request.Form.Get("env") != "qa" {
				t.Errorf("build form = %#v", request.Form)
			}
			response.Header().Set("Location", server.URL+"/jenkins/queue/item/42/")
			response.WriteHeader(http.StatusCreated)
		default:
			http.NotFound(response, request)
		}
	}))
	t.Cleanup(server.Close)

	runtime, err := BuildRuntime(context.Background(), RuntimeConfig{
		Address: server.URL, Username: "test-user", Token: "test-token",
	})
	if err != nil {
		t.Fatalf("BuildRuntime() error = %v", err)
	}
	snapshot := &ClientSnapshot{runtime: runtime}
	queueID, job, err := snapshot.QueueBuildTaskContext(context.Background(), "folder/demo", map[string]string{
		"branch": "main",
		"env":    "qa",
	})
	if err != nil {
		t.Fatalf("QueueBuildTaskContext() error = %v", err)
	}
	if queueID != 42 || job != "folder/demo" {
		t.Fatalf("QueueBuildTaskContext() = (%d, %q), want (42, %q)", queueID, job, "folder/demo")
	}
}

func TestQueueBuildTaskContextAllowsDisabledCrumbIssuer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/json":
			response.Header().Set("Content-Type", "application/json")
			_, _ = response.Write([]byte(`{"jobs":[]}`))
		case "/crumbIssuer/api/json":
			http.NotFound(response, request)
		case "/job/demo/build":
			response.Header().Set("Location", "/queue/item/7/")
			response.WriteHeader(http.StatusCreated)
		default:
			http.NotFound(response, request)
		}
	}))
	t.Cleanup(server.Close)

	runtime, err := BuildRuntime(context.Background(), RuntimeConfig{Address: server.URL})
	if err != nil {
		t.Fatalf("BuildRuntime() error = %v", err)
	}
	queueID, _, err := (&ClientSnapshot{runtime: runtime}).QueueBuildTaskContext(context.Background(), "demo", nil)
	if err != nil {
		t.Fatalf("QueueBuildTaskContext() error = %v", err)
	}
	if queueID != 7 {
		t.Fatalf("queueID=%d, want 7", queueID)
	}
}

func TestQueueBuildTaskContextCrumbTransportFailureDoesNotPanic(t *testing.T) {
	for _, test := range []struct {
		name    string
		handler http.HandlerFunc
		call    func(*ClientSnapshot) error
	}{
		{
			name: "disconnect",
			handler: func(response http.ResponseWriter, request *http.Request) {
				if request.URL.Path == "/api/json" {
					response.Header().Set("Content-Type", "application/json")
					_, _ = response.Write([]byte(`{"jobs":[]}`))
					return
				}
				connection, _, err := response.(http.Hijacker).Hijack()
				if err != nil {
					return
				}
				_ = connection.Close()
			},
			call: func(snapshot *ClientSnapshot) error {
				_, _, err := snapshot.QueueBuildTaskContext(context.Background(), "demo", map[string]string{"branch": "main"})
				return err
			},
		},
		{
			name: "timeout",
			handler: func(response http.ResponseWriter, request *http.Request) {
				if request.URL.Path == "/api/json" {
					response.Header().Set("Content-Type", "application/json")
					_, _ = response.Write([]byte(`{"jobs":[]}`))
					return
				}
				<-request.Context().Done()
			},
			call: func(snapshot *ClientSnapshot) error {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
				defer cancel()
				_, _, err := snapshot.QueueBuildTaskContext(ctx, "demo", map[string]string{"branch": "main"})
				return err
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(test.handler)
			t.Cleanup(server.Close)
			runtime, err := BuildRuntime(context.Background(), RuntimeConfig{Address: server.URL, Timeout: time.Second})
			if err != nil {
				t.Fatalf("BuildRuntime() error = %v", err)
			}
			err = test.call(&ClientSnapshot{runtime: runtime})
			if err == nil || !strings.Contains(err.Error(), "获取 Jenkins crumb") {
				t.Fatalf("QueueBuildTaskContext() error = %v, want explicit crumb error", err)
			}
		})
	}
}

func TestParseQueueID(t *testing.T) {
	for _, test := range []struct {
		location string
		want     int64
		wantErr  bool
	}{
		{location: "https://jenkins.example/queue/item/42/", want: 42},
		{location: "/jenkins/queue/item/73", want: 73},
		{location: "queue/item/9/", want: 9},
		{location: "", wantErr: true},
		{location: "/job/demo/42/", wantErr: true},
		{location: "/queue/item/0/", wantErr: true},
		{location: "/queue/item/not-a-number/", wantErr: true},
		{location: "://bad", wantErr: true},
	} {
		t.Run(url.PathEscape(fmt.Sprintf("%s-%v", test.location, test.wantErr)), func(t *testing.T) {
			got, err := parseQueueID(test.location)
			if (err != nil) != test.wantErr || got != test.want {
				t.Fatalf("parseQueueID(%q) = (%d, %v), want (%d, error=%v)", test.location, got, err, test.want, test.wantErr)
			}
		})
	}
}

func TestGetBuildStatusContextSupportsFolderJobWithSingleBuildPoll(t *testing.T) {
	var buildPolls atomic.Int32
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/api/json":
			_, _ = response.Write([]byte(`{"jobs":[]}`))
		case "/job/folder/job/demo/api/json":
			_, _ = fmt.Fprintf(response, `{"name":"demo","url":%q}`, server.URL+"/job/folder/job/demo")
		case "/job/folder/job/demo/42/api/json":
			buildPolls.Add(1)
			_, _ = response.Write([]byte(`{"number":42,"building":false,"result":"SUCCESS"}`))
		default:
			http.NotFound(response, request)
		}
	}))
	t.Cleanup(server.Close)

	runtime, err := BuildRuntime(context.Background(), RuntimeConfig{Address: server.URL})
	if err != nil {
		t.Fatalf("BuildRuntime() error = %v", err)
	}
	status, err := (&ClientSnapshot{runtime: runtime}).GetBuildStatusContext(context.Background(), "folder/demo", 42)
	if err != nil {
		t.Fatalf("GetBuildStatusContext() error = %v", err)
	}
	if status != "SUCCESS" {
		t.Fatalf("status = %q, want SUCCESS", status)
	}
	if got := buildPolls.Load(); got != 1 {
		t.Fatalf("build polls = %d, want exactly one", got)
	}
}
