package jenkins

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
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
		{name: "plaintext remote endpoint", address: "http://jenkins.example.com", want: "must use HTTPS"},
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

func TestNormalizeAddressAllowsPlaintextOnlyForLoopbackDevelopment(t *testing.T) {
	for _, address := range []string{
		"http://localhost:8080",
		"http://localhost.:8080",
		"http://127.0.0.1:8080",
		"http://[::1]:8080",
		"https://jenkins.example.com",
	} {
		if _, err := NormalizeAddress(address); err != nil {
			t.Errorf("NormalizeAddress(%q) error = %v", address, err)
		}
	}
	for _, address := range []string{
		"http://jenkins.example.com",
		"http://10.0.0.10:8080",
		"http://127.0.0.1.example.com",
	} {
		if _, err := NormalizeAddress(address); err == nil {
			t.Errorf("NormalizeAddress(%q) accepted a non-loopback plaintext endpoint", address)
		}
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

func TestBuildRuntimeBoundsJenkinsProbeResponse(t *testing.T) {
	const secretMarker = "sensitive-jenkins-response-payload"
	payload := `{"jobs":[],"description":"` +
		strings.Repeat(secretMarker, int(maxJenkinsJSONResponseBytes)/len(secretMarker)+2) + `"}`
	for _, test := range []struct {
		name           string
		contentLength  bool
		streamResponse bool
	}{
		{name: "known content length", contentLength: true},
		{name: "chunked stream", streamResponse: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
				response.Header().Set("Content-Type", "application/json")
				if test.contentLength {
					response.Header().Set("Content-Length", strconv.Itoa(len(payload)))
				}
				if test.streamResponse {
					response.WriteHeader(http.StatusOK)
					response.(http.Flusher).Flush()
				}
				_, _ = response.Write([]byte(payload))
			}))
			t.Cleanup(server.Close)

			_, err := BuildRuntime(context.Background(), RuntimeConfig{Address: server.URL, Timeout: 5 * time.Second})
			if err == nil || !strings.Contains(err.Error(), "upstream response exceeds configured size limit") {
				t.Fatalf("BuildRuntime() error = %v, want a response-size error", err)
			}
			if strings.Contains(err.Error(), secretMarker) {
				t.Fatalf("BuildRuntime() exposed upstream response content: %v", err)
			}
		})
	}
}

func TestRuntimeJenkinsClientBoundsGenericResponses(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/api/json" {
			response.Header().Set("Content-Type", "application/json")
			_, _ = response.Write([]byte(`{"jobs":[]}`))
			return
		}
		response.Header().Set("Content-Length", strconv.FormatInt(maxJenkinsJSONResponseBytes+1, 10))
		response.WriteHeader(http.StatusOK)
		response.(http.Flusher).Flush()
	}))
	t.Cleanup(server.Close)
	runtime, err := BuildRuntime(context.Background(), RuntimeConfig{Address: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodGet, server.URL+"/job/demo/api/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := runtime.Client.Requester.Client.Do(request)
	if response != nil {
		response.Body.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "upstream response exceeds configured size limit") {
		t.Fatalf("runtime Jenkins request error = %v, want a response-size error", err)
	}
}

func TestBuildRuntimeRejectsRedirectBeforeSendingCredentialsToTarget(t *testing.T) {
	var targetRequests atomic.Int64
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		targetRequests.Add(1)
	}))
	t.Cleanup(target.Close)

	source := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		http.Redirect(response, request, target.URL, http.StatusTemporaryRedirect)
	}))
	t.Cleanup(source.Close)
	originalTransport := http.DefaultTransport
	http.DefaultTransport = source.Client().Transport
	t.Cleanup(func() { http.DefaultTransport = originalTransport })

	_, err := BuildRuntime(context.Background(), RuntimeConfig{
		Address: source.URL, Username: "build-user", Token: "jenkins-secret",
	})
	if err == nil || !strings.Contains(err.Error(), "redirects are not allowed") {
		t.Fatalf("BuildRuntime() redirect error = %v", err)
	}
	if got := targetRequests.Load(); got != 0 {
		t.Fatalf("redirect target received %d request(s), want zero", got)
	}
}

func TestGetProgressiveTextBoundsEachUpstreamResponse(t *testing.T) {
	payload := strings.Repeat("x", int(maxJenkinsJSONResponseBytes)+17)
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/api/json" {
			response.Header().Set("Content-Type", "application/json")
			_, _ = response.Write([]byte(`{"jobs":[]}`))
			return
		}
		if request.URL.Path != "/job/demo/42/logText/progressiveText" {
			http.NotFound(response, request)
			return
		}
		if got := request.Header.Get("Accept-Encoding"); got != "identity" {
			t.Errorf("Accept-Encoding = %q, want identity", got)
		}
		start, err := strconv.ParseInt(request.URL.Query().Get("start"), 10, 64)
		if err != nil || start < 0 || start > int64(len(payload)) {
			t.Errorf("invalid start offset %q: %v", request.URL.Query().Get("start"), err)
			response.WriteHeader(http.StatusBadRequest)
			return
		}
		response.Header().Set("X-Text-Size", strconv.Itoa(len(payload)))
		response.Header().Set("X-More-Data", "false")
		_, _ = response.Write([]byte(payload[start:]))
	}))
	t.Cleanup(server.Close)
	runtime, err := BuildRuntime(context.Background(), RuntimeConfig{Address: server.URL})
	if err != nil {
		t.Fatal(err)
	}

	first, next, more, err := getProgressiveText(context.Background(), runtime, "demo", 42, 0)
	if err != nil {
		t.Fatal(err)
	}
	if int64(len(first)) != maxProgressiveTextResponseBytes || next != maxProgressiveTextResponseBytes || !more {
		t.Fatalf("first bounded response = bytes:%d next:%d more:%v", len(first), next, more)
	}
	lastStart := int64(len(payload) - 17)
	last, final, more, err := getProgressiveText(context.Background(), runtime, "demo", 42, lastStart)
	if err != nil {
		t.Fatal(err)
	}
	if len(last) != 17 || final != int64(len(payload)) || more {
		t.Fatalf("last response = bytes:%d next:%d more:%v", len(last), final, more)
	}
}

func TestGetProgressiveTextRejectsInvalidCursorHeaders(t *testing.T) {
	for _, test := range []struct {
		name     string
		textSize []string
		moreData []string
	}{
		{name: "missing text size"},
		{name: "non numeric text size", textSize: []string{"three"}},
		{name: "repeated text size", textSize: []string{"3", "3"}},
		{name: "inconsistent text size", textSize: []string{"2"}},
		{name: "invalid more data", textSize: []string{"3"}, moreData: []string{"perhaps"}},
		{name: "repeated more data", textSize: []string{"3"}, moreData: []string{"true", "false"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				if request.URL.Path == "/api/json" {
					response.Header().Set("Content-Type", "application/json")
					_, _ = response.Write([]byte(`{"jobs":[]}`))
					return
				}
				for _, value := range test.textSize {
					response.Header().Add("X-Text-Size", value)
				}
				for _, value := range test.moreData {
					response.Header().Add("X-More-Data", value)
				}
				_, _ = response.Write([]byte("abc"))
			}))
			t.Cleanup(server.Close)
			runtime, err := BuildRuntime(context.Background(), RuntimeConfig{Address: server.URL})
			if err != nil {
				t.Fatal(err)
			}
			if _, _, _, err := getProgressiveText(context.Background(), runtime, "demo", 42, 0); err == nil {
				t.Fatal("invalid progressiveText response was accepted")
			}
		})
	}
}

func TestStreamJenkinsBuildLogReportsFinalFetchFailure(t *testing.T) {
	var progressiveRequests atomic.Int32
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/api/json":
			_, _ = response.Write([]byte(`{"jobs":[]}`))
		case "/job/demo/api/json":
			_, _ = fmt.Fprintf(response, `{"name":"demo","url":%q}`, server.URL+"/job/demo")
		case "/job/demo/42/api/json":
			_, _ = response.Write([]byte(`{"number":42,"building":false,"result":"SUCCESS"}`))
		case "/job/demo/42/logText/progressiveText":
			if progressiveRequests.Add(1) == 1 {
				response.Header().Set("Content-Type", "text/plain")
				response.Header().Set("X-Text-Size", "0")
				response.Header().Set("X-More-Data", "false")
				return
			}
			http.Error(response, "upstream failed", http.StatusBadGateway)
		default:
			http.NotFound(response, request)
		}
	}))
	t.Cleanup(server.Close)
	runtime, err := BuildRuntime(context.Background(), RuntimeConfig{Address: server.URL})
	if err != nil {
		t.Fatal(err)
	}

	logs := make(chan BuildLogChunk, 1)
	errors := make(chan error, 1)
	succeeded := (&ClientSnapshot{runtime: runtime}).StreamJenkinsBuildLog(context.Background(), &BuildLogQuery{
		JobName: "demo", BuildId: 42,
	}, logs, errors)
	if succeeded {
		t.Fatal("final progressiveText failure was reported as a successful stream")
	}
	select {
	case streamErr := <-errors:
		if streamErr == nil || !strings.Contains(streamErr.Error(), "502") {
			t.Fatalf("stream error = %v, want final fetch status", streamErr)
		}
	default:
		t.Fatal("final progressiveText failure was not sent to the stream error channel")
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
