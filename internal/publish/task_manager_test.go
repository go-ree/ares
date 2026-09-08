package publish

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/go-ree/ares/internal/entity"
)

func TestTaskManagerNonLeaderDoesNotScan(t *testing.T) {
	roundCalled := false
	manager := &TaskManager{
		acquireLeadership: func(context.Context) (legacyLeadership, bool, error) {
			return legacyLeadership{}, false, nil
		},
		runLeaderRound: func(context.Context) error {
			roundCalled = true
			return nil
		},
	}
	if err := manager.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if roundCalled {
		t.Fatal("non-leader queried legacy tasks")
	}
}

func TestTaskManagerHoldsLeadershipUntilRoundReturns(t *testing.T) {
	released := false
	roundErr := errors.New("round failed")
	leaderContext := context.WithValue(context.Background(), struct{}{}, "leader")
	manager := &TaskManager{
		acquireLeadership: func(context.Context) (legacyLeadership, bool, error) {
			return legacyLeadership{ctx: leaderContext, release: func() { released = true }}, true, nil
		},
		runLeaderRound: func(ctx context.Context) error {
			if ctx != leaderContext {
				t.Fatal("RunOnce() did not pass the leader context to the round")
			}
			if released {
				t.Fatal("leadership was released before the round completed")
			}
			return roundErr
		},
	}
	if err := manager.RunOnce(context.Background()); !errors.Is(err, roundErr) {
		t.Fatalf("RunOnce() error = %v, want %v", err, roundErr)
	}
	if !released {
		t.Fatal("RunOnce() did not release leadership after the round")
	}
}

func TestTaskManagerPropagatesLeaderCancellation(t *testing.T) {
	leaderContext, cancelLeader := context.WithCancel(context.Background())
	released := make(chan struct{})
	roundStarted := make(chan struct{})
	manager := &TaskManager{
		acquireLeadership: func(context.Context) (legacyLeadership, bool, error) {
			return legacyLeadership{ctx: leaderContext, release: func() { close(released) }}, true, nil
		},
		runLeaderRound: func(ctx context.Context) error {
			close(roundStarted)
			<-ctx.Done()
			return ctx.Err()
		},
	}
	result := make(chan error, 1)
	go func() { result <- manager.RunOnce(context.Background()) }()
	select {
	case <-roundStarted:
	case <-time.After(time.Second):
		t.Fatal("legacy round did not start")
	}
	cancelLeader()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("RunOnce() error = %v, want context cancellation", err)
		}
	case <-time.After(time.Second):
		t.Fatal("legacy round ignored leader cancellation")
	}
	select {
	case <-released:
	case <-time.After(time.Second):
		t.Fatal("leadership was not released after cancellation")
	}
}

func TestTaskManagerFailsClosedWhenJenkinsSettingsCannotRefresh(t *testing.T) {
	settingsErr := errors.New("settings database unavailable")
	manager := &TaskManager{
		ensureJenkinsCurrent: func(context.Context) error { return settingsErr },
	}
	task := entity.TaskRecord{
		TaskId:         41,
		Status:         entity.StatusPackaging,
		JenkinsAddress: "https://jenkins.example",
		CiJobName:      "folder/build",
		CiBuildId:      17,
	}

	err := manager.processLegacyTask(context.Background(), task)
	if !errors.Is(err, settingsErr) {
		t.Fatalf("processLegacyTask() error = %v, want settings refresh failure", err)
	}
}

func TestLegacyLeaderLockNameIsStableBoundedAndOpaque(t *testing.T) {
	first := legacyLeaderLockName("ares-production")
	if first != legacyLeaderLockName(" ares-production ") {
		t.Fatal("lock name did not normalize surrounding whitespace")
	}
	if first == legacyLeaderLockName("ares-staging") {
		t.Fatal("different schemas produced the same lock name")
	}
	if len(first) > 64 {
		t.Fatalf("lock name length = %d, want at most 64", len(first))
	}
	if strings.Contains(first, "production") {
		t.Fatalf("lock name exposes the database name: %q", first)
	}
}

func TestTaskManagerRejectsInvalidLifecycle(t *testing.T) {
	if err := NewTaskManager().RunOnce(nil); err == nil {
		t.Fatal("RunOnce(nil) succeeded")
	}
	var manager *TaskManager
	if err := manager.RunOnce(context.Background()); err == nil {
		t.Fatal("nil TaskManager.RunOnce() succeeded")
	}
	invalidLeader := &TaskManager{
		acquireLeadership: func(context.Context) (legacyLeadership, bool, error) {
			return legacyLeadership{}, true, nil
		},
		runLeaderRound: func(context.Context) error { return nil },
	}
	if err := invalidLeader.RunOnce(context.Background()); err == nil {
		t.Fatal("RunOnce() accepted an invalid leadership handle")
	}
}

func TestValidateLegacyTaskAddress(t *testing.T) {
	for _, test := range []struct {
		name    string
		address string
		wantErr bool
	}{
		{name: "valid loopback http", address: "http://127.0.0.1:8080"},
		{name: "remote http", address: "http://jenkins.example", wantErr: true},
		{name: "valid https normalized", address: " https://jenkins.example/ "},
		{name: "missing", address: "", wantErr: true},
		{name: "whitespace", address: "  ", wantErr: true},
		{name: "file URL", address: "file:///tmp/jenkins", wantErr: true},
		{name: "embedded credentials", address: "https://user:secret@jenkins.example", wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := validateLegacyTaskAddress(test.address); (err != nil) != test.wantErr {
				t.Fatalf("validateLegacyTaskAddress(%q) error = %v, wantErr %v", test.address, err, test.wantErr)
			}
		})
	}
}

func TestValidateLegacyTaskExecution(t *testing.T) {
	validPackaging := entity.TaskRecord{
		Status: entity.StatusPackaging, CiJobName: "folder/build", CiBuildId: 7,
	}
	validPackaged := entity.TaskRecord{
		Status: entity.StatusPackaged, AutoDeploy: 1, CdJobName: "folder/deploy",
	}
	validDeploying := entity.TaskRecord{
		Status: entity.StatusDeploying, CdJobName: "folder/deploy", CdBuildId: 9,
	}
	for _, test := range []struct {
		name    string
		task    entity.TaskRecord
		wantErr bool
	}{
		{name: "packaging", task: validPackaging},
		{name: "packaging null build id", task: entity.TaskRecord{Status: entity.StatusPackaging, CiJobName: "folder/build"}, wantErr: true},
		{name: "packaging null job", task: entity.TaskRecord{Status: entity.StatusPackaging, CiBuildId: 7}, wantErr: true},
		{name: "packaged manual", task: entity.TaskRecord{Status: entity.StatusPackaged}},
		{name: "packaged automatic", task: validPackaged},
		{name: "packaged automatic null job", task: entity.TaskRecord{Status: entity.StatusPackaged, AutoDeploy: 1}, wantErr: true},
		{name: "deploying", task: validDeploying},
		{name: "deploying null build id", task: entity.TaskRecord{Status: entity.StatusDeploying, CdJobName: "folder/deploy"}, wantErr: true},
		{name: "invalid auto deploy", task: entity.TaskRecord{Status: entity.StatusPackaged, AutoDeploy: 2}, wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := validateLegacyTaskExecution(test.task); (err != nil) != test.wantErr {
				t.Fatalf("validateLegacyTaskExecution(%#v) error = %v, wantErr %t", test.task, err, test.wantErr)
			}
		})
	}
}

func TestLegacyUnsafeFailureStatus(t *testing.T) {
	for _, test := range []struct {
		current string
		want    string
	}{
		{current: entity.StatusPackaging, want: entity.StatusPackageFailed},
		{current: entity.StatusPackaged, want: entity.StatusDeployFailed},
		{current: entity.StatusDeploying, want: entity.StatusDeployFailed},
	} {
		if got := legacyUnsafeFailureStatus(test.current); got != test.want {
			t.Errorf("legacyUnsafeFailureStatus(%q) = %q, want %q", test.current, got, test.want)
		}
	}
}
