package entity

type App struct {
	AppId       int    `xorm:"primary_key;auto_increment:true" json:"app_id"`
	CreatedTime string `xorm:"created" json:"created_time"`
	UpdatedTime string `xorm:"updated" json:"updated_time"`
	DeletedTime string `xorm:"deleted" json:"deleted_time"`

	AppName       string `xorm:"VARCHAR(100)" json:"app_name"`
	AppNameCn     string `xorm:"VARCHAR(100)" json:"app_name_cn"`
	Owner         string `xorm:"VARCHAR(100)" json:"owner"`
	AppLevel      int    `xorm:"INT(11)" json:"app_level"`
	BusinessGroup string `xorm:"VARCHAR(100)" json:"business_group"`

	GitUrl          string `xorm:"VARCHAR(100)" json:"git_url"`
	DevLanguage     string `xorm:"VARCHAR(100)" json:"dev_language"`
	CodePackageName string `xorm:"VARCHAR(100)" json:"code_package_name"`
	CodePackagePath string `xorm:"VARCHAR(100)" json:"code_package_path"`
}

type AppConfig struct {
	ConfigId string `xorm:"VARCHAR(100)" json:"config_id"`

	AppId       int    `xorm:"primary_key;auto_increment:true" json:"app_id"`
	CreatedTime string `xorm:"created" json:"created_time"`
	UpdatedTime string `xorm:"updated" json:"updated_time"`
	DeletedTime string `xorm:"deleted" json:"deleted_time"`

	Env string `xorm:"VARCHAR(100)" json:"env"`
}

type PublishConfig struct {
	// 项目类型
	PublishType string `xorm:"VARCHAR(100)" json:"publish_type"`
	// 运行的基础镜像
	BaseImage string `xorm:"VARCHAR(100)" json:"base_image"`
	// 项目打包时需要用到的基础镜像
	PackImage string `xorm:"VARCHAR(100)" json:"pack_image"`
	// 项目类型所对应的jenkins任务
	JobName string `xorm:"VARCHAR(100)" json:"job_name"`
	// 项目所对应的JenkinsFile文件的git仓库地址
	JenkinsFileGitUrl string `xorm:"VARCHAR(100)" json:"jenkins_file_git_url"`
	// 在git仓库中的相对路径
	JenkinsFilePath string `xorm:"VARCHAR(100)" json:"jenkins_file_path"`
}
