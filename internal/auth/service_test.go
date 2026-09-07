package auth

import (
	"bytes"
	"context"
	"errors"
	"net/url"
	"testing"
	"time"
)

type fakeStore struct {
	bootstrapAvailable  bool
	hasEnabledAdmin     bool
	adminLocalEnabled   bool
	adminOIDCIssuer     string
	bootstrapUser       User
	localUser           User
	session             Session
	findSessionHash     []byte
	createdSessionHash  []byte
	createdFlow         OIDCFlow
	consumedFlow        OIDCFlow
	oidcUser            User
	bootstrapAudit      AuditEvent
	revoked             bool
	revokeErr           error
	changedPasswordHash string
	beforeLocalSession  func()
	touched             int
	touchErr            error
}

func (f *fakeStore) BootstrapAvailable(context.Context) (bool, error) {
	return f.bootstrapAvailable, nil
}
func (f *fakeStore) HasEnabledAdmin(_ context.Context, localLoginEnabled bool, oidcIssuer string) (bool, error) {
	f.adminLocalEnabled = localLoginEnabled
	f.adminOIDCIssuer = oidcIssuer
	return f.hasEnabledAdmin, nil
}
func (f *fakeStore) CreateBootstrapAdmin(_ context.Context, input BootstrapUser, event AuditEvent, now time.Time) (User, error) {
	if !f.bootstrapAvailable {
		return User{}, ErrBootstrapUnavailable
	}
	f.bootstrapAvailable = false
	userID := f.bootstrapUser.ID
	event.ActorUserID = &userID
	event.ActorUsername = input.Username
	event.ActorDisplayName = input.DisplayName
	event.AuthSource = "bootstrap"
	event.CreatedAt = now
	f.bootstrapAudit = event
	return f.bootstrapUser, nil
}
func (f *fakeStore) FindLocalUser(context.Context, string) (User, error) {
	if f.localUser.ID == 0 {
		return User{}, ErrUserNotFound
	}
	return f.localUser, nil
}
func (f *fakeStore) UpsertOIDCUser(context.Context, OIDCUser, time.Time, bool) (User, error) {
	return f.oidcUser, nil
}
func (f *fakeStore) CreateSession(_ context.Context, hash []byte, _ int64, _ time.Time, _ time.Time) error {
	f.createdSessionHash = append([]byte(nil), hash...)
	return nil
}
func (f *fakeStore) CreateLocalSession(
	_ context.Context,
	userID int64,
	expectedHash string,
	hash []byte,
	_ []byte,
	_ time.Time,
	now time.Time,
) (User, error) {
	if f.beforeLocalSession != nil {
		f.beforeLocalSession()
	}
	if f.localUser.ID != userID || f.localUser.PasswordHash != expectedHash ||
		!f.localUser.Enabled || f.localUser.AuthSource != "bootstrap" {
		return User{}, ErrInvalidCredentials
	}
	f.createdSessionHash = append([]byte(nil), hash...)
	login := now
	f.localUser.LastLoginAt = &login
	f.localUser.UpdatedAt = now
	return f.localUser, nil
}
func (f *fakeStore) FindSession(_ context.Context, hash []byte) (Session, error) {
	if len(f.findSessionHash) > 0 && !bytes.Equal(hash, f.findSessionHash) {
		return Session{}, ErrSessionNotFound
	}
	if f.session.User.ID == 0 {
		return Session{}, ErrSessionNotFound
	}
	return f.session, nil
}
func (f *fakeStore) TouchSession(context.Context, []byte, time.Time) error {
	f.touched++
	return f.touchErr
}
func (f *fakeStore) RevokeSession(context.Context, []byte, time.Time) error {
	f.revoked = true
	return f.revokeErr
}
func (f *fakeStore) ChangeLocalPassword(_ context.Context, userID int64, expectedHash, newHash string, _ time.Time) error {
	if f.localUser.ID != userID || f.localUser.PasswordHash != expectedHash {
		return ErrInvalidCredentials
	}
	f.localUser.PasswordHash = newHash
	f.changedPasswordHash = newHash
	f.revoked = true
	return nil
}
func (f *fakeStore) CreateOIDCFlow(_ context.Context, flow OIDCFlow) error {
	f.createdFlow = flow
	return nil
}
func (f *fakeStore) ConsumeOIDCFlow(_ context.Context, state, binding []byte, now time.Time) (OIDCFlow, error) {
	if !bytes.Equal(state, f.consumedFlow.StateHash) || !bytes.Equal(binding, f.consumedFlow.BindingHash) ||
		!now.Before(f.consumedFlow.ExpiresAt) {
		return OIDCFlow{}, ErrInvalidOIDCFlow
	}
	return f.consumedFlow, nil
}
func (f *fakeStore) AppendAudit(context.Context, AuditEvent) error { return nil }
func (f *fakeStore) LatestAuditID(context.Context) (int64, error)  { return 0, nil }
func (f *fakeStore) ListAudit(context.Context, int64, int64, int) ([]AuditEvent, error) {
	return nil, nil
}
func (f *fakeStore) ListUsers(context.Context, int, int) ([]User, error) { return nil, nil }
func (f *fakeStore) UpdateUser(context.Context, int64, UserPatch, time.Time, bool, string) (User, error) {
	return User{}, nil
}

type fakeOIDC struct {
	claims            OIDCClaims
	expectedNonceHash []byte
}

func (f *fakeOIDC) Issuer() string { return f.claims.Issuer }

func (f *fakeOIDC) AuthorizationURL(_ context.Context, state, nonce, verifier string) (string, error) {
	return "https://idp.example/authorize?state=" + state + "&nonce=" + nonce + "&challenge=" + verifier, nil
}
func (f *fakeOIDC) ExchangeAndVerify(_ context.Context, _, _ string, nonceHash []byte) (OIDCClaims, error) {
	if !bytes.Equal(nonceHash, f.expectedNonceHash) {
		return OIDCClaims{}, errors.New("nonce mismatch")
	}
	return f.claims, nil
}

func newTestService(t *testing.T, store Store, oidc OIDCClient) *Service {
	t.Helper()
	service, err := NewService(store, Config{
		RootKey:        "0123456789abcdef0123456789abcdef",
		BootstrapToken: "abcdef0123456789abcdef0123456789",
		PublicURL:      "http://localhost:8080", CookieSecure: false,
		LocalLoginEnabled: true, BootstrapEnabled: true, OIDCAutoProvision: true,
		SessionAbsoluteTimeout: time.Hour, SessionIdleTimeout: 30 * time.Minute,
		SessionTouchInterval: 5 * time.Minute, OIDCFlowTTL: 10 * time.Minute,
	}, oidc)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func TestRevalidateDoesNotRefreshIdleWindow(t *testing.T) {
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	store := &fakeStore{session: Session{
		User:      User{ID: 1, Username: "viewer", Role: RoleViewer, AuthSource: "oidc", Enabled: true},
		ExpiresAt: now.Add(time.Hour), LastSeenAt: now.Add(-10 * time.Minute),
	}}
	service := newTestService(t, store, nil)
	service.now = func() time.Time { return now }
	token := mustRandomToken(t)
	if _, err := service.Revalidate(context.Background(), token); err != nil {
		t.Fatal(err)
	}
	if store.touched != 0 {
		t.Fatalf("Revalidate touched the session %d times", store.touched)
	}
	if _, err := service.Authenticate(context.Background(), token); err != nil {
		t.Fatal(err)
	}
	if store.touched != 1 {
		t.Fatalf("Authenticate touched the session %d times", store.touched)
	}
}

func TestAuthenticatePreservesSessionStoreFailure(t *testing.T) {
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	databaseErr := errors.New("database unavailable")
	store := &fakeStore{touchErr: databaseErr, session: Session{
		User:      User{ID: 1, Username: "viewer", Role: RoleViewer, AuthSource: "oidc", Enabled: true},
		ExpiresAt: now.Add(time.Hour), LastSeenAt: now.Add(-10 * time.Minute),
	}}
	service := newTestService(t, store, nil)
	service.now = func() time.Time { return now }
	if _, err := service.Authenticate(context.Background(), mustRandomToken(t)); !errors.Is(err, databaseErr) {
		t.Fatalf("Authenticate error = %v, want infrastructure error", err)
	}
}

func TestBootstrapCreatesServerSessionOnce(t *testing.T) {
	store := &fakeStore{bootstrapAvailable: true, bootstrapUser: User{
		ID: 1, Username: "admin", DisplayName: "Administrator",
		Role: RoleAdmin, AuthSource: "bootstrap", Enabled: true,
	}}
	service := newTestService(t, store, nil)
	grant, err := service.Bootstrap(context.Background(),
		"abcdef0123456789abcdef0123456789", "admin", "Administrator",
		"correct horse battery staple", "", "bootstrap-request-id")
	if err != nil {
		t.Fatal(err)
	}
	if grant.Principal.Role != RoleAdmin || grant.Token == "" || grant.CSRFToken == "" {
		t.Fatalf("invalid bootstrap grant: %+v", grant)
	}
	if !bytes.Equal(store.createdSessionHash, service.keys.sessionHash(grant.Token)) {
		t.Fatal("store did not receive only the session token digest")
	}
	if store.bootstrapAudit.Action != "auth.bootstrap" || store.bootstrapAudit.Result != "succeeded" ||
		store.bootstrapAudit.HTTPStatus != 200 || store.bootstrapAudit.RequestID != "bootstrap-request-id" ||
		store.bootstrapAudit.ActorUserID == nil || *store.bootstrapAudit.ActorUserID != 1 {
		t.Fatalf("bootstrap audit was not committed with the administrator: %+v", store.bootstrapAudit)
	}
	if _, err := service.Bootstrap(context.Background(),
		"abcdef0123456789abcdef0123456789", "other", "Other Administrator",
		"correct horse battery staple", "", "second-request-id"); !errors.Is(err, ErrBootstrapUnavailable) {
		t.Fatalf("second bootstrap error = %v", err)
	}
}

func TestNewSessionFailsClosedWhenPreviousSessionCannotBeRevoked(t *testing.T) {
	databaseErr := errors.New("database unavailable")
	store := &fakeStore{revokeErr: databaseErr}
	service := newTestService(t, store, nil)
	_, err := service.newSession(context.Background(), User{
		ID: 1, Username: "administrator", DisplayName: "Administrator",
		Role: RoleAdmin, AuthSource: "bootstrap", Enabled: true,
	}, "previous-session-cookie", time.Now().UTC())
	if !errors.Is(err, databaseErr) {
		t.Fatalf("newSession error = %v, want revoke failure", err)
	}
	if len(store.createdSessionHash) != 0 {
		t.Fatal("new session was created after previous-session revoke failed")
	}
}

func TestLocalLoginRejectsPasswordChangedAfterVerification(t *testing.T) {
	oldHash, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	newHash, err := HashPassword("new correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	store := &fakeStore{localUser: User{
		ID: 7, Username: "administrator", PasswordHash: oldHash,
		Role: RoleAdmin, AuthSource: "bootstrap", Enabled: true,
	}}
	store.beforeLocalSession = func() { store.localUser.PasswordHash = newHash }
	service := newTestService(t, store, nil)

	if _, err := service.LocalLogin(
		context.Background(), "administrator", "correct horse battery staple", "",
	); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("stale local login error = %v, want ErrInvalidCredentials", err)
	}
	if len(store.createdSessionHash) != 0 {
		t.Fatal("stale local login created a session after password rotation")
	}
}

func TestChangePasswordRequiresCurrentPasswordAndRevokesSessions(t *testing.T) {
	oldHash, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	store := &fakeStore{localUser: User{
		ID: 7, Username: "administrator", PasswordHash: oldHash,
		Role: RoleAdmin, AuthSource: "bootstrap", Enabled: true,
	}}
	service := newTestService(t, store, nil)
	principal := Principal{UserID: 7, Username: "administrator", AuthSource: "bootstrap"}

	if err := service.ChangePassword(
		context.Background(), principal, "wrong current password", "new correct horse battery staple",
	); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("wrong current password error = %v", err)
	}
	if store.changedPasswordHash != "" || store.revoked {
		t.Fatal("wrong current password changed state")
	}
	if err := service.ChangePassword(
		context.Background(), principal, "correct horse battery staple", "correct horse battery staple",
	); err == nil {
		t.Fatal("password reuse was accepted")
	}
	if err := service.ChangePassword(
		context.Background(), principal, "correct horse battery staple", "new correct horse battery staple",
	); err != nil {
		t.Fatalf("ChangePassword() error = %v", err)
	}
	if !store.revoked || !VerifyPassword(store.changedPasswordHash, "new correct horse battery staple") {
		t.Fatal("new password was not persisted or sessions were not revoked")
	}
	if err := service.ChangePassword(context.Background(), Principal{
		UserID: 8, Username: "oidc-user", AuthSource: "oidc",
	}, "unused password", "another secure password"); !errors.Is(err, ErrPasswordChangeUnsupported) {
		t.Fatalf("OIDC password change error = %v", err)
	}
}

func TestEnsureAdministrativeAccessRequiresAdminOrAvailableBootstrap(t *testing.T) {
	tests := []struct {
		name               string
		hasAdmin           bool
		bootstrapAvailable bool
		bootstrapEnabled   bool
		localLoginEnabled  bool
		oidcIssuer         string
		wantError          bool
	}{
		{name: "enabled local administrator", hasAdmin: true, localLoginEnabled: true},
		{name: "enabled OIDC administrator", hasAdmin: true, oidcIssuer: "https://issuer.example.invalid"},
		{name: "fresh bootstrap", bootstrapAvailable: true, bootstrapEnabled: true, localLoginEnabled: true},
		{name: "OIDC-only fresh database", wantError: true},
		{name: "consumed bootstrap without administrator", bootstrapEnabled: true, wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := &fakeStore{hasEnabledAdmin: test.hasAdmin, bootstrapAvailable: test.bootstrapAvailable}
			var oidc OIDCClient
			if test.oidcIssuer != "" {
				oidc = &fakeOIDC{claims: OIDCClaims{Issuer: test.oidcIssuer}}
			}
			service := newTestService(t, store, oidc)
			service.config.BootstrapEnabled = test.bootstrapEnabled
			service.config.LocalLoginEnabled = test.localLoginEnabled
			err := service.EnsureAdministrativeAccess(context.Background())
			if (err != nil) != test.wantError {
				t.Fatalf("EnsureAdministrativeAccess() error = %v, wantError %v", err, test.wantError)
			}
			if store.adminLocalEnabled != test.localLoginEnabled || store.adminOIDCIssuer != test.oidcIssuer {
				t.Fatalf("administrator query methods = local:%v OIDC:%q, want local:%v OIDC:%q",
					store.adminLocalEnabled, store.adminOIDCIssuer, test.localLoginEnabled, test.oidcIssuer)
			}
		})
	}
}

func TestAuthenticateRejectsExpiredIdleAndRevokedSessions(t *testing.T) {
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	store := &fakeStore{}
	service := newTestService(t, store, nil)
	service.now = func() time.Time { return now }
	token, err := randomToken()
	if err != nil {
		t.Fatal(err)
	}
	base := Session{
		User:      User{ID: 1, Username: "viewer", Role: RoleViewer, AuthSource: "oidc", Enabled: true},
		ExpiresAt: now.Add(time.Hour), LastSeenAt: now.Add(-time.Minute),
	}
	store.session = base
	if _, err := service.Authenticate(context.Background(), token); err != nil {
		t.Fatalf("valid session rejected: %v", err)
	}
	store.session = base
	store.session.ExpiresAt = now
	if _, err := service.Authenticate(context.Background(), token); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("expired session error = %v", err)
	}
	store.session = base
	store.session.LastSeenAt = now.Add(-31 * time.Minute)
	if _, err := service.Authenticate(context.Background(), token); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("idle session error = %v", err)
	}
	store.session = base
	store.session.RevokedAt = &now
	if _, err := service.Authenticate(context.Background(), token); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("revoked session error = %v", err)
	}
}

func TestRootKeyRotationInvalidatesExistingSession(t *testing.T) {
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	token := mustRandomToken(t)
	store := &fakeStore{session: Session{
		User: User{
			ID: 1, Username: "administrator", Role: RoleAdmin,
			AuthSource: "bootstrap", Enabled: true,
		},
		ExpiresAt: now.Add(time.Hour), LastSeenAt: now.Add(-time.Minute),
	}}
	oldService := newTestService(t, store, nil)
	oldService.now = func() time.Time { return now }
	store.findSessionHash = oldService.keys.sessionHash(token)
	if _, err := oldService.Authenticate(context.Background(), token); err != nil {
		t.Fatalf("session created under original root key was rejected: %v", err)
	}

	rotatedConfig := oldService.config
	rotatedConfig.RootKey = "fedcba9876543210fedcba9876543210"
	rotatedService, err := NewService(store, rotatedConfig, nil)
	if err != nil {
		t.Fatal(err)
	}
	rotatedService.now = func() time.Time { return now }
	if bytes.Equal(store.findSessionHash, rotatedService.keys.sessionHash(token)) {
		t.Fatal("session digest did not change after root-key rotation")
	}
	if _, err := rotatedService.Authenticate(context.Background(), token); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("session survived root-key rotation: %v", err)
	}
}

func TestOIDCFlowIsBrowserBoundAndOneTime(t *testing.T) {
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	store := &fakeStore{oidcUser: User{
		ID: 2, Username: "oidc-user", DisplayName: "OIDC User",
		Role: RoleViewer, AuthSource: "oidc", Enabled: true,
	}}
	provider := &fakeOIDC{claims: OIDCClaims{Issuer: "https://idp.example", Subject: "subject", Name: "OIDC User"}}
	service := newTestService(t, store, provider)
	service.now = func() time.Time { return now }
	start, err := service.StartOIDC(context.Background(), "//evil.example")
	if err != nil {
		t.Fatal(err)
	}
	if store.createdFlow.ReturnPath != "/" {
		t.Fatalf("unsafe return path = %q", store.createdFlow.ReturnPath)
	}
	store.consumedFlow = store.createdFlow
	provider.expectedNonceHash = store.createdFlow.NonceHash
	state := authorizationURLParameter(t, start.AuthorizationURL, "state")
	grant, returnPath, err := service.CompleteOIDC(context.Background(), state, "code", start.BindingToken, "")
	if err != nil {
		t.Fatal(err)
	}
	if grant.Principal.UserID != 2 || returnPath != "/" {
		t.Fatalf("OIDC result = %+v, %q", grant, returnPath)
	}
	if _, _, err := service.CompleteOIDC(context.Background(), state, "code", mustRandomToken(t), ""); !errors.Is(err, ErrInvalidOIDCFlow) {
		t.Fatalf("different browser binding error = %v", err)
	}
}

func TestOIDCCallbackRejectsDisabledUserBeforeSessionCreation(t *testing.T) {
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	store := &fakeStore{oidcUser: User{
		ID: 2, Username: "disabled-user", DisplayName: "Disabled User",
		Role: RoleViewer, AuthSource: "oidc", Enabled: false,
	}}
	provider := &fakeOIDC{claims: OIDCClaims{
		Issuer: "https://idp.example", Subject: "disabled-subject", Name: "Disabled User",
	}}
	service := newTestService(t, store, provider)
	service.now = func() time.Time { return now }
	start, err := service.StartOIDC(context.Background(), "/")
	if err != nil {
		t.Fatal(err)
	}
	store.consumedFlow = store.createdFlow
	provider.expectedNonceHash = store.createdFlow.NonceHash
	state := authorizationURLParameter(t, start.AuthorizationURL, "state")
	if _, _, err := service.CompleteOIDC(context.Background(), state, "code", start.BindingToken, ""); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("disabled OIDC callback error = %v, want ErrUnauthenticated", err)
	}
	if len(store.createdSessionHash) != 0 {
		t.Fatal("disabled OIDC user received a persisted session")
	}
}

func authorizationURLParameter(t *testing.T, rawURL, name string) string {
	t.Helper()
	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	return parsed.Query().Get(name)
}

func mustRandomToken(t *testing.T) string {
	t.Helper()
	token, err := randomToken()
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func TestPublicURLAndReturnPathValidation(t *testing.T) {
	for _, raw := range []string{"http://example.com", "https://user@example.com", "https://example.com/path"} {
		if _, err := validatePublicURL(raw, true); err == nil {
			t.Errorf("unsafe public URL accepted: %s", raw)
		}
	}
	for _, raw := range []string{"https://evil.example", "//evil.example/path", "/\\evil", "/\r\nheader"} {
		if got := SafeReturnPath(raw); got != "/" {
			t.Errorf("SafeReturnPath(%q) = %q", raw, got)
		}
	}
	if got := SafeReturnPath("/application/list?tab=all"); got != "/application/list?tab=all" {
		t.Fatalf("safe return path = %q", got)
	}
}
