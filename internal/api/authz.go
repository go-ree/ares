package api

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-ree/ares/internal/api/controller"
	"github.com/go-ree/ares/internal/api/util"
	"github.com/go-ree/ares/internal/auth"
	"golang.org/x/time/rate"
)

const legacyAdminTokenHeader = "X-Ares-Admin-Token"

type Runtime struct {
	Auth                    *auth.Service
	LegacyAdminTokenEnabled bool
	LegacyAdminToken        string
	LegacyAdminTokenSunset  string
	anonymousAuditLimiter   *rate.Limiter
	rateLimitedAuditLimiter *rate.Limiter
	authenticationAdmission *requestAdmission
	authenticatedAdmission  *requestAdmission
	credentialAdmission     *requestAdmission
	deniedAdmission         *denialAdmission
}

func (runtime Runtime) withSecurityDefaults() Runtime {
	if runtime.anonymousAuditLimiter == nil {
		runtime.anonymousAuditLimiter = rate.NewLimiter(rate.Limit(2), 10)
	}
	if runtime.rateLimitedAuditLimiter == nil {
		runtime.rateLimitedAuditLimiter = rate.NewLimiter(rate.Every(5*time.Second), 10)
	}
	if runtime.authenticationAdmission == nil {
		// Bound database-backed session lookups before authentication has
		// established a principal. The key is a one-way session-token digest.
		runtime.authenticationAdmission = newRequestAdmission(100, 200, 10, 30, 32, 4)
	}
	if runtime.authenticatedAdmission == nil {
		runtime.authenticatedAdmission = newRequestAdmission(100, 200, 20, 80, 256, 16)
	}
	if runtime.credentialAdmission == nil {
		// Password verification is intentionally much more expensive than an
		// ordinary authenticated request. Isolate it by both stable user and
		// trusted client address before any Argon2 work or authorized audit write.
		runtime.credentialAdmission = newRequestAdmission(
			rate.Limit(2), 4, rate.Every(5*time.Second), 3, 2, 1,
		)
	}
	if runtime.deniedAdmission == nil {
		// Preserve the first security events, then cap permanent audit growth
		// from a compromised low-privilege account.
		runtime.deniedAdmission = newDenialAdmission(2, 256, rate.Every(time.Minute), 64)
	}
	return runtime
}

type routePolicy struct {
	Permission      auth.Permission
	Action          string
	ResourceType    string
	ResourceParam   string
	SensitiveRead   bool
	AllowLegacy     bool
	SSE             bool
	CredentialCheck bool
}

func (runtime Runtime) require(policy routePolicy) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Cookie-authenticated content must never be stored by shared or browser
		// caches. This is set before every failure path as well as successful reads.
		c.Header("Cache-Control", "private, no-store")
		if runtime.Auth == nil {
			c.AbortWithStatusJSON(http.StatusServiceUnavailable,
				util.ResponseFailure("认证服务不可用", "authentication unavailable"))
			return
		}

		if policy.AllowLegacy && runtime.validLegacyToken(c.GetHeader(legacyAdminTokenHeader)) {
			principal := auth.Principal{
				Username: "legacy-admin-token", DisplayName: "Legacy administrator token",
				Role: auth.RoleAdmin, AuthSource: "legacy-token",
			}
			release, admitted := runtime.authenticatedAdmission.acquire(principalAdmissionKey(principal))
			if !admitted {
				runtime.rejectRateLimited(c, policy, principal, 1)
				return
			}
			defer release()
			controller.SetPrincipal(c, principal)
			runtime.setLegacyDeprecationHeaders(c)
			runtime.runAuthorized(c, policy, principal)
			return
		}

		sessionToken, err := c.Cookie(runtime.Auth.SessionCookieName())
		if err != nil {
			runtime.auditDenied(c, policy, auth.Principal{}, http.StatusUnauthorized)
			c.AbortWithStatusJSON(http.StatusUnauthorized,
				util.ResponseFailure("未登录或会话已失效", "unauthenticated"))
			return
		}
		releaseAuthentication, admitted := runtime.authenticationAdmission.acquire(
			sessionAdmissionKey(sessionToken), clientAdmissionKey(c),
		)
		if !admitted {
			runtime.rejectRateLimited(c, policy, auth.Principal{}, 1)
			return
		}
		grant, err := runtime.Auth.Authenticate(c.Request.Context(), sessionToken)
		releaseAuthentication()
		if err != nil {
			status := http.StatusServiceUnavailable
			message, publicError := "认证服务不可用", "authentication unavailable"
			if errors.Is(err, auth.ErrUnauthenticated) {
				status, message, publicError = http.StatusUnauthorized, "未登录或会话已失效", "unauthenticated"
				clearSessionCookie(c, runtime.Auth)
			}
			runtime.auditDenied(c, policy, auth.Principal{}, status)
			c.AbortWithStatusJSON(status, util.ResponseFailure(message, publicError))
			return
		}
		releaseRequest, admitted := runtime.authenticatedAdmission.acquire(principalAdmissionKey(grant.Principal))
		if !admitted {
			runtime.rejectRateLimited(c, policy, grant.Principal, 1)
			return
		}
		defer releaseRequest()
		if policy.Permission != "" && !grant.Principal.Has(policy.Permission) {
			if !runtime.deniedAdmission.allow(principalAdmissionKey(grant.Principal)) {
				runtime.rejectRateLimited(c, policy, grant.Principal, 60)
				return
			}
			runtime.auditDenied(c, policy, grant.Principal, http.StatusForbidden)
			c.AbortWithStatusJSON(http.StatusForbidden,
				util.ResponseFailure("没有执行该操作的权限", "forbidden"))
			return
		}
		if isUnsafeMethod(c.Request.Method) &&
			(!runtime.Auth.ValidOrigin(c.GetHeader("Origin")) ||
				!runtime.Auth.ValidCSRF(sessionToken, c.GetHeader("X-CSRF-Token"))) {
			if !runtime.deniedAdmission.allow(principalAdmissionKey(grant.Principal)) {
				runtime.rejectRateLimited(c, policy, grant.Principal, 60)
				return
			}
			runtime.auditDenied(c, policy, grant.Principal, http.StatusForbidden)
			c.AbortWithStatusJSON(http.StatusForbidden,
				util.ResponseFailure("请求来源或 CSRF Token 无效", "invalid CSRF protection"))
			return
		}
		if policy.CredentialCheck {
			releaseCredential, admitted := runtime.credentialAdmission.acquire(
				principalAdmissionKey(grant.Principal), clientAdmissionKey(c),
			)
			if !admitted {
				runtime.rejectRateLimited(c, policy, grant.Principal, 5)
				return
			}
			defer releaseCredential()
		}

		c.Set("ares.auth.grant", grant)
		controller.SetPrincipal(c, grant.Principal)
		if policy.SSE {
			controller.AttachSSESessionRevalidator(c, func(ctx context.Context) error {
				revalidated, revalidateErr := runtime.Auth.Revalidate(ctx, sessionToken)
				if revalidateErr != nil {
					if errors.Is(revalidateErr, auth.ErrUnauthenticated) {
						return controller.ErrSSESessionExpired
					}
					return revalidateErr
				}
				if policy.Permission != "" && !revalidated.Principal.Has(policy.Permission) {
					return controller.ErrSSESessionExpired
				}
				return nil
			})
		}
		runtime.runAuthorized(c, policy, grant.Principal)
	}
}

func (runtime Runtime) rejectRateLimited(c *gin.Context, policy routePolicy, principal auth.Principal, retryAfter int) {
	if retryAfter < 1 {
		retryAfter = 1
	}
	c.Header("Retry-After", strconv.Itoa(retryAfter))
	if runtime.rateLimitedAuditLimiter == nil || runtime.rateLimitedAuditLimiter.Allow() {
		if err := runtime.appendAudit(c.Request.Context(), c, policy, principal, "denied", http.StatusTooManyRequests); err != nil {
			slog.Error("append rate-limit audit event failed", "request_id", controller.RequestID(c),
				"action", policy.Action)
		}
	}
	c.AbortWithStatusJSON(http.StatusTooManyRequests,
		util.ResponseFailure("请求过于频繁", "rate limit exceeded"))
}

func sessionAdmissionKey(sessionToken string) string {
	digest := sha256.Sum256([]byte(sessionToken))
	return "session:" + hex.EncodeToString(digest[:16])
}

// clientAdmissionKey uses the address resolved by Gin after the server has
// installed its explicit trusted-proxy policy. Pairing this with the session
// digest prevents an attacker from bypassing pre-authentication limits simply
// by rotating syntactically valid but nonexistent session cookies.
func clientAdmissionKey(c *gin.Context) string {
	if c != nil {
		if address := net.ParseIP(c.ClientIP()); address != nil {
			return "client:" + address.String()
		}
	}
	return "client:unknown"
}

func principalAdmissionKey(principal auth.Principal) string {
	if principal.UserID > 0 {
		return "user:" + strconv.FormatInt(principal.UserID, 10)
	}
	return "subject:" + principal.AuthSource + ":" + principal.Username
}

func (runtime Runtime) runAuthorized(c *gin.Context, policy routePolicy, principal auth.Principal) {
	audit := isUnsafeMethod(c.Request.Method) || policy.SensitiveRead
	if audit {
		if err := runtime.appendAudit(c.Request.Context(), c, policy, principal, "authorized", 0); err != nil {
			c.AbortWithStatusJSON(http.StatusServiceUnavailable,
				util.ResponseFailure("审计服务不可用", "audit unavailable"))
			return
		}
	}
	if !audit {
		c.Next()
		return
	}
	defer func() {
		panicValue := recover()
		status := c.Writer.Status()
		result := "succeeded"
		if panicValue != nil {
			status, result = http.StatusInternalServerError, "failed"
		} else if status >= http.StatusBadRequest || controller.RequestAuditFailureMarked(c) {
			result = "failed"
		}
		// A client disconnect cancels the request context, but the final audit
		// outcome is still security-significant. Detach cancellation while
		// retaining request-scoped values and impose a short database deadline.
		auditContext, cancelAudit := context.WithTimeout(context.WithoutCancel(c.Request.Context()), 2*time.Second)
		err := runtime.appendAudit(auditContext, c, policy, principal, result, status)
		cancelAudit()
		if err != nil {
			// The handler may already have committed its response. Never replace it
			// with a misleading second body; emit only a redacted operational signal.
			slog.Error("append final audit event failed", "request_id", controller.RequestID(c),
				"action", policy.Action)
		}
		if panicValue != nil {
			panic(panicValue)
		}
	}()
	c.Next()
}

func (runtime Runtime) auditDenied(c *gin.Context, policy routePolicy, principal auth.Principal, status int) {
	if principal.UserID <= 0 && runtime.anonymousAuditLimiter != nil && !runtime.anonymousAuditLimiter.Allow() {
		return
	}
	if err := runtime.appendAudit(c.Request.Context(), c, policy, principal, "denied", status); err != nil {
		slog.Error("append denied audit event failed", "request_id", controller.RequestID(c),
			"action", policy.Action)
	}
}

func (runtime Runtime) appendAudit(ctx context.Context, c *gin.Context, policy routePolicy, principal auth.Principal, result string, status int) error {
	event := auth.AuditEvent{
		ActorUsername: "anonymous", ActorDisplayName: "Anonymous", AuthSource: "anonymous",
		Action: truncateAuditValue(policy.Action, 100), ResourceType: truncateAuditValue(policy.ResourceType, 100),
		ResourceID: truncateAuditValue(resourceID(c, policy), 255), Result: result,
		HTTPStatus: status, RequestID: truncateAuditValue(controller.RequestID(c), 64),
	}
	if principal.UserID > 0 {
		id := principal.UserID
		event.ActorUserID = &id
	}
	if principal.Username != "" {
		event.ActorUsername = truncateAuditValue(principal.Username, 100)
		event.ActorDisplayName = truncateAuditValue(principal.DisplayName, 255)
		event.AuthSource = truncateAuditValue(principal.AuthSource, 32)
	}
	return runtime.Auth.AppendAudit(ctx, event)
}

func resourceID(c *gin.Context, policy routePolicy) string {
	if discovered := controller.RequestAuditResourceID(c); discovered != "" {
		return discovered
	}
	if policy.ResourceParam == "" {
		return ""
	}
	return c.Param(policy.ResourceParam)
}

func truncateAuditValue(value string, maximum int) string {
	value = strings.TrimSpace(value)
	normalized := strings.ToValidUTF8(value, "�")
	runes := []rune(normalized)
	if len(runes) <= maximum {
		return normalized
	}
	return string(runes[:maximum])
}

func (runtime Runtime) validLegacyToken(provided string) bool {
	expected := runtime.LegacyAdminToken
	return runtime.LegacyAdminTokenEnabled && len(expected) >= 32 && len(provided) == len(expected) &&
		subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) == 1
}

func (runtime Runtime) setLegacyDeprecationHeaders(c *gin.Context) {
	c.Header("Deprecation", "true")
	if sunset := strings.TrimSpace(runtime.LegacyAdminTokenSunset); sunset != "" {
		if parsed, err := time.Parse(time.RFC3339, sunset); err == nil {
			c.Header("Sunset", parsed.UTC().Format(http.TimeFormat))
		}
	}
	c.Header("Warning", `299 - "X-Ares-Admin-Token 已弃用，请迁移到管理员会话"`)
}

func isUnsafeMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	default:
		return true
	}
}

func clearSessionCookie(c *gin.Context, service *auth.Service) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name: service.SessionCookieName(), Value: "", Path: "/", MaxAge: -1,
		Expires: time.Unix(1, 0), Secure: service.CookieSecure(), HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}
