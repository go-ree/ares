package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-ree/ares/internal/entity"
	"github.com/go-ree/ares/internal/job"
)

type workerAcquireCall struct {
	Owner         string
	Limit         int
	LeaseDuration time.Duration
}

type workerRenewCall struct {
	Lease         TaskLease
	LeaseDuration time.Duration
}

type workerReleaseCall struct {
	Lease      TaskLease
	Delay      time.Duration
	PollFailed bool
}

type workerStatusCall struct {
	Lease   TaskLease
	Status  string
	Message string
}

// workerTestStore is deliberately strict about recording the scheduling
// boundary. Coordinator-only operations have harmless defaults and can be
// overridden where a test needs to hold an in-flight task or inject a fault.
type workerTestStore struct {
	mu sync.Mutex

	acquireCalls []workerAcquireCall
	renewCalls   []workerRenewCall
	releaseCalls []workerReleaseCall
	statusCalls  []workerStatusCall

	acquireFn func(context.Context, string, int, time.Duration, int) ([]TaskLease, error)
	renewFn   func(context.Context, TaskLease, time.Duration) error
	releaseFn func(context.Context, TaskLease, time.Duration, bool) error
	listFn    func(context.Context, TaskLease) ([]entity.TaskStepRecord, error)
}

func (s *workerTestStore) CreateTaskSnapshot(context.Context, int, WorkflowView) error {
	return nil
}

func (s *workerTestStore) ListTaskSteps(context.Context, int) ([]entity.TaskStepRecord, error) {
	return nil, nil
}

func (s *workerTestStore) GetTaskReleaseContext(context.Context, TaskLease) (ReleaseContext, error) {
	return ReleaseContext{}, nil
}

func (s *workerTestStore) ListTaskStepsForLease(ctx context.Context, lease TaskLease) ([]entity.TaskStepRecord, error) {
	if s.listFn != nil {
		return s.listFn(ctx, lease)
	}
	return terminalWorkerSteps(lease.TaskID), nil
}

func (s *workerTestStore) StepTimeRemaining(_ context.Context, _ TaskLease, _ int64, timeoutSeconds int) (time.Duration, error) {
	return time.Duration(timeoutSeconds) * time.Second, nil
}

func (s *workerTestStore) ClaimStep(context.Context, TaskLease, int64) (bool, error) {
	return true, nil
}

func (s *workerTestStore) ReleaseStep(context.Context, TaskLease, int64, string) (bool, error) {
	return true, nil
}

func (s *workerTestStore) SaveStepResult(context.Context, TaskLease, int64, Result) (bool, error) {
	return true, nil
}

func (s *workerTestStore) SkipPendingSteps(context.Context, TaskLease, string) error {
	return nil
}

func (s *workerTestStore) SetTaskStatus(_ context.Context, lease TaskLease, status, message string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.statusCalls = append(s.statusCalls, workerStatusCall{Lease: lease, Status: status, Message: message})
	return nil
}

func (s *workerTestStore) AcquireTaskLeases(
	ctx context.Context,
	owner string,
	limit int,
	leaseDuration time.Duration,
) ([]TaskLease, error) {
	s.mu.Lock()
	s.acquireCalls = append(s.acquireCalls, workerAcquireCall{
		Owner: owner, Limit: limit, LeaseDuration: leaseDuration,
	})
	callNumber := len(s.acquireCalls)
	acquireFn := s.acquireFn
	s.mu.Unlock()
	if acquireFn == nil {
		return nil, nil
	}
	return acquireFn(ctx, owner, limit, leaseDuration, callNumber)
}

func (s *workerTestStore) RenewTaskLease(ctx context.Context, lease TaskLease, leaseDuration time.Duration) error {
	s.mu.Lock()
	s.renewCalls = append(s.renewCalls, workerRenewCall{Lease: lease, LeaseDuration: leaseDuration})
	renewFn := s.renewFn
	s.mu.Unlock()
	if renewFn == nil {
		return nil
	}
	return renewFn(ctx, lease, leaseDuration)
}

func (s *workerTestStore) ReleaseTaskLease(
	ctx context.Context,
	lease TaskLease,
	delay time.Duration,
	pollFailed bool,
) error {
	s.mu.Lock()
	s.releaseCalls = append(s.releaseCalls, workerReleaseCall{
		Lease: lease, Delay: delay, PollFailed: pollFailed,
	})
	releaseFn := s.releaseFn
	s.mu.Unlock()
	if releaseFn == nil {
		return nil
	}
	return releaseFn(ctx, lease, delay, pollFailed)
}

func (s *workerTestStore) acquireSnapshot() []workerAcquireCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]workerAcquireCall(nil), s.acquireCalls...)
}

func (s *workerTestStore) renewSnapshot() []workerRenewCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]workerRenewCall(nil), s.renewCalls...)
}

func (s *workerTestStore) releaseSnapshot() []workerReleaseCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]workerReleaseCall(nil), s.releaseCalls...)
}

func (s *workerTestStore) statusSnapshot() []workerStatusCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]workerStatusCall(nil), s.statusCalls...)
}

func validWorkerTestOptions() WorkerOptions {
	return WorkerOptions{
		Owner:              "worker-test",
		Concurrency:        2,
		ClaimBatchSize:     2,
		ScanInterval:       5 * time.Millisecond,
		LeaseDuration:      300 * time.Millisecond,
		RenewInterval:      50 * time.Millisecond,
		NormalPollInterval: 40 * time.Millisecond,
		BackoffMin:         8 * time.Millisecond,
		BackoffMax:         128 * time.Millisecond,
		DrainTimeout:       300 * time.Millisecond,
		MaxTransitions:     10,
		Jitter:             func(time.Duration) time.Duration { return 0 },
	}
}

func newWorkerUnderTest(t *testing.T, store *workerTestStore, options WorkerOptions, registry *Registry) *Worker {
	t.Helper()
	if registry == nil {
		registry = NewRegistry()
	}
	worker, err := NewWorker(store, NewCoordinator(store, registry), options)
	if err != nil {
		t.Fatalf("NewWorker() error = %v", err)
	}
	return worker
}

func terminalWorkerSteps(taskID int) []entity.TaskStepRecord {
	return []entity.TaskStepRecord{{
		StepRecordID: 1,
		TaskID:       taskID,
		StepKey:      "done",
		Name:         "done",
		Uses:         NoopUses,
		Position:     1,
		OnFailure:    FailureStop,
		Status:       StepSucceeded,
	}}
}

func blockedWorkerSteps(taskID int) []entity.TaskStepRecord {
	return []entity.TaskStepRecord{{
		StepRecordID:   1,
		TaskID:         taskID,
		StepKey:        "waiting",
		Name:           "waiting",
		Uses:           NoopUses,
		Position:       1,
		TimeoutSeconds: 3600,
		OnFailure:      FailureStop,
		Status:         StepRunning,
	}}
}

func waitWorkerSignal[T any](t *testing.T, signal <-chan T, description string) T {
	t.Helper()
	select {
	case value := <-signal:
		return value
	case <-time.After(2 * time.Second):
		t.Fatalf("等待%s超时", description)
		var zero T
		return zero
	}
}

func TestWorkerClaimsOnlyFreeSlotsWithoutPrefetch(t *testing.T) {
	store := &workerTestStore{}
	gate := make(chan struct{})
	started := make(chan int, 2)
	var running atomic.Int32
	var maximum atomic.Int32
	store.acquireFn = func(_ context.Context, owner string, _ int, _ time.Duration, call int) ([]TaskLease, error) {
		if call > 2 {
			return nil, nil
		}
		return []TaskLease{{TaskID: call, Owner: owner, FencingToken: uint64(call)}}, nil
	}
	store.listFn = func(ctx context.Context, lease TaskLease) ([]entity.TaskStepRecord, error) {
		current := running.Add(1)
		defer running.Add(-1)
		for {
			observed := maximum.Load()
			if current <= observed || maximum.CompareAndSwap(observed, current) {
				break
			}
		}
		started <- lease.TaskID
		select {
		case <-gate:
			return terminalWorkerSteps(lease.TaskID), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	options := validWorkerTestOptions()
	worker := newWorkerUnderTest(t, store, options, nil)
	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- worker.Run(ctx) }()

	first := waitWorkerSignal(t, started, "第一个任务启动")
	second := waitWorkerSignal(t, started, "第二个任务启动")
	if first == second {
		t.Fatalf("启动了重复任务 %d", first)
	}
	// With both slots occupied, the worker waits for a completion signal rather
	// than acquiring work into a process-local queue.
	time.Sleep(4 * options.ScanInterval)
	calls := store.acquireSnapshot()
	if len(calls) != 2 {
		t.Fatalf("AcquireTaskLeases() calls = %d, want 2 (no prefetch)", len(calls))
	}
	if calls[0].Limit != 2 || calls[1].Limit != 1 {
		t.Fatalf("acquire limits = [%d %d], want [2 1]", calls[0].Limit, calls[1].Limit)
	}
	if got := maximum.Load(); got != 2 {
		t.Fatalf("maximum concurrent tasks = %d, want 2", got)
	}

	cancel()
	close(gate)
	if err := waitWorkerSignal(t, runDone, "worker 排空"); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
}

func TestWorkerReleasesNewGenerationWhenSameTaskIsStillRunningLocally(t *testing.T) {
	store := &workerTestStore{}
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	var startedOnce sync.Once
	store.acquireFn = func(_ context.Context, owner string, _ int, _ time.Duration, call int) ([]TaskLease, error) {
		switch call {
		case 1:
			return []TaskLease{{TaskID: 7, Owner: owner, FencingToken: 1}}, nil
		case 2:
			// Models the first generation expiring while an executor ignores its
			// cancelled context and this process has another free slot.
			return []TaskLease{{TaskID: 7, Owner: owner, FencingToken: 2}}, nil
		default:
			return nil, nil
		}
	}
	store.listFn = func(ctx context.Context, lease TaskLease) ([]entity.TaskStepRecord, error) {
		if lease.FencingToken != 1 {
			t.Fatalf("duplicate generation unexpectedly started: %#v", lease)
		}
		startedOnce.Do(func() { close(firstStarted) })
		select {
		case <-releaseFirst:
			return terminalWorkerSteps(lease.TaskID), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	options := validWorkerTestOptions()
	options.Concurrency = 2
	options.ClaimBatchSize = 1
	worker := newWorkerUnderTest(t, store, options, nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()
	waitWorkerSignal(t, firstStarted, "first local generation")

	deadline := time.Now().Add(time.Second)
	for len(store.releaseSnapshot()) == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	releases := store.releaseSnapshot()
	if len(releases) != 1 || releases[0].Lease.FencingToken != 2 || releases[0].Lease.TaskID != 7 {
		t.Fatalf("duplicate-generation releases = %#v, want generation two", releases)
	}

	cancel()
	close(releaseFirst)
	if err := waitWorkerSignal(t, done, "worker shutdown"); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
}

func TestWorkerNormalPollReleaseResetsFailureState(t *testing.T) {
	store := &workerTestStore{}
	store.listFn = func(_ context.Context, lease TaskLease) ([]entity.TaskStepRecord, error) {
		return blockedWorkerSteps(lease.TaskID), nil
	}
	options := validWorkerTestOptions()
	options.Jitter = func(upperBound time.Duration) time.Duration { return upperBound / 2 }
	worker := newWorkerUnderTest(t, store, options, nil)
	lease := TaskLease{TaskID: 11, Owner: options.Owner, FencingToken: 9, FailureCount: 7}
	taskCtx, cancelTask := context.WithCancelCause(context.Background())
	worker.runTaskLease(taskCtx, cancelTask, lease)
	cancelTask(context.Canceled)

	releases := store.releaseSnapshot()
	if len(releases) != 1 {
		t.Fatalf("ReleaseTaskLease() calls = %d, want 1", len(releases))
	}
	wantDelay := options.NormalPollInterval + options.NormalPollInterval/10
	if releases[0].Delay != wantDelay {
		t.Fatalf("release delay = %v, want %v", releases[0].Delay, wantDelay)
	}
	if releases[0].PollFailed {
		t.Fatal("normal blocked poll must clear persistent failure state")
	}
	if releases[0].Lease != lease {
		t.Fatalf("released lease = %#v, want %#v", releases[0].Lease, lease)
	}
}

type workerUnavailableExecutor struct{}

func (workerUnavailableExecutor) Descriptor() Descriptor {
	return Descriptor{
		Uses:         "test.worker-unavailable@v1",
		Name:         "worker unavailable",
		ConfigSchema: json.RawMessage(`{"type":"object"}`),
	}
}

func (workerUnavailableExecutor) Validate(json.RawMessage) error { return nil }

func (workerUnavailableExecutor) Start(context.Context, StartRequest) (Result, error) {
	return Result{}, errors.New("unexpected Start call")
}

func (workerUnavailableExecutor) Reconcile(context.Context, ReconcileRequest) (Result, error) {
	return Result{}, errors.New("unexpected Reconcile call")
}

func (workerUnavailableExecutor) Available(context.Context) error {
	return ErrExecutorUnavailable
}

func TestWorkerPollFailuresUseExponentialBackoffAndJitter(t *testing.T) {
	tests := []struct {
		name   string
		listFn func(context.Context, TaskLease) ([]entity.TaskStepRecord, error)
		setup  func(*testing.T, *Registry)
	}{
		{
			name: "availability requests PollBackoff",
			listFn: func(_ context.Context, lease TaskLease) ([]entity.TaskStepRecord, error) {
				return []entity.TaskStepRecord{{
					StepRecordID: 1,
					TaskID:       lease.TaskID,
					StepKey:      "unavailable",
					Name:         "unavailable",
					Uses:         "test.worker-unavailable@v1",
					Position:     1,
					OnFailure:    FailureStop,
					Status:       StepPending,
				}}, nil
			},
			setup: func(t *testing.T, registry *Registry) {
				t.Helper()
				if err := registry.Register(workerUnavailableExecutor{}); err != nil {
					t.Fatalf("Register() error = %v", err)
				}
			},
		},
		{
			name: "store error",
			listFn: func(context.Context, TaskLease) ([]entity.TaskStepRecord, error) {
				return nil, errors.New("temporary database error")
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := &workerTestStore{listFn: test.listFn}
			registry := NewRegistry()
			if test.setup != nil {
				test.setup(t, registry)
			}
			options := validWorkerTestOptions()
			var jitterWindow time.Duration
			options.Jitter = func(upperBound time.Duration) time.Duration {
				jitterWindow = upperBound
				return upperBound / 4
			}
			worker := newWorkerUnderTest(t, store, options, registry)
			lease := TaskLease{TaskID: 19, Owner: options.Owner, FencingToken: 3, FailureCount: 2}
			taskCtx, cancelTask := context.WithCancelCause(context.Background())
			worker.runTaskLease(taskCtx, cancelTask, lease)
			cancelTask(context.Canceled)

			releases := store.releaseSnapshot()
			if len(releases) != 1 {
				t.Fatalf("ReleaseTaskLease() calls = %d, want 1", len(releases))
			}
			wantWindow := 4 * options.BackoffMin
			if jitterWindow != wantWindow {
				t.Fatalf("jitter upper bound = %v, want %v", jitterWindow, wantWindow)
			}
			if releases[0].Delay != wantWindow/4 {
				t.Fatalf("release delay = %v, want %v", releases[0].Delay, wantWindow/4)
			}
			if !releases[0].PollFailed {
				t.Fatal("poll failure must increment persistent failure state")
			}
		})
	}
}

func TestWorkerFailureBackoffSaturates(t *testing.T) {
	options := validWorkerTestOptions()
	options.BackoffMin = 5 * time.Millisecond
	options.BackoffMax = 40 * time.Millisecond
	options.Jitter = func(upperBound time.Duration) time.Duration { return upperBound }
	worker := &Worker{options: options}
	tests := []struct {
		previousFailures uint32
		want             time.Duration
	}{
		{previousFailures: 0, want: 5 * time.Millisecond},
		{previousFailures: 1, want: 10 * time.Millisecond},
		{previousFailures: 2, want: 20 * time.Millisecond},
		{previousFailures: 3, want: 40 * time.Millisecond},
		{previousFailures: 4, want: 40 * time.Millisecond},
		{previousFailures: MaxPollFailureCount, want: 40 * time.Millisecond},
		{previousFailures: ^uint32(0), want: 40 * time.Millisecond},
	}
	for _, test := range tests {
		if got := worker.failureBackoffDelay(test.previousFailures); got != test.want {
			t.Errorf("failureBackoffDelay(%d) = %v, want %v", test.previousFailures, got, test.want)
		}
	}
}

func TestWorkerClaimFailureBackoffHasFloorAndResetsAfterSuccess(t *testing.T) {
	options := validWorkerTestOptions()
	options.ScanInterval = 7 * time.Millisecond
	options.BackoffMin = 20 * time.Millisecond
	options.BackoffMax = 80 * time.Millisecond
	options.Jitter = func(upperBound time.Duration) time.Duration { return upperBound }
	worker := &Worker{options: options}
	for previousFailures, want := range map[uint32]time.Duration{
		0: 20 * time.Millisecond,
		1: 40 * time.Millisecond,
		2: 80 * time.Millisecond,
		9: 80 * time.Millisecond,
	} {
		if got := worker.claimFailureDelay(previousFailures); got != want {
			t.Errorf("claimFailureDelay(%d) = %v, want %v", previousFailures, got, want)
		}
	}
	worker.options.Jitter = func(time.Duration) time.Duration { return 0 }
	if got := worker.claimFailureDelay(0); got != options.ScanInterval {
		t.Fatalf("zero-jitter claim delay = %v, want scan floor %v", got, options.ScanInterval)
	}

	store := &workerTestStore{}
	callTimes := make(chan time.Time, 4)
	store.acquireFn = func(_ context.Context, _ string, _ int, _ time.Duration, call int) ([]TaskLease, error) {
		callTimes <- time.Now()
		if call == 1 || call == 3 {
			return nil, errors.New("database unavailable")
		}
		return nil, nil
	}
	options.ScanInterval = 2 * time.Millisecond
	options.BackoffMin = 25 * time.Millisecond
	options.BackoffMax = 100 * time.Millisecond
	options.Jitter = func(upperBound time.Duration) time.Duration { return upperBound }
	runner := newWorkerUnderTest(t, store, options, nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runner.Run(ctx) }()
	times := []time.Time{
		waitWorkerSignal(t, callTimes, "first failed claim"),
		waitWorkerSignal(t, callTimes, "successful claim"),
		waitWorkerSignal(t, callTimes, "failed claim after reset"),
		waitWorkerSignal(t, callTimes, "claim after reset backoff"),
	}
	cancel()
	if err := waitWorkerSignal(t, done, "worker shutdown"); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if firstGap := times[1].Sub(times[0]); firstGap < 20*time.Millisecond {
		t.Fatalf("first claim backoff = %v, want about %v", firstGap, options.BackoffMin)
	}
	if resetGap := times[3].Sub(times[2]); resetGap < 20*time.Millisecond {
		t.Fatalf("claim backoff after success = %v, want reset floor about %v", resetGap, options.BackoffMin)
	}
}

func TestWorkerRenewFailureCancelsTaskWithoutRelease(t *testing.T) {
	store := &workerTestStore{}
	listStarted := make(chan struct{})
	observedCause := make(chan error, 1)
	renewed := make(chan struct{})
	var startOnce sync.Once
	store.listFn = func(ctx context.Context, _ TaskLease) ([]entity.TaskStepRecord, error) {
		startOnce.Do(func() { close(listStarted) })
		<-ctx.Done()
		observedCause <- context.Cause(ctx)
		return nil, ctx.Err()
	}
	renewFailure := errors.New("lease renewal failed")
	store.renewFn = func(context.Context, TaskLease, time.Duration) error {
		select {
		case <-renewed:
		default:
			close(renewed)
		}
		return renewFailure
	}
	options := validWorkerTestOptions()
	options.LeaseDuration = 30 * time.Millisecond
	options.RenewInterval = 5 * time.Millisecond
	worker := newWorkerUnderTest(t, store, options, nil)
	lease := TaskLease{TaskID: 23, Owner: options.Owner, FencingToken: 4}
	taskCtx, cancelTask := context.WithCancelCause(context.Background())
	done := make(chan struct{})
	go func() {
		worker.runTaskLease(taskCtx, cancelTask, lease)
		close(done)
	}()

	waitWorkerSignal(t, listStarted, "协调器开始执行")
	waitWorkerSignal(t, renewed, "首次续租")
	waitWorkerSignal(t, done, "续租失败后的任务退出")
	cause := waitWorkerSignal(t, observedCause, "任务取消原因")
	if !errors.Is(cause, renewFailure) {
		t.Fatalf("task cancellation cause = %v, want %v", cause, renewFailure)
	}
	if calls := store.renewSnapshot(); len(calls) != 1 {
		t.Fatalf("RenewTaskLease() calls = %d, want 1", len(calls))
	}
	if calls := store.releaseSnapshot(); len(calls) != 0 {
		t.Fatalf("ReleaseTaskLease() calls = %d, want 0 after lease loss", len(calls))
	}
}

func TestWorkerShutdownStopsClaimingBeforeDrain(t *testing.T) {
	store := &workerTestStore{}
	gate := make(chan struct{})
	started := make(chan struct{})
	var startOnce sync.Once
	store.acquireFn = func(_ context.Context, owner string, _ int, _ time.Duration, call int) ([]TaskLease, error) {
		if call != 1 {
			return nil, nil
		}
		return []TaskLease{{TaskID: 31, Owner: owner, FencingToken: 1}}, nil
	}
	store.listFn = func(ctx context.Context, lease TaskLease) ([]entity.TaskStepRecord, error) {
		startOnce.Do(func() { close(started) })
		select {
		case <-gate:
			return terminalWorkerSteps(lease.TaskID), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	options := validWorkerTestOptions()
	worker := newWorkerUnderTest(t, store, options, nil)
	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- worker.Run(ctx) }()
	waitWorkerSignal(t, started, "任务启动")

	cancel() // models SIGTERM: stop the claim loop, keep taskCtx alive for drain.
	wantAcquireCalls := len(store.acquireSnapshot())
	time.Sleep(options.RenewInterval + 4*options.ScanInterval)
	if got := len(store.acquireSnapshot()); got != wantAcquireCalls {
		t.Fatalf("AcquireTaskLeases() calls after shutdown = %d, want %d", got, wantAcquireCalls)
	}
	if got := len(store.renewSnapshot()); got == 0 {
		t.Fatal("in-flight task lease was not renewed during graceful drain")
	}
	select {
	case err := <-runDone:
		t.Fatalf("Run() returned before in-flight task drained: %v", err)
	default:
	}

	close(gate)
	if err := waitWorkerSignal(t, runDone, "正常排空"); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	statuses := store.statusSnapshot()
	if len(statuses) != 1 || statuses[0].Status != TaskSucceeded {
		t.Fatalf("terminal status calls = %#v, want one succeeded call", statuses)
	}
}

func TestWorkerDrainTimeoutIsBoundedAndCancelsTask(t *testing.T) {
	store := &workerTestStore{}
	started := make(chan struct{})
	cancelCause := make(chan error, 1)
	var startOnce sync.Once
	store.acquireFn = func(_ context.Context, owner string, _ int, _ time.Duration, call int) ([]TaskLease, error) {
		if call != 1 {
			return nil, nil
		}
		return []TaskLease{{TaskID: 37, Owner: owner, FencingToken: 1}}, nil
	}
	store.listFn = func(ctx context.Context, _ TaskLease) ([]entity.TaskStepRecord, error) {
		startOnce.Do(func() { close(started) })
		<-ctx.Done()
		cancelCause <- context.Cause(ctx)
		return nil, ctx.Err()
	}
	options := validWorkerTestOptions()
	options.DrainTimeout = 25 * time.Millisecond
	worker := newWorkerUnderTest(t, store, options, nil)
	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- worker.Run(ctx) }()
	waitWorkerSignal(t, started, "任务启动")

	startedDrain := time.Now()
	cancel()
	err := waitWorkerSignal(t, runDone, "排空超时")
	elapsed := time.Since(startedDrain)
	if !errors.Is(err, ErrWorkerDrainTimeout) {
		t.Fatalf("Run() error = %v, want %v", err, ErrWorkerDrainTimeout)
	}
	if elapsed < options.DrainTimeout/2 {
		t.Fatalf("Run() returned too early after %v, drain timeout %v", elapsed, options.DrainTimeout)
	}
	if elapsed > time.Second {
		t.Fatalf("Run() cancellation was not bounded: %v", elapsed)
	}
	if cause := waitWorkerSignal(t, cancelCause, "排空超时取消任务"); !errors.Is(cause, ErrWorkerDrainTimeout) {
		t.Fatalf("task cancellation cause = %v, want %v", cause, ErrWorkerDrainTimeout)
	}
	if calls := store.releaseSnapshot(); len(calls) != 0 {
		t.Fatalf("ReleaseTaskLease() calls = %d, want 0 after forced drain", len(calls))
	}
}

func TestWorkerAndManagerShareShutdownDeadlineAcrossUnstartedRelease(t *testing.T) {
	store := &workerTestStore{}
	activeStarted := make(chan struct{})
	secondAcquireStarted := make(chan struct{})
	returnSecondAcquire := make(chan struct{})
	unstartedReleaseStarted := make(chan struct{})
	activeCanceled := make(chan error, 1)
	store.acquireFn = func(_ context.Context, owner string, _ int, _ time.Duration, call int) ([]TaskLease, error) {
		switch call {
		case 1:
			return []TaskLease{{TaskID: 41, Owner: owner, FencingToken: 1}}, nil
		case 2:
			close(secondAcquireStarted)
			// Model a database call that returns a successful claim concurrently
			// with cancellation rather than returning ctx.Err().
			<-returnSecondAcquire
			return []TaskLease{{TaskID: 42, Owner: owner, FencingToken: 1}}, nil
		default:
			return nil, nil
		}
	}
	store.listFn = func(ctx context.Context, lease TaskLease) ([]entity.TaskStepRecord, error) {
		if lease.TaskID != 41 {
			t.Fatalf("unstarted task unexpectedly launched: %#v", lease)
		}
		close(activeStarted)
		<-ctx.Done()
		activeCanceled <- context.Cause(ctx)
		return nil, ctx.Err()
	}
	store.releaseFn = func(ctx context.Context, lease TaskLease, _ time.Duration, _ bool) error {
		if lease.TaskID != 42 {
			return nil
		}
		close(unstartedReleaseStarted)
		// Consume the complete per-release allowance. The active task must only
		// receive the remainder of the process-wide drain budget.
		<-ctx.Done()
		return ctx.Err()
	}

	options := validWorkerTestOptions()
	options.Concurrency = 2
	options.ClaimBatchSize = 1
	options.DrainTimeout = 200 * time.Millisecond
	worker := newWorkerUnderTest(t, store, options, nil)
	manager, err := job.NewManager(worker, nil, nil, job.Options{DrainTimeout: options.DrainTimeout})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitWorkerSignal(t, activeStarted, "active task start")
	waitWorkerSignal(t, secondAcquireStarted, "second claim start")

	startedDrain := time.Now()
	manager.Stop()
	close(returnSecondAcquire)
	waitWorkerSignal(t, unstartedReleaseStarted, "unstarted lease release")
	waitErr := manager.Wait()
	elapsed := time.Since(startedDrain)
	if !errors.Is(waitErr, ErrWorkerDrainTimeout) {
		t.Fatalf("Manager.Wait() error = %v, want %v", waitErr, ErrWorkerDrainTimeout)
	}
	if errors.Is(waitErr, job.ErrDrainTimeout) {
		t.Fatalf("Manager.Wait() exceeded its own deadline: %v", waitErr)
	}
	if elapsed > options.DrainTimeout+50*time.Millisecond {
		t.Fatalf("shared drain took %v, want at most one %v budget", elapsed, options.DrainTimeout)
	}
	if cause := waitWorkerSignal(t, activeCanceled, "active task cancellation"); !errors.Is(cause, ErrWorkerDrainTimeout) {
		t.Fatalf("active task cancellation cause = %v, want %v", cause, ErrWorkerDrainTimeout)
	}
}

func TestNewWorkerValidatesOptionsAndGeneratesOwner(t *testing.T) {
	store := &workerTestStore{}
	coordinator := NewCoordinator(store, NewRegistry())
	if _, err := NewWorker(nil, coordinator, WorkerOptions{}); err == nil {
		t.Fatal("NewWorker(nil store) error = nil")
	}
	if _, err := NewWorker(store, nil, WorkerOptions{}); err == nil {
		t.Fatal("NewWorker(nil coordinator) error = nil")
	}

	valid, err := NewWorker(store, coordinator, WorkerOptions{})
	if err != nil {
		t.Fatalf("NewWorker(default options) error = %v", err)
	}
	if !strings.HasPrefix(valid.owner, "ares-") || len(valid.owner) != len("ares-")+32 {
		t.Fatalf("generated owner = %q, want ares- plus 32 hex characters", valid.owner)
	}
	if valid.options.ClaimBatchSize != valid.options.Concurrency {
		t.Fatalf("default claim batch = %d, want concurrency %d", valid.options.ClaimBatchSize, valid.options.Concurrency)
	}

	tests := []struct {
		name   string
		mutate func(*WorkerOptions)
	}{
		{name: "invalid owner", mutate: func(options *WorkerOptions) { options.Owner = "contains space" }},
		{name: "owner too long", mutate: func(options *WorkerOptions) { options.Owner = strings.Repeat("x", MaxLeaseOwnerBytes+1) }},
		{name: "concurrency", mutate: func(options *WorkerOptions) { options.Concurrency = maxTaskLeaseBatch + 1 }},
		{name: "claim batch", mutate: func(options *WorkerOptions) { options.ClaimBatchSize = options.Concurrency + 1 }},
		{name: "scan interval", mutate: func(options *WorkerOptions) { options.ScanInterval = -time.Second }},
		{name: "renew interval", mutate: func(options *WorkerOptions) { options.RenewInterval = options.LeaseDuration/3 + time.Nanosecond }},
		{name: "backoff order", mutate: func(options *WorkerOptions) { options.BackoffMin = options.BackoffMax + time.Nanosecond }},
		{name: "max transitions", mutate: func(options *WorkerOptions) { options.MaxTransitions = 10001 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			options := validWorkerTestOptions()
			test.mutate(&options)
			if _, err := NewWorker(store, coordinator, options); err == nil {
				t.Fatal("NewWorker() error = nil")
			}
		})
	}
}

func TestWorkerCanRunOnlyOnce(t *testing.T) {
	store := &workerTestStore{}
	worker := newWorkerUnderTest(t, store, validWorkerTestOptions(), nil)
	if err := worker.Run(nil); err == nil {
		t.Fatal("Run(nil) error = nil")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := worker.Run(ctx); err != nil {
		t.Fatalf("first Run() error = %v", err)
	}
	if err := worker.Run(ctx); err == nil {
		t.Fatal("second Run() error = nil")
	}
}
