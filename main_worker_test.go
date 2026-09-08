package main

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-ree/ares/internal/config"
)

type startupJobStub struct {
	starts atomic.Int32
}

func (s *startupJobStub) Start(context.Context) error {
	s.starts.Add(1)
	return nil
}

func TestWorkflowWorkerOptionsMapEverySchedulingSetting(t *testing.T) {
	settings := config.WorkerRuntimeConfig{
		Concurrency: 3, ClaimBatchSize: 2,
		ScanInterval: 101 * time.Millisecond, LeaseDuration: 31 * time.Second,
		RenewInterval: 7 * time.Second, NormalPollInterval: 4 * time.Second,
		BackoffMin: 3 * time.Second, BackoffMax: 45 * time.Second,
		DrainTimeout: 17 * time.Second,
	}
	got := workflowWorkerOptions(settings)
	if got.Concurrency != settings.Concurrency || got.ClaimBatchSize != settings.ClaimBatchSize ||
		got.ScanInterval != settings.ScanInterval || got.LeaseDuration != settings.LeaseDuration ||
		got.RenewInterval != settings.RenewInterval || got.NormalPollInterval != settings.NormalPollInterval ||
		got.BackoffMin != settings.BackoffMin || got.BackoffMax != settings.BackoffMax ||
		got.DrainTimeout != settings.DrainTimeout {
		t.Fatalf("workflowWorkerOptions() = %#v, settings = %#v", got, settings)
	}
}

func TestHTTPBindFailureDoesNotStartBackgroundJobs(t *testing.T) {
	jobs := &startupJobStub{}
	bindFailure := errors.New("address already in use")
	server, err := prepareHTTPAndStartJobs(context.Background(), jobs, func() (preparedHTTPServer, error) {
		return nil, bindFailure
	})
	if server != nil {
		t.Fatalf("prepared server = %#v, want nil", server)
	}
	if !errors.Is(err, bindFailure) {
		t.Fatalf("prepareHTTPAndStartJobs() error = %v, want %v", err, bindFailure)
	}
	if got := jobs.starts.Load(); got != 0 {
		t.Fatalf("background Start() calls = %d, want 0 after bind failure", got)
	}
}
