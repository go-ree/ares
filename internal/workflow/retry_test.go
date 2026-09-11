package workflow

import (
	"encoding/json"
	"testing"

	"github.com/go-ree/ares/internal/entity"
)

func TestRetryPolicyValidationAndDefaults(t *testing.T) {
	for _, test := range []struct {
		raw   string
		valid bool
	}{
		{`{"max_attempts":3}`, true}, {`{"max_attempts":1,"mode":"manual"}`, true},
		{`{"max_attempts":0}`, false}, {`{"max_attempts":6}`, false},
		{`{"max_attempts":3,"initial_delay_seconds":-1}`, false},
		{`{"max_attempts":3,"initial_delay_seconds":5,"max_delay_seconds":2}`, false},
		{`{"max_attempts":3,"max_delay_seconds":3601}`, false},
		{`{"max_attempts":3,"mode":"unbounded"}`, false},
	} {
		var policy RetryPolicy
		if err := json.Unmarshal([]byte(test.raw), &policy); err != nil {
			t.Fatal(err)
		}
		spec := WorkflowSpec{SchemaVersion: 1, Name: "retry", Steps: []StepSpec{{Key: "build", Name: "Build", Uses: NoopUses, Retry: &policy}}}
		_, err := NormalizeAndValidate(spec, DefaultRegistry())
		if (err == nil) != test.valid {
			t.Fatalf("policy=%s err=%v", test.raw, err)
		}
	}
	spec := WorkflowSpec{SchemaVersion: 1, Name: "legacy", Steps: []StepSpec{{Key: "build", Name: "Build", Uses: NoopUses}}}
	normalized, err := NormalizeAndValidate(spec, DefaultRegistry())
	if err != nil || normalized.Steps[0].Retry != nil {
		t.Fatal("legacy spec gained retry policy")
	}
	spec.Steps[0].Retry = &RetryPolicy{MaxAttempts: 3, Mode: "manual"}
	spec.Steps[0].OnFailure = FailureContinue
	if _, err := NormalizeAndValidate(spec, DefaultRegistry()); err == nil {
		t.Fatal("manual continue accepted")
	}
}

func TestRetryClassificationRejectsUnknownTimeoutAndUnqualifiedFailures(t *testing.T) {
	c := NewCoordinator(nil, DefaultRegistry())
	for _, state := range []string{ResultRunning, ResultUnknown, ResultOutcomeUnknown, ResultTimedOut, ResultSucceeded, ResultCancelled} {
		result := Result{State: state, RetryClass: RetryCompletedSafe}
		c.classifyRetry(NoopUses, &result)
		if result.RetryClass != "" {
			t.Fatalf("unsafe state=%s", state)
		}
	}
	result := Result{State: ResultFailed, RetryClass: RetryNoSideEffect}
	c.classifyRetry("missing.executor@v1", &result)
	if result.RetryClass != "" {
		t.Fatal("unqualified executor accepted")
	}
	for attempt, want := range []int{2, 4, 5, 5} {
		if got := retryDelay(entity.TaskStepRecord{Attempt: attempt + 1, RetryDelaySeconds: 2, RetryMaxDelaySeconds: 5}); got != want {
			t.Fatalf("delay=%d want=%d", got, want)
		}
	}
}
