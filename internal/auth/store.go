package auth

import (
	"context"
	"time"
)

type BootstrapUser struct {
	Username     string
	DisplayName  string
	PasswordHash string
}

type OIDCUser struct {
	Issuer            string
	Subject           string
	IdentityHash      []byte
	Email             string
	DisplayName       string
	PreferredUsername string
}

type Store interface {
	BootstrapAvailable(context.Context) (bool, error)
	HasEnabledAdmin(context.Context, bool, string) (bool, error)
	CreateBootstrapAdmin(context.Context, BootstrapUser, AuditEvent, time.Time) (User, error)
	FindLocalUser(context.Context, string) (User, error)
	UpsertOIDCUser(context.Context, OIDCUser, time.Time, bool) (User, error)

	CreateSession(context.Context, []byte, int64, time.Time, time.Time) error
	CreateLocalSession(context.Context, int64, string, []byte, []byte, time.Time, time.Time) (User, error)
	FindSession(context.Context, []byte) (Session, error)
	TouchSession(context.Context, []byte, time.Time) error
	RevokeSession(context.Context, []byte, time.Time) error
	ChangeLocalPassword(context.Context, int64, string, string, time.Time) error

	CreateOIDCFlow(context.Context, OIDCFlow) error
	ConsumeOIDCFlow(context.Context, []byte, []byte, time.Time) (OIDCFlow, error)

	AppendAudit(context.Context, AuditEvent) error
	LatestAuditID(context.Context) (int64, error)
	ListAudit(context.Context, int64, int64, int) ([]AuditEvent, error)
	ListUsers(context.Context, int, int) ([]User, error)
	UpdateUser(context.Context, int64, UserPatch, time.Time, bool, string) (User, error)
}
