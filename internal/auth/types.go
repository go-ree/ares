package auth

import (
	"errors"
	"sort"
	"strings"
	"time"
)

type Role string

const (
	RoleViewer    Role = "viewer"
	RoleDeveloper Role = "developer"
	RoleReleaser  Role = "releaser"
	RoleAdmin     Role = "admin"
)

type Permission string

const (
	PermissionApplicationsRead    Permission = "applications:read"
	PermissionApplicationsWrite   Permission = "applications:write"
	PermissionAppConfigsRead      Permission = "app-configs:read"
	PermissionAppConfigsWrite     Permission = "app-configs:write"
	PermissionDomainsRead         Permission = "domains:read"
	PermissionDomainsWrite        Permission = "domains:write"
	PermissionWorkflowsRead       Permission = "workflows:read"
	PermissionWorkflowsWrite      Permission = "workflows:write"
	PermissionReleasesRead        Permission = "releases:read"
	PermissionReleasesCreate      Permission = "releases:create"
	PermissionTasksRead           Permission = "tasks:read"
	PermissionTasksWrite          Permission = "tasks:write"
	PermissionLogsRead            Permission = "logs:read"
	PermissionKubernetesRead      Permission = "kubernetes:read"
	PermissionKubernetesDebug     Permission = "kubernetes:debug"
	PermissionSystemSettingsRead  Permission = "system-settings:read"
	PermissionSystemSettingsWrite Permission = "system-settings:write"
	PermissionUsersRead           Permission = "users:read"
	PermissionUsersWrite          Permission = "users:write"
	PermissionAuditRead           Permission = "audit:read"
)

var rolePermissions = map[Role]map[Permission]struct{}{
	RoleViewer: permissionSet(
		PermissionApplicationsRead, PermissionAppConfigsRead, PermissionDomainsRead,
		PermissionWorkflowsRead, PermissionReleasesRead, PermissionTasksRead,
		PermissionLogsRead, PermissionKubernetesRead,
	),
	RoleDeveloper: permissionSet(
		PermissionApplicationsRead, PermissionApplicationsWrite,
		PermissionAppConfigsRead, PermissionAppConfigsWrite,
		PermissionDomainsRead, PermissionDomainsWrite, PermissionWorkflowsRead,
		PermissionReleasesRead, PermissionTasksRead, PermissionLogsRead,
		PermissionKubernetesRead,
	),
	RoleReleaser: permissionSet(
		PermissionApplicationsRead, PermissionAppConfigsRead, PermissionDomainsRead,
		PermissionWorkflowsRead, PermissionReleasesRead, PermissionReleasesCreate,
		PermissionTasksRead, PermissionTasksWrite, PermissionLogsRead,
		PermissionKubernetesRead,
	),
	RoleAdmin: permissionSet(
		PermissionApplicationsRead, PermissionApplicationsWrite,
		PermissionAppConfigsRead, PermissionAppConfigsWrite,
		PermissionDomainsRead, PermissionDomainsWrite,
		PermissionWorkflowsRead, PermissionWorkflowsWrite,
		PermissionReleasesRead, PermissionReleasesCreate,
		PermissionTasksRead, PermissionTasksWrite, PermissionLogsRead,
		PermissionKubernetesRead, PermissionKubernetesDebug,
		PermissionSystemSettingsRead, PermissionSystemSettingsWrite,
		PermissionUsersRead, PermissionUsersWrite, PermissionAuditRead,
	),
}

func permissionSet(permissions ...Permission) map[Permission]struct{} {
	result := make(map[Permission]struct{}, len(permissions))
	for _, permission := range permissions {
		result[permission] = struct{}{}
	}
	return result
}

func ParseRole(value string) (Role, error) {
	role := Role(strings.TrimSpace(value))
	if _, ok := rolePermissions[role]; !ok {
		return "", errors.New("不支持的角色")
	}
	return role, nil
}

func PermissionsForRole(role Role) []Permission {
	permissions := rolePermissions[role]
	result := make([]Permission, 0, len(permissions))
	for permission := range permissions {
		result = append(result, permission)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

func RoleHasPermission(role Role, permission Permission) bool {
	_, ok := rolePermissions[role][permission]
	return ok
}

type User struct {
	ID           int64
	Username     string
	DisplayName  string
	Email        string
	PasswordHash string
	Role         Role
	AuthSource   string
	Enabled      bool
	LastLoginAt  *time.Time
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type Principal struct {
	UserID      int64
	Username    string
	DisplayName string
	Email       string
	Role        Role
	AuthSource  string
	SessionHash []byte
	ExpiresAt   time.Time
}

func (p Principal) Has(permission Permission) bool {
	return RoleHasPermission(p.Role, permission)
}

type Session struct {
	Hash       []byte
	User       User
	ExpiresAt  time.Time
	RevokedAt  *time.Time
	LastSeenAt time.Time
	CreatedAt  time.Time
}

type OIDCFlow struct {
	StateHash          []byte
	NonceHash          []byte
	BindingHash        []byte
	VerifierCiphertext string
	ReturnPath         string
	ExpiresAt          time.Time
	ConsumedAt         *time.Time
	CreatedAt          time.Time
}

type OIDCClaims struct {
	Issuer            string
	Subject           string
	Email             string
	EmailVerified     bool
	Name              string
	PreferredUsername string
}

type AuditEvent struct {
	ID               int64
	ActorUserID      *int64
	ActorUsername    string
	ActorDisplayName string
	AuthSource       string
	Action           string
	ResourceType     string
	ResourceID       string
	Result           string
	HTTPStatus       int
	RequestID        string
	CreatedAt        time.Time
}

type UserPatch struct {
	Role    *Role
	Enabled *bool
}

var (
	ErrUnauthenticated           = errors.New("未登录或会话已失效")
	ErrForbidden                 = errors.New("没有执行该操作的权限")
	ErrBootstrapUnavailable      = errors.New("首次管理员初始化不可用")
	ErrInvalidCredentials        = errors.New("用户名或密码错误")
	ErrPasswordChangeUnsupported = errors.New("当前身份不支持修改本地密码")
	ErrOIDCUnavailable           = errors.New("OIDC 登录未配置")
	ErrInvalidOIDCFlow           = errors.New("OIDC 登录流程无效或已过期")
	ErrOIDCFlowCapacity          = errors.New("OIDC 登录流程容量已满")
	ErrLastAdmin                 = errors.New("不能禁用或降级最后一个可用管理员")
	ErrUserNotFound              = errors.New("用户不存在")
	ErrSessionNotFound           = errors.New("会话不存在")
)
