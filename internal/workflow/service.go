package workflow

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/go-ree/ares/internal/entity"
)

type Service struct {
	definitions DefinitionStore
	registry    *Registry
}

func NewService(definitions DefinitionStore, registry *Registry) *Service {
	return &Service{definitions: definitions, registry: registry}
}

func (s *Service) Registry() *Registry { return s.registry }

func (s *Service) GetCurrent(ctx context.Context, configID int) (WorkflowView, error) {
	if configID <= 0 {
		return WorkflowView{}, fmt.Errorf("config_id 必须大于 0")
	}
	return s.definitions.GetCurrentWorkflow(ctx, configID)
}

func (s *Service) Save(ctx context.Context, configID, revision int, actor string, actorUserID int64, spec WorkflowSpec) (WorkflowView, error) {
	if configID <= 0 {
		return WorkflowView{}, fmt.Errorf("config_id 必须大于 0")
	}
	if revision < 0 {
		return WorkflowView{}, fmt.Errorf("revision 不能小于 0")
	}
	actor = strings.TrimSpace(actor)
	if actor == "" || utf8.RuneCountInString(actor) > 100 {
		return WorkflowView{}, fmt.Errorf("修改主体不能为空且不能超过 100 个字符")
	}
	normalized, err := NormalizeAndValidate(spec, s.registry)
	if err != nil {
		return WorkflowView{}, err
	}
	var stableActorUserID *int64
	if actorUserID > 0 {
		stableActorUserID = &actorUserID
	}
	return s.definitions.SaveWorkflow(ctx, SaveWorkflowCommand{
		ConfigID:         configID,
		ExpectedRevision: revision,
		Actor:            actor,
		ActorUserID:      stableActorUserID,
		Spec:             normalized,
	})
}

// SnapshotTask upgrades an already inserted legacy TaskRecord with a workflow
// snapshot. New v2 publishing code should use CreateTask for full atomicity.
func (s *Service) SnapshotTask(ctx context.Context, executionStore ExecutionStore, taskID, configID int) (WorkflowView, error) {
	workflow, err := s.runnableWorkflow(ctx, configID)
	if err != nil {
		return WorkflowView{}, err
	}
	if err := executionStore.CreateTaskSnapshot(ctx, taskID, workflow); err != nil {
		return WorkflowView{}, err
	}
	return workflow, nil
}

// CreateTask is the normal v2 publishing entry point. It inserts TaskRecord
// and every TaskStepRecord in one database transaction.
func (s *Service) CreateTask(ctx context.Context, executionStore ExecutionStore, configID int, task *entity.TaskRecord) (WorkflowView, error) {
	if task == nil {
		return WorkflowView{}, fmt.Errorf("任务不能为空")
	}
	creator, ok := executionStore.(AtomicTaskCreator)
	if !ok {
		return WorkflowView{}, fmt.Errorf("执行存储不支持原子创建任务")
	}
	workflow, err := s.runnableWorkflow(ctx, configID)
	if err != nil {
		return WorkflowView{}, err
	}
	if err := creator.CreateTaskWithSnapshot(ctx, task, workflow); err != nil {
		return WorkflowView{}, err
	}
	return workflow, nil
}

func (s *Service) runnableWorkflow(ctx context.Context, configID int) (WorkflowView, error) {
	workflow, err := s.GetCurrent(ctx, configID)
	if err != nil {
		return WorkflowView{}, err
	}
	for _, step := range workflow.Spec.Steps {
		executor, found := s.registry.Get(step.Uses)
		if !found {
			return WorkflowView{}, fmt.Errorf("执行器未注册：%s", step.Uses)
		}
		if checker, ok := executor.(AvailabilityChecker); ok {
			if err := checker.Available(ctx); err != nil {
				return WorkflowView{}, fmt.Errorf("执行器 %s 当前不可用：%w", step.Uses, err)
			}
		}
	}
	return workflow, nil
}
