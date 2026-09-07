package entity

import "time"

const (
	TableAuthUsers          = "auth_users"
	TableAuthIdentities     = "auth_identities"
	TableAuthSessions       = "auth_sessions"
	TableAuthOIDCFlows      = "auth_oidc_flows"
	TableAuthBootstrapState = "auth_bootstrap_state"
	TableAuditEvents        = "audit_events"
)

// AuthUser is the stable, server-owned identity used by authorization and
// audit records. PasswordHash is intentionally excluded from JSON even when
// an entity is accidentally passed to a response encoder.
type AuthUser struct {
	UserID       int64      `xorm:"BIGINT pk autoincr 'user_id'" json:"user_id"`
	Username     string     `xorm:"VARCHAR(100) notnull unique(uk_auth_users_username) 'username'" json:"username"`
	DisplayName  string     `xorm:"VARCHAR(255) notnull 'display_name'" json:"display_name"`
	Email        string     `xorm:"VARCHAR(320) null 'email'" json:"email"`
	PasswordHash string     `xorm:"VARCHAR(255) null 'password_hash'" json:"-"`
	Role         string     `xorm:"VARCHAR(32) notnull default 'viewer' 'role'" json:"role"`
	AuthSource   string     `xorm:"VARCHAR(32) notnull 'auth_source'" json:"auth_source"`
	Enabled      bool       `xorm:"TINYINT(1) notnull default 1 'enabled'" json:"enabled"`
	LastLoginAt  *time.Time `xorm:"DATETIME(6) null 'last_login_at'" json:"last_login_at,omitempty"`
	CreatedAt    time.Time  `xorm:"DATETIME(6) created notnull default CURRENT_TIMESTAMP(6) 'created_at'" json:"created_at"`
	UpdatedAt    time.Time  `xorm:"DATETIME(6) updated notnull default CURRENT_TIMESTAMP(6) 'updated_at'" json:"updated_at"`
}

func (*AuthUser) TableName() string { return TableAuthUsers }

// AuthIdentity preserves the exact OIDC issuer and subject while enforcing a
// fixed-size, collision-resistant lookup key. IdentityHash contains the raw
// 32-byte digest, not a hexadecimal encoding.
type AuthIdentity struct {
	IdentityID   int64     `xorm:"BIGINT pk autoincr 'identity_id'" json:"identity_id"`
	UserID       int64     `xorm:"BIGINT notnull index(idx_auth_identities_user) 'user_id'" json:"user_id"`
	Issuer       string    `xorm:"VARCHAR(2048) notnull 'issuer'" json:"issuer"`
	Subject      string    `xorm:"VARCHAR(255) notnull 'subject'" json:"subject"`
	IdentityHash []byte    `xorm:"BINARY(32) notnull unique(uk_auth_identities_hash) 'identity_hash'" json:"-"`
	CreatedAt    time.Time `xorm:"DATETIME(6) created notnull default CURRENT_TIMESTAMP(6) 'created_at'" json:"created_at"`
}

func (*AuthIdentity) TableName() string { return TableAuthIdentities }

// AuthSession stores only a raw SHA-256 digest of the opaque browser token.
type AuthSession struct {
	SessionHash string     `xorm:"BINARY(32) pk 'session_hash'" json:"-"`
	UserID      int64      `xorm:"BIGINT notnull index(idx_auth_sessions_user_state) 'user_id'" json:"user_id"`
	ExpiresAt   time.Time  `xorm:"DATETIME(6) notnull index(idx_auth_sessions_expires) 'expires_at'" json:"expires_at"`
	RevokedAt   *time.Time `xorm:"DATETIME(6) null index(idx_auth_sessions_user_state) 'revoked_at'" json:"revoked_at,omitempty"`
	LastSeenAt  time.Time  `xorm:"DATETIME(6) notnull index(idx_auth_sessions_user_state) 'last_seen_at'" json:"last_seen_at"`
	CreatedAt   time.Time  `xorm:"DATETIME(6) created notnull default CURRENT_TIMESTAMP(6) 'created_at'" json:"created_at"`
}

func (*AuthSession) TableName() string { return TableAuthSessions }

// AuthOIDCFlow is a short-lived, one-shot login transaction. The three hash
// fields contain raw 32-byte digests; VerifierCiphertext is encrypted at rest.
type AuthOIDCFlow struct {
	StateHash          string     `xorm:"BINARY(32) pk 'state_hash'" json:"-"`
	NonceHash          string     `xorm:"BINARY(32) notnull 'nonce_hash'" json:"-"`
	BindingHash        string     `xorm:"BINARY(32) notnull 'binding_hash'" json:"-"`
	VerifierCiphertext string     `xorm:"TEXT notnull 'verifier_ciphertext'" json:"-"`
	ReturnPath         string     `xorm:"VARCHAR(512) notnull default '/' 'return_path'" json:"return_path"`
	ExpiresAt          time.Time  `xorm:"DATETIME(6) notnull index(idx_auth_oidc_flows_expires) 'expires_at'" json:"expires_at"`
	ConsumedAt         *time.Time `xorm:"DATETIME(6) null 'consumed_at'" json:"consumed_at,omitempty"`
	CreatedAt          time.Time  `xorm:"DATETIME(6) created notnull default CURRENT_TIMESTAMP(6) 'created_at'" json:"created_at"`
}

func (*AuthOIDCFlow) TableName() string { return TableAuthOIDCFlows }

// AuthBootstrapState is a singleton row created by the epoch-5 migration.
// Runtime code may only atomically transition it from incomplete to complete.
type AuthBootstrapState struct {
	ID          int        `xorm:"TINYINT pk 'id'" json:"id"`
	CompletedAt *time.Time `xorm:"DATETIME(6) null 'completed_at'" json:"completed_at,omitempty"`
	CompletedBy *int64     `xorm:"BIGINT null 'completed_by'" json:"completed_by,omitempty"`
}

func (*AuthBootstrapState) TableName() string { return TableAuthBootstrapState }

// AuditEvent is append-only at the application database privilege boundary.
// It deliberately contains no request body, headers, query string, token, or
// arbitrary metadata field.
type AuditEvent struct {
	AuditID          int64     `xorm:"BIGINT pk autoincr 'audit_id'" json:"audit_id"`
	ActorUserID      *int64    `xorm:"BIGINT null index(idx_audit_actor_time) 'actor_user_id'" json:"actor_user_id,omitempty"`
	ActorUsername    string    `xorm:"VARCHAR(100) notnull 'actor_username'" json:"actor_username"`
	ActorDisplayName string    `xorm:"VARCHAR(255) notnull 'actor_display_name'" json:"actor_display_name"`
	AuthSource       string    `xorm:"VARCHAR(32) notnull 'auth_source'" json:"auth_source"`
	Action           string    `xorm:"VARCHAR(100) notnull 'action'" json:"action"`
	ResourceType     string    `xorm:"VARCHAR(100) notnull 'resource_type'" json:"resource_type"`
	ResourceID       string    `xorm:"VARCHAR(255) notnull 'resource_id'" json:"resource_id"`
	Result           string    `xorm:"VARCHAR(32) notnull 'result'" json:"result"`
	HTTPStatus       int       `xorm:"SMALLINT UNSIGNED notnull 'http_status'" json:"http_status"`
	RequestID        string    `xorm:"VARCHAR(64) notnull 'request_id'" json:"request_id"`
	CreatedAt        time.Time `xorm:"DATETIME(6) created notnull default CURRENT_TIMESTAMP(6) index(idx_audit_actor_time) index(idx_audit_created) 'created_at'" json:"created_at"`
}

func (*AuditEvent) TableName() string { return TableAuditEvents }
