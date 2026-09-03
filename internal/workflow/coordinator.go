package workflow

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"ares/internal/entity"
	"ares/internal/security"
)

type Coordinator struct {
	store    ExecutionStore
	registry *Registry
	now      func() time.Time
}

type AdvanceResult struct {
	TaskID     int    `json:"task_id"`
	TaskStatus string `json:"task_status"`
	StepKey    string `json:"step_key,omitempty"`
	StepStatus string `json:"step_status,omitempty"`
	Blocked    bool   `json:"blocked"`
	Terminal   bool   `json:"terminal"`
}

func NewCoordinator(store ExecutionStore, registry *Registry) *Coordinator {
	return &Coordinator{store: store, registry: registry, now: time.Now}
}

func (c *Coordinator) ListTaskSteps(ctx context.Context, taskID int) ([]entity.TaskStepRecord, error) {
	if taskID <= 0 {
		return nil, fmt.Errorf("task_id 必须大于 0")
	}
	return c.store.ListTaskSteps(ctx, taskID)
}

// Advance performs at most one executor call. Database CAS in ClaimStep makes
// concurrent workers safe: losers observe claimed=false and do no external IO.
func (c *Coordinator) Advance(ctx context.Context, taskID int) (AdvanceResult, error) {
	steps, err := c.store.ListTaskSteps(ctx, taskID)
	if err != nil {
		return AdvanceResult{}, err
	}
	if len(steps) == 0 {
		return AdvanceResult{}, fmt.Errorf("任务 %d 没有步骤快照: %w", taskID, ErrNotFound)
	}

	running := make([]entity.TaskStepRecord, 0, 1)
	for _, step := range steps {
		if step.Status == StepRunning {
			running = append(running, step)
		}
	}
	if len(running) > 1 {
		message := "检测到多个同时运行的串行步骤"
		_ = c.store.SetTaskStatus(ctx, taskID, TaskFailed, message)
		return AdvanceResult{}, fmt.Errorf("任务 %d 状态损坏：%s", taskID, message)
	}
	if len(running) == 1 {
		return c.reconcile(ctx, taskID, running[0])
	}

	for _, step := range steps {
		if step.Status == StepFailed && step.OnFailure == FailureStop {
			if err := c.store.SkipPendingSteps(ctx, taskID, "前置步骤失败，流程已停止"); err != nil {
				return AdvanceResult{}, err
			}
			if err := c.store.SetTaskStatus(ctx, taskID, TaskFailed, step.Message); err != nil {
				return AdvanceResult{}, err
			}
			return AdvanceResult{TaskID: taskID, TaskStatus: TaskFailed, Terminal: true}, nil
		}
		if step.Status == StepCancelled {
			if err := c.store.SkipPendingSteps(ctx, taskID, "流程已取消"); err != nil {
				return AdvanceResult{}, err
			}
			if err := c.store.SetTaskStatus(ctx, taskID, TaskCancelled, step.Message); err != nil {
				return AdvanceResult{}, err
			}
			return AdvanceResult{TaskID: taskID, TaskStatus: TaskCancelled, Terminal: true}, nil
		}
	}

	for _, step := range steps {
		if step.Status != StepPending {
			continue
		}
		// Availability is intentionally checked before the CAS claim. Persisted
		// integrations are restored asynchronously during startup; claiming first
		// would turn a healthy-but-still-loading Jenkins step into a permanent
		// failure. Leave it pending and let the bounded worker poll again.
		if executor, found := c.registry.Get(step.Uses); found {
			if checker, ok := executor.(AvailabilityChecker); ok {
				if err := checker.Available(ctx); err != nil {
					return AdvanceResult{
						TaskID: taskID, TaskStatus: TaskQueued, StepKey: step.StepKey,
						StepStatus: StepPending, Blocked: true,
					}, nil
				}
			}
		}
		claimed, err := c.store.ClaimStep(ctx, step.StepRecordID)
		if err != nil {
			return AdvanceResult{}, err
		}
		if !claimed {
			return AdvanceResult{TaskID: taskID, TaskStatus: TaskRunning, Blocked: true}, nil
		}
		if err := c.store.SetTaskStatus(ctx, taskID, TaskRunning, ""); err != nil {
			return AdvanceResult{}, err
		}
		step.Status = StepRunning
		now := c.now()
		step.StartedTime = &now
		return c.start(ctx, taskID, step, steps)
	}

	status := TaskSucceeded
	message := ""
	for _, step := range steps {
		if step.Status == StepFailed {
			status = TaskSucceededWithWarnings
			message = "部分步骤失败，但按 continue 策略完成"
			break
		}
	}
	if err := c.store.SetTaskStatus(ctx, taskID, status, message); err != nil {
		return AdvanceResult{}, err
	}
	return AdvanceResult{TaskID: taskID, TaskStatus: status, Terminal: true}, nil
}

func (c *Coordinator) start(ctx context.Context, taskID int, step entity.TaskStepRecord, all []entity.TaskStepRecord) (AdvanceResult, error) {
	executor, found := c.registry.Get(step.Uses)
	if !found {
		return c.finishExecutorError(ctx, taskID, step, fmt.Errorf("执行器未注册：%s", step.Uses))
	}
	if checker, ok := executor.(AvailabilityChecker); ok {
		if err := checker.Available(ctx); err != nil {
			return c.releaseUnavailableStep(ctx, taskID, step)
		}
	}
	release, err := c.store.GetTaskReleaseContext(ctx, taskID)
	if err != nil {
		return c.finishExecutorError(ctx, taskID, step, err)
	}
	previous := make(map[string]json.RawMessage)
	for _, candidate := range all {
		if candidate.Position >= step.Position {
			break
		}
		if candidate.Status == StepSucceeded && hasJSONValue(candidate.Output) {
			previous[candidate.StepKey] = append(json.RawMessage(nil), candidate.Output...)
		}
	}
	callCtx, cancel := context.WithTimeout(ctx, time.Duration(step.TimeoutSeconds)*time.Second)
	defer cancel()
	result, err := executor.Start(callCtx, StartRequest{
		TaskID:         taskID,
		StepKey:        step.StepKey,
		Attempt:        step.Attempt,
		IdempotencyKey: idempotencyKey(taskID, step.StepKey, step.Attempt),
		Config:         append(json.RawMessage(nil), step.Config...),
		Release:        release,
		PreviousOutput: previous,
	})
	if err != nil {
		if errors.Is(err, ErrExecutorUnavailable) {
			return c.releaseUnavailableStep(ctx, taskID, step)
		}
		return c.finishExecutorError(ctx, taskID, step, err)
	}
	return c.applyResult(ctx, taskID, step, result)
}

func (c *Coordinator) releaseUnavailableStep(ctx context.Context, taskID int, step entity.TaskStepRecord) (AdvanceResult, error) {
	released, err := c.store.ReleaseStep(ctx, step.StepRecordID, "执行器暂不可用，等待重试")
	if err != nil {
		return AdvanceResult{}, err
	}
	stepStatus := StepPending
	if !released {
		stepStatus = StepRunning
	}
	return AdvanceResult{
		TaskID: taskID, TaskStatus: TaskRunning, StepKey: step.StepKey,
		StepStatus: stepStatus, Blocked: true,
	}, nil
}

func (c *Coordinator) reconcile(ctx context.Context, taskID int, step entity.TaskStepRecord) (AdvanceResult, error) {
	if step.StartedTime != nil && step.TimeoutSeconds > 0 && c.now().After(step.StartedTime.Add(time.Duration(step.TimeoutSeconds)*time.Second)) {
		return c.finishExecutorError(ctx, taskID, step, fmt.Errorf("步骤执行超时"))
	}
	// A worker may observe the CAS claim while the winning worker is still in
	// Start. Until Start persists an opaque reference there is nothing safe to
	// reconcile. A crashed starter is eventually handled by the timeout above.
	if !hasJSONValue(step.ExternalRef) {
		return AdvanceResult{
			TaskID: taskID, TaskStatus: TaskRunning, StepKey: step.StepKey,
			StepStatus: StepRunning, Blocked: true,
		}, nil
	}
	executor, found := c.registry.Get(step.Uses)
	if !found {
		return c.finishExecutorError(ctx, taskID, step, fmt.Errorf("执行器未注册：%s", step.Uses))
	}
	release, err := c.store.GetTaskReleaseContext(ctx, taskID)
	if err != nil {
		return AdvanceResult{}, err
	}
	result, err := executor.Reconcile(ctx, ReconcileRequest{
		TaskID:            taskID,
		StepKey:           step.StepKey,
		Attempt:           step.Attempt,
		IdempotencyKey:    idempotencyKey(taskID, step.StepKey, step.Attempt),
		Config:            append(json.RawMessage(nil), step.Config...),
		ExternalReference: append(json.RawMessage(nil), step.ExternalRef...),
		Release:           release,
	})
	if err != nil {
		return AdvanceResult{}, err
	}
	return c.applyResult(ctx, taskID, step, result)
}

func (c *Coordinator) finishExecutorError(ctx context.Context, taskID int, step entity.TaskStepRecord, executorErr error) (AdvanceResult, error) {
	// Executor errors may contain internal URLs or untrusted upstream response
	// text. Keep public task history stable and free from raw provider details.
	slog.Warn("执行器调用失败", "task_id", taskID, "step_key", step.StepKey, "uses", step.Uses, "error_type", fmt.Sprintf("%T", executorErr))
	result := Result{State: ResultFailed, Message: "执行器调用失败，请检查服务端运行状态"}
	return c.applyResult(ctx, taskID, step, result)
}

func (c *Coordinator) applyResult(ctx context.Context, taskID int, step entity.TaskStepRecord, result Result) (AdvanceResult, error) {
	if !validResultState(result.State) {
		// A misbehaving executor may already have created an external resource.
		// Preserve its opaque reference for audit/log lookup while rejecting the
		// invalid state and any output it supplied.
		result.State = ResultFailed
		result.Output = nil
		result.Message = "执行器返回了无效状态"
	}
	if hasJSONValue(result.Output) {
		if err := security.ValidateJSONNoSensitiveKeys(result.Output, "executor.output"); err != nil {
			result.State = ResultFailed
			result.Output = nil
			result.Message = "执行器输出不符合安全策略，已拒绝持久化"
		}
	}
	// Reconcile implementations may return only a new state. Keep the opaque
	// reference owned by the executor so terminal history and logs remain usable.
	if !hasJSONValue(result.ExternalReference) && hasJSONValue(step.ExternalRef) {
		result.ExternalReference = append(json.RawMessage(nil), step.ExternalRef...)
	}
	saved, err := c.store.SaveStepResult(ctx, step.StepRecordID, result)
	if err != nil {
		return AdvanceResult{}, err
	}
	if !saved {
		return AdvanceResult{TaskID: taskID, TaskStatus: TaskRunning, Blocked: true}, nil
	}
	stepStatus := result.State
	if stepStatus == ResultUnknown {
		stepStatus = StepRunning
	}
	response := AdvanceResult{TaskID: taskID, TaskStatus: TaskRunning, StepKey: step.StepKey, StepStatus: stepStatus}
	if stepStatus == StepRunning {
		response.Blocked = true
		return response, nil
	}
	if stepStatus == StepFailed && step.OnFailure == FailureStop {
		if err := c.store.SkipPendingSteps(ctx, taskID, "前置步骤失败，流程已停止"); err != nil {
			return AdvanceResult{}, err
		}
		if err := c.store.SetTaskStatus(ctx, taskID, TaskFailed, result.Message); err != nil {
			return AdvanceResult{}, err
		}
		response.TaskStatus = TaskFailed
		response.Terminal = true
	}
	if stepStatus == StepCancelled {
		if err := c.store.SkipPendingSteps(ctx, taskID, "流程已取消"); err != nil {
			return AdvanceResult{}, err
		}
		if err := c.store.SetTaskStatus(ctx, taskID, TaskCancelled, result.Message); err != nil {
			return AdvanceResult{}, err
		}
		response.TaskStatus = TaskCancelled
		response.Terminal = true
	}
	return response, nil
}

func hasJSONValue(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) > 0 && !bytes.Equal(trimmed, []byte("null"))
}

// RunUntilBlocked advances synchronous steps until the task reaches an async
// boundary or a terminal state. The bound prevents a faulty store/executor from
// causing an unbounded loop.
func (c *Coordinator) RunUntilBlocked(ctx context.Context, taskID, maxTransitions int) (AdvanceResult, error) {
	if maxTransitions <= 0 {
		maxTransitions = 101
	}
	var result AdvanceResult
	for i := 0; i < maxTransitions; i++ {
		var err error
		result, err = c.Advance(ctx, taskID)
		if err != nil {
			return AdvanceResult{}, err
		}
		if result.Blocked || result.Terminal {
			return result, nil
		}
	}
	return result, fmt.Errorf("任务 %d 单次推进超过 %d 次状态转换", taskID, maxTransitions)
}

func idempotencyKey(taskID int, stepKey string, attempt int) string {
	return fmt.Sprintf("%d/%s/%d", taskID, stepKey, attempt)
}

func validResultState(state string) bool {
	switch state {
	case ResultRunning, ResultSucceeded, ResultFailed, ResultCancelled, ResultUnknown:
		return true
	default:
		return false
	}
}
