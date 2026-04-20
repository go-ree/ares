package entity

import (
	"encoding/json"
	"time"
)

// Apps 应用信息
type Apps struct {
	AppId          int        `xorm:"INT(11) pk autoincr 'app_id'" json:"app_id"`
	AppName        string     `xorm:"varchar(255) notnull 'app_name'" json:"app_name"`
	RundeckAppName *string    `xorm:"varchar(255) 'rundeck_app_name'" json:"rundeck_app_name"`
	AppNameCn      string     `xorm:"varchar(255) default 'NULL' 'app_name_cn'" json:"app_name_cn"`
	Owner          string     `xorm:"varchar(100) notnull 'owner'" json:"owner"`
	OwnerCN        string     `xorm:"varchar(100) notnull 'owner_cn'" json:"owner_cn"`
	DevLanguage    string     `xorm:"varchar(100) notnull 'dev_language'" json:"dev_language"`
	DescriptionCN  string     `xorm:"varchar(255) default 'NULL' 'description_cn'" json:"description_cn"`
	GitUrl         string     `xorm:"varchar(255) notnull 'git_url'" json:"git_url"`
	CreatedTime    time.Time  `xorm:"timestamp created notnull DEFAULT CURRENT_TIMESTAMP 'created_at'" json:"created_at" swaggertype:"string" format:"date-time"`
	UpdatedTime    time.Time  `xorm:"timestamp updated notnull DEFAULT CURRENT_TIMESTAMP 'updated_at'" json:"updated_at" swaggertype:"string" format:"date-time"`
	DeletedTime    *time.Time `xorm:"timestamp deleted 'deleted_at'" json:"deleted_at" swaggertype:"string" format:"date-time"`
}

// AppConfigs 应用配置
type AppConfigs struct {
	ConfigID        int    `xorm:"INT(11) pk autoincr 'config_id'" json:"config_id"`
	AppID           int    `xorm:"INT(11) not null 'app_id' index" json:"app_id"`
	Env             string `xorm:"varchar(100) not null 'env'" json:"env"`
	CodePackageType string `xorm:"varchar(100) default 'NULL' 'code_package_type'" json:"code_package_type"`
	CodePackagePath string `xorm:"varchar(255) default 'NULL' 'code_package_path'" json:"code_package_path"`
	CodePackageName string `xorm:"varchar(255) default 'NULL' 'code_package_name'" json:"code_package_name"`
	BaseImage       string `xorm:"default 'NULL' 'base_image'" json:"base_image"`

	PodCount               int    `xorm:"int(11) default 1 'pod_count'" json:"pod_count"`
	LimitsMemory           int    `xorm:"int(11) default 2 'limits_memory'" json:"limits_memory"`
	GpuCount               int    `xorm:"int(11) default 0 'gpu_count'" json:"gpu_count"`
	ProbeType              string `xorm:"varchar(100) default 'TCP' 'probe_type'" json:"probe_type"`
	ProbeCheckPath         string `xorm:"varchar(100) default '/inside/checkup' 'probe_check_path'" json:"probe_check_path"`
	ProbeCheckTcpPort      int    `xorm:"int(11) notnull default 8080 'probe_check_tcp_port'" json:"probe_check_tcp_port"`
	ProbeCheckHttpPort     int    `xorm:"int(11) notnull default 8080 'probe_check_http_port'" json:"probe_check_http_port"`
	ProbeStopCheckHttpPort int    `xorm:"int(11) notnull default 8080 'probe_stop_check_http_port'" json:"probe_stop_check_http_port"`
	ContainerPort          int    `xorm:"int(11) notnull default 8080 'container_port'" json:"container_port"`
	PreStopType            string `xorm:"varchar(100) default 'TCP' 'pre_stop_type'" json:"pre_stop_type"`
	PreStopCheckPath       string `xorm:"varchar(100) default '/inside/prestop' 'pre_stop_check_path' " json:"pre_stop_check_path"`
	PreStopCommand         string `xorm:"varchar(255) default 'NULL' 'pre_stop_command'" json:"pre_stop_command"`

	CreatedTime time.Time  `xorm:"timestamp created notnull DEFAULT CURRENT_TIMESTAMP 'created_at'" json:"created_at" swaggertype:"string" format:"date-time"`
	UpdatedTime time.Time  `xorm:"timestamp updated notnull DEFAULT CURRENT_TIMESTAMP 'updated_at'" json:"updated_at" swaggertype:"string" format:"date-time"`
	DeletedTime *time.Time `xorm:"timestamp deleted 'deleted_at'" json:"deleted_at" swaggertype:"string" format:"date-time"`
}

//
//以上为投产结构体
//

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
