package entity

import (
	"encoding/json"
	"time"
)

// ReleaseWorkflow is the stable identity of an app-config release workflow.
// Its versions are immutable; AppConfigWorkflow points at the current one.
type ReleaseWorkflow struct {
	WorkflowID  int64      `xorm:"BIGINT pk autoincr 'workflow_id'" json:"workflow_id"`
	Name        string     `xorm:"VARCHAR(120) notnull 'name'" json:"name"`
	Description string     `xorm:"VARCHAR(500) null 'description'" json:"description"`
	CreatedTime time.Time  `xorm:"timestamp created notnull DEFAULT CURRENT_TIMESTAMP 'created_at'" json:"created_at"`
	UpdatedTime time.Time  `xorm:"timestamp updated notnull DEFAULT CURRENT_TIMESTAMP 'updated_at'" json:"updated_at"`
	DeletedTime *time.Time `xorm:"timestamp deleted 'deleted_at'" json:"deleted_at,omitempty"`
}

// ReleaseWorkflowVersion stores a canonical, validated workflow document.
// Rows are append-only after creation.
type ReleaseWorkflowVersion struct {
	VersionID   int64           `xorm:"BIGINT pk autoincr 'version_id'" json:"version_id"`
	WorkflowID  int64           `xorm:"BIGINT notnull index unique(uk_workflow_version) 'workflow_id'" json:"workflow_id"`
	Version     int             `xorm:"INT notnull unique(uk_workflow_version) 'version'" json:"version"`
	Spec        json.RawMessage `xorm:"JSON notnull 'spec'" json:"spec" swaggertype:"object"`
	Checksum    string          `xorm:"CHAR(64) notnull 'checksum'" json:"checksum"`
	CreatedBy   string          `xorm:"VARCHAR(100) notnull 'created_by'" json:"created_by"`
	CreatedTime time.Time       `xorm:"timestamp created notnull DEFAULT CURRENT_TIMESTAMP 'created_at'" json:"created_at"`
}

// AppConfigWorkflow atomically binds an app-config to its current immutable
// workflow version. AppConfigID is unique by schema migration.
type AppConfigWorkflow struct {
	BindingID   int64     `xorm:"BIGINT pk autoincr 'binding_id'" json:"binding_id"`
	AppConfigID int       `xorm:"INT notnull unique 'app_config_id'" json:"app_config_id"`
	WorkflowID  int64     `xorm:"BIGINT notnull index 'workflow_id'" json:"workflow_id"`
	VersionID   int64     `xorm:"BIGINT notnull index 'version_id'" json:"version_id"`
	Revision    int       `xorm:"INT notnull default 1 'revision'" json:"revision"`
	CreatedTime time.Time `xorm:"timestamp created notnull DEFAULT CURRENT_TIMESTAMP 'created_at'" json:"created_at"`
	UpdatedTime time.Time `xorm:"timestamp updated notnull DEFAULT CURRENT_TIMESTAMP 'updated_at'" json:"updated_at"`
}

// TaskStepRecord is a task-local snapshot. It deliberately stores the full
// step configuration so editing a workflow never changes an in-flight task.
type TaskStepRecord struct {
	StepRecordID      int64           `xorm:"BIGINT pk autoincr 'step_record_id'" json:"step_record_id"`
	TaskID            int             `xorm:"INT notnull index(idx_task_position) index(idx_task_status) unique(uk_task_step_key) 'task_id'" json:"task_id"`
	WorkflowVersionID int64           `xorm:"BIGINT notnull index 'workflow_version_id'" json:"workflow_version_id"`
	StepKey           string          `xorm:"VARCHAR(63) notnull unique(uk_task_step_key) 'step_key'" json:"step_key"`
	Name              string          `xorm:"VARCHAR(120) notnull 'name'" json:"name"`
	Uses              string          `xorm:"VARCHAR(120) notnull 'uses'" json:"uses"`
	Category          string          `xorm:"VARCHAR(32) null 'category'" json:"category,omitempty"`
	Position          int             `xorm:"INT notnull index(idx_task_position) 'position'" json:"position"`
	Config            json.RawMessage `xorm:"JSON notnull 'config'" json:"-"`
	TimeoutSeconds    int             `xorm:"INT notnull default 3600 'timeout_seconds'" json:"timeout_seconds"`
	OnFailure         string          `xorm:"VARCHAR(16) notnull default 'stop' 'on_failure'" json:"on_failure"`
	Status            string          `xorm:"VARCHAR(32) notnull default 'pending' index(idx_task_status) 'status'" json:"status"`
	Attempt           int             `xorm:"INT notnull default 1 'attempt'" json:"attempt"`
	ExternalRef       json.RawMessage `xorm:"JSON null 'external_ref'" json:"-"`
	// Output is an internal hand-off between workflow steps. Executor output is
	// opaque and may contain sensitive delivery metadata, so public task APIs do
	// not serialize it. A future authenticated/public-output contract can expose
	// explicitly classified fields without weakening this default.
	Output       json.RawMessage `xorm:"JSON null 'output'" json:"-"`
	Message      string          `xorm:"VARCHAR(1000) null 'message'" json:"message,omitempty"`
	StartedTime  *time.Time      `xorm:"timestamp null 'started_at'" json:"started_at,omitempty"`
	FinishedTime *time.Time      `xorm:"timestamp null 'finished_at'" json:"finished_at,omitempty"`
	CreatedTime  time.Time       `xorm:"timestamp created notnull DEFAULT CURRENT_TIMESTAMP 'created_at'" json:"created_at"`
	UpdatedTime  time.Time       `xorm:"timestamp updated notnull DEFAULT CURRENT_TIMESTAMP 'updated_at'" json:"updated_at"`
}
