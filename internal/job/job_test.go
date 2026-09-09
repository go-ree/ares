package job

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

type runnerFunc func(context.Context) error

func (fn runnerFunc) Run(ctx context.Context) error { return fn(ctx) }

type legacyRunnerFunc func(context.Context) error

func (fn legacyRunnerFunc) RunOnce(ctx context.Context) error { return fn(ctx) }

func TestManagerPropagatesContextAndWaitsForRunners(t *testing.T) {
	started := make(chan struct{})
	released := make(chan struct{})
	runner := runnerFunc(func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		<-released
		return ctx.Err()
	})
	manager, err := NewManager(runner, nil, nil, Options{DrainTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	if err := manager.Start(ctx); err != nil {
		t.Fatal(err)
	}
	<-started
	cancel()
	waitResult := make(chan error, 1)
	go func() { waitResult <- manager.Wait() }()
	select {
	case err := <-waitResult:
		t.Fatalf("Wait() returned before the runner drained: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	close(released)
	select {
	case err := <-waitResult:
		if err != nil {
			t.Fatalf("Wait() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Wait() did not return after the runner drained")
	}
}

func TestManagerStopIsIdempotentAndBoundsDrain(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	runner := runnerFunc(func(context.Context) error {
		close(started)
		<-release
		return nil
	})
	manager, err := NewManager(runner, nil, nil, Options{DrainTimeout: 25 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	<-started
	manager.Stop()
	manager.Stop()
	startedAt := time.Now()
	if err := manager.Wait(); !errors.Is(err, ErrDrainTimeout) {
		t.Fatalf("Wait() error = %v, want ErrDrainTimeout", err)
	}
	if elapsed := time.Since(startedAt); elapsed > 250*time.Millisecond {
		t.Fatalf("bounded drain took %s", elapsed)
	}
	close(release)
}

func TestManagerWaitPrefersCompletedDrainAfterDeadline(t *testing.T) {
	started := make(chan struct{})
	runner := runnerFunc(func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	})
	manager, err := NewManager(runner, nil, nil, Options{DrainTimeout: 5 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	<-started
	manager.Stop()
	<-manager.Done()
	time.Sleep(10 * time.Millisecond)

	// Both done and stopping are closed here. Every late waiter must observe the
	// completed drain, never a random timeout selected from two ready channels.
	for attempt := 0; attempt < 32; attempt++ {
		if err := manager.Wait(); err != nil {
			t.Fatalf("late Wait() attempt %d error = %v", attempt, err)
		}
	}
}

func TestManagerDrainTimeoutPreservesRecordedRunnerFailure(t *testing.T) {
	failure := errors.New("workflow runner failed before peer drained")
	peerStarted := make(chan struct{})
	releasePeer := make(chan struct{})
	manager, err := NewManager(
		runnerFunc(func(context.Context) error {
			<-peerStarted
			return failure
		}),
		runnerFunc(func(context.Context) error {
			close(peerStarted)
			<-releasePeer
			return nil
		}),
		nil,
		Options{DrainTimeout: 20 * time.Millisecond},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	waitErr := manager.Wait()
	if !errors.Is(waitErr, ErrDrainTimeout) {
		t.Fatalf("Wait() error = %v, want ErrDrainTimeout", waitErr)
	}
	if !errors.Is(waitErr, failure) {
		t.Fatalf("Wait() error = %v, want recorded failure %v", waitErr, failure)
	}
	close(releasePeer)
	select {
	case <-manager.Done():
	case <-time.After(time.Second):
		t.Fatal("manager did not finish after releasing timed-out peer")
	}
}

func TestManagerLegacyLoopRunsImmediatelyAndStops(t *testing.T) {
	var calls atomic.Int64
	called := make(chan struct{}, 1)
	legacy := legacyRunnerFunc(func(context.Context) error {
		calls.Add(1)
		select {
		case called <- struct{}{}:
		default:
		}
		return errors.New("transient")
	})
	manager, err := NewManager(nil, nil, legacy, Options{
		LegacyPollInterval: 20 * time.Millisecond,
		DrainTimeout:       time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	if err := manager.Start(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-called:
	case <-time.After(time.Second):
		t.Fatal("legacy loop did not run immediately")
	}
	cancel()
	if err := manager.Wait(); err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
	stoppedAt := calls.Load()
	time.Sleep(50 * time.Millisecond)
	if calls.Load() != stoppedAt {
		t.Fatal("legacy loop continued after shutdown")
	}
}

func TestManagerUnexpectedRunnerExitStopsPeersAndReturnsError(t *testing.T) {
	peerCanceled := make(chan struct{})
	failure := errors.New("fatal worker error")
	manager, err := NewManager(
		runnerFunc(func(context.Context) error { return failure }),
		runnerFunc(func(ctx context.Context) error {
			<-ctx.Done()
			close(peerCanceled)
			return ctx.Err()
		}),
		nil,
		Options{DrainTimeout: time.Second},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := manager.Wait(); !errors.Is(err, failure) {
		t.Fatalf("Wait() error = %v, want %v", err, failure)
	}
	select {
	case <-peerCanceled:
	case <-time.After(time.Second):
		t.Fatal("peer runner did not receive cancellation")
	}
}

func TestManagerReportsNonContextDrainFailure(t *testing.T) {
	drainFailure := errors.New("worker forced drain timeout")
	runnerStarted := make(chan struct{})
	manager, err := NewManager(runnerFunc(func(ctx context.Context) error {
		close(runnerStarted)
		<-ctx.Done()
		return drainFailure
	}), nil, nil, Options{DrainTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	if err := manager.Start(ctx); err != nil {
		t.Fatal(err)
	}
	<-runnerStarted
	cancel()
	if err := manager.Wait(); !errors.Is(err, drainFailure) {
		t.Fatalf("Wait() error = %v, want %v", err, drainFailure)
	}
}

func TestManagerRejectsInvalidLifecycle(t *testing.T) {
	if _, err := NewManager(nil, nil, nil, Options{LegacyPollInterval: -1}); err == nil {
		t.Fatal("negative legacy interval was accepted")
	}
	if _, err := NewManager(nil, nil, nil, Options{DrainTimeout: -1}); err == nil {
		t.Fatal("negative drain timeout was accepted")
	}
	manager, err := NewManager(nil, nil, nil, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Wait(); !errors.Is(err, ErrNotStarted) {
		t.Fatalf("Wait() error = %v, want ErrNotStarted", err)
	}
	if err := manager.Start(nil); err == nil {
		t.Fatal("Start(nil) succeeded")
	}
	if err := manager.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := manager.Start(context.Background()); !errors.Is(err, ErrAlreadyStarted) {
		t.Fatalf("second Start() error = %v, want ErrAlreadyStarted", err)
	}
	manager.Stop()
	if err := manager.Wait(); err != nil {
		t.Fatal(err)
	}
}
