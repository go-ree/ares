package workflow

import (
	"context"
	"errors"

	"github.com/go-ree/ares/internal/entity"
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

type ExecutionStore interface {
	CreateTaskSnapshot(context.Context, int, WorkflowView) error
	GetTaskReleaseContext(context.Context, int) (ReleaseContext, error)
	ListTaskSteps(context.Context, int) ([]entity.TaskStepRecord, error)
	ClaimStep(context.Context, int64) (bool, error)
	ReleaseStep(context.Context, int64, string) (bool, error)
	SaveStepResult(context.Context, int64, Result) (bool, error)
	SkipPendingSteps(context.Context, int, string) error
	SetTaskStatus(context.Context, int, string, string) error
}

// AtomicTaskCreator is implemented by stores that can persist the TaskRecord
// and all of its step snapshots in one transaction. Production publishing must
// use this path; CreateTaskSnapshot remains for compatibility/migrations.
type AtomicTaskCreator interface {
	CreateTaskWithSnapshot(context.Context, *entity.TaskRecord, WorkflowView) error
}
