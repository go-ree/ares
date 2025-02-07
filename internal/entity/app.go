package entity

import (
	"encoding/json"
	"time"
)

type Apps struct {
	AppId         int        `xorm:"pk autoincr 'app_id'" json:"app_id"`
	AppName       string     `xorm:"varchar(255) notnull 'app_name'" json:"app_name"`
	AppNameCn     string     `xorm:"varchar(255) default 'NULL' 'app_name_cn'" json:"app_name_cn"`
	Owner         string     `xorm:"varchar(100) notnull 'owner'" json:"owner"`
	OwnerCN       string     `xorm:"varchar(100) notnull 'owner_cn'" json:"owner_cn"`
	DevLanguage   string     `xorm:"varchar(100) notnull 'dev_language'" json:"dev_language"`
	DescriptionCN string     `xorm:"varchar(255) default 'NULL' 'description_cn'" json:"description_cn"`
	GitUrl        string     `xorm:"varchar(255) notnull 'git_url'" json:"git_url"`
	CreatedTime   time.Time  `xorm:"timestamp created notnull default CURRENT_TIMESTAMP 'created_at'" json:"created_at"`
	UpdatedTime   time.Time  `xorm:"timestamp updated notnull default CURRENT_TIMESTAMP 'updated_at'" json:"updated_at"`
	DeletedTime   *time.Time `xorm:"timestamp deleted 'deleted_at'" json:"deleted_at"`
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

type JenkinsJobData struct {
	JobName  string          `xorm:"VARCHAR(100)" json:"job_name"`
	Describe string          `xorm:"VARCHAR(100)" json:"describe"`
	Json     json.RawMessage `xorm:"json" json:"json"`
}
