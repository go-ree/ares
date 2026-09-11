package workflow

import (
	"context"
	"encoding/json"
	"time"

	"github.com/go-ree/ares/internal/entity"
)

const (
	SchemaVersionV1 = 1

	FailureStop     = "stop"
	FailureContinue = "continue"

	StepPending        = "pending"
	StepRetryWait      = "retry_wait"
	StepRunning        = "running"
	StepSucceeded      = "succeeded"
	StepFailed         = "failed"
	StepTimedOut       = "timed_out"
	StepOutcomeUnknown = "outcome_unknown"
	StepCancelled      = "cancelled"
	StepSkipped        = "skipped"

	TaskQueued                = "queued"
	TaskRunning               = "running"
	TaskSucceeded             = "succeeded"
	TaskFailed                = "failed"
	TaskTimedOut              = "timed_out"
	TaskOutcomeUnknown        = "outcome_unknown"
	TaskCancelled             = "cancelled"
	TaskSucceededWithWarnings = "succeeded_with_warnings"

	ResultRunning        = "running"
	ResultSucceeded      = "succeeded"
	ResultFailed         = "failed"
	ResultCancelled      = "cancelled"
	ResultUnknown        = "unknown"
	ResultTimedOut       = "timed_out"
	ResultOutcomeUnknown = "outcome_unknown"
)

type WorkflowSpec struct {
	SchemaVersion int        `json:"schema_version"`
	Name          string     `json:"name"`
	Steps         []StepSpec `json:"steps"`
}

type StepSpec struct {
	Key            string          `json:"key"`
	Name           string          `json:"name"`
	Uses           string          `json:"uses"`
	Category       string          `json:"category,omitempty"`
	With           json.RawMessage `json:"with" swaggertype:"object"`
	TimeoutSeconds int             `json:"timeout_seconds,omitempty"`
	OnFailure      string          `json:"on_failure,omitempty"`
	Retry          *RetryPolicy    `json:"retry,omitempty"`
}

type Capabilities struct {
	Retry  bool `json:"retry"`
	Logs   bool `json:"logs"`
	Cancel bool `json:"cancel"`
}

// TaskStepView is the public projection of a persisted task-step snapshot.
// Executor config, external references, and opaque output are deliberately
// absent. Capabilities are derived from the live registry, never persisted.
type TaskStepView struct {
	StepRecordID      int64        `json:"step_record_id"`
	TaskID            int          `json:"task_id"`
	WorkflowVersionID int64        `json:"workflow_version_id"`
	StepKey           string       `json:"step_key"`
	Name              string       `json:"name"`
	Uses              string       `json:"uses"`
	Category          string       `json:"category,omitempty"`
	Position          int          `json:"position"`
	TimeoutSeconds    int          `json:"timeout_seconds"`
	OnFailure         string       `json:"on_failure"`
	Status            string       `json:"status"`
	Attempt           int          `json:"attempt"`
	MaxAttempts       int          `json:"max_attempts"`
	RetryAt           *time.Time   `json:"retry_at,omitempty"`
	RetryEligible     bool         `json:"retry_eligible"`
	Message           string       `json:"message,omitempty"`
	StartedTime       *time.Time   `json:"started_at,omitempty"`
	FinishedTime      *time.Time   `json:"finished_at,omitempty"`
	CreatedTime       time.Time    `json:"created_at"`
	UpdatedTime       time.Time    `json:"updated_at"`
	Capabilities      Capabilities `json:"capabilities"`
}

func taskStepView(record entity.TaskStepRecord, capabilities Capabilities) TaskStepView {
	return TaskStepView{
		StepRecordID: record.StepRecordID, TaskID: record.TaskID,
		WorkflowVersionID: record.WorkflowVersionID, StepKey: record.StepKey,
		Name: record.Name, Uses: record.Uses, Category: record.Category,
		Position: record.Position, TimeoutSeconds: record.TimeoutSeconds,
		OnFailure: record.OnFailure, Status: record.Status, Attempt: record.Attempt,
		Message: record.Message, StartedTime: record.StartedTime,
		FinishedTime: record.FinishedTime, CreatedTime: record.CreatedTime,
		UpdatedTime: record.UpdatedTime, Capabilities: capabilities,
		MaxAttempts: record.MaxAttempts, RetryAt: record.RetryAt,
		RetryEligible: capabilities.Retry && retryEligible(record) && record.OnFailure == FailureStop,
	}
}

type Descriptor struct {
	Uses              string          `json:"uses"`
	Name              string          `json:"name"`
	Description       string          `json:"description"`
	Available         bool            `json:"available"`
	UnavailableReason string          `json:"unavailable_reason"`
	ConfigSchema      json.RawMessage `json:"config_schema" swaggertype:"object"`
	Capabilities      Capabilities    `json:"capabilities"`
}

type ReleaseContext struct {
	AppName   string          `json:"app_name"`
	Env       string          `json:"env"`
	Ref       string          `json:"ref"`
	Publisher string          `json:"publisher"`
	Inputs    json.RawMessage `json:"inputs,omitempty"`
}

type StartRequest struct {
	TaskID         int
	StepKey        string
	Attempt        int
	IdempotencyKey string
	Config         json.RawMessage
	Release        ReleaseContext
	PreviousOutput map[string]json.RawMessage
}

type ReconcileRequest struct {
	TaskID            int
	StepKey           string
	Attempt           int
	IdempotencyKey    string
	Config            json.RawMessage
	ExternalReference json.RawMessage
	Release           ReleaseContext
}

type Result struct {
	RetryClass        string          `json:"-"`
	State             string          `json:"state"`
	ExternalReference json.RawMessage `json:"external_reference,omitempty"`
	Output            json.RawMessage `json:"output,omitempty"`
	Message           string          `json:"message,omitempty"`
}

type Executor interface {
	Descriptor() Descriptor
	Validate(config json.RawMessage) error
	Start(context.Context, StartRequest) (Result, error)
	Reconcile(context.Context, ReconcileRequest) (Result, error)
}

type AvailabilityChecker interface {
	Available(context.Context) error
}

type LogReader interface {
	ReadLogs(context.Context, LogRequest) (LogChunk, error)
}

type Canceller interface {
	Cancel(context.Context, CancelRequest) error
}

type LogRequest struct {
	TaskID            int
	StepKey           string
	ExternalReference json.RawMessage
	Cursor            string
}

type LogChunk struct {
	Content string `json:"content"`
	Cursor  string `json:"cursor,omitempty"`
	EOF     bool   `json:"eof"`
}

type CancelRequest struct {
	TaskID            int
	StepKey           string
	ExternalReference json.RawMessage
}

type WorkflowView struct {
	ConfigID          int          `json:"config_id"`
	WorkflowID        int64        `json:"workflow_id"`
	WorkflowVersionID int64        `json:"workflow_version_id"`
	Version           int          `json:"version"`
	Revision          int          `json:"revision"`
	Spec              WorkflowSpec `json:"spec"`
}
