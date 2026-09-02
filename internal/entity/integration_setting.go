package entity

import "time"

// IntegrationSetting stores one encrypted, provider-specific configuration.
// ConfigData is JSON text rather than a MySQL JSON column so future providers
// can evolve their schema without coupling database migrations to Xorm's JSON
// type handling.
type IntegrationSetting struct {
	Provider   string    `xorm:"VARCHAR(64) pk 'provider'" json:"provider"`
	ConfigData string    `xorm:"MEDIUMTEXT notnull 'config_data'" json:"-"`
	CreatedAt  time.Time `xorm:"timestamp created notnull DEFAULT CURRENT_TIMESTAMP 'created_at'" json:"created_at"`
	UpdatedAt  time.Time `xorm:"timestamp updated notnull DEFAULT CURRENT_TIMESTAMP 'updated_at'" json:"updated_at"`
}

func (*IntegrationSetting) TableName() string { return TableIntegrationSettings }
