package publish

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/go-ree/ares/internal/entity"
	"github.com/go-ree/ares/internal/workflow"
)

const (
	SemanticReleaseCreate      = "release.create@v1"
	SemanticReleaseBatchCreate = "release.batch.create@v1"

	ReleaseOutcomeAccepted = "accepted"
	ReleaseOutcomeRejected = "rejected"
)

const (
	ReleaseCodeInvalidRequest               = "invalid_request"
	ReleaseCodeRequestTooLarge              = "request_too_large"
	ReleaseCodeTargetNotFound               = "release_target_not_found"
	ReleaseCodeEnvironmentDisabled          = "environment_disabled"
	ReleaseCodeWorkflowNotConfigured        = "workflow_not_configured"
	ReleaseCodeWorkflowVersionChanged       = "workflow_version_changed"
	ReleaseCodeWorkflowInvalid              = "workflow_invalid"
	ReleaseCodeExecutorUnavailable          = "executor_unavailable"
	ReleaseCodeIdempotencyKeyInvalid        = "idempotency_key_invalid"
	ReleaseCodeIdempotencyKeyConflict       = "idempotency_key_conflict"
	ReleaseCodeIdempotencyRequestInProgress = "idempotency_request_in_progress"
	ReleaseCodeOutcomeUnknown               = "outcome_unknown"
	ReleaseCodeInternalError                = "internal_error"
)

// ReleaseCommand is the normalized, platform-neutral intent used by both the
// canonical API and the legacy adapter. Inputs are retained as JSON so request
// digests never pass through float64.
type ReleaseCommand struct {
	ConfigID                  int             `json:"config_id"`
	Ref                       string          `json:"ref"`
	Inputs                    json.RawMessage `json:"inputs,omitempty" swaggertype:"object"`
	ExpectedWorkflowVersionID *int64          `json:"expected_workflow_version_id,omitempty,string" swaggertype:"string" format:"int64"`
}

// CreateReleaseRequest is used by the config-scoped single release endpoint.
// ConfigID comes from the path and is added before validation/digesting.
type CreateReleaseRequest struct {
	Ref                       string          `json:"ref"`
	Inputs                    json.RawMessage `json:"inputs,omitempty" swaggertype:"object"`
	ExpectedWorkflowVersionID *int64          `json:"expected_workflow_version_id,omitempty,string" swaggertype:"string" format:"int64"`
}

type CreateBatchReleaseRequest struct {
	Items []ReleaseCommand `json:"items"`
}

type ReleaseStepPreflight struct {
	Key          string                `json:"key"`
	Name         string                `json:"name"`
	Uses         string                `json:"uses"`
	Available    bool                  `json:"available"`
	Capabilities workflow.Capabilities `json:"capabilities"`
	ErrorCode    string                `json:"error_code,omitempty"`
}

type ReleasePreflight struct {
	RequestIndex      int                    `json:"request_index"`
	ConfigID          int                    `json:"config_id"`
	AppID             int                    `json:"app_id,omitempty"`
	AppName           string                 `json:"app_name,omitempty"`
	AppNameCN         string                 `json:"app_name_cn,omitempty"`
	Env               string                 `json:"env,omitempty"`
	WorkflowID        int64                  `json:"workflow_id,omitempty,string" swaggertype:"string" format:"int64"`
	WorkflowVersionID int64                  `json:"workflow_version_id,omitempty,string" swaggertype:"string" format:"int64"`
	WorkflowVersion   int                    `json:"workflow_version,omitempty"`
	WorkflowRevision  int                    `json:"workflow_revision,omitempty"`
	Ready             bool                   `json:"ready"`
	ErrorCode         string                 `json:"error_code,omitempty"`
	Steps             []ReleaseStepPreflight `json:"steps"`
}

type BatchReleasePreflight struct {
	TotalCount   int                `json:"total_count"`
	ReadyCount   int                `json:"ready_count"`
	FailureCount int                `json:"failure_count"`
	Items        []ReleasePreflight `json:"items"`
}

type ReleaseReceiptItem struct {
	RequestIndex      int    `json:"request_index"`
	ConfigID          int    `json:"config_id"`
	Success           bool   `json:"success"`
	TaskID            *int   `json:"task_id,omitempty"`
	WorkflowVersionID *int64 `json:"workflow_version_id,omitempty,string" swaggertype:"string" format:"int64"`
	ErrorCode         string `json:"error_code,omitempty"`
}

type ReleaseReceipt struct {
	RecordID     int64                `json:"record_id,string" swaggertype:"string" format:"int64"`
	TotalCount   int                  `json:"total_count"`
	SuccessCount int                  `json:"success_count"`
	FailureCount int                  `json:"failure_count"`
	Items        []ReleaseReceiptItem `json:"items"`
}

type ReleaseTarget struct {
	ConfigID          int                    `json:"config_id,omitempty"`
	AppID             int                    `json:"app_id"`
	AppName           string                 `json:"app_name"`
	AppNameCN         string                 `json:"app_name_cn"`
	Env               string                 `json:"env"`
	WorkflowVersionID int64                  `json:"workflow_version_id,omitempty,string" swaggertype:"string" format:"int64"`
	Available         bool                   `json:"available"`
	UnavailableCode   string                 `json:"unavailable_code,omitempty"`
	Steps             []ReleaseStepPreflight `json:"steps"`
}

type ReleaseTargetPage struct {
	Total      int64           `json:"total"`
	PageNum    int             `json:"page_num"`
	PageSize   int             `json:"page_size"`
	TotalPages int             `json:"total_pages"`
	Targets    []ReleaseTarget `json:"targets"`
}

type TaskStepSummary struct {
	Total     int `json:"total"`
	Settled   int `json:"settled"`
	Pending   int `json:"pending"`
	Running   int `json:"running"`
	Succeeded int `json:"succeeded"`
	Failed    int `json:"failed"`
	Cancelled int `json:"cancelled"`
	Skipped   int `json:"skipped"`
}

type ActiveTaskView struct {
	entity.TaskRecord
	StepSummary *TaskStepSummary `json:"step_summary,omitempty"`
}

// ReleaseCommandError is a deliberately public, stable domain failure. Cause
// text is never serialized by the API.
type ReleaseCommandError struct {
	Code       string
	HTTPStatus int
	Message    string
	Cause      error
}

func (e *ReleaseCommandError) Error() string {
	if e == nil {
		return ""
	}
	if e.Cause != nil {
		return fmt.Sprintf("%s: %v", e.Message, e.Cause)
	}
	return e.Message
}

func (e *ReleaseCommandError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func releaseError(code string, status int, message string) *ReleaseCommandError {
	return &ReleaseCommandError{Code: code, HTTPStatus: status, Message: message}
}

func invalidReleaseRequest(message string) *ReleaseCommandError {
	return releaseError(ReleaseCodeInvalidRequest, http.StatusBadRequest, message)
}
