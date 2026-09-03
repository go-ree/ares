package entity

import (
	"encoding/json"
	"time"
)

// TaskRecord 任务记录
// swagger:model
type TaskRecord struct {
	TaskId         int     `xorm:"INT(11) pk autoincr 'task_id'" json:"task_id"`
	AppName        string  `xorm:"VARCHAR(255) not null 'app_name'" json:"app_name"`
	RundeckAppName *string `xorm:"VARCHAR(255) 'rundeck_app_name'" json:"rundeck_app_name"`
	Branch         string  `xorm:"VARCHAR(100) not null 'branch'" json:"branch"`
	Env            string  `xorm:"VARCHAR(255) not null 'env'" json:"env"`
	Publisher      string  `xorm:"VARCHAR(255) not null 'publisher'" json:"publisher"`
	CiBuildId      int64   `xorm:"int(11) DEFAULT 0 'ci_build_id'" json:"ci_build_id"`
	CdBuildId      int64   `xorm:"int(11) DEFAULT 0 'cd_build_id'" json:"cd_build_id"`
	// PipelineParam is an internal execution snapshot. It may contain deployment
	// inputs and must never be serialized by public task APIs.
	PipelineParam     json.RawMessage  `xorm:"JSON  'pipeline_param' " json:"-"`
	Status            string           `xorm:"VARCHAR(100) DEFAULT 'init' 'status'" json:"status"`
	Message           string           `xorm:"VARCHAR(255) null 'message'" json:"message"`
	CiJobName         string           `xorm:"VARCHAR(100) null 'ci_job_name'" json:"ci_job_name"`
	CdJobName         string           `xorm:"VARCHAR(100) null 'cd_job_name'" json:"cd_job_name"`
	JenkinsAddress    string           `xorm:"TEXT null 'jenkins_address'" json:"-"`
	AutoDeploy        int              `xorm:"TINYINT(1) DEFAULT(1) 'auto_deploy'" json:"auto_deploy"`
	Products          string           `xorm:"VARCHAR(255) null 'products'" json:"products"`
	EngineVersion     int              `xorm:"INT notnull DEFAULT 1 'engine_version'" json:"engine_version"`
	WorkflowVersionID int64            `xorm:"BIGINT notnull DEFAULT 0 'workflow_version_id'" json:"workflow_version_id"`
	Steps             []TaskStepRecord `xorm:"-" json:"steps,omitempty"`
	AppletImages      []AppletImage    `xorm:"-" json:"applet_images"` // 新增：任务图片（仅对外返回）
	CreatedTime       time.Time        `xorm:"timestamp created notnull DEFAULT CURRENT_TIMESTAMP 'created_at'" json:"created_at" swaggertype:"string" format:"date-time"`
	UpdatedTime       time.Time        `xorm:"timestamp updated notnull DEFAULT CURRENT_TIMESTAMP 'updated_at'" json:"updated_at" swaggertype:"string" format:"date-time"`
	DeletedTime       *time.Time       `xorm:"timestamp deleted 'deleted_at'" json:"deleted_at" swaggertype:"string" format:"date-time"`
}

type Pipelines struct {
	Id            int    `xorm:"INT(11) pk autoincr 'id'" json:"id"`
	JobName       string `xorm:"VARCHAR(100) notnull unique 'job_name'" json:"job_name"`
	DescriptionCN string `xorm:"VARCHAR(255) notnull 'description_cn'" json:"description_cn"`
	URL           string `xorm:"VARCHAR(255) notnull 'url'" json:"url"`

	CreatedTime time.Time  `xorm:"timestamp created notnull DEFAULT CURRENT_TIMESTAMP 'created_at'" json:"created_at" swaggertype:"string" format:"date-time"`
	UpdatedTime time.Time  `xorm:"timestamp updated notnull DEFAULT CURRENT_TIMESTAMP 'updated_at'" json:"updated_at" swaggertype:"string" format:"date-time"`
	DeletedTime *time.Time `xorm:"timestamp deleted 'deleted_at'" json:"deleted_at" swaggertype:"string" format:"date-time"`
}

type PipelinesJobCombination struct {
	Id              int    `xorm:"INT(11) pk autoincr 'id'" json:"id"`
	DescriptionCN   string `xorm:"VARCHAR(255) notnull 'description_cn'" json:"description_cn"`
	CiJobName       string `xorm:"VARCHAR(100) notnull index(idx_ci_job) unique(uk_ci_cd_combination) 'ci_job_name'" json:"ci_job_name"` // 关联pipelines.job_name
	CdJobName       string `xorm:"VARCHAR(100) notnull index(idx_cd_job) unique(uk_ci_cd_combination) 'cd_job_name'" json:"cd_job_name"` // 关联pipelines.job_name
	CodePackageType string `xorm:"VARCHAR(100) notnull unique 'code_package_type'" json:"code_package_type"`

	CreatedTime time.Time  `xorm:"timestamp created notnull DEFAULT CURRENT_TIMESTAMP 'created_at'" json:"created_at" swaggertype:"string" format:"date-time"`
	UpdatedTime time.Time  `xorm:"timestamp updated notnull DEFAULT CURRENT_TIMESTAMP 'updated_at'" json:"updated_at" swaggertype:"string" format:"date-time"`
	DeletedTime *time.Time `xorm:"timestamp deleted 'deleted_at'" json:"deleted_at" swaggertype:"string" format:"date-time"`
}

type EnvConfigs struct {
	ID                int    `xorm:"INT(11) pk autoincr 'id'" json:"id"`
	Env               string `xorm:"VARCHAR(100) notnull unique 'env'" json:"env"`
	ClusterName       string `xorm:"VARCHAR(255) null 'cluster_name'" json:"cluster_name"`
	DescriptionCN     string `xorm:"VARCHAR(255) notnull 'description_cn'" json:"description_cn"`
	Enabled           bool   `xorm:"TINYINT(1) notnull default 1 'enabled'" json:"enabled"`
	SortOrder         int    `xorm:"INT notnull default 0 'sort_order'" json:"sort_order"`
	HarborURL         string `xorm:"VARCHAR(255) null 'harbor_url'" json:"harbor_url"`
	HarborProjectName string `xorm:"VARCHAR(255) null 'harbor_project_name'" json:"harbor_project_name"`
	NodeVersion       string `xorm:"VARCHAR(255) null 'node_version'" json:"node_version"`
	MavenVersion      string `xorm:"VARCHAR(255) null 'maven_version'" json:"maven_version"`

	CreatedTime time.Time  `xorm:"timestamp created notnull DEFAULT CURRENT_TIMESTAMP 'created_at'" json:"created_at" swaggertype:"string" format:"date-time"`
	UpdatedTime time.Time  `xorm:"timestamp updated notnull DEFAULT CURRENT_TIMESTAMP 'updated_at'" json:"updated_at" swaggertype:"string" format:"date-time"`
	DeletedTime *time.Time `xorm:"timestamp deleted 'deleted_at'" json:"deleted_at" swaggertype:"string" format:"date-time"`
}
