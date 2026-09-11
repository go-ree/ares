package workflow

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/go-ree/ares/internal/entity"
	"github.com/go-ree/ares/internal/security"
)

type Coordinator struct {
	store    LeasedExecutionStore
	registry *Registry
}

type AdvanceResult struct {
	TaskID      int    `json:"task_id"`
	TaskStatus  string `json:"task_status"`
	StepKey     string `json:"step_key,omitempty"`
	StepStatus  string `json:"step_status,omitempty"`
	Blocked     bool   `json:"blocked"`
	Terminal    bool   `json:"terminal"`
	PollBackoff bool   `json:"-"`
}

func NewCoordinator(store LeasedExecutionStore, registry *Registry) *Coordinator {
	return &Coordinator{store: store, registry: registry}
}

func (c *Coordinator) ListTaskSteps(ctx context.Context, taskID int) ([]entity.TaskStepRecord, error) {
	if taskID <= 0 {
		return nil, fmt.Errorf("task_id 必须大于 0")
	}
	return c.store.ListTaskSteps(ctx, taskID)
}

func (c *Coordinator) ListTaskStepViews(ctx context.Context, taskID int) ([]TaskStepView, error) {
	steps, err := c.ListTaskSteps(ctx, taskID)
	if err != nil {
		return nil, err
	}
	return c.TaskStepViews(steps), nil
}

// TaskStepViews projects internal snapshots into public views and derives
// capabilities only from the live executor registry.
func (c *Coordinator) TaskStepViews(steps []entity.TaskStepRecord) []TaskStepView {
	views := make([]TaskStepView, len(steps))
	for index, step := range steps {
		capabilities := Capabilities{}
		if c != nil && c.registry != nil {
			capabilities, _ = c.registry.Capabilities(step.Uses)
		}
		views[index] = taskStepView(step, capabilities)
	}
	return views
}

// Advance performs at most one executor call while holding one immutable task
// lease generation. Every read and write is fenced by the store; losing a
// lease stops this coordinator pass without converting cancellation into a
// business failure.
func (c *Coordinator) Advance(ctx context.Context, lease TaskLease) (AdvanceResult, error) {
	taskID := lease.TaskID
	steps, err := c.store.ListTaskStepsForLease(ctx, lease)
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
		if err := c.store.SetTaskStatus(ctx, lease, TaskFailed, message); err != nil {
			return AdvanceResult{}, err
		}
		return AdvanceResult{TaskID: taskID, TaskStatus: TaskFailed, Terminal: true}, nil
	}
	if len(running) == 1 {
		return c.reconcile(ctx, lease, running[0])
	}

	for _, step := range steps {
		if isUncertainTerminal(step.Status) {
			return c.stopUncertainTask(ctx, lease, step.StepKey, step.Status, step.Message)
		}
		if step.Status == StepFailed && step.OnFailure == FailureStop {
			if err := c.store.SkipPendingSteps(ctx, lease, "前置步骤失败，流程已停止"); err != nil {
				return AdvanceResult{}, err
			}
			if err := c.store.SetTaskStatus(ctx, lease, TaskFailed, step.Message); err != nil {
				return AdvanceResult{}, err
			}
			return AdvanceResult{TaskID: taskID, TaskStatus: TaskFailed, Terminal: true}, nil
		}
		if step.Status == StepCancelled {
			if err := c.store.SkipPendingSteps(ctx, lease, "流程已取消"); err != nil {
				return AdvanceResult{}, err
			}
			if err := c.store.SetTaskStatus(ctx, lease, TaskCancelled, step.Message); err != nil {
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
						StepStatus: StepPending, Blocked: true, PollBackoff: true,
					}, nil
				}
			}
		}
		claimed, err := c.store.ClaimStep(ctx, lease, step.StepRecordID)
		if err != nil {
			return AdvanceResult{}, err
		}
		if !claimed {
			return AdvanceResult{TaskID: taskID, TaskStatus: TaskRunning, Blocked: true}, nil
		}
		step.Status = StepRunning
		return c.start(ctx, lease, step, steps)
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
	if err := c.store.SetTaskStatus(ctx, lease, status, message); err != nil {
		return AdvanceResult{}, err
	}
	return AdvanceResult{TaskID: taskID, TaskStatus: status, Terminal: true}, nil
}

func (c *Coordinator) start(ctx context.Context, lease TaskLease, step entity.TaskStepRecord, all []entity.TaskStepRecord) (AdvanceResult, error) {
	taskID := lease.TaskID
	executor, found := c.registry.Get(step.Uses)
	if !found {
		return c.finishExecutorError(ctx, lease, step, fmt.Errorf("执行器未注册：%s", step.Uses))
	}
	if checker, ok := executor.(AvailabilityChecker); ok {
		if err := checker.Available(ctx); err != nil {
			return c.releaseUnavailableStep(ctx, lease, step)
		}
	}
	release, err := c.store.GetTaskReleaseContext(ctx, lease)
	if err != nil {
		return AdvanceResult{}, err
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
	remaining, err := c.store.StepTimeRemaining(ctx, lease, step.StepRecordID, step.TimeoutSeconds)
	if err != nil {
		return AdvanceResult{}, err
	}
	if remaining <= 0 {
		return c.finishTimedOut(ctx, lease, step, Result{})
	}
	callCtx, cancel := context.WithTimeout(ctx, remaining)
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
		if ctx.Err() != nil {
			return AdvanceResult{}, ctx.Err()
		}
		if callCtx.Err() != nil {
			return c.finishTimedOut(ctx, lease, step, result)
		}
		if errors.Is(err, ErrExecutorUnavailable) && !hasJSONValue(result.ExternalReference) {
			return c.releaseUnavailableStep(ctx, lease, step)
		}
		return c.finishStartError(ctx, lease, step, result, err)
	}
	if err := ctx.Err(); err != nil {
		return AdvanceResult{}, err
	}
	if err := callCtx.Err(); err != nil {
		return c.finishTimedOut(ctx, lease, step, result)
	}
	return c.applyResult(ctx, lease, step, result)
}

func (c *Coordinator) releaseUnavailableStep(ctx context.Context, lease TaskLease, step entity.TaskStepRecord) (AdvanceResult, error) {
	taskID := lease.TaskID
	released, err := c.store.ReleaseStep(ctx, lease, step.StepRecordID, "执行器暂不可用，等待重试")
	if err != nil {
		return AdvanceResult{}, err
	}
	stepStatus := StepPending
	if !released {
		stepStatus = StepRunning
	}
	return AdvanceResult{
		TaskID: taskID, TaskStatus: TaskRunning, StepKey: step.StepKey,
		StepStatus: stepStatus, Blocked: true, PollBackoff: true,
	}, nil
}

func (c *Coordinator) reconcile(ctx context.Context, lease TaskLease, step entity.TaskStepRecord) (AdvanceResult, error) {
	taskID := lease.TaskID
	remaining, err := c.store.StepTimeRemaining(ctx, lease, step.StepRecordID, step.TimeoutSeconds)
	if err != nil {
		return AdvanceResult{}, err
	}
	if remaining <= 0 {
		return c.finishTimedOut(ctx, lease, step, Result{})
	}
	callCtx, cancel := context.WithTimeout(ctx, remaining)
	defer cancel()
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
		return c.applyResult(ctx, lease, step, Result{State: ResultOutcomeUnknown, Message: "执行器未注册，无法确认外部执行结果；请核查外部任务"})
	}
	release, err := c.store.GetTaskReleaseContext(callCtx, lease)
	if err != nil {
		if ctx.Err() != nil {
			return AdvanceResult{}, ctx.Err()
		}
		if callCtx.Err() != nil {
			return c.finishTimedOut(ctx, lease, step, Result{})
		}
		return AdvanceResult{}, err
	}
	result, err := executor.Reconcile(callCtx, ReconcileRequest{
		TaskID:            taskID,
		StepKey:           step.StepKey,
		Attempt:           step.Attempt,
		IdempotencyKey:    idempotencyKey(taskID, step.StepKey, step.Attempt),
		Config:            append(json.RawMessage(nil), step.Config...),
		ExternalReference: append(json.RawMessage(nil), step.ExternalRef...),
		Release:           release,
	})
	if err != nil {
		if ctx.Err() != nil {
			return AdvanceResult{}, ctx.Err()
		}
		if callCtx.Err() != nil {
			return c.finishTimedOut(ctx, lease, step, result)
		}
		// Query failure is not proof that the external execution failed. Persist
		// any newly resolved reference (e.g. queue -> build) and poll the same
		// attempt with the worker's durable backoff until its original deadline.
		return c.applyResult(ctx, lease, step, Result{
			State: ResultUnknown, ExternalReference: result.ExternalReference,
			Message: "暂时无法查询执行结果，将继续查询；不会重新提交任务",
		})
	}
	if err := ctx.Err(); err != nil {
		return AdvanceResult{}, err
	}
	if err := callCtx.Err(); err != nil {
		return c.finishTimedOut(ctx, lease, step, result)
	}
	return c.applyResult(ctx, lease, step, result)
}

func (c *Coordinator) finishExecutorError(ctx context.Context, lease TaskLease, step entity.TaskStepRecord, executorErr error) (AdvanceResult, error) {
	taskID := lease.TaskID
	// Executor errors may contain internal URLs or untrusted upstream response
	// text. Keep public task history stable and free from raw provider details.
	slog.Warn("执行器调用失败", "task_id", taskID, "step_key", step.StepKey, "uses", step.Uses, "error_type", fmt.Sprintf("%T", executorErr))
	result := Result{State: ResultFailed, Message: "执行器调用失败，请检查服务端运行状态"}
	return c.applyResult(ctx, lease, step, result)
}

func (c *Coordinator) finishStartError(ctx context.Context, lease TaskLease, step entity.TaskStepRecord, result Result, executorErr error) (AdvanceResult, error) {
	slog.Warn("执行器提交结果不明确", "task_id", lease.TaskID, "step_key", step.StepKey, "uses", step.Uses, "error_type", fmt.Sprintf("%T", executorErr))
	return c.applyResult(ctx, lease, step, Result{
		State: ResultOutcomeUnknown, ExternalReference: result.ExternalReference,
		Message: "无法确认任务是否已提交或完成；请核查外部任务，避免重复执行",
	})
}

func (c *Coordinator) finishTimedOut(ctx context.Context, lease TaskLease, step entity.TaskStepRecord, result Result) (AdvanceResult, error) {
	return c.applyResult(ctx, lease, step, Result{
		State: ResultTimedOut, ExternalReference: result.ExternalReference,
		Message: "步骤总执行时限已耗尽；外部任务可能仍在运行，请核查后再操作",
	})
}

func isUncertainTerminal(status string) bool {
	return status == StepTimedOut || status == StepOutcomeUnknown
}

func (c *Coordinator) stopUncertainTask(ctx context.Context, lease TaskLease, stepKey, status, message string) (AdvanceResult, error) {
	if err := c.store.SkipPendingSteps(ctx, lease, "前置步骤超时或结果不明确，流程已停止；请核查外部任务"); err != nil {
		return AdvanceResult{}, err
	}
	if err := c.store.SetTaskStatus(ctx, lease, status, message); err != nil {
		return AdvanceResult{}, err
	}
	return AdvanceResult{TaskID: lease.TaskID, TaskStatus: status, StepKey: stepKey, StepStatus: status, Terminal: true}, nil
}

func (c *Coordinator) applyResult(ctx context.Context, lease TaskLease, step entity.TaskStepRecord, result Result) (AdvanceResult, error) {
	taskID := lease.TaskID
	if !validResultState(result.State) {
		// A misbehaving executor may already have created an external resource.
		// Preserve its opaque reference for audit/log lookup while rejecting the
		// invalid state and any output it supplied.
		result.State = ResultOutcomeUnknown
		result.Output = nil
		result.Message = "执行器返回了无效状态，无法确认外部结果；请核查外部任务"
	}
	if hasJSONValue(result.Output) {
		if err := security.ValidateJSONNoSensitiveKeys(result.Output, "executor.output"); err != nil {
			if result.State == ResultRunning || result.State == ResultUnknown {
				result.State = ResultOutcomeUnknown
			} else if !isUncertainTerminal(result.State) {
				result.State = ResultFailed
			}
			result.Output = nil
			result.Message = "执行器输出不符合安全策略，已拒绝持久化"
		}
	}
	// Reconcile implementations may return only a new state. Keep the opaque
	// reference owned by the executor so terminal history and logs remain usable.
	if !hasJSONValue(result.ExternalReference) && hasJSONValue(step.ExternalRef) {
		result.ExternalReference = append(json.RawMessage(nil), step.ExternalRef...)
	}
	saved, err := c.store.SaveStepResult(ctx, lease, step.StepRecordID, result)
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
		response.PollBackoff = result.State == ResultUnknown
		return response, nil
	}
	if isUncertainTerminal(stepStatus) {
		return c.stopUncertainTask(ctx, lease, step.StepKey, stepStatus, result.Message)
	}
	if stepStatus == StepFailed && step.OnFailure == FailureStop {
		if err := c.store.SkipPendingSteps(ctx, lease, "前置步骤失败，流程已停止"); err != nil {
			return AdvanceResult{}, err
		}
		if err := c.store.SetTaskStatus(ctx, lease, TaskFailed, result.Message); err != nil {
			return AdvanceResult{}, err
		}
		response.TaskStatus = TaskFailed
		response.Terminal = true
	}
	if stepStatus == StepCancelled {
		if err := c.store.SkipPendingSteps(ctx, lease, "流程已取消"); err != nil {
			return AdvanceResult{}, err
		}
		if err := c.store.SetTaskStatus(ctx, lease, TaskCancelled, result.Message); err != nil {
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
func (c *Coordinator) RunUntilBlocked(ctx context.Context, lease TaskLease, maxTransitions int) (AdvanceResult, error) {
	taskID := lease.TaskID
	if maxTransitions <= 0 {
		maxTransitions = 101
	}
	var result AdvanceResult
	for i := 0; i < maxTransitions; i++ {
		var err error
		result, err = c.Advance(ctx, lease)
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
	case ResultRunning, ResultSucceeded, ResultFailed, ResultCancelled, ResultUnknown, ResultTimedOut, ResultOutcomeUnknown:
		return true
	default:
		return false
	}
}
