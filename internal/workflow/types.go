package workflow

import (
	"context"
	"encoding/json"
)

const (
	SchemaVersionV1 = 1

	FailureStop     = "stop"
	FailureContinue = "continue"

	StepPending   = "pending"
	StepRunning   = "running"
	StepSucceeded = "succeeded"
	StepFailed    = "failed"
	StepCancelled = "cancelled"
	StepSkipped   = "skipped"

	TaskQueued                = "queued"
	TaskRunning               = "running"
	TaskSucceeded             = "succeeded"
	TaskFailed                = "failed"
	TaskCancelled             = "cancelled"
	TaskSucceededWithWarnings = "succeeded_with_warnings"

	ResultRunning   = "running"
	ResultSucceeded = "succeeded"
	ResultFailed    = "failed"
	ResultCancelled = "cancelled"
	ResultUnknown   = "unknown"
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
}

type Capabilities struct {
	Logs   bool `json:"logs"`
	Cancel bool `json:"cancel"`
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
