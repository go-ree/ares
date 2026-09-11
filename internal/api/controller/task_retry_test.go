package controller

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/go-ree/ares/internal/entity"
	"github.com/go-ree/ares/internal/workflow"
)

type retryControllerStore struct {
	workflow.LeasedExecutionStore
	calls    int
	expected int
	err      error
}

func (s *retryControllerStore) ListTaskSteps(context.Context, int) ([]entity.TaskStepRecord, error) {
	return []entity.TaskStepRecord{{StepKey: "build", Uses: workflow.NoopUses}}, nil
}
func (s *retryControllerStore) RequestRetry(_ context.Context, taskID int, key string, expected int, uses string) error {
	s.calls++
	s.expected = expected
	return s.err
}
func (s *retryControllerStore) AdvanceRetry(context.Context, workflow.TaskLease, int64) (string, error) {
	return "", nil
}
func (s *retryControllerStore) ListAttempts(context.Context, int, string) ([]workflow.AttemptView, error) {
	return []workflow.AttemptView{{Attempt: 1, Status: workflow.StepFailed}}, nil
}

func TestRetryControllerStrictRequestAndConflict(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, test := range []struct {
		body     string
		status   int
		conflict bool
	}{
		{`{"expected_attempt":1}`, http.StatusAccepted, false},
		{`{"expected_attempt":1}`, http.StatusConflict, true},
		{`{"expected_attempt":0}`, http.StatusBadRequest, false},
		{`{"expected_attempt":5}`, http.StatusBadRequest, false},
		{`{"expected_attempt":1,"force":true}`, http.StatusBadRequest, false},
		{`{"expected_attempt":1,"expected_attempt":2}`, http.StatusBadRequest, false},
	} {
		store := &retryControllerStore{}
		if test.conflict {
			store.err = workflow.ErrRetryConflict
		}
		wc := NewWorkflowController(nil, workflow.NewCoordinator(store, workflow.DefaultRegistry()))
		router := gin.New()
		router.POST("/tasks/:task_id/steps/:step_key/retry", wc.RetryTaskStep)
		response := httptest.NewRecorder()
		request := httptest.NewRequest("POST", "/tasks/7/steps/build/retry", strings.NewReader(test.body))
		request.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(response, request)
		if response.Code != test.status {
			t.Fatalf("body=%s code=%d response=%s", test.body, response.Code, response.Body.String())
		}
		if test.status == http.StatusBadRequest && store.calls != 0 {
			t.Fatal("invalid request reached store")
		}
	}
}

func TestAttemptQueryRejectsAmbiguousNumbers(t *testing.T) {
	for _, query := range []string{"attempt=0", "attempt=01", "attempt=-1", "attempt=1&attempt=2", "attempt=1.5", "attempt=", "attempt=999999999999999999999"} {
		if _, err := taskStepLogCursor(query, http.Header{}); err == nil {
			t.Fatalf("accepted %s", query)
		}
	}
	if cursor, err := taskStepLogCursor("attempt=2&cursor=abc", http.Header{}); err != nil || cursor != "abc" {
		t.Fatalf("cursor=%s err=%v", cursor, err)
	}
}
