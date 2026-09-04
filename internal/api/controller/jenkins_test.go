package controller

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-ree/ares/internal/entity"
	"github.com/go-ree/ares/internal/jenkins"
)

func TestTaskBuildLogReferenceUsesPersistedTaskIdentity(t *testing.T) {
	task := entity.TaskRecord{
		TaskId: 17, CiJobName: "folder/build", CiBuildId: 42,
		CdJobName: "folder/deploy", CdBuildId: 73,
		JenkinsAddress: "https://jenkins.example/",
	}
	ci, err := taskBuildLogReference(task, "ci", 100, "https://jenkins.example")
	if err != nil {
		t.Fatal(err)
	}
	if ci.JobName != task.CiJobName || ci.BuildId != task.CiBuildId || ci.Start != 100 {
		t.Fatalf("CI reference = %#v", ci)
	}
	cd, err := taskBuildLogReference(task, "cd", 0, "https://jenkins.example/")
	if err != nil {
		t.Fatal(err)
	}
	if cd.JobName != task.CdJobName || cd.BuildId != task.CdBuildId {
		t.Fatalf("CD reference = %#v", cd)
	}
	if _, err := taskBuildLogReference(task, "other", 0, "https://jenkins.example"); err == nil {
		t.Fatal("arbitrary log type should be rejected")
	}
	if _, err := taskBuildLogReference(entity.TaskRecord{TaskId: 18}, "ci", 0, "https://jenkins.example"); err == nil {
		t.Fatal("task without a persisted build reference should be rejected")
	}
	if _, err := taskBuildLogReference(task, "ci", 0, "https://jenkins-new.example"); err == nil {
		t.Fatal("a different Jenkins instance should be rejected")
	}
}

func TestParseLastEventID(t *testing.T) {
	tests := []struct {
		name        string
		values      []string
		want        int64
		wantPresent bool
		wantError   bool
	}{
		{name: "absent"},
		{name: "zero", values: []string{"0"}, wantPresent: true},
		{name: "positive", values: []string{"9223372036854775807"}, want: 9223372036854775807, wantPresent: true},
		{name: "empty", values: []string{""}, wantPresent: true, wantError: true},
		{name: "negative", values: []string{"-1"}, wantPresent: true, wantError: true},
		{name: "explicit plus", values: []string{"+1"}, wantPresent: true, wantError: true},
		{name: "whitespace", values: []string{" 1"}, wantPresent: true, wantError: true},
		{name: "decimal", values: []string{"1.0"}, wantPresent: true, wantError: true},
		{name: "overflow", values: []string{"9223372036854775808"}, wantPresent: true, wantError: true},
		{name: "multiple", values: []string{"1", "2"}, wantPresent: true, wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			header := make(http.Header)
			for _, value := range test.values {
				header.Add("Last-Event-ID", value)
			}
			got, present, err := parseLastEventID(header)
			if (err != nil) != test.wantError {
				t.Fatalf("error = %v, wantError = %v", err, test.wantError)
			}
			if present != test.wantPresent || got != test.want {
				t.Fatalf("parseLastEventID() = (%d, %v), want (%d, %v)", got, present, test.want, test.wantPresent)
			}
		})
	}
}

func TestValidJenkinsStreamQueryRejectsUnknownAndRepeatedParameters(t *testing.T) {
	tests := []struct {
		name   string
		values url.Values
		want   bool
	}{
		{name: "valid", values: url.Values{"task_id": {"1"}, "log_type": {"ci"}, "start": {"0"}}, want: true},
		{name: "unknown", values: url.Values{"task_id": {"1"}, "log_type": {"ci"}, "token": {"secret"}}},
		{name: "repeated", values: url.Values{"task_id": {"1", "2"}, "log_type": {"ci"}}},
		{name: "empty cursor", values: url.Values{"task_id": {"1"}, "log_type": {"ci"}, "start": {""}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := validJenkinsStreamQuery(test.values); got != test.want {
				t.Fatalf("validJenkinsStreamQuery() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestJenkinsSSEAdmissionEnforcesPerPrincipalAndProcessCapacity(t *testing.T) {
	admission := newConcurrentAdmission(3, 2)
	releaseA1, ok := admission.acquire("user:1")
	if !ok {
		t.Fatal("first stream for user 1 was rejected")
	}
	releaseA2, ok := admission.acquire("user:1")
	if !ok {
		t.Fatal("second stream for user 1 was rejected")
	}
	if _, ok := admission.acquire("user:1"); ok {
		t.Fatal("third stream for one principal bypassed the per-principal ceiling")
	}
	releaseB1, ok := admission.acquire("user:2")
	if !ok {
		t.Fatal("first stream for user 2 was rejected")
	}
	if _, ok := admission.acquire("user:3"); ok {
		t.Fatal("fourth stream bypassed the process-wide ceiling")
	}

	releaseA1()
	releaseA1() // release is deliberately idempotent.
	releaseC, ok := admission.acquire("user:3")
	if !ok {
		t.Fatal("capacity was not returned after release")
	}
	releaseA2()
	releaseB1()
	releaseC()
	if admission.activeTotal != 0 || len(admission.activeByKey) != 0 {
		t.Fatalf("admission leaked active streams: total=%d by-key=%v", admission.activeTotal, admission.activeByKey)
	}
}

func TestUpstreamFailureResponseDoesNotExposeProviderDetails(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodGet, "/nodes", nil)
	context.Set("request_id", "redaction-test")
	secret := `Get "https://jenkins.internal.example/api": X-Error reflected-token-value`

	respondUpstreamFailure(context, "jenkins", "list_nodes", errors.New(secret))
	if recorder.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", recorder.Code)
	}
	body := recorder.Body.String()
	for _, forbidden := range []string{"jenkins.internal.example", "reflected-token-value", "X-Error"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("upstream failure exposed %q: %s", forbidden, body)
		}
	}
	if !strings.Contains(body, "upstream integration unavailable") {
		t.Fatalf("generic upstream failure missing: %s", body)
	}
}

func TestSSEFrameUsesFiniteRollingDeadline(t *testing.T) {
	writer := &deadlineBuffer{}
	cursor := int64(42)
	if err := clearSSEWriteDeadline(writer); err != nil {
		t.Fatal(err)
	}
	if err := writeSSEJSON(writer, writer, 2*time.Second, "ping", &cursor, struct{}{}); err != nil {
		t.Fatal(err)
	}
	if got, want := writer.String(), "event: ping\nid: 42\ndata: {}\n\n"; got != want {
		t.Fatalf("frame = %q, want %q", got, want)
	}
	if writer.flushes != 1 {
		t.Fatalf("flushes = %d", writer.flushes)
	}
	if len(writer.deadlines) != 3 || !writer.deadlines[0].IsZero() ||
		writer.deadlines[1].IsZero() || !writer.deadlines[2].IsZero() {
		t.Fatalf("deadline sequence = %#v", writer.deadlines)
	}
	remaining := time.Until(writer.deadlines[1])
	if remaining <= 0 || remaining > 2*time.Second {
		t.Fatalf("rolling deadline remaining = %v", remaining)
	}
}

func TestSSEFrameDoesNotWriteWhenDeadlineCannotBeSet(t *testing.T) {
	writer := &deadlineBuffer{failNonZero: true}
	err := writeSSEJSON(writer, writer, time.Second, "ping", nil, struct{}{})
	if err == nil {
		t.Fatal("write succeeded without a write deadline")
	}
	if writer.Len() != 0 || writer.flushes != 0 {
		t.Fatalf("writer changed after deadline failure: %q, flushes=%d", writer.String(), writer.flushes)
	}
}

func TestSSEHeaderFlushFailureMarksRequestAuditFailed(t *testing.T) {
	gin.SetMode(gin.TestMode)
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	writer := &deadlineBuffer{failNonZero: true}
	if err := flushSSEHeadersForRequest(context, context.Writer, writer, time.Second); err == nil {
		t.Fatal("header flush succeeded without a write deadline")
	}
	if !RequestAuditFailureMarked(context) {
		t.Fatal("SSE header flush failure was not propagated to request audit")
	}
}

func TestSSESessionRevalidatorHook(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	want := errors.New("revalidate")
	AttachSSESessionRevalidator(c, func(context.Context) error { return want })
	revalidator := sseSessionRevalidator(c)
	if revalidator == nil || !errors.Is(revalidator(context.Background()), want) {
		t.Fatal("request-scoped SSE session revalidator was not retained")
	}
}

func TestSSEAuthExpiredEventContract(t *testing.T) {
	writer := &deadlineBuffer{}
	if err := writeSSEJSON(writer, writer, time.Second, "auth-expired", nil,
		map[string]string{"reason": "session_expired"}); err != nil {
		t.Fatal(err)
	}
	frame := writer.String()
	if !strings.Contains(frame, "event: auth-expired\n") ||
		!strings.Contains(frame, `data: {"reason":"session_expired"}`) {
		t.Fatalf("unexpected auth-expired frame: %q", frame)
	}
}

func TestSSEStreamErrorEventDoesNotUseReservedErrorName(t *testing.T) {
	writer := &deadlineBuffer{}
	if err := writeSSEJSON(writer, writer, time.Second, sseStreamErrorEvent, nil,
		map[string]string{"code": "upstream_error"}); err != nil {
		t.Fatal(err)
	}
	frame := writer.String()
	if !strings.Contains(frame, "event: stream-error\n") ||
		strings.Contains(frame, "event: error\n") ||
		!strings.Contains(frame, `data: {"code":"upstream_error"}`) {
		t.Fatalf("unexpected stream-error frame: %q", frame)
	}
}

func TestStreamJenkinsSSERevalidatesAndClosesExpiredSession(t *testing.T) {
	requestContext := context.Background()
	streamContext, cancel := context.WithTimeout(requestContext, time.Second)
	defer cancel()
	writer := &deadlineBuffer{}
	streamJenkinsSSE(
		requestContext,
		streamContext,
		writer,
		writer,
		7,
		nil,
		nil,
		func(context.Context) error { return ErrSSESessionExpired },
		sseStreamLimits{
			heartbeatInterval: 200 * time.Millisecond,
			reauthInterval:    5 * time.Millisecond,
			writeTimeout:      50 * time.Millisecond,
			idleTimeout:       500 * time.Millisecond,
		},
	)
	if got := writer.String(); !strings.Contains(got, "event: auth-expired\n") ||
		!strings.Contains(got, `data: {"reason":"session_expired"}`) {
		t.Fatalf("expired session did not close with auth-expired: %q", got)
	}
}

func TestStreamJenkinsSSEEnforcesMaximumDuration(t *testing.T) {
	requestContext := context.Background()
	streamContext, cancel := context.WithTimeout(requestContext, 25*time.Millisecond)
	defer cancel()
	writer := &deadlineBuffer{}
	streamJenkinsSSE(
		requestContext,
		streamContext,
		writer,
		writer,
		11,
		nil,
		nil,
		nil,
		sseStreamLimits{
			heartbeatInterval: 5 * time.Millisecond,
			reauthInterval:    10 * time.Millisecond,
			writeTimeout:      50 * time.Millisecond,
			idleTimeout:       500 * time.Millisecond,
		},
	)
	if got := writer.String(); !strings.Contains(got, `data: {"reason":"max_duration"}`) {
		t.Fatalf("maximum duration did not close stream: %q", got)
	}
}

func TestStreamJenkinsSSEClassifiesUpstreamIdleAsFailure(t *testing.T) {
	requestContext := context.Background()
	streamContext, cancel := context.WithTimeout(requestContext, time.Second)
	defer cancel()
	writer := &deadlineBuffer{}
	succeeded := streamJenkinsSSE(
		requestContext,
		streamContext,
		writer,
		writer,
		13,
		nil,
		nil,
		nil,
		sseStreamLimits{
			heartbeatInterval: time.Second,
			reauthInterval:    time.Second,
			writeTimeout:      50 * time.Millisecond,
			idleTimeout:       5 * time.Millisecond,
		},
	)
	if succeeded {
		t.Fatal("an upstream-idle stream was classified as successful")
	}
	if got := writer.String(); !strings.Contains(got, "id: 13\n") ||
		!strings.Contains(got, `data: {"reason":"upstream_idle"}`) {
		t.Fatalf("upstream-idle contract = %q", got)
	}
}

func TestStreamJenkinsSSERejectsCursorRegression(t *testing.T) {
	requestContext := context.Background()
	streamContext, cancel := context.WithTimeout(requestContext, time.Second)
	defer cancel()
	writer := &deadlineBuffer{}
	logs := make(chan jenkins.BuildLogChunk, 1)
	logs <- jenkins.BuildLogChunk{NextStart: 8, Lines: []string{"should not be sent"}}
	streamJenkinsSSE(
		requestContext,
		streamContext,
		writer,
		writer,
		9,
		logs,
		nil,
		nil,
		sseStreamLimits{
			heartbeatInterval: time.Second,
			reauthInterval:    time.Second,
			writeTimeout:      50 * time.Millisecond,
			idleTimeout:       time.Second,
		},
	)
	if got := writer.String(); !strings.Contains(got, "event: stream-error\n") ||
		!strings.Contains(got, `data: {"code":"cursor_regression"}`) ||
		strings.Contains(got, "should not be sent") {
		t.Fatalf("cursor regression handling = %q", got)
	}
}

func TestStreamJenkinsSSESanitizesUpstreamErrors(t *testing.T) {
	requestContext := context.Background()
	streamContext, cancel := context.WithTimeout(requestContext, time.Second)
	defer cancel()
	writer := &deadlineBuffer{}
	results := make(chan error, 1)
	results <- errors.New("upstream-secret-value")
	succeeded := streamJenkinsSSE(
		requestContext,
		streamContext,
		writer,
		writer,
		0,
		nil,
		results,
		nil,
		sseStreamLimits{
			heartbeatInterval: time.Second,
			reauthInterval:    time.Second,
			writeTimeout:      50 * time.Millisecond,
			idleTimeout:       time.Second,
		},
	)
	if succeeded {
		t.Fatal("upstream stream failure was classified as successful")
	}
	got := writer.String()
	if strings.Contains(got, "upstream-secret-value") ||
		!strings.Contains(got, "event: stream-error\n") ||
		!strings.Contains(got, `data: {"code":"upstream_error"}`) ||
		strings.Contains(got, `data: {"reason":"completed"}`) {
		t.Fatalf("upstream error contract = %q", got)
	}
}

type deadlineBuffer struct {
	bytes.Buffer
	deadlines   []time.Time
	flushes     int
	failNonZero bool
}

func (w *deadlineBuffer) SetWriteDeadline(deadline time.Time) error {
	w.deadlines = append(w.deadlines, deadline)
	if w.failNonZero && !deadline.IsZero() {
		return errors.New("deadline unavailable")
	}
	return nil
}

func (w *deadlineBuffer) Flush() error {
	w.flushes++
	return nil
}
