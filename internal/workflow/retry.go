package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/go-ree/ares/internal/entity"
)

const RetryNoSideEffect = "no_side_effect"
const RetryCompletedSafe = "completed_safe"

var ErrRetryConflict = errors.New("任务状态、尝试编号或安全重试条件已变化")

type RetryPolicy struct {
	MaxAttempts         int    `json:"max_attempts"`
	InitialDelaySeconds int    `json:"initial_delay_seconds,omitempty"`
	MaxDelaySeconds     int    `json:"max_delay_seconds,omitempty"`
	Mode                string `json:"mode,omitempty"`
}

func normalizeRetry(p *RetryPolicy) RetryPolicy {
	if p == nil {
		return RetryPolicy{MaxAttempts: 1, InitialDelaySeconds: 1, MaxDelaySeconds: 60, Mode: "automatic"}
	}
	value := *p
	if value.InitialDelaySeconds == 0 {
		value.InitialDelaySeconds = 1
	}
	if value.MaxDelaySeconds == 0 {
		value.MaxDelaySeconds = 60
	}
	if value.Mode == "" {
		value.Mode = "automatic"
	}
	return value
}

func safeRetryClass(value string) bool {
	return value == RetryNoSideEffect || value == RetryCompletedSafe
}
func retryEligible(step entity.TaskStepRecord) bool {
	return step.Status == StepFailed && step.Attempt >= 1 && step.Attempt < step.MaxAttempts && step.MaxAttempts <= 5 && safeRetryClass(step.RetryClass)
}
func retryDelay(step entity.TaskStepRecord) int {
	delay := step.RetryDelaySeconds
	for i := 1; i < step.Attempt && delay < step.RetryMaxDelaySeconds; i++ {
		delay *= 2
	}
	if delay > step.RetryMaxDelaySeconds {
		delay = step.RetryMaxDelaySeconds
	}
	return delay
}

type AttemptView struct {
	Attempt    int        `json:"attempt"`
	Status     string     `json:"status"`
	Message    string     `json:"message,omitempty"`
	StartedAt  *time.Time `json:"started_at,omitempty" xorm:"'started_at'"`
	FinishedAt *time.Time `json:"finished_at,omitempty" xorm:"'finished_at'"`
}

// RetryStore extends leased execution; the optional boundary keeps simple
// embedders without retries compatible while production always uses XORMStore.
type RetryStore interface {
	AdvanceRetry(context.Context, TaskLease, int64) (string, error)
	RequestRetry(context.Context, int, string, int, string) error
	ListAttempts(context.Context, int, string) ([]AttemptView, error)
}

func (c *Coordinator) ListAttempts(ctx context.Context, taskID int, stepKey string) ([]AttemptView, error) {
	store, ok := c.store.(RetryStore)
	if !ok {
		return nil, ErrNotFound
	}
	return store.ListAttempts(ctx, taskID, stepKey)
}

func (c *Coordinator) RequestRetry(ctx context.Context, taskID int, stepKey string, expected int) error {
	if taskID <= 0 || !ValidStepKey(stepKey) || expected < 1 || expected >= 5 {
		return ErrRetryConflict
	}
	steps, err := c.store.ListTaskSteps(ctx, taskID)
	if err != nil {
		return err
	}
	for _, step := range steps {
		if step.StepKey != stepKey {
			continue
		}
		caps, _ := c.registry.Capabilities(step.Uses)
		if !caps.Retry {
			return ErrRetryConflict
		}
		store, ok := c.store.(RetryStore)
		if !ok {
			return ErrRetryConflict
		}
		return store.RequestRetry(ctx, taskID, stepKey, expected, step.Uses)
	}
	return ErrNotFound
}

func (c *Coordinator) advanceRetry(ctx context.Context, lease TaskLease, step entity.TaskStepRecord) (AdvanceResult, bool, error) {
	store, ok := c.store.(RetryStore)
	if !ok {
		if step.Status == StepRetryWait {
			return AdvanceResult{}, true, ErrRetryConflict
		}
		return AdvanceResult{}, false, nil
	}
	if step.Status != StepRetryWait && !(retryEligible(step) && step.RetryMode == "automatic") {
		return AdvanceResult{}, false, nil
	}
	caps, _ := c.registry.Capabilities(step.Uses)
	if !caps.Retry {
		// A removed capability cannot result in another Start.
		return AdvanceResult{TaskID: lease.TaskID, TaskStatus: TaskRunning, StepStatus: step.Status, Blocked: true, PollBackoff: true}, true, nil
	}
	status, err := store.AdvanceRetry(ctx, lease, step.StepRecordID)
	if err != nil {
		return AdvanceResult{}, true, err
	}
	if status != StepRetryWait && status != StepPending {
		return AdvanceResult{}, false, nil
	}
	return AdvanceResult{TaskID: lease.TaskID, TaskStatus: TaskRunning, StepKey: step.StepKey, StepStatus: status, Blocked: status == StepRetryWait}, true, nil
}

// Persist only an explicitly classified failed result from a capable adapter.
func (c *Coordinator) classifyRetry(uses string, result *Result) {
	caps, _ := c.registry.Capabilities(uses)
	if result.State != ResultFailed || !caps.Retry || !safeRetryClass(result.RetryClass) {
		result.RetryClass = ""
	}
}

// JSON reference copies never cross the public AttemptView boundary.
func copyAttemptReference(value []byte) json.RawMessage {
	return append(json.RawMessage(nil), value...)
}
