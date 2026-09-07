package controller

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-ree/ares/internal/auth"
	"github.com/go-ree/ares/internal/workflow"
)

const handlerLogUses = "test.handler-logs@v1"

type handlerLogStore struct {
	source workflow.TaskStepLogSource
	err    error
}

func (s *handlerLogStore) GetTaskStepLogSource(_ context.Context, taskID int, stepKey string) (workflow.TaskStepLogSource, error) {
	if s.err != nil {
		return workflow.TaskStepLogSource{}, s.err
	}
	return s.source, nil
}

type handlerLogExecutor struct {
	*workflow.NoopExecutor
	read func(context.Context, workflow.LogRequest) (workflow.LogChunk, error)
}

func (e *handlerLogExecutor) Descriptor() workflow.Descriptor {
	descriptor := e.NoopExecutor.Descriptor()
	descriptor.Uses = handlerLogUses
	descriptor.Name = "Handler log test"
	descriptor.Capabilities.Logs = true
	return descriptor
}

func (e *handlerLogExecutor) ReadLogs(ctx context.Context, request workflow.LogRequest) (workflow.LogChunk, error) {
	return e.read(ctx, request)
}

func newHandlerLogController(t *testing.T, store workflow.LogSourceStore, read func(context.Context, workflow.LogRequest) (workflow.LogChunk, error)) *WorkflowController {
	t.Helper()
	registry := workflow.NewRegistry()
	if err := registry.Register(&handlerLogExecutor{NoopExecutor: workflow.NewNoopExecutor(), read: read}); err != nil {
		t.Fatal(err)
	}
	return NewWorkflowController(nil, nil, workflow.NewLogService(store, registry))
}

func newHandlerLogRouter(controller *WorkflowController) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/api/v1/tasks/:task_id/steps/:step_key/logs/stream", func(c *gin.Context) {
		SetPrincipal(c, auth.Principal{UserID: 7, Username: "viewer", AuthSource: "test"})
		controller.StreamTaskStepLogs(c)
	})
	return router
}

func TestTaskStepLogCursorContract(t *testing.T) {
	longCursor := strings.Repeat("x", workflow.MaxLogCursorBytes+1)
	for _, test := range []struct {
		name      string
		query     string
		headers   []string
		want      string
		wantError error
	}{
		{name: "initial"},
		{name: "query", query: "cursor=abc", want: "abc"},
		{name: "header", headers: []string{"abc"}, want: "abc"},
		{name: "matching", query: "cursor=abc", headers: []string{"abc"}, want: "abc"},
		{name: "conflict", query: "cursor=abc", headers: []string{"other"}, wantError: errLogCursorConflict},
		{name: "unknown", query: "job_name=demo", wantError: errInvalidLogQuery},
		{name: "provider identity", query: "cursor=1&build_id=42", wantError: errInvalidLogQuery},
		{name: "duplicate", query: "cursor=1&cursor=2", wantError: errInvalidLogQuery},
		{name: "empty", query: "cursor=", wantError: errInvalidLogQuery},
		{name: "newline", query: "cursor=" + url.QueryEscape("a\nb"), wantError: workflow.ErrInvalidLogCursor},
		{name: "multiple headers", headers: []string{"1", "2"}, wantError: workflow.ErrInvalidLogCursor},
		{name: "oversize", query: "cursor=" + longCursor, wantError: workflow.ErrInvalidLogCursor},
	} {
		t.Run(test.name, func(t *testing.T) {
			header := make(http.Header)
			for _, value := range test.headers {
				header.Add("Last-Event-ID", value)
			}
			got, err := taskStepLogCursor(test.query, header)
			if test.wantError == nil {
				if err != nil || got != test.want {
					t.Fatalf("cursor=(%q,%v), want %q", got, err, test.want)
				}
				return
			}
			if !errors.Is(err, test.wantError) {
				t.Fatalf("error=%v want=%v", err, test.wantError)
			}
		})
	}
}

func TestTaskStepSSEWritesLogBeforeCompletedEnd(t *testing.T) {
	chunks := make(chan workflow.LogChunk)
	results := make(chan error, 1)
	go func() {
		chunks <- workflow.LogChunk{Content: "hello\n", Cursor: "5", EOF: true}
		results <- nil
	}()
	writer := &deadlineBuffer{}
	succeeded := streamTaskStepSSE(
		context.Background(), context.Background(), writer, writer, "0", chunks, results, nil,
		sseStreamLimits{heartbeatInterval: time.Second, reauthInterval: time.Second, writeTimeout: time.Second, idleTimeout: time.Second},
	)
	if !succeeded {
		t.Fatal("completed stream was marked failed")
	}
	got := writer.String()
	logAt, endAt := strings.Index(got, "event: log\n"), strings.Index(got, "event: end\n")
	if logAt < 0 || endAt <= logAt || !strings.Contains(got, "id: 5\n") ||
		!strings.Contains(got, `data: {"content":"hello\n","cursor":"5","eof":true}`) ||
		!strings.Contains(got, `data: {"reason":"completed"}`) {
		t.Fatalf("stream=%q", got)
	}
}

func TestStreamTaskStepLogsHandlerEmitsLogAndCompletedEnd(t *testing.T) {
	reference := json.RawMessage(`{"provider_id":"server-owned"}`)
	controller := newHandlerLogController(t, &handlerLogStore{source: workflow.TaskStepLogSource{
		TaskID: 17, StepKey: "build", Uses: handlerLogUses, Status: workflow.StepRunning,
		ExternalReference: reference,
	}}, func(_ context.Context, request workflow.LogRequest) (workflow.LogChunk, error) {
		if request.TaskID != 17 || request.StepKey != "build" || request.Cursor != "9" ||
			string(request.ExternalReference) != string(reference) {
			t.Fatalf("reader request = %#v", request)
		}
		return workflow.LogChunk{Content: "hello\n", Cursor: "15", EOF: true}, nil
	})
	server := httptest.NewServer(newHandlerLogRouter(controller))
	defer server.Close()

	request, err := http.NewRequest(http.MethodGet,
		server.URL+"/api/v1/tasks/17/steps/build/logs/stream?cursor=9", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Last-Event-ID", "9")
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	stream := string(body)
	if response.StatusCode != http.StatusOK ||
		!strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream") ||
		!strings.Contains(stream, "event: log\nid: 15\n") ||
		!strings.Contains(stream, `data: {"content":"hello\n","cursor":"15","eof":true}`) ||
		!strings.Contains(stream, "event: end\nid: 15\n") ||
		!strings.Contains(stream, `data: {"reason":"completed"}`) {
		t.Fatalf("status=%d headers=%v stream=%q", response.StatusCode, response.Header, stream)
	}
}

func TestStreamTaskStepLogsFirstReadOutlivesOrdinaryServerWriteTimeout(t *testing.T) {
	controller := newHandlerLogController(t, &handlerLogStore{source: workflow.TaskStepLogSource{
		TaskID: 17, StepKey: "build", Uses: handlerLogUses,
		ExternalReference: []byte(`{"id":1}`),
	}}, func(ctx context.Context, _ workflow.LogRequest) (workflow.LogChunk, error) {
		timer := time.NewTimer(40 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-timer.C:
			return workflow.LogChunk{Content: "ready\n", Cursor: "6", EOF: true}, nil
		case <-ctx.Done():
			return workflow.LogChunk{}, ctx.Err()
		}
	})
	server := httptest.NewUnstartedServer(newHandlerLogRouter(controller))
	server.Config.WriteTimeout = 10 * time.Millisecond
	server.Start()
	defer server.Close()

	response, err := server.Client().Get(server.URL + "/api/v1/tasks/17/steps/build/logs/stream")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || !strings.Contains(string(body), "ready\\n") ||
		!strings.Contains(string(body), `{"reason":"completed"}`) {
		t.Fatalf("status=%d body=%q", response.StatusCode, body)
	}
}

func TestStreamTaskStepLogsSlowFirstFailureRetainsHTTPStatus(t *testing.T) {
	controller := newHandlerLogController(t, &handlerLogStore{source: workflow.TaskStepLogSource{
		TaskID: 17, StepKey: "build", Uses: handlerLogUses,
		ExternalReference: []byte(`{"id":1}`),
	}}, func(ctx context.Context, _ workflow.LogRequest) (workflow.LogChunk, error) {
		timer := time.NewTimer(40 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-timer.C:
			return workflow.LogChunk{}, errors.New("private delayed provider failure")
		case <-ctx.Done():
			return workflow.LogChunk{}, ctx.Err()
		}
	})
	server := httptest.NewUnstartedServer(newHandlerLogRouter(controller))
	server.Config.WriteTimeout = 10 * time.Millisecond
	server.Start()
	defer server.Close()

	response, err := server.Client().Get(server.URL + "/api/v1/tasks/17/steps/build/logs/stream")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusBadGateway ||
		!strings.Contains(string(body), `"error":"upstream_unavailable"`) ||
		strings.Contains(string(body), "private delayed provider failure") ||
		strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("status=%d headers=%v body=%q", response.StatusCode, response.Header, body)
	}
}

func TestStreamTaskStepLogsHandlerKeepsDeterministicErrorsAsHTTP(t *testing.T) {
	for _, test := range []struct {
		name       string
		store      workflow.LogSourceStore
		read       func(context.Context, workflow.LogRequest) (workflow.LogChunk, error)
		wantStatus int
		wantCode   string
	}{
		{
			name: "missing pair", store: &handlerLogStore{err: workflow.ErrNotFound},
			read:       func(context.Context, workflow.LogRequest) (workflow.LogChunk, error) { return workflow.LogChunk{}, nil },
			wantStatus: http.StatusNotFound, wantCode: "task_or_step_not_found",
		},
		{
			name: "legacy task", store: &handlerLogStore{err: workflow.ErrLegacyTask},
			read:       func(context.Context, workflow.LogRequest) (workflow.LogChunk, error) { return workflow.LogChunk{}, nil },
			wantStatus: http.StatusConflict, wantCode: "legacy_task",
		},
		{
			name: "not ready", store: &handlerLogStore{source: workflow.TaskStepLogSource{
				TaskID: 17, StepKey: "build", Uses: handlerLogUses,
			}},
			read:       func(context.Context, workflow.LogRequest) (workflow.LogChunk, error) { return workflow.LogChunk{}, nil },
			wantStatus: http.StatusConflict, wantCode: "logs_not_ready",
		},
		{
			name: "first upstream read", store: &handlerLogStore{source: workflow.TaskStepLogSource{
				TaskID: 17, StepKey: "build", Uses: handlerLogUses, ExternalReference: []byte(`{"id":1}`),
			}},
			read: func(context.Context, workflow.LogRequest) (workflow.LogChunk, error) {
				return workflow.LogChunk{}, errors.New("provider secret")
			},
			wantStatus: http.StatusBadGateway, wantCode: "upstream_unavailable",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			controller := newHandlerLogController(t, test.store, test.read)
			server := httptest.NewServer(newHandlerLogRouter(controller))
			defer server.Close()
			response, err := server.Client().Get(server.URL + "/api/v1/tasks/17/steps/build/logs/stream")
			if err != nil {
				t.Fatal(err)
			}
			body, readErr := io.ReadAll(response.Body)
			_ = response.Body.Close()
			if readErr != nil {
				t.Fatal(readErr)
			}
			if response.StatusCode != test.wantStatus ||
				!strings.Contains(string(body), `"error":"`+test.wantCode+`"`) ||
				strings.Contains(string(body), "provider secret") ||
				strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream") {
				t.Fatalf("status=%d headers=%v body=%s", response.StatusCode, response.Header, body)
			}
		})
	}
}

func TestStreamTaskStepLogsHandlerUsesStableInvalidRequestCode(t *testing.T) {
	controller := newHandlerLogController(t, &handlerLogStore{}, func(context.Context, workflow.LogRequest) (workflow.LogChunk, error) {
		t.Fatal("invalid path reached the log reader")
		return workflow.LogChunk{}, nil
	})
	for _, path := range []string{
		"/api/v1/tasks/not-an-id/steps/build/logs/stream",
		"/api/v1/tasks/0/steps/build/logs/stream",
		"/api/v1/tasks/01/steps/build/logs/stream",
		"/api/v1/tasks/+1/steps/build/logs/stream",
	} {
		recorder := httptest.NewRecorder()
		newHandlerLogRouter(controller).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusBadRequest ||
			!strings.Contains(recorder.Body.String(), `"error":"invalid_request"`) ||
			strings.Contains(recorder.Body.String(), "not-an-id") {
			t.Fatalf("path=%q status=%d body=%s", path, recorder.Code, recorder.Body.String())
		}
	}
}

func TestStreamTaskStepLogsHandlerCancelsReaderOnDisconnect(t *testing.T) {
	started := make(chan struct{})
	canceled := make(chan struct{})
	var calls atomic.Int32
	controller := newHandlerLogController(t, &handlerLogStore{source: workflow.TaskStepLogSource{
		TaskID: 17, StepKey: "build", Uses: handlerLogUses, ExternalReference: []byte(`{"id":1}`),
	}}, func(ctx context.Context, _ workflow.LogRequest) (workflow.LogChunk, error) {
		if calls.Add(1) == 1 {
			return workflow.LogChunk{Content: "first\n", Cursor: "1"}, nil
		}
		close(started)
		<-ctx.Done()
		close(canceled)
		return workflow.LogChunk{}, ctx.Err()
	})
	server := httptest.NewServer(newHandlerLogRouter(controller))
	defer server.Close()

	response, err := server.Client().Get(server.URL + "/api/v1/tasks/17/steps/build/logs/stream")
	if err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(response.Body)
	for {
		line, readErr := reader.ReadString('\n')
		if readErr != nil {
			t.Fatalf("read first SSE frame: %v", readErr)
		}
		if line == "\n" {
			break
		}
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("second reader call did not start")
	}
	if err := response.Body.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-canceled:
	case <-time.After(2 * time.Second):
		t.Fatal("client disconnect did not cancel the active reader")
	}
}

func TestTaskStepSSEPermissionRevocationIsNotSessionExpiration(t *testing.T) {
	writer := &deadlineBuffer{}
	streamTaskStepSSE(
		context.Background(), context.Background(), writer, writer, "7", nil, nil,
		func(context.Context) error { return ErrSSEPermissionRevoked },
		sseStreamLimits{heartbeatInterval: time.Second, reauthInterval: time.Millisecond, writeTimeout: time.Second, idleTimeout: time.Second},
	)
	got := writer.String()
	if !strings.Contains(got, "event: stream-error\n") || !strings.Contains(got, `{"code":"forbidden"}`) ||
		strings.Contains(got, "auth-expired") || strings.Contains(got, "session_expired") {
		t.Fatalf("permission revocation stream=%q", got)
	}
}

func TestTaskStepSSEPreservesSessionRevalidationFailureCode(t *testing.T) {
	writer := &deadlineBuffer{}
	streamTaskStepSSE(
		context.Background(), context.Background(), writer, writer, "7", nil, nil,
		func(context.Context) error { return errors.New("authentication store secret") },
		sseStreamLimits{heartbeatInterval: time.Second, reauthInterval: time.Millisecond, writeTimeout: time.Second, idleTimeout: time.Second},
	)
	got := writer.String()
	if !strings.Contains(got, "event: stream-error\n") ||
		!strings.Contains(got, `{"code":"session_revalidation_failed"}`) ||
		strings.Contains(got, "authentication store secret") {
		t.Fatalf("revalidation failure stream=%q", got)
	}
}

func TestTaskStepSSEHandlesClosedProducerChannels(t *testing.T) {
	chunks := make(chan workflow.LogChunk)
	results := make(chan error, 1)
	close(chunks)
	results <- nil
	close(results)
	writer := &deadlineBuffer{}
	if !streamTaskStepSSE(
		context.Background(), context.Background(), writer, writer, "7", chunks, results, nil,
		sseStreamLimits{heartbeatInterval: time.Second, reauthInterval: time.Second, writeTimeout: time.Second, idleTimeout: time.Second},
	) || !strings.Contains(writer.String(), `{"reason":"completed"}`) {
		t.Fatalf("closed producer stream=%q", writer.String())
	}
}

func TestLegacySSEPermissionRevocationIsNotSessionExpiration(t *testing.T) {
	writer := &deadlineBuffer{}
	streamJenkinsSSE(
		context.Background(), context.Background(), writer, writer, 7, nil, nil,
		func(context.Context) error { return ErrSSEPermissionRevoked },
		sseStreamLimits{heartbeatInterval: time.Second, reauthInterval: time.Millisecond, writeTimeout: time.Second, idleTimeout: time.Second},
	)
	got := writer.String()
	if !strings.Contains(got, "event: stream-error\n") || !strings.Contains(got, `{"code":"forbidden"}`) ||
		strings.Contains(got, "auth-expired") || strings.Contains(got, "session_expired") {
		t.Fatalf("permission revocation stream=%q", got)
	}
}

func TestSSETextIDRejectsEventInjectionBeforeWrite(t *testing.T) {
	writer := &deadlineBuffer{}
	id := "7\nevent: auth-expired"
	err := writeSSETextJSON(writer, writer, time.Second, "ping", &id, struct{}{})
	if !errors.Is(err, workflow.ErrInvalidLogCursor) || writer.Len() != 0 || writer.flushes != 0 {
		t.Fatalf("error=%v output=%q flushes=%d", err, writer.String(), writer.flushes)
	}
}

func TestTaskStepLogHTTPAndStreamErrorCodesAreStable(t *testing.T) {
	for _, test := range []struct {
		err        error
		reader     bool
		status     int
		code       string
		streamCode string
	}{
		{err: workflow.ErrInvalidLogCursor, reader: true, status: 400, code: "invalid_cursor", streamCode: "invalid_log_chunk"},
		{err: workflow.ErrNotFound, status: 404, code: "task_or_step_not_found"},
		{err: workflow.ErrLegacyTask, status: 409, code: "legacy_task"},
		{err: workflow.ErrLogsNotReady, reader: true, status: 409, code: "logs_not_ready", streamCode: "upstream_unavailable"},
		{err: workflow.ErrLogSourceMismatch, reader: true, status: 409, code: "log_source_mismatch", streamCode: "log_source_mismatch"},
		{err: workflow.ErrLogsUnsupported, status: 422, code: "logs_unsupported"},
		{err: workflow.ErrExecutorUnavailable, status: 503, code: "executor_unavailable", streamCode: "executor_unavailable"},
		{err: workflow.ErrInvalidLogChunk, reader: true, status: 502, code: "invalid_log_chunk", streamCode: "invalid_log_chunk"},
		{err: errors.New("database contains secret"), status: 500, code: "internal_error"},
		{err: errors.New("upstream contains secret"), reader: true, status: 502, code: "upstream_unavailable", streamCode: "upstream_unavailable"},
	} {
		gin.SetMode(gin.TestMode)
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		respondTaskStepLogError(c, test.err, test.reader)
		if recorder.Code != test.status || !strings.Contains(recorder.Body.String(), `"error":"`+test.code+`"`) ||
			strings.Contains(recorder.Body.String(), "contains secret") {
			t.Fatalf("err=%v status=%d body=%s", test.err, recorder.Code, recorder.Body.String())
		}
		if test.streamCode != "" && taskStepLogStreamErrorCode(test.err) != test.streamCode {
			t.Fatalf("stream code=%q want=%q", taskStepLogStreamErrorCode(test.err), test.streamCode)
		}
	}
}

func TestGenericAndLegacyLogRoutesShareAdmissionBeforeReader(t *testing.T) {
	original := logSSEAdmission
	logSSEAdmission = newConcurrentAdmission(1, 1)
	t.Cleanup(func() { logSSEAdmission = original })
	release, ok := logSSEAdmission.acquire("user:7")
	if !ok {
		t.Fatal("failed to reserve test admission")
	}
	defer release()

	gin.SetMode(gin.TestMode)
	controller := &WorkflowController{}
	router := gin.New()
	router.GET("/api/v1/tasks/:task_id/steps/:step_key/logs/stream", func(c *gin.Context) {
		SetPrincipal(c, auth.Principal{UserID: 7, Username: "viewer", AuthSource: "test"})
		controller.StreamTaskStepLogs(c)
	})
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/tasks/1/steps/build/logs/stream", nil))
	if recorder.Code != http.StatusTooManyRequests || recorder.Header().Get("Retry-After") != "5" ||
		!strings.Contains(recorder.Body.String(), `"error":"stream_capacity_exceeded"`) {
		t.Fatalf("status=%d headers=%v body=%s", recorder.Code, recorder.Header(), recorder.Body.String())
	}
}

func TestLegacyLogDeprecationHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/legacy", LegacyJenkinsLogDeprecationHeaders, func(c *gin.Context) { c.Status(http.StatusNoContent) })
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/legacy", nil))
	if recorder.Header().Get("Deprecation") != "true" || !strings.Contains(recorder.Header().Get("Warning"), "deprecated") ||
		recorder.Header().Get("Sunset") != "" || recorder.Header().Get("Link") != "" {
		t.Fatalf("headers=%v", recorder.Header())
	}
}
