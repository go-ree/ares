package auth

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const (
	productionSessionCookie  = "__Host-ares_session"
	developmentSessionCookie = "ares_dev_session"
	productionFlowCookie     = "__Host-ares_oidc_flow"
	developmentFlowCookie    = "ares_dev_oidc_flow"
)

var localUsernamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{2,63}$`)

type OIDCClient interface {
	Issuer() string
	AuthorizationURL(context.Context, string, string, string) (string, error)
	ExchangeAndVerify(context.Context, string, string, []byte) (OIDCClaims, error)
}

func (s *Service) ValidOIDCCallbackIssuer(provided string) bool {
	if provided == "" {
		return true
	}
	return s.oidc != nil && provided == s.oidc.Issuer()
}

type Config struct {
	RootKey                string
	BootstrapToken         string
	PublicURL              string
	CookieSecure           bool
	LocalLoginEnabled      bool
	BootstrapEnabled       bool
	OIDCAutoProvision      bool
	SessionAbsoluteTimeout time.Duration
	SessionIdleTimeout     time.Duration
	SessionTouchInterval   time.Duration
	OIDCFlowTTL            time.Duration
}

type Service struct {
	store          Store
	keys           *keySet
	oidc           OIDCClient
	config         Config
	now            func() time.Time
	dummyHash      string
	expectedOrigin string
}

type Options struct {
	OIDCEnabled        bool
	LocalLoginEnabled  bool
	BootstrapAvailable bool
}

type SessionGrant struct {
	Token     string
	Principal Principal
	CSRFToken string
}

type OIDCStart struct {
	AuthorizationURL string
	BindingToken     string
}

func NewService(store Store, config Config, oidcClient OIDCClient) (*Service, error) {
	if store == nil {
		return nil, errors.New("auth store is required")
	}
	keys, err := newKeySet(config.RootKey)
	if err != nil {
		return nil, err
	}
	publicURL, err := validatePublicURL(config.PublicURL, config.CookieSecure)
	if err != nil {
		return nil, err
	}
	config.PublicURL = strings.TrimRight(publicURL.String(), "/")
	if config.SessionAbsoluteTimeout <= 0 {
		config.SessionAbsoluteTimeout = 8 * time.Hour
	}
	if config.SessionIdleTimeout <= 0 || config.SessionIdleTimeout > config.SessionAbsoluteTimeout {
		config.SessionIdleTimeout = 30 * time.Minute
	}
	if config.SessionTouchInterval <= 0 || config.SessionTouchInterval >= config.SessionIdleTimeout {
		config.SessionTouchInterval = 5 * time.Minute
	}
	if config.OIDCFlowTTL <= 0 || config.OIDCFlowTTL > 30*time.Minute {
		config.OIDCFlowTTL = 10 * time.Minute
	}
	dummyHash, err := HashPassword("ares dummy password for constant work")
	if err != nil {
		return nil, err
	}
	return &Service{
		store: store, keys: keys, oidc: oidcClient, config: config,
		now: time.Now, dummyHash: dummyHash,
		expectedOrigin: publicURL.Scheme + "://" + publicURL.Host,
	}, nil
}

func validatePublicURL(raw string, cookieSecure bool) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return nil, errors.New("auth public URL must be an origin without credentials, path, query or fragment")
	}
	loopback := parsed.Hostname() == "localhost"
	if address := net.ParseIP(parsed.Hostname()); address != nil {
		loopback = address.IsLoopback()
	}
	if parsed.Scheme != "https" && !(parsed.Scheme == "http" && loopback) {
		return nil, errors.New("auth public URL must use HTTPS except on loopback")
	}
	if parsed.Scheme == "https" && !cookieSecure {
		return nil, errors.New("secure public URL requires secure auth cookies")
	}
	if parsed.Scheme == "http" && cookieSecure {
		return nil, errors.New("loopback HTTP cannot use secure auth cookies")
	}
	return parsed, nil
}

func (s *Service) Options(ctx context.Context) (Options, error) {
	available, err := s.store.BootstrapAvailable(ctx)
	if err != nil {
		return Options{}, err
	}
	return Options{
		OIDCEnabled:        s.oidc != nil,
		LocalLoginEnabled:  s.config.LocalLoginEnabled,
		BootstrapAvailable: s.config.BootstrapEnabled && available && len(s.config.BootstrapToken) >= 32,
	}, nil
}

// EnsureAdministrativeAccess prevents an otherwise healthy deployment from
// starting in a state where no authenticated user can ever administer it.
// A still-available, explicitly enabled bootstrap ceremony is a valid initial
// state; once it is consumed, at least one enabled administrator must remain.
func (s *Service) EnsureAdministrativeAccess(ctx context.Context) error {
	oidcIssuer := ""
	if s.oidc != nil {
		oidcIssuer = s.oidc.Issuer()
	}
	hasAdmin, err := s.store.HasEnabledAdmin(ctx, s.config.LocalLoginEnabled, oidcIssuer)
	if err != nil {
		return err
	}
	if hasAdmin {
		return nil
	}
	available, err := s.store.BootstrapAvailable(ctx)
	if err != nil {
		return err
	}
	if s.config.BootstrapEnabled && len(s.config.BootstrapToken) >= 32 && available {
		return nil
	}
	return errors.New("没有可登录管理员；首次部署必须启用一次性 bootstrap，已初始化部署必须至少保留一个可通过当前认证方式登录的 admin")
}

func (s *Service) Bootstrap(ctx context.Context, token, username, displayName, password, previousSession, requestID string) (SessionGrant, error) {
	if !s.config.BootstrapEnabled || len(s.config.BootstrapToken) < 32 || len(token) != len(s.config.BootstrapToken) ||
		subtle.ConstantTimeCompare([]byte(token), []byte(s.config.BootstrapToken)) != 1 {
		return SessionGrant{}, ErrBootstrapUnavailable
	}
	username = strings.TrimSpace(username)
	displayName = strings.TrimSpace(displayName)
	if !localUsernamePattern.MatchString(username) {
		return SessionGrant{}, newInputError("用户名必须为 3 到 64 位字母、数字、点、下划线或连字符")
	}
	if displayName == "" || len(displayName) > 255 {
		return SessionGrant{}, newInputError("显示名称不能为空且不能超过 255 字节")
	}
	passwordHash, err := HashPasswordContext(ctx, password)
	if err != nil {
		return SessionGrant{}, err
	}
	now := s.now().UTC()
	user, err := s.store.CreateBootstrapAdmin(ctx, BootstrapUser{
		Username: username, DisplayName: displayName, PasswordHash: passwordHash,
	}, AuditEvent{
		Action: "auth.bootstrap", ResourceType: "authentication", Result: "succeeded",
		HTTPStatus: 200, RequestID: requestID,
	}, now)
	if err != nil {
		if errors.Is(err, ErrBootstrapUnavailable) {
			return SessionGrant{}, ErrBootstrapUnavailable
		}
		return SessionGrant{}, err
	}
	return s.newSession(ctx, user, previousSession, now)
}

func (s *Service) LocalLogin(ctx context.Context, username, password, previousSession string) (SessionGrant, error) {
	if !s.config.LocalLoginEnabled {
		return SessionGrant{}, ErrInvalidCredentials
	}
	username = strings.TrimSpace(username)
	user, err := s.store.FindLocalUser(ctx, username)
	if err != nil && !errors.Is(err, ErrUserNotFound) {
		return SessionGrant{}, err
	}
	hash := s.dummyHash
	if err == nil {
		hash = user.PasswordHash
	}
	valid, verifyErr := VerifyPasswordContext(ctx, hash, password)
	if verifyErr != nil {
		return SessionGrant{}, verifyErr
	}
	if err != nil || !valid || !user.Enabled || user.AuthSource != "bootstrap" {
		return SessionGrant{}, ErrInvalidCredentials
	}
	now := s.now().UTC()
	token, err := randomToken()
	if err != nil {
		return SessionGrant{}, fmt.Errorf("generate session token: %w", err)
	}
	expiresAt := now.Add(s.config.SessionAbsoluteTimeout)
	sessionHash := s.keys.sessionHash(token)
	var previousHash []byte
	if previousSession != "" {
		previousHash = s.keys.sessionHash(previousSession)
	}
	user, err = s.store.CreateLocalSession(
		ctx, user.ID, user.PasswordHash, sessionHash, previousHash, expiresAt, now,
	)
	if err != nil {
		return SessionGrant{}, err
	}
	return s.sessionGrant(token, sessionHash, user, expiresAt), nil
}

func (s *Service) newSession(ctx context.Context, user User, previousSession string, now time.Time) (SessionGrant, error) {
	if previousSession != "" {
		if err := s.store.RevokeSession(ctx, s.keys.sessionHash(previousSession), now); err != nil {
			return SessionGrant{}, fmt.Errorf("revoke previous session: %w", err)
		}
	}
	token, err := randomToken()
	if err != nil {
		return SessionGrant{}, fmt.Errorf("generate session token: %w", err)
	}
	expiresAt := now.Add(s.config.SessionAbsoluteTimeout)
	hash := s.keys.sessionHash(token)
	if err := s.store.CreateSession(ctx, hash, user.ID, expiresAt, now); err != nil {
		return SessionGrant{}, err
	}
	return s.sessionGrant(token, hash, user, expiresAt), nil
}

func (s *Service) sessionGrant(token string, hash []byte, user User, expiresAt time.Time) SessionGrant {
	return SessionGrant{
		Token: token,
		Principal: Principal{
			UserID: user.ID, Username: user.Username, DisplayName: user.DisplayName,
			Email: user.Email, Role: user.Role, AuthSource: user.AuthSource,
			SessionHash: append([]byte(nil), hash...), ExpiresAt: expiresAt,
		},
		CSRFToken: s.keys.csrfToken(token),
	}
}

func (s *Service) Authenticate(ctx context.Context, token string) (SessionGrant, error) {
	return s.authenticate(ctx, token, true)
}

// Revalidate verifies an existing session without refreshing its idle window.
// It is intended for long-lived transports such as SSE: transport heartbeats
// must not turn an otherwise idle browser session into an immortal session.
func (s *Service) Revalidate(ctx context.Context, token string) (SessionGrant, error) {
	return s.authenticate(ctx, token, false)
}

func (s *Service) authenticate(ctx context.Context, token string, touch bool) (SessionGrant, error) {
	if !validOpaqueToken(token) {
		return SessionGrant{}, ErrUnauthenticated
	}
	hash := s.keys.sessionHash(token)
	session, err := s.store.FindSession(ctx, hash)
	if err != nil {
		if errors.Is(err, ErrSessionNotFound) {
			return SessionGrant{}, ErrUnauthenticated
		}
		return SessionGrant{}, err
	}
	now := s.now().UTC()
	if session.RevokedAt != nil || !session.User.Enabled || !now.Before(session.ExpiresAt) ||
		now.Sub(session.LastSeenAt) >= s.config.SessionIdleTimeout {
		_ = s.store.RevokeSession(ctx, hash, now)
		return SessionGrant{}, ErrUnauthenticated
	}
	if touch && now.Sub(session.LastSeenAt) >= s.config.SessionTouchInterval {
		if err := s.store.TouchSession(ctx, hash, now); err != nil {
			if errors.Is(err, ErrSessionNotFound) {
				return SessionGrant{}, ErrUnauthenticated
			}
			return SessionGrant{}, err
		}
	}
	return SessionGrant{
		Token: token,
		Principal: Principal{
			UserID: session.User.ID, Username: session.User.Username,
			DisplayName: session.User.DisplayName, Email: session.User.Email,
			Role: session.User.Role, AuthSource: session.User.AuthSource,
			SessionHash: append([]byte(nil), hash...), ExpiresAt: session.ExpiresAt,
		},
		CSRFToken: s.keys.csrfToken(token),
	}, nil
}

func validOpaqueToken(token string) bool {
	if len(token) != 43 {
		return false
	}
	decoded, err := base64.RawURLEncoding.DecodeString(token)
	return err == nil && len(decoded) == 32
}

func (s *Service) ValidCSRF(sessionToken, provided string) bool {
	return s.keys.validCSRF(sessionToken, provided)
}

func (s *Service) ValidOrigin(origin string) bool {
	return origin == s.expectedOrigin
}

func (s *Service) Logout(ctx context.Context, sessionToken string) error {
	if !validOpaqueToken(sessionToken) {
		return nil
	}
	return s.store.RevokeSession(ctx, s.keys.sessionHash(sessionToken), s.now().UTC())
}

// ChangePassword rotates the only application-managed credential type. The
// store revokes every session for the user in the same transaction as the
// password update, so a copied browser session cannot survive the rotation.
func (s *Service) ChangePassword(ctx context.Context, principal Principal, currentPassword, newPassword string) error {
	if principal.UserID <= 0 || principal.AuthSource != "bootstrap" {
		return ErrPasswordChangeUnsupported
	}
	user, err := s.store.FindLocalUser(ctx, principal.Username)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			return ErrUnauthenticated
		}
		return err
	}
	if user.ID != principal.UserID || user.AuthSource != "bootstrap" || !user.Enabled {
		return ErrUnauthenticated
	}
	valid, err := VerifyPasswordContext(ctx, user.PasswordHash, currentPassword)
	if err != nil {
		return err
	}
	if !valid {
		return ErrInvalidCredentials
	}
	if currentPassword == newPassword {
		return newInputError("新密码不能与当前密码相同")
	}
	newHash, err := HashPasswordContext(ctx, newPassword)
	if err != nil {
		return err
	}
	return s.store.ChangeLocalPassword(ctx, user.ID, user.PasswordHash, newHash, s.now().UTC())
}

func (s *Service) StartOIDC(ctx context.Context, returnPath string) (OIDCStart, error) {
	if s.oidc == nil {
		return OIDCStart{}, ErrOIDCUnavailable
	}
	returnPath = SafeReturnPath(returnPath)
	state, err := randomToken()
	if err != nil {
		return OIDCStart{}, err
	}
	nonce, err := randomToken()
	if err != nil {
		return OIDCStart{}, err
	}
	verifier, err := randomToken()
	if err != nil {
		return OIDCStart{}, err
	}
	binding, err := randomToken()
	if err != nil {
		return OIDCStart{}, err
	}
	stateHash, bindingHash := tokenHash(state), tokenHash(binding)
	ciphertext, err := s.keys.encryptFlowVerifier(verifier, stateHash, bindingHash)
	if err != nil {
		return OIDCStart{}, err
	}
	now := s.now().UTC()
	authorizationURL, err := s.oidc.AuthorizationURL(ctx, state, nonce, verifier)
	if err != nil {
		return OIDCStart{}, err
	}
	if err := s.store.CreateOIDCFlow(ctx, OIDCFlow{
		StateHash: stateHash, NonceHash: tokenHash(nonce), BindingHash: bindingHash,
		VerifierCiphertext: ciphertext, ReturnPath: returnPath,
		ExpiresAt: now.Add(s.config.OIDCFlowTTL), CreatedAt: now,
	}); err != nil {
		return OIDCStart{}, err
	}
	return OIDCStart{AuthorizationURL: authorizationURL, BindingToken: binding}, nil
}

func (s *Service) CompleteOIDC(ctx context.Context, state, code, binding, previousSession string) (SessionGrant, string, error) {
	if s.oidc == nil || !validOpaqueToken(state) || !validOpaqueToken(binding) || strings.TrimSpace(code) == "" {
		return SessionGrant{}, "", ErrInvalidOIDCFlow
	}
	stateHash, bindingHash := tokenHash(state), tokenHash(binding)
	now := s.now().UTC()
	flow, err := s.store.ConsumeOIDCFlow(ctx, stateHash, bindingHash, now)
	if err != nil {
		return SessionGrant{}, "", ErrInvalidOIDCFlow
	}
	verifier, err := s.keys.decryptFlowVerifier(flow.VerifierCiphertext, stateHash, bindingHash)
	if err != nil {
		return SessionGrant{}, "", ErrInvalidOIDCFlow
	}
	claims, err := s.oidc.ExchangeAndVerify(ctx, code, verifier, flow.NonceHash)
	if err != nil {
		return SessionGrant{}, "", ErrInvalidOIDCFlow
	}
	user, err := s.store.UpsertOIDCUser(ctx, OIDCUser{
		Issuer: claims.Issuer, Subject: claims.Subject,
		IdentityHash: identityHash(claims.Issuer, claims.Subject),
		Email:        claims.Email, DisplayName: claims.Name,
		PreferredUsername: claims.PreferredUsername,
	}, now, s.config.OIDCAutoProvision)
	if err != nil {
		return SessionGrant{}, "", err
	}
	if !user.Enabled {
		return SessionGrant{}, "", ErrUnauthenticated
	}
	grant, err := s.newSession(ctx, user, previousSession, now)
	if err != nil {
		return SessionGrant{}, "", err
	}
	return grant, SafeReturnPath(flow.ReturnPath), nil
}

func SafeReturnPath(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || !strings.HasPrefix(value, "/") || strings.HasPrefix(value, "//") ||
		strings.ContainsAny(value, "\r\n\\") {
		return "/"
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.IsAbs() || parsed.Host != "" {
		return "/"
	}
	return parsed.RequestURI()
}

func (s *Service) SessionCookieName() string {
	if s.config.CookieSecure {
		return productionSessionCookie
	}
	return developmentSessionCookie
}

func (s *Service) FlowCookieName() string {
	if s.config.CookieSecure {
		return productionFlowCookie
	}
	return developmentFlowCookie
}

func (s *Service) CookieSecure() bool { return s.config.CookieSecure }

func (s *Service) SessionMaxAge() int { return int(s.config.SessionAbsoluteTimeout.Seconds()) }

func (s *Service) FlowMaxAge() int { return int(s.config.OIDCFlowTTL.Seconds()) }

func (s *Service) AppendAudit(ctx context.Context, event AuditEvent) error {
	event.CreatedAt = s.now().UTC()
	return s.store.AppendAudit(ctx, event)
}

func (s *Service) LatestAuditID(ctx context.Context) (int64, error) {
	return s.store.LatestAuditID(ctx)
}

func (s *Service) ListAudit(ctx context.Context, afterID, throughID int64, limit int) ([]AuditEvent, error) {
	return s.store.ListAudit(ctx, afterID, throughID, limit)
}

func (s *Service) ListUsers(ctx context.Context, offset, limit int) ([]User, error) {
	return s.store.ListUsers(ctx, offset, limit)
}

func (s *Service) UpdateUser(ctx context.Context, userID int64, patch UserPatch) (User, error) {
	if userID <= 0 || (patch.Role == nil && patch.Enabled == nil) {
		return User{}, errors.New("用户变更无效")
	}
	if patch.Role != nil {
		if _, err := ParseRole(string(*patch.Role)); err != nil {
			return User{}, err
		}
	}
	oidcIssuer := ""
	if s.oidc != nil {
		oidcIssuer = s.oidc.Issuer()
	}
	return s.store.UpdateUser(ctx, userID, patch, s.now().UTC(), s.config.LocalLoginEnabled, oidcIssuer)
}
