package workflow

import (
	"context"
	"errors"

	"github.com/go-ree/ares/internal/entity"
)

var (
	ErrNotFound         = errors.New("工作流不存在")
	ErrRevisionConflict = errors.New("工作流已被其他请求修改")
	ErrTaskHasSnapshot  = errors.New("任务已经存在工作流步骤快照")
	// ErrExecutorUnavailable may only be returned before an executor has made
	// an external side effect. The coordinator releases that claim for retry.
	ErrExecutorUnavailable = errors.New("执行器暂不可用")
)

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
