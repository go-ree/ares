package entity

// TableNames 定义所有表名常量
const (
	TableApps                = "apps"
	TableAppConfigs          = "app_configs"
	TableAppConfigDomains    = "app_config_domains"
	TableTaskRecord          = "task_record"
	TableTaskRecordImages    = "task_record_images"
	TablePipelines           = "pipelines"
	TablePipelineJobs        = "pipelines_job_combination"
	TableEnvConfigs          = "env_configs"
	TableDevLanguageRules    = "dev_language_rules"
	TableIntegrationSettings = "integration_settings"
	TableReleaseWorkflows    = "release_workflows"
	TableWorkflowVersions    = "release_workflow_versions"
	TableAppConfigWorkflows  = "app_config_workflows"
	TableTaskStepRecords     = "task_step_records"
)

func (a *Apps) TableName() string {
	return TableApps
}

func (a *AppConfigs) TableName() string {
	return TableAppConfigs
}

func (a *TaskRecord) TableName() string {
	return TableTaskRecord
}

func (a *Pipelines) TableName() string {
	return TablePipelines
}

func (a *PipelinesJobCombination) TableName() string {
	return TablePipelineJobs
}

func (a *EnvConfigs) TableName() string {
	return TableEnvConfigs
}

func (w *ReleaseWorkflow) TableName() string {
	return TableReleaseWorkflows
}

func (v *ReleaseWorkflowVersion) TableName() string {
	return TableWorkflowVersions
}

func (b *AppConfigWorkflow) TableName() string {
	return TableAppConfigWorkflows
}

func (s *TaskStepRecord) TableName() string {
	return TableTaskStepRecords
}
