package webserver

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestReadinessProbeCoalescesConcurrentChecks(t *testing.T) {
	var calls atomic.Int64
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	probe := newReadinessProbe(func(context.Context) error {
		calls.Add(1)
		once.Do(func() { close(started) })
		<-release
		return nil
	})

	const workers = 32
	results := make(chan bool, workers)
	for index := 0; index < workers; index++ {
		go func() { results <- probe.status(context.Background()) }()
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("readiness check did not start")
	}
	time.Sleep(20 * time.Millisecond)
	if got := calls.Load(); got != 1 {
		t.Fatalf("concurrent readiness checks = %d, want one", got)
	}
	close(release)
	for index := 0; index < workers; index++ {
		if !<-results {
			t.Fatal("successful coalesced readiness check reported unavailable")
		}
	}
	if !probe.status(context.Background()) || calls.Load() != 1 {
		t.Fatalf("cached readiness result made %d checks, want one", calls.Load())
	}
}

func TestReadinessProbeUsesShorterFailureCache(t *testing.T) {
	clock := time.Unix(1_700_000_000, 0)
	var calls atomic.Int64
	probe := newReadinessProbe(func(context.Context) error {
		if calls.Add(1) == 1 {
			return errors.New("database unavailable")
		}
		return nil
	})
	probe.now = func() time.Time { return clock }

	if probe.status(context.Background()) {
		t.Fatal("failed database check reported ready")
	}
	clock = clock.Add(readinessFailureTTL / 2)
	if probe.status(context.Background()) || calls.Load() != 1 {
		t.Fatalf("failure cache made %d checks, want one", calls.Load())
	}
	clock = clock.Add(readinessFailureTTL)
	if !probe.status(context.Background()) || calls.Load() != 2 {
		t.Fatalf("expired failure cache result ready=%v calls=%d", probe.ready, calls.Load())
	}
}
