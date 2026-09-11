package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-ree/ares/internal/entity"
)

type memoryExecutionStore struct {
	mu          sync.Mutex
	release     ReleaseContext
	steps       []entity.TaskStepRecord
	taskStatus  string
	taskMessage string
}

func (m *memoryExecutionStore) CreateTaskSnapshot(context.Context, int, WorkflowView) error {
	return errors.New("not used")
}

func (m *memoryExecutionStore) GetTaskReleaseContext(context.Context, TaskLease) (ReleaseContext, error) {
	return m.release, nil
}

func (m *memoryExecutionStore) ListTaskSteps(context.Context, int) ([]entity.TaskStepRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rows := make([]entity.TaskStepRecord, len(m.steps))
	copy(rows, m.steps)
	return rows, nil
}

func (m *memoryExecutionStore) ListTaskStepsForLease(ctx context.Context, lease TaskLease) ([]entity.TaskStepRecord, error) {
	return m.ListTaskSteps(ctx, lease.TaskID)
}

func (m *memoryExecutionStore) StepTimeRemaining(_ context.Context, _ TaskLease, id int64, timeoutSeconds int) (time.Duration, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.steps {
		if m.steps[i].StepRecordID == id {
			if m.steps[i].StartedTime == nil {
				return time.Duration(timeoutSeconds) * time.Second, nil
			}
			remaining := time.Duration(timeoutSeconds)*time.Second - time.Since(*m.steps[i].StartedTime)
			if remaining < 0 {
				remaining = 0
			}
			return remaining, nil
		}
	}
	return 0, ErrNotFound
}

func (m *memoryExecutionStore) ClaimStep(_ context.Context, _ TaskLease, id int64) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.steps {
		if m.steps[i].StepRecordID == id && m.steps[i].Status == StepPending {
			now := time.Now()
			m.steps[i].Status = StepRunning
			m.steps[i].StartedTime = &now
			return true, nil
		}
	}
	return false, nil
}

func (m *memoryExecutionStore) ReleaseStep(_ context.Context, _ TaskLease, id int64, message string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.steps {
		if m.steps[i].StepRecordID == id && m.steps[i].Status == StepRunning && !hasJSONValue(m.steps[i].ExternalRef) {
			m.steps[i].Status = StepPending
			m.steps[i].StartedTime = nil
			m.steps[i].Message = message
			return true, nil
		}
	}
	return false, nil
}

func (m *memoryExecutionStore) SaveStepResult(_ context.Context, _ TaskLease, id int64, result Result) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.steps {
		if m.steps[i].StepRecordID != id || m.steps[i].Status != StepRunning {
			continue
		}
		status := result.State
		if status == ResultUnknown {
			status = StepRunning
		}
		m.steps[i].Status = status
		m.steps[i].Message = result.Message
		m.steps[i].Output = append(json.RawMessage(nil), result.Output...)
		m.steps[i].ExternalRef = append(json.RawMessage(nil), result.ExternalReference...)
		return true, nil
	}
	return false, nil
}

func (m *memoryExecutionStore) SkipPendingSteps(_ context.Context, _ TaskLease, message string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.steps {
		if m.steps[i].Status == StepPending {
			m.steps[i].Status = StepSkipped
			m.steps[i].Message = message
		}
	}
	return nil
}

func (m *memoryExecutionStore) SetTaskStatus(_ context.Context, _ TaskLease, status, message string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.taskStatus = status
	m.taskMessage = message
	return nil
}

func testLease(taskID int) TaskLease {
	return TaskLease{TaskID: taskID, Owner: "test-worker", FencingToken: 1}
}

func noopStep(id int64, key, outcome, onFailure string) entity.TaskStepRecord {
	config, _ := json.Marshal(map[string]any{"outcome": outcome})
	return entity.TaskStepRecord{
		StepRecordID:   id,
		StepKey:        key,
		Name:           key,
		Uses:           NoopUses,
		Position:       int(id - 1),
		Config:         config,
		TimeoutSeconds: 60,
		OnFailure:      onFailure,
		Status:         StepPending,
		Attempt:        1,
	}
}

func TestCoordinatorRunsArbitraryStepsAndContinuePolicy(t *testing.T) {
	store := &memoryExecutionStore{steps: []entity.TaskStepRecord{
		noopStep(1, "build", ResultSucceeded, FailureStop),
		noopStep(2, "verify", ResultFailed, FailureContinue),
		noopStep(3, "notify", ResultSucceeded, FailureStop),
	}}
	coordinator := NewCoordinator(store, DefaultRegistry())
	result, err := coordinator.RunUntilBlocked(context.Background(), testLease(8), 10)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Terminal || result.TaskStatus != TaskSucceededWithWarnings {
		t.Fatalf("result = %#v", result)
	}
	for _, step := range store.steps {
		if step.Status != StepSucceeded && step.Status != StepFailed {
			t.Fatalf("step %s status = %s", step.StepKey, step.Status)
		}
	}
}

func TestCoordinatorStopsAndSkipsRemainingSteps(t *testing.T) {
	store := &memoryExecutionStore{steps: []entity.TaskStepRecord{
		noopStep(1, "fail", ResultFailed, FailureStop),
		noopStep(2, "never", ResultSucceeded, FailureStop),
	}}
	result, err := NewCoordinator(store, DefaultRegistry()).RunUntilBlocked(context.Background(), testLease(9), 10)
	if err != nil {
		t.Fatal(err)
	}
	if result.TaskStatus != TaskFailed || store.steps[1].Status != StepSkipped {
		t.Fatalf("result=%#v steps=%#v", result, store.steps)
	}
}

type blockingExecutor struct {
	started chan struct{}
	release chan struct{}
	calls   atomic.Int32
}

type sensitiveOutputExecutor struct{}

func (sensitiveOutputExecutor) Descriptor() Descriptor {
	return Descriptor{Uses: "test.sensitive-output@v1", Name: "sensitive output", ConfigSchema: json.RawMessage(`{"type":"object"}`)}
}

type rawErrorExecutor struct{}

func (rawErrorExecutor) Descriptor() Descriptor {
	return Descriptor{Uses: "test.raw-error@v1", Name: "raw error", ConfigSchema: json.RawMessage(`{"type":"object"}`)}
}

type unavailableStartExecutor struct {
	available            atomic.Bool
	startUnavailable     atomic.Bool
	reconcileUnavailable atomic.Bool
	starts               atomic.Int32
}

func (e *unavailableStartExecutor) Descriptor() Descriptor {
	return Descriptor{Uses: "test.startup-loading@v1", Name: "startup loading", ConfigSchema: json.RawMessage(`{"type":"object"}`)}
}
func (e *unavailableStartExecutor) Validate(json.RawMessage) error { return nil }
func (e *unavailableStartExecutor) Available(context.Context) error {
	if !e.available.Load() {
		return errors.New("integration is still loading")
	}
	return nil
}
func (e *unavailableStartExecutor) Start(context.Context, StartRequest) (Result, error) {
	e.starts.Add(1)
	if e.startUnavailable.Load() {
		return Result{}, fmt.Errorf("%w: runtime changed before start", ErrExecutorUnavailable)
	}
	return Result{State: ResultSucceeded}, nil
}

func TestCoordinatorReleasesClaimWhenRuntimeDisappearsBeforeStart(t *testing.T) {
	executor := &unavailableStartExecutor{}
	executor.available.Store(true)
	executor.startUnavailable.Store(true)
	registry := NewRegistry()
	if err := registry.Register(executor); err != nil {
		t.Fatal(err)
	}
	store := &memoryExecutionStore{steps: []entity.TaskStepRecord{{
		StepRecordID: 1, StepKey: "jenkins", Name: "jenkins", Uses: "test.startup-loading@v1",
		Config: json.RawMessage(`{}`), TimeoutSeconds: 60, OnFailure: FailureStop,
		Status: StepPending, Attempt: 1,
	}}}
	coordinator := NewCoordinator(store, registry)
	blocked, err := coordinator.RunUntilBlocked(context.Background(), testLease(16), 10)
	if err != nil {
		t.Fatal(err)
	}
	if !blocked.Blocked || store.steps[0].Status != StepPending || executor.starts.Load() != 1 {
		t.Fatalf("blocked=%#v step=%#v starts=%d", blocked, store.steps[0], executor.starts.Load())
	}

	executor.startUnavailable.Store(false)
	completed, err := coordinator.RunUntilBlocked(context.Background(), testLease(16), 10)
	if err != nil {
		t.Fatal(err)
	}
	if !completed.Terminal || completed.TaskStatus != TaskSucceeded || executor.starts.Load() != 2 {
		t.Fatalf("completed=%#v starts=%d", completed, executor.starts.Load())
	}
}
func (e *unavailableStartExecutor) Reconcile(context.Context, ReconcileRequest) (Result, error) {
	if e.reconcileUnavailable.Load() {
		return Result{}, fmt.Errorf("%w: revision refresh failed", ErrExecutorUnavailable)
	}
	return Result{State: ResultSucceeded}, nil
}

func TestCoordinatorBacksOffRunningStepWhenExecutorIsUnavailable(t *testing.T) {
	executor := &unavailableStartExecutor{}
	executor.reconcileUnavailable.Store(true)
	registry := NewRegistry()
	if err := registry.Register(executor); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	store := &memoryExecutionStore{steps: []entity.TaskStepRecord{{
		StepRecordID: 1, StepKey: "jenkins", Name: "jenkins", Uses: "test.startup-loading@v1",
		Config: json.RawMessage(`{}`), TimeoutSeconds: 60, OnFailure: FailureStop,
		Status: StepRunning, Attempt: 1, StartedTime: &started,
		ExternalRef: json.RawMessage(`{"build_id":1}`),
	}}}
	result, err := NewCoordinator(store, registry).Advance(context.Background(), testLease(19))
	if err != nil {
		t.Fatal(err)
	}
	if !result.Blocked || !result.PollBackoff || result.Terminal || store.steps[0].Status != StepRunning {
		t.Fatalf("result=%#v step=%#v", result, store.steps[0])
	}
}

func TestCoordinatorLeavesPendingStepUnclaimedWhileIntegrationLoads(t *testing.T) {
	executor := &unavailableStartExecutor{}
	registry := NewRegistry()
	if err := registry.Register(executor); err != nil {
		t.Fatal(err)
	}
	store := &memoryExecutionStore{steps: []entity.TaskStepRecord{{
		StepRecordID: 1, StepKey: "jenkins", Name: "jenkins", Uses: "test.startup-loading@v1",
		Config: json.RawMessage(`{}`), TimeoutSeconds: 60, OnFailure: FailureStop,
		Status: StepPending, Attempt: 1,
	}}}
	coordinator := NewCoordinator(store, registry)
	blocked, err := coordinator.RunUntilBlocked(context.Background(), testLease(15), 10)
	if err != nil {
		t.Fatal(err)
	}
	if !blocked.Blocked || store.steps[0].Status != StepPending || executor.starts.Load() != 0 {
		t.Fatalf("blocked=%#v step=%#v starts=%d", blocked, store.steps[0], executor.starts.Load())
	}

	executor.available.Store(true)
	completed, err := coordinator.RunUntilBlocked(context.Background(), testLease(15), 10)
	if err != nil {
		t.Fatal(err)
	}
	if !completed.Terminal || completed.TaskStatus != TaskSucceeded || executor.starts.Load() != 1 {
		t.Fatalf("completed=%#v starts=%d", completed, executor.starts.Load())
	}
}
func (rawErrorExecutor) Validate(json.RawMessage) error { return nil }
func (rawErrorExecutor) Start(context.Context, StartRequest) (Result, error) {
	return Result{}, errors.New("https://internal-jenkins.example X-Error=must-not-persist")
}
func (rawErrorExecutor) Reconcile(context.Context, ReconcileRequest) (Result, error) {
	return Result{}, errors.New("must-not-persist")
}

func TestCoordinatorDoesNotPersistRawExecutorErrors(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register(rawErrorExecutor{}); err != nil {
		t.Fatal(err)
	}
	store := &memoryExecutionStore{steps: []entity.TaskStepRecord{{
		StepRecordID: 1, StepKey: "network", Name: "network", Uses: "test.raw-error@v1",
		Config: json.RawMessage(`{}`), TimeoutSeconds: 60, OnFailure: FailureStop,
		Status: StepPending, Attempt: 1,
	}}}
	result, err := NewCoordinator(store, registry).RunUntilBlocked(context.Background(), testLease(14), 10)
	if err != nil {
		t.Fatal(err)
	}
	if result.TaskStatus != TaskOutcomeUnknown || strings.Contains(store.steps[0].Message, "must-not-persist") {
		t.Fatalf("result=%#v message=%q", result, store.steps[0].Message)
	}
}

func TestCoordinatorDoesNotReturnOrLogRawReconcileErrors(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register(rawErrorExecutor{}); err != nil {
		t.Fatal(err)
	}
	store := &memoryExecutionStore{steps: []entity.TaskStepRecord{{
		StepRecordID: 1, StepKey: "network", Name: "network", Uses: "test.raw-error@v1",
		Config: json.RawMessage(`{}`), ExternalRef: json.RawMessage(`{"run_id":"42"}`),
		TimeoutSeconds: 60, OnFailure: FailureStop, Status: StepRunning, Attempt: 1,
	}}}
	result, err := NewCoordinator(store, registry).RunUntilBlocked(context.Background(), testLease(18), 10)
	if err != nil {
		t.Fatalf("raw reconcile error escaped coordinator: %v", err)
	}
	if result.TaskStatus != TaskRunning || !result.Blocked || !result.PollBackoff || strings.Contains(store.steps[0].Message, "must-not-persist") {
		t.Fatalf("result=%#v message=%q", result, store.steps[0].Message)
	}
}
func (sensitiveOutputExecutor) Validate(json.RawMessage) error { return nil }
func (sensitiveOutputExecutor) Start(context.Context, StartRequest) (Result, error) {
	return Result{State: ResultSucceeded, Output: json.RawMessage(`{"clientSecret":"must-not-persist"}`)}, nil
}
func (sensitiveOutputExecutor) Reconcile(context.Context, ReconcileRequest) (Result, error) {
	return Result{State: ResultSucceeded, Output: json.RawMessage(`{"clientSecret":"must-not-persist"}`)}, nil
}

func TestCoordinatorRejectsSensitiveExecutorOutputBeforePersistence(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register(sensitiveOutputExecutor{}); err != nil {
		t.Fatal(err)
	}
	store := &memoryExecutionStore{steps: []entity.TaskStepRecord{{
		StepRecordID: 1, StepKey: "unsafe", Name: "unsafe", Uses: "test.sensitive-output@v1",
		Config: json.RawMessage(`{}`), TimeoutSeconds: 60, OnFailure: FailureStop,
		Status: StepPending, Attempt: 1,
	}}}
	result, err := NewCoordinator(store, registry).RunUntilBlocked(context.Background(), testLease(13), 10)
	if err != nil {
		t.Fatal(err)
	}
	if result.TaskStatus != TaskFailed || store.steps[0].Status != StepFailed {
		t.Fatalf("result=%#v step=%#v", result, store.steps[0])
	}
	if len(store.steps[0].Output) != 0 {
		t.Fatalf("sensitive output persisted: %s", store.steps[0].Output)
	}
	if store.steps[0].Message == "" || strings.Contains(store.steps[0].Message, "must-not-persist") {
		t.Fatalf("unexpected failure message %q", store.steps[0].Message)
	}
}

func TestCoordinatorPreservesExternalReferenceWhenExecutorReturnsInvalidState(t *testing.T) {
	reference := json.RawMessage(`{"provider":"test","run_id":"42"}`)
	store := &memoryExecutionStore{steps: []entity.TaskStepRecord{{
		StepRecordID: 1, StepKey: "invalid", Name: "invalid", Uses: NoopUses,
		Status: StepRunning, OnFailure: FailureStop,
	}}}
	result, err := NewCoordinator(store, DefaultRegistry()).applyResult(context.Background(), testLease(17), store.steps[0], Result{
		State:             "not-a-valid-state",
		Output:            json.RawMessage(`{"discarded":true}`),
		ExternalReference: reference,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.TaskStatus != TaskOutcomeUnknown || store.steps[0].Status != StepOutcomeUnknown {
		t.Fatalf("result=%#v step=%#v", result, store.steps[0])
	}
	if string(store.steps[0].ExternalRef) != string(reference) {
		t.Fatalf("external reference = %s, want %s", store.steps[0].ExternalRef, reference)
	}
	if len(store.steps[0].Output) != 0 {
		t.Fatalf("invalid executor output persisted: %s", store.steps[0].Output)
	}
}

type deadlineIgnoringResultExecutor struct {
	starts     atomic.Int32
	reconciles atomic.Int32
}

func (e *deadlineIgnoringResultExecutor) Descriptor() Descriptor {
	return Descriptor{
		Uses: "test.deadline-result@v1", Name: "deadline result",
		ConfigSchema: json.RawMessage(`{"type":"object"}`),
	}
}

func (e *deadlineIgnoringResultExecutor) Validate(json.RawMessage) error { return nil }

func (e *deadlineIgnoringResultExecutor) Start(ctx context.Context, _ StartRequest) (Result, error) {
	e.starts.Add(1)
	<-ctx.Done()
	// A broken adapter may report success after its deadline. The coordinator
	// must not persist that late result.
	return Result{State: ResultSucceeded, Message: "late start success", ExternalReference: json.RawMessage(`{"run_id":"late"}`), Output: json.RawMessage(`{"late":true}`)}, nil
}

func (e *deadlineIgnoringResultExecutor) Reconcile(ctx context.Context, _ ReconcileRequest) (Result, error) {
	e.reconciles.Add(1)
	<-ctx.Done()
	return Result{State: ResultSucceeded, Message: "late reconcile success", ExternalReference: json.RawMessage(`{"run_id":"late"}`)}, nil
}

func TestCoordinatorRejectsSuccessfulResultsReturnedAfterStepDeadline(t *testing.T) {
	for _, test := range []struct {
		name string
		step entity.TaskStepRecord
	}{
		{
			name: "start",
			step: entity.TaskStepRecord{
				StepRecordID: 1, StepKey: "deadline", Name: "deadline", Uses: "test.deadline-result@v1",
				Config: json.RawMessage(`{}`), TimeoutSeconds: 1, OnFailure: FailureStop,
				Status: StepPending, Attempt: 1,
			},
		},
		{
			name: "reconcile uses database remaining time",
			step: func() entity.TaskStepRecord {
				started := time.Now().Add(-900 * time.Millisecond)
				return entity.TaskStepRecord{
					StepRecordID: 1, StepKey: "deadline", Name: "deadline", Uses: "test.deadline-result@v1",
					Config: json.RawMessage(`{}`), TimeoutSeconds: 1, OnFailure: FailureStop,
					Status: StepRunning, Attempt: 1, StartedTime: &started,
					ExternalRef: json.RawMessage(`{"run_id":"42"}`),
				}
			}(),
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			executor := &deadlineIgnoringResultExecutor{}
			if test.step.StartedTime != nil {
				started := time.Now().Add(-900 * time.Millisecond)
				test.step.StartedTime = &started
			}
			registry := NewRegistry()
			if err := registry.Register(executor); err != nil {
				t.Fatal(err)
			}
			store := &memoryExecutionStore{steps: []entity.TaskStepRecord{test.step}}
			result, err := NewCoordinator(store, registry).RunUntilBlocked(context.Background(), testLease(27), 10)
			if err != nil {
				t.Fatal(err)
			}
			if !result.Terminal || result.TaskStatus != TaskTimedOut || store.steps[0].Status != StepTimedOut {
				t.Fatalf("late deadline result persisted: result=%#v step=%#v", result, store.steps[0])
			}
			if strings.Contains(store.steps[0].Message, "late") {
				t.Fatalf("late executor message persisted: %q", store.steps[0].Message)
			}
			if string(store.steps[0].ExternalRef) != `{"run_id":"late"}` || len(store.steps[0].Output) != 0 {
				t.Fatalf("late reference lost or output accepted: %#v", store.steps[0])
			}
		})
	}
}

func (b *blockingExecutor) Descriptor() Descriptor {
	return Descriptor{Uses: "test.blocking@v1", Name: "blocking", ConfigSchema: json.RawMessage(`{"type":"object"}`)}
}
func (b *blockingExecutor) Validate(json.RawMessage) error { return nil }
func (b *blockingExecutor) Start(context.Context, StartRequest) (Result, error) {
	if b.calls.Add(1) == 1 {
		close(b.started)
	}
	<-b.release
	return Result{State: ResultSucceeded}, nil
}
func (b *blockingExecutor) Reconcile(context.Context, ReconcileRequest) (Result, error) {
	return Result{State: ResultRunning}, nil
}

func TestCoordinatorCASStartsStepOnce(t *testing.T) {
	executor := &blockingExecutor{started: make(chan struct{}), release: make(chan struct{})}
	registry := NewRegistry()
	if err := registry.Register(executor); err != nil {
		t.Fatal(err)
	}
	store := &memoryExecutionStore{steps: []entity.TaskStepRecord{{
		StepRecordID: 1, StepKey: "once", Name: "once", Uses: "test.blocking@v1",
		Config: json.RawMessage(`{}`), TimeoutSeconds: 60, OnFailure: FailureStop,
		Status: StepPending, Attempt: 1,
	}}}
	coordinator := NewCoordinator(store, registry)
	firstDone := make(chan error, 1)
	go func() {
		_, err := coordinator.Advance(context.Background(), testLease(10))
		firstDone <- err
	}()
	<-executor.started
	second, err := coordinator.Advance(context.Background(), testLease(10))
	if err != nil {
		t.Fatal(err)
	}
	if !second.Blocked {
		t.Fatalf("second result = %#v", second)
	}
	close(executor.release)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	if calls := executor.calls.Load(); calls != 1 {
		t.Fatalf("Start calls = %d", calls)
	}
}

func TestHasJSONValueTreatsBlankAndNullAsEmpty(t *testing.T) {
	for _, value := range []json.RawMessage{nil, {}, json.RawMessage("   "), json.RawMessage("null"), json.RawMessage(" \n null\t")} {
		if hasJSONValue(value) {
			t.Errorf("hasJSONValue(%q) = true, want false", value)
		}
	}
	for _, value := range []json.RawMessage{json.RawMessage(`{}`), json.RawMessage(`[]`), json.RawMessage(`0`), json.RawMessage(`false`)} {
		if !hasJSONValue(value) {
			t.Errorf("hasJSONValue(%q) = false, want true", value)
		}
	}
}

func TestCoordinatorDoesNotReconcileJSONNullReference(t *testing.T) {
	store := &memoryExecutionStore{steps: []entity.TaskStepRecord{{
		StepRecordID:   1,
		TaskID:         12,
		StepKey:        "waiting-for-start",
		Name:           "waiting-for-start",
		Uses:           NoopUses,
		Status:         StepRunning,
		TimeoutSeconds: 60,
		ExternalRef:    json.RawMessage(" \n null\t"),
	}}}
	result, err := NewCoordinator(store, DefaultRegistry()).Advance(context.Background(), testLease(12))
	if err != nil {
		t.Fatal(err)
	}
	if !result.Blocked || result.StepStatus != StepRunning {
		t.Fatalf("Advance() = %#v, want a blocked running step", result)
	}
}
