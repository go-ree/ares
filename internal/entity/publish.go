package entity

import (
	"encoding/json"
	"time"
)

// TaskRecord 任务记录
// swagger:model
type TaskRecord struct {
	TaskId        int             `xorm:"INT(11) pk autoincr 'task_id'" json:"task_id"`
	AppName       string          `xorm:"VARCHAR(255) not null 'app_name'" json:"app_name"`
	CiBuildId     int64           `xorm:"int(11) DEFAULT 0 'ci_build_id'" json:"ci_build_id"`
	CdBuildId     int64           `xorm:"int(11) DEFAULT 0 'cd_build_id'" json:"cd_build_id"`
	PipelineParam json.RawMessage `xorm:"JSON  'pipeline_param' " json:"pipeline_param" swaggertype:"string"`
	Status        string          `xorm:"VARCHAR(100) DEFAULT 'init' 'status'" json:"status"`
	Message       string          `xorm:"VARCHAR(255) DEFAULT 'NULL' 'message'" json:"message"`
	CiJobName     string          `xorm:"VARCHAR(100) DEFAULT 'NULL' 'ci_job_name'" json:"ci_job_name"`
	CdJobName     string          `xorm:"VARCHAR(100) DEFAULT 'NULL' 'cd_job_name'" json:"cd_job_name"`
	AutoDeploy    int             `xorm:"TINYINT(1) DEFAULT(1) 'auto_deploy'" json:"auto_deploy"`
	Products      string          `xorm:"VARCHAR(255) DEFAULT 'NULL' 'products'" json:"products"`
	CreatedTime   time.Time       `xorm:"timestamp created notnull DEFAULT CURRENT_TIMESTAMP 'created_at'" json:"created_at" swaggertype:"string" format:"date-time"`
	UpdatedTime   time.Time       `xorm:"timestamp updated notnull DEFAULT CURRENT_TIMESTAMP 'updated_at'" json:"updated_at" swaggertype:"string" format:"date-time"`
	DeletedTime   *time.Time      `xorm:"timestamp deleted 'deleted_at'" json:"deleted_at" swaggertype:"string" format:"date-time"`
}

type Pipelines struct {
	Id              int    `xorm:"INT(11) pk autoincr 'id'" json:"id"`
	JobName         string `xorm:"VARCHAR(100) notnull unique 'job_name'" json:"job_name"`
	DescriptionCN   string `xorm:"VARCHAR(255) notnull 'description_cn'" json:"description_cn"`
	CodePackageType string `xorm:"VARCHAR(100) notnull unique 'code_package_type'" json:"code_package_type"`
	URL             string `xorm:"VARCHAR(255) notnull 'url'" json:"url"`

	CreatedTime time.Time  `xorm:"timestamp created notnull DEFAULT CURRENT_TIMESTAMP 'created_at'" json:"created_at" swaggertype:"string" format:"date-time"`
	UpdatedTime time.Time  `xorm:"timestamp updated notnull DEFAULT CURRENT_TIMESTAMP 'updated_at'" json:"updated_at" swaggertype:"string" format:"date-time"`
	DeletedTime *time.Time `xorm:"timestamp deleted 'deleted_at'" json:"deleted_at" swaggertype:"string" format:"date-time"`
}

type EnvConfigs struct {
	ID                int    `xorm:"INT(11) pk autoincr 'id'" json:"id"`
	Env               string `xorm:"VARCHAR(100) notnull unique 'env'" json:"env"`
	ClusterName       string `xorm:"VARCHAR(255) notnull 'cluster_name'" json:"cluster_name"`
	DescriptionCN     string `xorm:"VARCHAR(255) notnull 'description_cn'" json:"description_cn"`
	HarborURL         string `xorm:"VARCHAR(255) notnull 'harbor_url'" json:"harbor_url"`
	HarborProjectName string `xorm:"VARCHAR(255) notnull 'harbor_project_name'" json:"harbor_project_name"`
	NodeVersion       string `xorm:"VARCHAR(255) notnull 'node_version'" json:"node_version"`
	MavenVersion      string `xorm:"VARCHAR(255) notnull 'maven_version'" json:"maven_version"`

	CreatedTime time.Time  `xorm:"timestamp created notnull DEFAULT CURRENT_TIMESTAMP 'created_at'" json:"created_at" swaggertype:"string" format:"date-time"`
	UpdatedTime time.Time  `xorm:"timestamp updated notnull DEFAULT CURRENT_TIMESTAMP 'updated_at'" json:"updated_at" swaggertype:"string" format:"date-time"`
	DeletedTime *time.Time `xorm:"timestamp deleted 'deleted_at'" json:"deleted_at" swaggertype:"string" format:"date-time"`
}
