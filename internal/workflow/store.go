package workflow

import (
	"context"
	"errors"
	"time"

	"github.com/go-ree/ares/internal/entity"
)

const (
	MaxPollFailureCount = 31
	MaxLeaseOwnerBytes  = 64
)

var (
	ErrNotFound          = errors.New("工作流不存在")
	ErrRevisionConflict  = errors.New("工作流已被其他请求修改")
	ErrTaskHasSnapshot   = errors.New("任务已经存在工作流步骤快照")
	ErrLegacyTask        = errors.New("旧版任务不支持通用步骤日志")
	ErrLogsUnsupported   = errors.New("步骤执行器不支持日志")
	ErrLogsNotReady      = errors.New("步骤日志尚未就绪")
	ErrLogSourceMismatch = errors.New("步骤日志来源与当前执行器实例不匹配")
	ErrInvalidLogCursor  = errors.New("日志游标无效")
	ErrInvalidLogChunk   = errors.New("执行器返回了无效日志块")
	ErrLeaseLost         = errors.New("任务租约已失效")
	// ErrExecutorUnavailable may only be returned before an executor has made
	// an external side effect. The coordinator releases that claim for retry.
	ErrExecutorUnavailable = errors.New("执行器暂不可用")
)

// TaskStepLogSource is the minimum immutable execution snapshot needed to
// dispatch logs. Config and output are intentionally absent so they cannot
// cross the optional capability boundary by accident.
type TaskStepLogSource struct {
	TaskID            int
	StepKey           string
	Uses              string
	Status            string
	ExternalReference []byte
}

// LogSourceStore resolves one exact v2 task/step pair. Implementations must
// exclude deleted tasks, distinguish v1 legacy tasks, and fail closed on
// snapshot inconsistencies.
type LogSourceStore interface {
	GetTaskStepLogSource(context.Context, int, string) (TaskStepLogSource, error)
}

type SaveWorkflowCommand struct {
	ConfigID         int
	ExpectedRevision int
	Actor            string
	ActorUserID      *int64
	Spec             WorkflowSpec
}

type DefinitionStore interface {
	SaveWorkflow(context.Context, SaveWorkflowCommand) (WorkflowView, error)
	GetCurrentWorkflow(context.Context, int) (WorkflowView, error)
}

// TaskLease is an immutable capability for one task ownership generation.
// Owner and token are internal scheduling data and must never be exposed by
// public task APIs or included in executor idempotency keys.
type TaskLease struct {
	TaskID       int
	Owner        string
	FencingToken uint64
	FailureCount uint32
}

// TaskSnapshotStore owns task creation and public step reads. It is kept
// separate from leased execution so publishing cannot accidentally acquire or
// bypass a worker lease.
type TaskSnapshotStore interface {
	CreateTaskSnapshot(context.Context, int, WorkflowView) error
	ListTaskSteps(context.Context, int) ([]entity.TaskStepRecord, error)
}

// LeasedExecutionStore is the only storage boundary through which a v2 task
// may advance. Every method must verify owner, fencing token, engine version,
// active task status, deletion state, and lease expiry using database time.
type LeasedExecutionStore interface {
	TaskSnapshotStore
	GetTaskReleaseContext(context.Context, TaskLease) (ReleaseContext, error)
	ListTaskStepsForLease(context.Context, TaskLease) ([]entity.TaskStepRecord, error)
	StepTimeRemaining(context.Context, TaskLease, int64, int) (time.Duration, error)
	ClaimStep(context.Context, TaskLease, int64) (bool, error)
	ReleaseStep(context.Context, TaskLease, int64, string) (bool, error)
	SaveStepResult(context.Context, TaskLease, int64, Result) (bool, error)
	SkipPendingSteps(context.Context, TaskLease, string) error
	SetTaskStatus(context.Context, TaskLease, string, string) error
}

// WorkerStore adds durable scheduling operations to the fenced execution
// store. Delays are relative durations converted to database time by the
// implementation. pollFailed increments persistent backoff state; a healthy
// pass resets it. Terminal task status is finalized by SetTaskStatus and
// clears the lease there, so ReleaseTaskLease handles active tasks only.
type WorkerStore interface {
	LeasedExecutionStore
	AcquireTaskLeases(context.Context, string, int, time.Duration) ([]TaskLease, error)
	RenewTaskLease(context.Context, TaskLease, time.Duration) error
	ReleaseTaskLease(context.Context, TaskLease, time.Duration, bool) error
}

// AtomicTaskCreator is implemented by stores that can persist the TaskRecord
// and all of its step snapshots in one transaction. Production publishing must
// use this path; CreateTaskSnapshot remains for compatibility/migrations.
type AtomicTaskCreator interface {
	CreateTaskWithSnapshot(context.Context, *entity.TaskRecord, WorkflowView) error
}
