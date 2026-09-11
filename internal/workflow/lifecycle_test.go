package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/go-ree/ares/internal/entity"
)

type lifecycleExecutor struct {
	starts, polls int
	result        Result
	err           error
}

func (e *lifecycleExecutor) Descriptor() Descriptor {
	return Descriptor{Uses: "test.lifecycle@v1", Name: "lifecycle", ConfigSchema: json.RawMessage(`{"type":"object"}`)}
}
func (e *lifecycleExecutor) Validate(json.RawMessage) error { return nil }
func (e *lifecycleExecutor) Start(context.Context, StartRequest) (Result, error) {
	e.starts++
	return e.result, e.err
}
func (e *lifecycleExecutor) Reconcile(context.Context, ReconcileRequest) (Result, error) {
	e.polls++
	return e.result, e.err
}

func lifecycleCoordinator(t *testing.T, store LeasedExecutionStore, executor *lifecycleExecutor) *Coordinator {
	t.Helper()
	r := NewRegistry()
	if err := r.Register(executor); err != nil {
		t.Fatal(err)
	}
	return NewCoordinator(store, r)
}

func lifecycleStep(status string) entity.TaskStepRecord {
	step := noopStep(1, "build", ResultSucceeded, FailureContinue)
	step.Uses = "test.lifecycle@v1"
	step.Status = status
	step.ExternalRef = json.RawMessage(`{"run_id":"42"}`)
	return step
}

func TestUncertainTerminalStopsContinueAndRecoversAfterRestart(t *testing.T) {
	for _, status := range []string{StepTimedOut, StepOutcomeUnknown} {
		t.Run(status, func(t *testing.T) {
			store := &memoryExecutionStore{steps: []entity.TaskStepRecord{lifecycleStep(StepRunning), noopStep(2, "deploy", ResultSucceeded, FailureStop)}}
			executor := &lifecycleExecutor{result: Result{State: status}}
			result, err := lifecycleCoordinator(t, store, executor).Advance(context.Background(), testLease(1))
			if err != nil || !result.Terminal || result.TaskStatus != status || store.steps[1].Status != StepSkipped {
				t.Fatalf("result=%+v err=%v steps=%+v", result, err, store.steps)
			}
			if string(store.steps[0].ExternalRef) != `{"run_id":"42"}` {
				t.Fatal("lost external reference")
			}
			// Simulate a crash between persisting the terminal step and updating the task.
			store.taskStatus = TaskRunning
			store.steps[1].Status = StepPending
			result, err = lifecycleCoordinator(t, store, executor).Advance(context.Background(), testLease(1))
			if err != nil || !result.Terminal || store.taskStatus != status || store.steps[1].Status != StepSkipped || executor.polls != 1 || executor.starts != 0 {
				t.Fatalf("recovery=%+v err=%v calls=%+v", result, err, executor)
			}
		})
	}
}

func TestReconcileErrorKeepsAttemptAndRecovers(t *testing.T) {
	store := &memoryExecutionStore{steps: []entity.TaskStepRecord{lifecycleStep(StepRunning)}}
	executor := &lifecycleExecutor{result: Result{ExternalReference: json.RawMessage(`{"run_id":"resolved"}`)}, err: errors.New("upstream private error")}
	c := lifecycleCoordinator(t, store, executor)
	result, err := c.Advance(context.Background(), testLease(1))
	if err != nil || !result.Blocked || !result.PollBackoff || store.steps[0].Status != StepRunning || string(store.steps[0].ExternalRef) != `{"run_id":"resolved"}` {
		t.Fatalf("result=%+v err=%v step=%+v", result, err, store.steps[0])
	}
	executor.err = nil
	executor.result = Result{State: ResultSucceeded}
	result, err = c.RunUntilBlocked(context.Background(), testLease(1), 3)
	if err != nil || result.TaskStatus != TaskSucceeded || executor.starts != 0 || executor.polls != 2 || store.steps[0].Attempt != 1 {
		t.Fatalf("recovery=%+v err=%v calls=%+v", result, err, executor)
	}
}

type remainingBudgetStore struct {
	*memoryExecutionStore
	remaining time.Duration
}

func (s *remainingBudgetStore) StepTimeRemaining(context.Context, TaskLease, int64, int) (time.Duration, error) {
	return s.remaining, nil
}

func TestExpiredBudgetNeverCallsExecutorOrContinues(t *testing.T) {
	for _, status := range []string{StepPending, StepRunning} {
		for _, reference := range []json.RawMessage{nil, json.RawMessage(`{"run_id":"42"}`)} {
			step := lifecycleStep(status)
			step.ExternalRef = reference
			store := &remainingBudgetStore{memoryExecutionStore: &memoryExecutionStore{steps: []entity.TaskStepRecord{step, noopStep(2, "deploy", ResultSucceeded, FailureStop)}}}
			executor := &lifecycleExecutor{}
			result, err := lifecycleCoordinator(t, store, executor).Advance(context.Background(), testLease(1))
			if err != nil || result.TaskStatus != TaskTimedOut || executor.starts != 0 || executor.polls != 0 || store.steps[1].Status != StepSkipped {
				t.Fatalf("status=%s result=%+v err=%v", status, result, err)
			}
		}
	}
}

func TestStartErrorCannotReleaseClaimWhenReferenceExists(t *testing.T) {
	for _, startErr := range []error{errors.New("connection interrupted"), ErrExecutorUnavailable} {
		step := lifecycleStep(StepPending)
		step.ExternalRef = nil
		store := &memoryExecutionStore{steps: []entity.TaskStepRecord{step}}
		executor := &lifecycleExecutor{result: Result{ExternalReference: json.RawMessage(`{"run_id":"created"}`)}, err: startErr}
		result, err := lifecycleCoordinator(t, store, executor).Advance(context.Background(), testLease(1))
		if err != nil || result.TaskStatus != TaskOutcomeUnknown || string(store.steps[0].ExternalRef) != `{"run_id":"created"}` || executor.starts != 1 {
			t.Fatalf("result=%+v err=%v step=%+v", result, err, store.steps[0])
		}
	}
}

func TestUnsafeOutputCannotDowngradeUncertainStateToContinueFailure(t *testing.T) {
	for _, state := range []string{ResultRunning, ResultUnknown, ResultOutcomeUnknown, ResultTimedOut} {
		store := &memoryExecutionStore{steps: []entity.TaskStepRecord{lifecycleStep(StepRunning), noopStep(2, "deploy", ResultSucceeded, FailureStop)}}
		executor := &lifecycleExecutor{result: Result{State: state, Output: json.RawMessage(`{"password":"private"}`)}}
		result, err := lifecycleCoordinator(t, store, executor).Advance(context.Background(), testLease(1))
		if err != nil || !result.Terminal || !isUncertainTerminal(result.TaskStatus) || store.steps[1].Status != StepSkipped || len(store.steps[0].Output) != 0 {
			t.Fatalf("state=%s result=%+v err=%v", state, result, err)
		}
	}
}

func TestParentCancellationDoesNotBecomeBusinessTimeout(t *testing.T) {
	for _, status := range []string{StepPending, StepRunning} {
		step := lifecycleStep(status)
		step.Uses = "test.deadline-result@v1"
		store := &memoryExecutionStore{steps: []entity.TaskStepRecord{step}}
		registry := NewRegistry()
		if err := registry.Register(&deadlineIgnoringResultExecutor{}); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := NewCoordinator(store, registry).Advance(ctx, testLease(1))
		if !errors.Is(err, context.Canceled) || store.steps[0].Status != StepRunning {
			t.Fatalf("status=%s err=%v step=%+v", status, err, store.steps[0])
		}
	}
}
