package entity

import "time"

// AppConfigDomain 应用环境配置的多域名信息（基于 app_configs.config_id）
type AppConfigDomain struct {
	ID       int64  `xorm:"BIGINT(20) pk autoincr 'id'" json:"id"`
	ConfigID int    `xorm:"INT(11) notnull unique(config_id_host_path) index 'config_id'" json:"config_id"`
	Host     string `xorm:"VARCHAR(255) notnull unique(config_id_host_path) 'host'" json:"host"`
	Path     string `xorm:"VARCHAR(255) notnull default '/' unique(config_id_host_path) 'path'" json:"path"`

	CreatedTime time.Time  `xorm:"timestamp created notnull DEFAULT CURRENT_TIMESTAMP 'created_at'" json:"created_at"`
	UpdatedTime time.Time  `xorm:"timestamp updated notnull DEFAULT CURRENT_TIMESTAMP 'updated_at'" json:"updated_at"`
	DeletedTime *time.Time `xorm:"timestamp deleted 'deleted_at'" json:"deleted_at"`
}

func (AppConfigDomain) TableName() string {
	return "app_config_domains"
}
