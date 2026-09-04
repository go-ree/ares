package controller

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-ree/ares/internal/api/util"
	"github.com/go-ree/ares/internal/auth"
	"github.com/go-ree/ares/internal/config"
	"golang.org/x/time/rate"
)

const (
	principalContextKey     = "ares.auth.principal"
	auditFailureContextKey  = "ares.auth.audit-failure"
	auditResourceContextKey = "ares.auth.audit-resource-id"
)

type AuthController struct {
	service               *auth.Service
	optionsGuard          *publicAuthGuard
	bootstrapGuard        *publicAuthGuard
	loginGuard            *publicAuthGuard
	oidcStartGuard        *publicAuthGuard
	oidcCallbackGuard     *publicAuthGuard
	anonymousAuditLimiter *rate.Limiter
	authFailureAuditLimit *rate.Limiter
	optionsMu             sync.Mutex
	optionsCache          auth.Options
	optionsCacheUntil     time.Time
	optionsLoading        chan struct{}
}

const authOptionsCacheTTL = 2 * time.Second

func NewAuthController(service *auth.Service) *AuthController {
	return &AuthController{
		service: service,
		// These process-local limits bound expensive password work and anonymous
		// OIDC flow creation. Production edges should additionally enforce
		// per-client/distributed limits before traffic reaches Ares.
		optionsGuard:      newPublicAuthGuard(rate.Limit(10), 30, 32, 8),
		bootstrapGuard:    newPublicAuthGuard(rate.Limit(2), 6, 4, 2),
		loginGuard:        newPublicAuthGuard(rate.Limit(2), 6, 8, 2),
		oidcStartGuard:    newPublicAuthGuard(rate.Limit(5), 20, 8, 2),
		oidcCallbackGuard: newPublicAuthGuard(rate.Limit(5), 20, 16, 2),
		// Anonymous denials are sampled through a separate bounded budget so a
		// flood of already-rejected requests cannot become an unbounded stream
		// of synchronous database writes. Attempts admitted to an authentication
		// endpoint have an independent budget so cheap preflight noise cannot
		// suppress their security signal. Successful authentication is never sampled.
		anonymousAuditLimiter: rate.NewLimiter(rate.Limit(2), 10),
		authFailureAuditLimit: rate.NewLimiter(rate.Limit(2), 10),
	}
}

// SetPrincipal exposes the authenticated, server-owned identity to business
// handlers. Only authentication middleware should call it.
func SetPrincipal(c *gin.Context, principal auth.Principal) {
	c.Set(principalContextKey, principal)
}

func CurrentPrincipal(c *gin.Context) (auth.Principal, bool) {
	value, ok := c.Get(principalContextKey)
	if !ok {
		return auth.Principal{}, false
	}
	principal, ok := value.(auth.Principal)
	return principal, ok
}

// MarkRequestAuditFailure records a server-owned outcome that cannot be
// supplied by request headers or parameters. It is needed for protocols such
// as SSE where an application failure is emitted after HTTP 200 is committed.
func MarkRequestAuditFailure(c *gin.Context) {
	if c != nil {
		c.Set(auditFailureContextKey, true)
	}
}

func RequestAuditFailureMarked(c *gin.Context) bool {
	if c == nil {
		return false
	}
	marked, _ := c.Get(auditFailureContextKey)
	failed, _ := marked.(bool)
	return failed
}

// SetRequestAuditResourceID attaches a validated, server-owned resource
// identity discovered by a handler (for example a query-bound task after its
// database lookup). Raw query strings and headers must never be stored here.
func SetRequestAuditResourceID(c *gin.Context, resourceID string) {
	if c != nil {
		c.Set(auditResourceContextKey, strings.TrimSpace(resourceID))
	}
}

func RequestAuditResourceID(c *gin.Context) string {
	if c == nil {
		return ""
	}
	value, _ := c.Get(auditResourceContextKey)
	resourceID, _ := value.(string)
	return resourceID
}

type authOptionsResponse struct {
	OIDCEnabled        bool `json:"oidc_enabled"`
	LocalLoginEnabled  bool `json:"local_login_enabled"`
	BootstrapAvailable bool `json:"bootstrap_available"`
}

// Options
// @Tags Auth
// @Summary 查询可用登录方式
// @Success 200 {object} util.ResponseTemplate{code=int,result=authOptionsResponse}
// @Router /api/v1/auth/options [get]
func (ac *AuthController) Options(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	if ac.service == nil {
		c.JSON(http.StatusServiceUnavailable, util.ResponseFailure("认证服务不可用", "authentication unavailable"))
		return
	}
	release, ok := ac.allowPublicAuthAttempt(c, ac.optionsGuard, "auth.options")
	if !ok {
		return
	}
	defer release()
	options, err := ac.cachedOptions(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, util.ResponseFailure("认证服务不可用", "authentication unavailable"))
		return
	}
	c.JSON(http.StatusOK, util.ResponseSuccessful("查询成功", authOptionsResponse{
		OIDCEnabled: options.OIDCEnabled, LocalLoginEnabled: options.LocalLoginEnabled,
		BootstrapAvailable: options.BootstrapAvailable,
	}))
}

func (ac *AuthController) cachedOptions(ctx context.Context) (auth.Options, error) {
	for {
		ac.optionsMu.Lock()
		if time.Now().Before(ac.optionsCacheUntil) {
			options := ac.optionsCache
			ac.optionsMu.Unlock()
			return options, nil
		}
		if ac.optionsLoading != nil {
			done := ac.optionsLoading
			ac.optionsMu.Unlock()
			select {
			case <-done:
				continue
			case <-ctx.Done():
				return auth.Options{}, ctx.Err()
			}
		}
		done := make(chan struct{})
		ac.optionsLoading = done
		ac.optionsMu.Unlock()

		options, err := ac.service.Options(ctx)
		ac.optionsMu.Lock()
		if err == nil {
			ac.optionsCache = options
			ac.optionsCacheUntil = time.Now().Add(authOptionsCacheTTL)
		}
		ac.optionsLoading = nil
		close(done)
		ac.optionsMu.Unlock()
		return options, err
	}
}

func (ac *AuthController) invalidateOptionsCache() {
	ac.optionsMu.Lock()
	ac.optionsCacheUntil = time.Time{}
	ac.optionsMu.Unlock()
}

type bootstrapRequest struct {
	BootstrapToken string `json:"bootstrap_token"`
	Username       string `json:"username"`
	DisplayName    string `json:"display_name"`
	Password       string `json:"password"`
}

// Bootstrap
// @Tags Auth
// @Summary 一次性创建首次本地管理员并登录
// @Param request body bootstrapRequest true "首次管理员信息"
// @Success 200 {object} util.ResponseTemplate
// @Failure 409 {object} util.ResponseTemplate
// @Router /api/v1/auth/bootstrap [post]
func (ac *AuthController) Bootstrap(c *gin.Context) {
	if !ac.requirePublicWriteOrigin(c, "auth.bootstrap") {
		return
	}
	release, ok := ac.allowPublicAuthAttempt(c, ac.bootstrapGuard, "auth.bootstrap")
	if !ok {
		return
	}
	defer release()
	var request bootstrapRequest
	if !BindJSON(c, &request, config.WebMaxJSONBodyBytes()) {
		ac.auditAdmittedAuthenticationFailureBestEffort(c, "auth.bootstrap", c.Writer.Status())
		return
	}
	grant, err := ac.service.Bootstrap(c.Request.Context(), request.BootstrapToken,
		request.Username, request.DisplayName, request.Password, ac.sessionCookie(c), RequestID(c))
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, auth.ErrBootstrapUnavailable) {
			status = http.StatusConflict
		}
		ac.auditAdmittedAuthenticationFailureBestEffort(c, "auth.bootstrap", status)
		c.JSON(status, util.ResponseFailure("首次管理员创建失败", publicAuthError(err)))
		return
	}
	ac.setSessionCookie(c, grant.Token)
	ac.invalidateOptionsCache()
	c.JSON(http.StatusOK, util.ResponseSuccessful("首次管理员已创建", sessionResponse(grant)))
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// Login
// @Tags Auth
// @Summary 使用本地恢复管理员账号登录
// @Param request body loginRequest true "登录信息"
// @Success 200 {object} util.ResponseTemplate
// @Failure 401 {object} util.ResponseTemplate
// @Router /api/v1/auth/login [post]
func (ac *AuthController) Login(c *gin.Context) {
	if !ac.requirePublicWriteOrigin(c, "auth.login") {
		return
	}
	release, ok := ac.allowPublicAuthAttempt(c, ac.loginGuard, "auth.login")
	if !ok {
		return
	}
	defer release()
	var request loginRequest
	if !BindJSON(c, &request, config.WebMaxJSONBodyBytes()) {
		ac.auditAdmittedAuthenticationFailureBestEffort(c, "auth.login", c.Writer.Status())
		return
	}
	grant, err := ac.service.LocalLogin(c.Request.Context(), request.Username, request.Password, ac.sessionCookie(c))
	if err != nil {
		status := http.StatusUnauthorized
		if !errors.Is(err, auth.ErrInvalidCredentials) {
			status = http.StatusServiceUnavailable
		}
		ac.auditAdmittedAuthenticationFailureBestEffort(c, "auth.login", status)
		c.JSON(status, util.ResponseFailure("登录失败", publicAuthError(err)))
		return
	}
	if err := ac.auditAuthentication(c, "auth.login", grant.Principal, "succeeded", http.StatusOK); err != nil {
		_ = ac.service.Logout(c.Request.Context(), grant.Token)
		c.JSON(http.StatusServiceUnavailable, util.ResponseFailure("审计服务不可用", "audit unavailable"))
		return
	}
	ac.setSessionCookie(c, grant.Token)
	c.JSON(http.StatusOK, util.ResponseSuccessful("登录成功", sessionResponse(grant)))
}

// Session
// @Tags Auth
// @Summary 查询当前服务端会话与最终权限
// @Success 200 {object} util.ResponseTemplate
// @Failure 401 {object} util.ResponseTemplate
// @Router /api/v1/auth/session [get]
func (ac *AuthController) Session(c *gin.Context) {
	grant, ok := AuthGrant(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, util.ResponseFailure("未登录或会话已失效", "unauthenticated"))
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, util.ResponseSuccessful("查询成功", sessionResponse(grant)))
}

// Logout
// @Tags Auth
// @Summary 撤销当前服务端会话
// @Success 200 {object} util.ResponseTemplate
// @Failure 401 {object} util.ResponseTemplate
// @Router /api/v1/auth/logout [post]
func (ac *AuthController) Logout(c *gin.Context) {
	if err := ac.service.Logout(c.Request.Context(), ac.sessionCookie(c)); err != nil {
		c.JSON(http.StatusServiceUnavailable, util.ResponseFailure("退出登录失败", "authentication unavailable"))
		return
	}
	ac.clearSessionCookie(c)
	c.JSON(http.StatusOK, util.ResponseSuccessful("已退出登录", nil))
}

type changePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

// ChangePassword
// @Tags Auth
// @Summary 修改当前本地用户密码并撤销该用户的全部会话
// @Param request body changePasswordRequest true "密码变更"
// @Success 200 {object} util.ResponseTemplate
// @Failure 400 {object} util.ResponseTemplate
// @Failure 422 {object} util.ResponseTemplate
// @Router /api/v1/auth/password [post]
func (ac *AuthController) ChangePassword(c *gin.Context) {
	grant, ok := AuthGrant(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, util.ResponseFailure("会话无效", "authentication required"))
		return
	}
	var request changePasswordRequest
	if !BindJSON(c, &request, 8*1024) {
		return
	}
	if err := ac.service.ChangePassword(
		c.Request.Context(), grant.Principal, request.CurrentPassword, request.NewPassword,
	); err != nil {
		status := http.StatusServiceUnavailable
		publicError := "authentication unavailable"
		var inputError *auth.InputError
		switch {
		case errors.Is(err, auth.ErrInvalidCredentials):
			status = http.StatusUnprocessableEntity
			publicError = "当前密码错误"
		case errors.As(err, &inputError):
			status = http.StatusBadRequest
			publicError = inputError.Error()
		case errors.Is(err, auth.ErrPasswordChangeUnsupported):
			status = http.StatusBadRequest
			publicError = auth.ErrPasswordChangeUnsupported.Error()
		case errors.Is(err, auth.ErrUnauthenticated):
			status = http.StatusUnauthorized
			ac.clearSessionCookie(c)
		}
		c.JSON(status, util.ResponseFailure("密码修改失败", publicError))
		return
	}
	ac.clearSessionCookie(c)
	c.JSON(http.StatusOK, util.ResponseSuccessful("密码已更新，所有会话均已撤销，请重新登录", nil))
}

// OIDCStart
// @Tags Auth
// @Summary 启动 OIDC Authorization Code + PKCE 登录
// @Param return_to query string false "登录后站内回跳路径"
// @Success 302
// @Router /api/v1/auth/oidc/start [get]
func (ac *AuthController) OIDCStart(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	c.Header("Referrer-Policy", "no-referrer")
	if ac.service == nil {
		c.JSON(http.StatusServiceUnavailable, util.ResponseFailure("认证服务不可用", "authentication unavailable"))
		return
	}
	values, err := url.ParseQuery(c.Request.URL.RawQuery)
	if err != nil || !validSingleQuery(values, map[string]struct{}{"return_to": {}}) {
		ac.auditAnonymousAuthenticationBestEffort(c, "auth.oidc.start", "failed", http.StatusBadRequest)
		c.JSON(http.StatusBadRequest, util.ResponseFailure("参数错误", "invalid query"))
		return
	}
	release, ok := ac.allowPublicAuthAttempt(c, ac.oidcStartGuard, "auth.oidc.start")
	if !ok {
		return
	}
	defer release()
	start, err := ac.service.StartOIDC(c.Request.Context(), values.Get("return_to"))
	if err != nil {
		status := http.StatusServiceUnavailable
		if !errors.Is(err, auth.ErrOIDCUnavailable) {
			status = http.StatusBadGateway
		}
		if errors.Is(err, auth.ErrOIDCFlowCapacity) {
			status = http.StatusServiceUnavailable
			c.Header("Retry-After", "60")
		}
		ac.auditAdmittedAuthenticationFailureBestEffort(c, "auth.oidc.start", status)
		c.JSON(status, util.ResponseFailure("OIDC 登录不可用", "OIDC unavailable"))
		return
	}
	http.SetCookie(c.Writer, &http.Cookie{
		Name: ac.service.FlowCookieName(), Value: start.BindingToken, Path: "/",
		MaxAge: ac.service.FlowMaxAge(), Secure: ac.service.CookieSecure(), HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	c.Header("Cache-Control", "no-store")
	c.Redirect(http.StatusFound, start.AuthorizationURL)
}

// OIDCCallback
// @Tags Auth
// @Summary 完成 OIDC 回调并建立服务端会话
// @Param state query string true "一次性 state"
// @Param code query string true "授权码"
// @Success 303
// @Failure 401 {object} util.ResponseTemplate
// @Router /api/v1/auth/oidc/callback [get]
func (ac *AuthController) OIDCCallback(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	c.Header("Referrer-Policy", "no-referrer")
	if ac.service == nil {
		c.JSON(http.StatusServiceUnavailable, util.ResponseFailure("认证服务不可用", "authentication unavailable"))
		return
	}
	values, parseErr := url.ParseQuery(c.Request.URL.RawQuery)
	if parseErr != nil || !validSingleQuery(values, map[string]struct{}{
		"state": {}, "code": {}, "iss": {}, "session_state": {},
	}) || !ac.service.ValidOIDCCallbackIssuer(values.Get("iss")) {
		ac.oidcFailure(c, false)
		return
	}
	binding, err := c.Cookie(ac.service.FlowCookieName())
	if err != nil {
		ac.oidcFailure(c, false)
		return
	}
	release, ok := ac.allowPublicAuthAttempt(c, ac.oidcCallbackGuard, "auth.oidc.callback")
	if !ok {
		return
	}
	defer release()
	grant, returnPath, err := ac.service.CompleteOIDC(c.Request.Context(), values.Get("state"), values.Get("code"), binding, ac.sessionCookie(c))
	if err != nil {
		ac.oidcFailure(c, true)
		return
	}
	if err := ac.auditAuthentication(c, "auth.oidc.callback", grant.Principal, "succeeded", http.StatusSeeOther); err != nil {
		_ = ac.service.Logout(c.Request.Context(), grant.Token)
		ac.clearFlowCookie(c)
		c.JSON(http.StatusServiceUnavailable, util.ResponseFailure("审计服务不可用", "audit unavailable"))
		return
	}
	ac.clearFlowCookie(c)
	ac.setSessionCookie(c, grant.Token)
	c.Header("Cache-Control", "no-store")
	c.Redirect(http.StatusSeeOther, returnPath)
}

func (ac *AuthController) oidcFailure(c *gin.Context, admitted bool) {
	ac.clearFlowCookie(c)
	if admitted {
		ac.auditAdmittedAuthenticationFailureBestEffort(c, "auth.oidc.callback", http.StatusUnauthorized)
	} else {
		ac.auditAnonymousAuthenticationBestEffort(c, "auth.oidc.callback", "failed", http.StatusUnauthorized)
	}
	c.JSON(http.StatusUnauthorized, util.ResponseFailure("OIDC 登录失败", "invalid OIDC flow"))
}

type userResponse struct {
	ID          string     `json:"id"`
	Username    string     `json:"username"`
	DisplayName string     `json:"display_name"`
	Email       string     `json:"email,omitempty"`
	AuthSource  string     `json:"auth_source"`
	Role        auth.Role  `json:"role"`
	Enabled     bool       `json:"enabled"`
	LastLoginAt *time.Time `json:"last_login_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// ListUsers
// @Tags System
// @Summary 分页查询身份用户
// @Param offset query int false "偏移量"
// @Param limit query int false "每页条数，最大 200"
// @Success 200 {object} util.ResponseTemplate
// @Failure 403 {object} util.ResponseTemplate
// @Router /api/v1/system/users [get]
func (ac *AuthController) ListUsers(c *gin.Context) {
	offset, limit, ok := parsePaginationQuery(c)
	if !ok {
		return
	}
	users, err := ac.service.ListUsers(c.Request.Context(), offset, limit)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, util.ResponseFailure("查询用户失败", "database unavailable"))
		return
	}
	items := make([]userResponse, 0, len(users))
	for _, user := range users {
		items = append(items, publicUser(user))
	}
	c.JSON(http.StatusOK, util.ResponseSuccessful("查询成功", gin.H{"items": items, "next_offset": offset + len(items)}))
}

type updateUserRequest struct {
	Role    *string `json:"role"`
	Enabled *bool   `json:"enabled"`
}

// UpdateUser
// @Tags System
// @Summary 修改用户角色或启用状态
// @Param user_id path int true "用户 ID"
// @Param request body updateUserRequest true "用户变更"
// @Success 200 {object} util.ResponseTemplate
// @Failure 409 {object} util.ResponseTemplate
// @Router /api/v1/system/users/{user_id} [patch]
func (ac *AuthController) UpdateUser(c *gin.Context) {
	userID, err := strconv.ParseInt(c.Param("user_id"), 10, 64)
	if err != nil || userID <= 0 {
		c.JSON(http.StatusBadRequest, util.ResponseFailure("无效的用户 ID", "invalid user id"))
		return
	}
	var request updateUserRequest
	if !BindJSON(c, &request, config.WebMaxJSONBodyBytes()) {
		return
	}
	if request.Role == nil && request.Enabled == nil {
		c.JSON(http.StatusBadRequest, util.ResponseFailure("至少提供一个变更字段", "empty patch"))
		return
	}
	patch := auth.UserPatch{Enabled: request.Enabled}
	if request.Role != nil {
		role, parseErr := auth.ParseRole(*request.Role)
		if parseErr != nil {
			c.JSON(http.StatusBadRequest, util.ResponseFailure("角色无效", parseErr.Error()))
			return
		}
		patch.Role = &role
	}
	user, err := ac.service.UpdateUser(c.Request.Context(), userID, patch)
	if err != nil {
		switch {
		case errors.Is(err, auth.ErrUserNotFound):
			c.JSON(http.StatusNotFound, util.ResponseFailure("更新用户失败", auth.ErrUserNotFound.Error()))
		case errors.Is(err, auth.ErrLastAdmin):
			c.JSON(http.StatusConflict, util.ResponseFailure("更新用户失败", auth.ErrLastAdmin.Error()))
		default:
			writeInternalFailure(c, http.StatusServiceUnavailable, "更新用户失败", "database", "update_user", err)
		}
		return
	}
	c.JSON(http.StatusOK, util.ResponseSuccessful("用户已更新", publicUser(user)))
}

type auditEventResponse struct {
	ID               string    `json:"id"`
	ActorUserID      *string   `json:"actor_user_id,omitempty"`
	ActorUsername    string    `json:"actor_username"`
	ActorDisplayName string    `json:"actor_display_name"`
	AuthSource       string    `json:"auth_source"`
	Action           string    `json:"action"`
	ResourceType     string    `json:"resource_type"`
	ResourceID       string    `json:"resource_id"`
	Result           string    `json:"result"`
	HTTPStatus       int       `json:"http_status"`
	RequestID        string    `json:"request_id"`
	CreatedAt        time.Time `json:"created_at"`
}

// ListAudit
// @Tags System
// @Summary 按只增游标查询安全审计事件
// @Param after_id query int false "上一页最后一条审计 ID"
// @Param through_id query int false "首屏返回的固定快照上界，翻页时原样传回"
// @Param limit query int false "每页条数，最大 200"
// @Success 200 {object} util.ResponseTemplate
// @Failure 403 {object} util.ResponseTemplate
// @Router /api/v1/system/audit-events [get]
func (ac *AuthController) ListAudit(c *gin.Context) {
	afterID, throughID, limit, ok := parseAuditQuery(c)
	if !ok {
		return
	}
	latest, err := ac.service.LatestAuditID(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, util.ResponseFailure("查询审计事件失败", "database unavailable"))
		return
	}
	if throughID == nil {
		throughID = &latest
	} else if *throughID > latest {
		c.JSON(http.StatusBadRequest, util.ResponseFailure("分页参数无效", "through_id exceeds current audit boundary"))
		return
	}
	if afterID > *throughID {
		c.JSON(http.StatusBadRequest, util.ResponseFailure("分页参数无效", "after_id exceeds through_id"))
		return
	}
	events, err := ac.service.ListAudit(c.Request.Context(), afterID, *throughID, limit+1)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, util.ResponseFailure("查询审计事件失败", "database unavailable"))
		return
	}
	hasMore := len(events) > limit
	if hasMore {
		events = events[:limit]
	}
	items := make([]auditEventResponse, 0, len(events))
	for _, event := range events {
		item := auditEventResponse{
			ID: strconv.FormatInt(event.ID, 10), ActorUsername: event.ActorUsername,
			ActorDisplayName: event.ActorDisplayName, AuthSource: event.AuthSource,
			Action: event.Action, ResourceType: event.ResourceType, ResourceID: event.ResourceID,
			Result: event.Result, HTTPStatus: event.HTTPStatus, RequestID: event.RequestID,
			CreatedAt: event.CreatedAt,
		}
		if event.ActorUserID != nil {
			value := strconv.FormatInt(*event.ActorUserID, 10)
			item.ActorUserID = &value
		}
		items = append(items, item)
	}
	next := afterID
	if len(events) > 0 {
		next = events[len(events)-1].ID
	}
	c.JSON(http.StatusOK, util.ResponseSuccessful("查询成功", gin.H{
		"items": items, "next_after_id": strconv.FormatInt(next, 10),
		"through_id": strconv.FormatInt(*throughID, 10), "has_more": hasMore,
	}))
}

func sessionResponse(grant auth.SessionGrant) gin.H {
	permissions := auth.PermissionsForRole(grant.Principal.Role)
	permissionStrings := make([]string, 0, len(permissions))
	for _, permission := range permissions {
		permissionStrings = append(permissionStrings, string(permission))
	}
	return gin.H{
		"user": gin.H{
			"id": strconv.FormatInt(grant.Principal.UserID, 10), "username": grant.Principal.Username,
			"display_name": grant.Principal.DisplayName, "email": grant.Principal.Email,
			"auth_source": grant.Principal.AuthSource, "roles": []string{string(grant.Principal.Role)},
			"permissions": permissionStrings,
		},
		"expires_at": grant.Principal.ExpiresAt.UTC().Format(time.RFC3339Nano),
		"csrf_token": grant.CSRFToken,
	}
}

func publicUser(user auth.User) userResponse {
	return userResponse{
		ID: strconv.FormatInt(user.ID, 10), Username: user.Username, DisplayName: user.DisplayName,
		Email: user.Email, AuthSource: user.AuthSource, Role: user.Role, Enabled: user.Enabled,
		LastLoginAt: user.LastLoginAt, CreatedAt: user.CreatedAt, UpdatedAt: user.UpdatedAt,
	}
}

func publicAuthError(err error) string {
	if errors.Is(err, auth.ErrInvalidCredentials) {
		return auth.ErrInvalidCredentials.Error()
	}
	if errors.Is(err, auth.ErrBootstrapUnavailable) {
		return auth.ErrBootstrapUnavailable.Error()
	}
	var inputError *auth.InputError
	if errors.As(err, &inputError) {
		return inputError.Error()
	}
	return "authentication unavailable"
}

func (ac *AuthController) requirePublicWriteOrigin(c *gin.Context, action string) bool {
	if ac.service == nil {
		c.JSON(http.StatusServiceUnavailable, util.ResponseFailure("认证服务不可用", "authentication unavailable"))
		return false
	}
	if !ac.service.ValidOrigin(c.GetHeader("Origin")) {
		ac.auditAnonymousAuthenticationBestEffort(c, action, "denied", http.StatusForbidden)
		c.JSON(http.StatusForbidden, util.ResponseFailure("请求来源无效", "invalid origin"))
		return false
	}
	return true
}

func (ac *AuthController) allowPublicAuthAttempt(c *gin.Context, guard *publicAuthGuard, action string) (func(), bool) {
	c.Header("Cache-Control", "no-store")
	release, ok := guard.acquire(publicAuthClientKey(c))
	if ok {
		return release, true
	}
	c.Header("Retry-After", "1")
	ac.auditRateLimitBestEffort(c, action)
	c.JSON(http.StatusTooManyRequests, util.ResponseFailure("请求过于频繁", "rate limit exceeded"))
	return nil, false
}

func (ac *AuthController) auditRateLimitBestEffort(c *gin.Context, action string) {
	ac.auditAnonymousAuthenticationBestEffort(c, action, "denied", http.StatusTooManyRequests)
}

func (ac *AuthController) auditAnonymousAuthenticationBestEffort(c *gin.Context, action, result string, status int) {
	if ac.anonymousAuditLimiter == nil || ac.anonymousAuditLimiter.Allow() {
		ac.auditAuthenticationBestEffort(c, action, auth.Principal{}, result, status)
	}
}

func (ac *AuthController) auditAdmittedAuthenticationFailureBestEffort(c *gin.Context, action string, status int) {
	if ac.authFailureAuditLimit == nil || ac.authFailureAuditLimit.Allow() {
		ac.auditAuthenticationBestEffort(c, action, auth.Principal{}, "failed", status)
	}
}

func (ac *AuthController) sessionCookie(c *gin.Context) string {
	if ac.service == nil {
		return ""
	}
	value, err := c.Cookie(ac.service.SessionCookieName())
	if err != nil {
		return ""
	}
	return value
}

func (ac *AuthController) setSessionCookie(c *gin.Context, value string) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name: ac.service.SessionCookieName(), Value: value, Path: "/",
		MaxAge: ac.service.SessionMaxAge(), Secure: ac.service.CookieSecure(), HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

func (ac *AuthController) clearSessionCookie(c *gin.Context) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name: ac.service.SessionCookieName(), Value: "", Path: "/", MaxAge: -1,
		Expires: time.Unix(1, 0), Secure: ac.service.CookieSecure(), HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

func (ac *AuthController) clearFlowCookie(c *gin.Context) {
	if ac.service == nil {
		return
	}
	http.SetCookie(c.Writer, &http.Cookie{
		Name: ac.service.FlowCookieName(), Value: "", Path: "/", MaxAge: -1,
		Expires: time.Unix(1, 0), Secure: ac.service.CookieSecure(), HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

func (ac *AuthController) auditAuthentication(c *gin.Context, action string, principal auth.Principal, result string, status int) error {
	if ac.service == nil {
		return errors.New("authentication unavailable")
	}
	event := auth.AuditEvent{
		ActorUsername: "anonymous", ActorDisplayName: "Anonymous", AuthSource: "anonymous",
		Action: action, ResourceType: "authentication", ResourceID: "", Result: result,
		HTTPStatus: status, RequestID: RequestID(c),
	}
	if principal.UserID > 0 {
		id := principal.UserID
		event.ActorUserID = &id
		event.ActorUsername = principal.Username
		event.ActorDisplayName = principal.DisplayName
		event.AuthSource = principal.AuthSource
	}
	return ac.service.AppendAudit(c.Request.Context(), event)
}

func (ac *AuthController) auditAuthenticationBestEffort(c *gin.Context, action string, principal auth.Principal, result string, status int) {
	if err := ac.auditAuthentication(c, action, principal, result, status); err != nil {
		slog.Error("append authentication audit event failed",
			"request_id", RequestID(c), "action", action)
	}
}

func validSingleQuery(values url.Values, allowed map[string]struct{}) bool {
	for key, entries := range values {
		if _, ok := allowed[key]; !ok || len(entries) != 1 || len(entries[0]) > 4096 {
			return false
		}
	}
	return true
}

func parsePaginationQuery(c *gin.Context) (int, int, bool) {
	values, parseErr := url.ParseQuery(c.Request.URL.RawQuery)
	if parseErr != nil || !validSingleQuery(values, map[string]struct{}{"offset": {}, "limit": {}}) {
		c.JSON(http.StatusBadRequest, util.ResponseFailure("分页参数无效", "invalid pagination"))
		return 0, 0, false
	}
	offset, limit := 0, 100
	var err error
	if raw := values.Get("offset"); raw != "" {
		offset, err = strconv.Atoi(raw)
		if err != nil || offset < 0 {
			c.JSON(http.StatusBadRequest, util.ResponseFailure("分页参数无效", "invalid pagination"))
			return 0, 0, false
		}
	}
	if raw := values.Get("limit"); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > 200 {
			c.JSON(http.StatusBadRequest, util.ResponseFailure("分页参数无效", "invalid pagination"))
			return 0, 0, false
		}
	}
	return offset, limit, true
}

func parseAuditQuery(c *gin.Context) (int64, *int64, int, bool) {
	values, parseErr := url.ParseQuery(c.Request.URL.RawQuery)
	if parseErr != nil || !validSingleQuery(values, map[string]struct{}{"after_id": {}, "through_id": {}, "limit": {}}) {
		c.JSON(http.StatusBadRequest, util.ResponseFailure("分页参数无效", "invalid pagination"))
		return 0, nil, 0, false
	}
	afterID, limit := int64(0), 100
	var throughID *int64
	var err error
	if raw := values.Get("after_id"); raw != "" {
		afterID, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || afterID < 0 {
			c.JSON(http.StatusBadRequest, util.ResponseFailure("分页参数无效", "invalid pagination"))
			return 0, nil, 0, false
		}
	}
	if raw := values.Get("through_id"); raw != "" {
		parsed, parseErr := strconv.ParseInt(raw, 10, 64)
		if parseErr != nil || parsed < 0 {
			c.JSON(http.StatusBadRequest, util.ResponseFailure("分页参数无效", "invalid pagination"))
			return 0, nil, 0, false
		}
		throughID = &parsed
	}
	if raw := values.Get("limit"); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > 200 {
			c.JSON(http.StatusBadRequest, util.ResponseFailure("分页参数无效", "invalid pagination"))
			return 0, nil, 0, false
		}
	}
	return afterID, throughID, limit, true
}

// AuthGrant is set by the API authentication middleware.
func AuthGrant(c *gin.Context) (auth.SessionGrant, bool) {
	value, ok := c.Get("ares.auth.grant")
	if !ok {
		return auth.SessionGrant{}, false
	}
	grant, ok := value.(auth.SessionGrant)
	return grant, ok
}

func RequestID(c *gin.Context) string {
	if value, ok := c.Get("request_id"); ok {
		if requestID, valid := value.(string); valid {
			return requestID
		}
	}
	return c.Writer.Header().Get("X-Request-ID")
}
