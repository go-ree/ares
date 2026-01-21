package entity

// TableNames 定义所有表名常量
const (
	TableApps             = "apps"
	TableAppConfigs       = "app_configs"
	TableTaskRecord       = "task_record"
	TablePipelines        = "pipelines"
	TableEnvConfigs       = "env_configs"
	TableDevLanguageRules = "dev_language_rules"
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

func (a *EnvConfigs) TableName() string {
	return TableEnvConfigs
}
