package entity

import (
	"encoding/json"
	"time"
)

// DevLanguageRule 存储 dev_language 对应的规则（JSON）
// 表：dev_language_rules
type DevLanguageRule struct {
	DevLanguage string          `xorm:"VARCHAR(100) pk 'dev_language'" json:"dev_language"`
	Rules       json.RawMessage `xorm:"JSON notnull 'rules'" json:"rules"`

	CreatedTime time.Time  `xorm:"timestamp created notnull DEFAULT CURRENT_TIMESTAMP 'created_at'" json:"created_at" swaggertype:"string" format:"date-time"`
	UpdatedTime time.Time  `xorm:"timestamp updated notnull DEFAULT CURRENT_TIMESTAMP 'updated_at'" json:"updated_at" swaggertype:"string" format:"date-time"`
	DeletedTime *time.Time `xorm:"timestamp deleted 'deleted_at'" json:"deleted_at" swaggertype:"string" format:"date-time"`
}

func (d *DevLanguageRule) TableName() string { return TableDevLanguageRules }
