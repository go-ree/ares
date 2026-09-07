package entity

import "time"

// ReleaseIdempotencyRecord stores the durable identity of one committed
// release command. Digests are binary so raw client keys and request bodies do
// not become persistent data.
type ReleaseIdempotencyRecord struct {
	IdempotencyID     int64     `xorm:"BIGINT pk autoincr 'idempotency_id'" json:"idempotency_id"`
	ActorUserID       int64     `xorm:"BIGINT notnull unique(uk_release_idempotency_scope_actor_key) 'actor_user_id'" json:"actor_user_id"`
	SemanticOperation string    `xorm:"VARCHAR(64) notnull unique(uk_release_idempotency_scope_actor_key) 'semantic_operation'" json:"semantic_operation"`
	KeyDigest         []byte    `xorm:"BINARY(32) notnull unique(uk_release_idempotency_scope_actor_key) 'key_digest'" json:"-"`
	RequestDigest     []byte    `xorm:"BINARY(32) notnull 'request_digest'" json:"-"`
	ItemCount         uint16    `xorm:"SMALLINT UNSIGNED notnull 'item_count'" json:"item_count"`
	CreatedTime       time.Time `xorm:"DATETIME(6) created notnull DEFAULT CURRENT_TIMESTAMP(6) index(idx_release_idempotency_created) 'created_at'" json:"created_at"`
}

// ReleaseIdempotencyItem is an ordered, immutable result row. Rejected items
// intentionally retain config_id even if that target never existed.
type ReleaseIdempotencyItem struct {
	RecordID          int64     `xorm:"BIGINT notnull pk 'record_id'" json:"record_id"`
	RequestIndex      uint16    `xorm:"SMALLINT UNSIGNED notnull pk 'request_index'" json:"request_index"`
	ConfigID          int       `xorm:"INT notnull index(idx_release_idempotency_items_config) 'config_id'" json:"config_id"`
	TaskID            *int      `xorm:"INT null unique(uk_release_idempotency_items_task) 'task_id'" json:"task_id,omitempty"`
	WorkflowVersionID *int64    `xorm:"BIGINT null 'workflow_version_id'" json:"workflow_version_id,omitempty"`
	Outcome           string    `xorm:"VARCHAR(16) notnull 'outcome'" json:"outcome"`
	ErrorCode         *string   `xorm:"VARCHAR(64) null 'error_code'" json:"error_code,omitempty"`
	CreatedTime       time.Time `xorm:"DATETIME(6) created notnull DEFAULT CURRENT_TIMESTAMP(6) 'created_at'" json:"created_at"`
}
