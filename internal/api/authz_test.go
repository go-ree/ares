package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-ree/ares/internal/api/controller"
	"github.com/go-ree/ares/internal/auth"
	"golang.org/x/time/rate"
)

const authBoundaryOrigin = "http://localhost:8080"

type authBoundaryStore struct {
	mu sync.Mutex

	bootstrapAvailable bool
	localUser          *auth.User
	users              map[int64]auth.User
	sessions           map[string]auth.Session
	audits             []auth.AuditEvent
	auditErr           error
}

func newAuthBoundaryStore() *authBoundaryStore {
	return &authBoundaryStore{
		users:    make(map[int64]auth.User),
		sessions: make(map[string]auth.Session),
	}
}

func (s *authBoundaryStore) BootstrapAvailable(context.Context) (bool, error) {
	return s.bootstrapAvailable, nil
}

func (s *authBoundaryStore) HasEnabledAdmin(_ context.Context, localLoginEnabled bool, oidcIssuer string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, user := range s.users {
		loginAvailable := (localLoginEnabled && user.AuthSource == "bootstrap" && user.PasswordHash != "") ||
			(oidcIssuer != "" && user.AuthSource == "oidc")
		if user.Enabled && user.Role == auth.RoleAdmin && loginAvailable {
			return true, nil
		}
	}
	return false, nil
}

func (s *authBoundaryStore) CreateBootstrapAdmin(context.Context, auth.BootstrapUser, auth.AuditEvent, time.Time) (auth.User, error) {
	return auth.User{}, auth.ErrBootstrapUnavailable
}

func (s *authBoundaryStore) FindLocalUser(_ context.Context, username string) (auth.User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.localUser == nil || s.localUser.Username != username {
		return auth.User{}, auth.ErrUserNotFound
	}
	return *s.localUser, nil
}

func (s *authBoundaryStore) UpsertOIDCUser(context.Context, auth.OIDCUser, time.Time, bool) (auth.User, error) {
	return auth.User{}, errors.New("not implemented in boundary store")
}

func (s *authBoundaryStore) CreateSession(_ context.Context, hash []byte, userID int64, expiresAt, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	user, ok := s.users[userID]
	if !ok {
		return auth.ErrUserNotFound
	}
	s.sessions[string(hash)] = auth.Session{
		Hash: append([]byte(nil), hash...), User: user, ExpiresAt: expiresAt,
		LastSeenAt: now, CreatedAt: now,
	}
	return nil
}

func (s *authBoundaryStore) CreateLocalSession(
	_ context.Context,
	userID int64,
	expectedHash string,
	hash []byte,
	previousHash []byte,
	expiresAt time.Time,
	now time.Time,
) (auth.User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	user, ok := s.users[userID]
	if !ok || !user.Enabled || user.AuthSource != "bootstrap" || user.PasswordHash != expectedHash {
		return auth.User{}, auth.ErrInvalidCredentials
	}
	if len(previousHash) > 0 {
		if previous, exists := s.sessions[string(previousHash)]; exists && previous.RevokedAt == nil {
			revokedAt := now
			previous.RevokedAt = &revokedAt
			previous.ExpiresAt = now
			s.sessions[string(previousHash)] = previous
		}
	}
	login := now
	user.LastLoginAt = &login
	user.UpdatedAt = now
	s.users[userID] = user
	if s.localUser != nil && s.localUser.ID == userID {
		copy := user
		s.localUser = &copy
	}
	s.sessions[string(hash)] = auth.Session{
		Hash: append([]byte(nil), hash...), User: user, ExpiresAt: expiresAt,
		LastSeenAt: now, CreatedAt: now,
	}
	return user, nil
}

func (s *authBoundaryStore) FindSession(_ context.Context, hash []byte) (auth.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[string(hash)]
	if !ok {
		return auth.Session{}, auth.ErrSessionNotFound
	}
	session.Hash = append([]byte(nil), session.Hash...)
	return session, nil
}

func (s *authBoundaryStore) TouchSession(_ context.Context, hash []byte, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[string(hash)]
	if !ok {
		return auth.ErrSessionNotFound
	}
	session.LastSeenAt = at
	s.sessions[string(hash)] = session
	return nil
}

func (s *authBoundaryStore) RevokeSession(_ context.Context, hash []byte, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[string(hash)]
	if !ok {
		return nil
	}
	session.RevokedAt = &at
	s.sessions[string(hash)] = session
	return nil
}

func (s *authBoundaryStore) ChangeLocalPassword(_ context.Context, userID int64, expectedHash, newHash string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	user, ok := s.users[userID]
	if !ok || user.AuthSource != "bootstrap" || !user.Enabled || user.PasswordHash != expectedHash {
		return auth.ErrInvalidCredentials
	}
	user.PasswordHash = newHash
	user.UpdatedAt = at
	s.users[userID] = user
	if s.localUser != nil && s.localUser.ID == userID {
		copy := user
		s.localUser = &copy
	}
	for hash, session := range s.sessions {
		if session.User.ID == userID && session.RevokedAt == nil {
			revokedAt := at
			session.RevokedAt = &revokedAt
			session.ExpiresAt = at
			s.sessions[hash] = session
		}
	}
	return nil
}

func (s *authBoundaryStore) CreateOIDCFlow(context.Context, auth.OIDCFlow) error {
	return errors.New("not implemented in boundary store")
}

func (s *authBoundaryStore) ConsumeOIDCFlow(context.Context, []byte, []byte, time.Time) (auth.OIDCFlow, error) {
	return auth.OIDCFlow{}, auth.ErrInvalidOIDCFlow
}

func (s *authBoundaryStore) AppendAudit(ctx context.Context, event auth.AuditEvent) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.auditErr != nil {
		return s.auditErr
	}
	event.ID = int64(len(s.audits) + 1)
	s.audits = append(s.audits, event)
	return nil
}

func (s *authBoundaryStore) LatestAuditID(context.Context) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return int64(len(s.audits)), nil
}

func (s *authBoundaryStore) ListAudit(_ context.Context, afterID, throughID int64, limit int) ([]auth.AuditEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]auth.AuditEvent, 0, limit)
	for _, event := range s.audits {
		if event.ID > afterID && event.ID <= throughID {
			result = append(result, event)
			if len(result) == limit {
				break
			}
		}
	}
	return result, nil
}

func (s *authBoundaryStore) ListUsers(context.Context, int, int) ([]auth.User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	users := make([]auth.User, 0, len(s.users))
	for _, user := range s.users {
		users = append(users, user)
	}
	return users, nil
}

func (s *authBoundaryStore) UpdateUser(
	_ context.Context,
	userID int64,
	patch auth.UserPatch,
	now time.Time,
	_ bool,
	_ string,
) (auth.User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	user, ok := s.users[userID]
	if !ok {
		return auth.User{}, auth.ErrUserNotFound
	}
	if patch.Role != nil {
		user.Role = *patch.Role
	}
	if patch.Enabled != nil {
		user.Enabled = *patch.Enabled
	}
	user.UpdatedAt = now
	s.users[userID] = user
	return user, nil
}

type boundarySession struct {
	token string
	csrf  string
}

func newAuthBoundary(t *testing.T) (*auth.Service, *authBoundaryStore, map[auth.Role]boundarySession) {
	t.Helper()
	store := newAuthBoundaryStore()
	service, err := auth.NewService(store, auth.Config{
		RootKey:                strings.Repeat("root-key-", 6),
		PublicURL:              authBoundaryOrigin,
		CookieSecure:           false,
		LocalLoginEnabled:      true,
		SessionAbsoluteTimeout: 8 * time.Hour,
		SessionIdleTimeout:     30 * time.Minute,
		SessionTouchInterval:   5 * time.Minute,
	}, nil)
	if err != nil {
		t.Fatalf("create auth service: %v", err)
	}

	now := time.Now().UTC()
	sessions := make(map[auth.Role]boundarySession)
	roles := []auth.Role{auth.RoleViewer, auth.RoleDeveloper, auth.RoleReleaser, auth.RoleAdmin}
	password := "correct horse battery staple"
	passwordHash, err := auth.HashPassword(password)
	if err != nil {
		t.Fatalf("hash boundary password: %v", err)
	}
	for index, role := range roles {
		userID := int64(index + 1)
		user := auth.User{
			ID: userID, Username: string(role), DisplayName: strings.ToUpper(string(role)),
			PasswordHash: passwordHash, Role: role, AuthSource: "bootstrap",
			Enabled: true, CreatedAt: now, UpdatedAt: now,
		}
		store.users[userID] = user
		copy := user
		store.localUser = &copy
		grant, loginErr := service.LocalLogin(context.Background(), user.Username, password, "")
		if loginErr != nil {
			t.Fatalf("create seeded %s session: %v", role, loginErr)
		}
		sessions[role] = boundarySession{token: grant.Token, csrf: grant.CSRFToken}
	}
	return service, store, sessions
}

func authenticatedRequest(t *testing.T, service *auth.Service, session boundarySession, method, target, body string) *http.Request {
	t.Helper()
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	request.AddCookie(&http.Cookie{Name: service.SessionCookieName(), Value: session.token})
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	if isUnsafeMethod(method) {
		request.Header.Set("Origin", authBoundaryOrigin)
		request.Header.Set("X-CSRF-Token", session.csrf)
	}
	return request
}

func TestActualRouteRoleMatrix(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service, _, sessions := newAuthBoundary(t)
	router := gin.New()
	RouterWithRuntime(router, Runtime{Auth: service})

	tests := []struct {
		name       string
		method     string
		path       string
		body       string
		wantStatus map[auth.Role]int
	}{
		{
			name: "application read", method: http.MethodGet,
			path: "/api/v1/compatible/metadata/relation/all",
			wantStatus: map[auth.Role]int{
				auth.RoleViewer: http.StatusOK, auth.RoleDeveloper: http.StatusOK,
				auth.RoleReleaser: http.StatusOK, auth.RoleAdmin: http.StatusOK,
			},
		},
		{
			name: "application write", method: http.MethodPatch,
			path: "/api/v1/apps/not-a-number", body: `{}`,
			wantStatus: map[auth.Role]int{
				auth.RoleViewer: http.StatusForbidden, auth.RoleDeveloper: http.StatusBadRequest,
				auth.RoleReleaser: http.StatusForbidden, auth.RoleAdmin: http.StatusBadRequest,
			},
		},
		{
			name: "release", method: http.MethodPost,
			path: "/api/v1/deploy/publish", body: `{}`,
			wantStatus: map[auth.Role]int{
				auth.RoleViewer: http.StatusForbidden, auth.RoleDeveloper: http.StatusForbidden,
				auth.RoleReleaser: http.StatusUnprocessableEntity, auth.RoleAdmin: http.StatusUnprocessableEntity,
			},
		},
		{
			name: "system settings", method: http.MethodGet,
			path: "/api/v1/system/integrations",
			wantStatus: map[auth.Role]int{
				auth.RoleViewer: http.StatusForbidden, auth.RoleDeveloper: http.StatusForbidden,
				auth.RoleReleaser: http.StatusForbidden, auth.RoleAdmin: http.StatusOK,
			},
		},
	}

	for _, test := range tests {
		for role, wantStatus := range test.wantStatus {
			t.Run(test.name+"/"+string(role), func(t *testing.T) {
				recorder := httptest.NewRecorder()
				request := authenticatedRequest(t, service, sessions[role], test.method, test.path, test.body)
				router.ServeHTTP(recorder, request)
				if recorder.Code != wantStatus {
					t.Fatalf("%s %s as %s returned %d, want %d: %s",
						test.method, test.path, role, recorder.Code, wantStatus, recorder.Body.String())
				}
			})
		}
	}
}

func TestProtectedRouteRequiresSessionAndCSRFOrigin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service, _, sessions := newAuthBoundary(t)
	runtime := Runtime{Auth: service}
	router := gin.New()
	router.POST("/api/v1/apps", runtime.require(routePolicy{
		Permission: auth.PermissionApplicationsWrite,
		Action:     "application.create", ResourceType: "application",
	}), func(c *gin.Context) { c.Status(http.StatusNoContent) })

	developer := sessions[auth.RoleDeveloper]
	tests := []struct {
		name       string
		cookie     bool
		origin     string
		csrf       string
		wantStatus int
	}{
		{name: "missing session", origin: authBoundaryOrigin, csrf: developer.csrf, wantStatus: http.StatusUnauthorized},
		{name: "missing origin", cookie: true, csrf: developer.csrf, wantStatus: http.StatusForbidden},
		{name: "cross site origin", cookie: true, origin: "https://attacker.invalid", csrf: developer.csrf, wantStatus: http.StatusForbidden},
		{name: "missing csrf", cookie: true, origin: authBoundaryOrigin, wantStatus: http.StatusForbidden},
		{name: "wrong csrf", cookie: true, origin: authBoundaryOrigin, csrf: strings.Repeat("x", 43), wantStatus: http.StatusForbidden},
		{name: "valid session protection", cookie: true, origin: authBoundaryOrigin, csrf: developer.csrf, wantStatus: http.StatusNoContent},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/api/v1/apps", strings.NewReader(`{}`))
			request.Header.Set("Content-Type", "application/json")
			if test.cookie {
				request.AddCookie(&http.Cookie{Name: service.SessionCookieName(), Value: developer.token})
			}
			if test.origin != "" {
				request.Header.Set("Origin", test.origin)
			}
			if test.csrf != "" {
				request.Header.Set("X-CSRF-Token", test.csrf)
			}
			router.ServeHTTP(recorder, request)
			if recorder.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d: %s", recorder.Code, test.wantStatus, recorder.Body.String())
			}
			if got := recorder.Header().Get("Cache-Control"); got != "private, no-store" {
				t.Fatalf("Cache-Control = %q, want private, no-store", got)
			}
		})
	}
}

func TestAuditPaginationUsesFixedSnapshotBoundary(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service, store, sessions := newAuthBoundary(t)
	for index := 0; index < 3; index++ {
		if err := service.AppendAudit(context.Background(), auth.AuditEvent{
			ActorUsername: "seed", ActorDisplayName: "Seed", AuthSource: "test",
			Action: "seed", ResourceType: "test", Result: "succeeded",
		}); err != nil {
			t.Fatal(err)
		}
	}
	router := gin.New()
	RouterWithRuntime(router, Runtime{Auth: service})

	first := httptest.NewRecorder()
	router.ServeHTTP(first, authenticatedRequest(t, service, sessions[auth.RoleAdmin],
		http.MethodGet, "/api/v1/system/audit-events?limit=2", ""))
	if first.Code != http.StatusOK {
		t.Fatalf("first audit page returned %d: %s", first.Code, first.Body.String())
	}
	firstResult := decodeSuccessfulResult(t, first.Body.Bytes())
	if firstResult["through_id"] != "4" || firstResult["next_after_id"] != "2" || firstResult["has_more"] != true {
		t.Fatalf("unexpected first snapshot page: %#v", firstResult)
	}

	// Events appended after the first page—including audits generated by the
	// read itself—must not move the frozen upper boundary.
	if err := service.AppendAudit(context.Background(), auth.AuditEvent{
		ActorUsername: "later", ActorDisplayName: "Later", AuthSource: "test",
		Action: "later", ResourceType: "test", Result: "succeeded",
	}); err != nil {
		t.Fatal(err)
	}
	second := httptest.NewRecorder()
	router.ServeHTTP(second, authenticatedRequest(t, service, sessions[auth.RoleAdmin],
		http.MethodGet, "/api/v1/system/audit-events?after_id=2&through_id=4&limit=2", ""))
	if second.Code != http.StatusOK {
		t.Fatalf("second audit page returned %d: %s", second.Code, second.Body.String())
	}
	secondResult := decodeSuccessfulResult(t, second.Body.Bytes())
	if secondResult["through_id"] != "4" || secondResult["next_after_id"] != "4" || secondResult["has_more"] != false {
		t.Fatalf("unexpected second snapshot page: %#v", secondResult)
	}
	items, ok := secondResult["items"].([]any)
	if !ok || len(items) != 2 {
		t.Fatalf("unexpected second page items: %#v", secondResult["items"])
	}
	for _, raw := range items {
		item := raw.(map[string]any)
		if item["id"] != "3" && item["id"] != "4" {
			t.Fatalf("post-snapshot event leaked into page: %#v", item)
		}
	}

	future := httptest.NewRecorder()
	router.ServeHTTP(future, authenticatedRequest(t, service, sessions[auth.RoleAdmin],
		http.MethodGet, "/api/v1/system/audit-events?after_id=4&through_id=9223372036854775807&limit=2", ""))
	if future.Code != http.StatusBadRequest || !strings.Contains(future.Body.String(), "through_id exceeds current audit boundary") {
		t.Fatalf("future audit boundary returned %d: %s", future.Code, future.Body.String())
	}

	store.mu.Lock()
	totalEvents := len(store.audits)
	store.mu.Unlock()
	if totalEvents <= 4 {
		t.Fatalf("test did not create post-snapshot audit events: %d", totalEvents)
	}
}

func TestAuthorizationAuditBoundary(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service, store, sessions := newAuthBoundary(t)
	runtime := Runtime{Auth: service}
	router := gin.New()
	handlerCalls := 0
	router.POST("/resource/:id", runtime.require(routePolicy{
		Permission: auth.PermissionApplicationsWrite, Action: "application.update",
		ResourceType: "application", ResourceParam: "id",
	}), func(c *gin.Context) {
		handlerCalls++
		c.Status(http.StatusNoContent)
	})

	request := authenticatedRequest(t, service, sessions[auth.RoleDeveloper],
		http.MethodPost, "/resource/42", `{"secret":"must-not-be-audited"}`)
	request.Header.Set("X-Request-ID", "audit-request-1")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNoContent || handlerCalls != 1 {
		t.Fatalf("authorized write status=%d calls=%d", recorder.Code, handlerCalls)
	}
	store.mu.Lock()
	events := append([]auth.AuditEvent(nil), store.audits...)
	store.mu.Unlock()
	if len(events) != 2 || events[0].Result != "authorized" || events[1].Result != "succeeded" {
		t.Fatalf("write audit events = %#v", events)
	}
	for _, event := range events {
		if event.ActorUserID == nil || *event.ActorUserID != 2 || event.ResourceID != "42" ||
			event.Action != "application.update" || strings.Contains(event.ResourceID, "must-not-be-audited") {
			t.Fatalf("unsafe audit event = %#v", event)
		}
	}

	store.mu.Lock()
	store.auditErr = errors.New("audit unavailable")
	store.mu.Unlock()
	request = authenticatedRequest(t, service, sessions[auth.RoleDeveloper],
		http.MethodPost, "/resource/43", `{}`)
	recorder = httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusServiceUnavailable || handlerCalls != 1 {
		t.Fatalf("audit fail-closed status=%d calls=%d body=%s", recorder.Code, handlerCalls, recorder.Body.String())
	}
}

func TestAuthorizedPanicRecordsFinalFailedAuditAndPropagates(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service, store, sessions := newAuthBoundary(t)
	router := gin.New()
	router.POST("/resource/:id", Runtime{Auth: service}.require(routePolicy{
		Permission: auth.PermissionApplicationsWrite, Action: "application.update",
		ResourceType: "application", ResourceParam: "id",
	}), func(*gin.Context) { panic("sensitive panic payload") })

	request := authenticatedRequest(t, service, sessions[auth.RoleDeveloper],
		http.MethodPost, "/resource/42", `{}`)
	var recovered any
	func() {
		defer func() { recovered = recover() }()
		router.ServeHTTP(httptest.NewRecorder(), request)
	}()
	if recovered == nil {
		t.Fatal("authorized handler panic was not propagated to the outer recovery boundary")
	}
	store.mu.Lock()
	events := append([]auth.AuditEvent(nil), store.audits...)
	store.mu.Unlock()
	if len(events) != 2 || events[0].Result != "authorized" || events[1].Result != "failed" ||
		events[1].HTTPStatus != http.StatusInternalServerError {
		t.Fatalf("panic audit events = %#v", events)
	}
}

func TestFinalAuditSurvivesRequestCancellation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service, store, sessions := newAuthBoundary(t)
	router := gin.New()
	requestContext, cancelRequest := context.WithCancel(context.Background())
	router.POST("/resource/:id", Runtime{Auth: service}.require(routePolicy{
		Permission: auth.PermissionApplicationsWrite, Action: "application.update",
		ResourceType: "application", ResourceParam: "id",
	}), func(c *gin.Context) {
		cancelRequest()
		c.Status(http.StatusNoContent)
	})

	request := authenticatedRequest(t, service, sessions[auth.RoleDeveloper],
		http.MethodPost, "/resource/42", `{}`).WithContext(requestContext)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	store.mu.Lock()
	events := append([]auth.AuditEvent(nil), store.audits...)
	store.mu.Unlock()
	if len(events) != 2 || events[0].Result != "authorized" || events[1].Result != "succeeded" ||
		events[1].HTTPStatus != http.StatusNoContent {
		t.Fatalf("canceled-request audit events = %#v", events)
	}
}

func TestServerMarkedStreamingFailureOverridesCommittedHTTP200Audit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service, store, sessions := newAuthBoundary(t)
	router := gin.New()
	router.GET("/stream", Runtime{Auth: service}.require(routePolicy{
		Permission: auth.PermissionLogsRead, Action: "release.log.read",
		ResourceType: "release-log", SensitiveRead: true,
	}), func(c *gin.Context) {
		c.Status(http.StatusOK)
		controller.SetRequestAuditResourceID(c, "42")
		controller.MarkRequestAuditFailure(c)
	})

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, authenticatedRequest(t, service, sessions[auth.RoleViewer],
		http.MethodGet, "/stream", ""))
	store.mu.Lock()
	events := append([]auth.AuditEvent(nil), store.audits...)
	store.mu.Unlock()
	if recorder.Code != http.StatusOK || len(events) != 2 || events[0].ResourceID != "" ||
		events[1].Result != "failed" || events[1].HTTPStatus != http.StatusOK || events[1].ResourceID != "42" {
		t.Fatalf("stream audit = status:%d events:%#v", recorder.Code, events)
	}
}

func TestDeniedRequestIsAuditedWithoutCredentials(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service, store, _ := newAuthBoundary(t)
	router := gin.New()
	router.GET("/resource", Runtime{Auth: service}.require(routePolicy{
		Permission: auth.PermissionApplicationsRead, Action: "application.list", ResourceType: "application",
	}), func(c *gin.Context) { c.Status(http.StatusNoContent) })
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/resource", nil))
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", recorder.Code)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.audits) != 1 || store.audits[0].Result != "denied" ||
		store.audits[0].ActorUsername != "anonymous" || store.audits[0].HTTPStatus != http.StatusUnauthorized {
		t.Fatalf("denial audit = %#v", store.audits)
	}
}

func TestAnonymousDenialAuditWritesAreBounded(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service, store, _ := newAuthBoundary(t)
	runtime := Runtime{
		Auth: service, anonymousAuditLimiter: rate.NewLimiter(0, 2),
	}
	router := gin.New()
	router.GET("/resource", runtime.require(routePolicy{
		Permission: auth.PermissionApplicationsRead,
		Action:     "application.list", ResourceType: "application",
	}), func(c *gin.Context) { c.Status(http.StatusNoContent) })
	for requestNumber := 0; requestNumber < 10; requestNumber++ {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/resource", nil))
		if recorder.Code != http.StatusUnauthorized {
			t.Fatalf("request %d status = %d, want 401", requestNumber, recorder.Code)
		}
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.audits) != 2 {
		t.Fatalf("anonymous denial audit events = %d, want bounded burst of 2", len(store.audits))
	}
}

func TestAuthenticatedDenialAdmissionBoundsPermanentAuditGrowth(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service, store, sessions := newAuthBoundary(t)
	runtime := Runtime{
		Auth: service,
		deniedAdmission: &denialAdmission{
			global: rate.NewLimiter(rate.Inf, 100),
			perKey: newBoundedKeyRateLimiter(0, 2),
		},
		rateLimitedAuditLimiter: rate.NewLimiter(0, 1),
	}
	router := gin.New()
	router.GET("/admin", runtime.require(routePolicy{
		Permission: auth.PermissionAuditRead, Action: "audit.list", ResourceType: "audit",
	}), func(c *gin.Context) { c.Status(http.StatusNoContent) })

	for requestNumber := 0; requestNumber < 10; requestNumber++ {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, authenticatedRequest(t, service, sessions[auth.RoleViewer],
			http.MethodGet, "/admin", ""))
		wantStatus := http.StatusTooManyRequests
		if requestNumber < 2 {
			wantStatus = http.StatusForbidden
		}
		if recorder.Code != wantStatus {
			t.Fatalf("request %d status = %d, want %d", requestNumber, recorder.Code, wantStatus)
		}
		if wantStatus == http.StatusTooManyRequests && recorder.Header().Get("Retry-After") != "60" {
			t.Fatalf("request %d Retry-After = %q, want 60", requestNumber, recorder.Header().Get("Retry-After"))
		}
	}

	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.audits) != 3 {
		t.Fatalf("authenticated denial audit events = %d, want two denials plus one sampled 429", len(store.audits))
	}
	if store.audits[2].HTTPStatus != http.StatusTooManyRequests || store.audits[2].ActorUserID == nil {
		t.Fatalf("sampled authenticated rate-limit audit = %#v", store.audits[2])
	}
}

func TestAuthenticatedRequestAdmissionRejectsBeforeHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service, store, sessions := newAuthBoundary(t)
	runtime := Runtime{
		Auth: service,
		authenticatedAdmission: newRequestAdmission(
			rate.Inf, 10, 0, 1, 10, 10,
		),
		rateLimitedAuditLimiter: rate.NewLimiter(0, 1),
	}
	handled := 0
	router := gin.New()
	router.GET("/resource", runtime.require(routePolicy{
		Permission: auth.PermissionApplicationsRead, Action: "application.read", ResourceType: "application",
	}), func(c *gin.Context) {
		handled++
		c.Status(http.StatusNoContent)
	})

	first := httptest.NewRecorder()
	router.ServeHTTP(first, authenticatedRequest(t, service, sessions[auth.RoleViewer],
		http.MethodGet, "/resource", ""))
	second := httptest.NewRecorder()
	router.ServeHTTP(second, authenticatedRequest(t, service, sessions[auth.RoleViewer],
		http.MethodGet, "/resource", ""))
	if first.Code != http.StatusNoContent || second.Code != http.StatusTooManyRequests || handled != 1 {
		t.Fatalf("admission results = first:%d second:%d handled:%d", first.Code, second.Code, handled)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.audits) != 1 || store.audits[0].HTTPStatus != http.StatusTooManyRequests {
		t.Fatalf("rate-limit audit = %#v", store.audits)
	}
}

func TestCredentialAdmissionRejectsBeforePasswordHandlerAndAuthorizedAudit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service, store, sessions := newAuthBoundary(t)
	runtime := Runtime{
		Auth: service,
		credentialAdmission: newRequestAdmission(
			rate.Inf, 10, 0, 1, 10, 10,
		),
		rateLimitedAuditLimiter: rate.NewLimiter(0, 1),
	}
	handled := 0
	router := gin.New()
	router.POST("/password", runtime.require(routePolicy{
		Action: "auth.password.change", ResourceType: "authentication", CredentialCheck: true,
	}), func(c *gin.Context) {
		handled++
		c.Status(http.StatusNoContent)
	})

	first := httptest.NewRecorder()
	router.ServeHTTP(first, authenticatedRequest(t, service, sessions[auth.RoleAdmin],
		http.MethodPost, "/password", `{}`))
	second := httptest.NewRecorder()
	router.ServeHTTP(second, authenticatedRequest(t, service, sessions[auth.RoleAdmin],
		http.MethodPost, "/password", `{}`))
	if first.Code != http.StatusNoContent || second.Code != http.StatusTooManyRequests || handled != 1 {
		t.Fatalf("credential admission = first:%d second:%d handled:%d", first.Code, second.Code, handled)
	}
	if second.Header().Get("Retry-After") != "5" {
		t.Fatalf("credential admission Retry-After = %q, want 5", second.Header().Get("Retry-After"))
	}

	store.mu.Lock()
	defer store.mu.Unlock()
	authorized := 0
	for _, event := range store.audits {
		if event.Action == "auth.password.change" && event.Result == "authorized" {
			authorized++
		}
	}
	if authorized != 1 {
		t.Fatalf("credential admission authorized audits = %d, want only the admitted request", authorized)
	}
}

func TestAuthenticationAdmissionCannotBeBypassedByRotatingInvalidCookies(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service, _, _ := newAuthBoundary(t)
	runtime := Runtime{
		Auth: service,
		authenticationAdmission: newRequestAdmission(
			rate.Inf, 100, 0, 2, 10, 10,
		),
		rateLimitedAuditLimiter: rate.NewLimiter(0, 1),
	}
	router := gin.New()
	router.GET("/resource", runtime.require(routePolicy{
		Permission: auth.PermissionApplicationsRead, Action: "application.read", ResourceType: "application",
	}), func(c *gin.Context) { c.Status(http.StatusNoContent) })

	for requestNumber := 0; requestNumber < 3; requestNumber++ {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/resource", nil)
		request.RemoteAddr = "192.0.2.10:12345"
		request.AddCookie(&http.Cookie{
			Name: service.SessionCookieName(), Value: "rotating-invalid-session-" + strconv.Itoa(requestNumber),
		})
		router.ServeHTTP(recorder, request)
		wantStatus := http.StatusUnauthorized
		if requestNumber == 2 {
			wantStatus = http.StatusTooManyRequests
		}
		if recorder.Code != wantStatus {
			t.Fatalf("rotating-cookie request %d status = %d, want %d", requestNumber, recorder.Code, wantStatus)
		}
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/resource", nil)
	request.RemoteAddr = "192.0.2.11:12345"
	request.AddCookie(&http.Cookie{Name: service.SessionCookieName(), Value: "independent-invalid-session"})
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("independent client status = %d, want 401", recorder.Code)
	}
}

func TestLegacyAdminTokenIsScopedAndDeprecated(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service, _, _ := newAuthBoundary(t)
	legacyToken := strings.Repeat("legacy-token-", 3)
	runtime := Runtime{
		Auth: service, LegacyAdminTokenEnabled: true, LegacyAdminToken: legacyToken,
		LegacyAdminTokenSunset: "2027-01-02T03:04:05Z",
	}
	router := gin.New()
	RouterWithRuntime(router, runtime)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/system/integrations", nil)
	request.Header.Set(legacyAdminTokenHeader, legacyToken)
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("legacy token on compatibility route returned %d: %s", recorder.Code, recorder.Body.String())
	}
	if got := recorder.Header().Get("Deprecation"); got != "true" {
		t.Fatalf("Deprecation header = %q, want true", got)
	}
	if got := recorder.Header().Get("Sunset"); got != "Sat, 02 Jan 2027 03:04:05 GMT" {
		t.Fatalf("Sunset header = %q", got)
	}
	if got := recorder.Header().Get("Warning"); !strings.Contains(got, "已弃用") {
		t.Fatalf("Warning header did not explain deprecation: %q", got)
	}

	for _, target := range []string{
		"/api/v1/compatible/metadata/relation/all",
		"/api/v1/auth/session",
		"/api/v1/system/users",
	} {
		recorder = httptest.NewRecorder()
		request = httptest.NewRequest(http.MethodGet, target, nil)
		request.Header.Set(legacyAdminTokenHeader, legacyToken)
		router.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusUnauthorized {
			t.Fatalf("legacy token unexpectedly authorized %s: status %d, body %s", target, recorder.Code, recorder.Body.String())
		}
		if recorder.Header().Get("Deprecation") != "" {
			t.Fatalf("non-legacy route %s received deprecation headers", target)
		}
	}

	disabledRouter := gin.New()
	RouterWithRuntime(disabledRouter, Runtime{Auth: service, LegacyAdminToken: legacyToken})
	recorder = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodGet, "/api/v1/system/integrations", nil)
	request.Header.Set(legacyAdminTokenHeader, legacyToken)
	disabledRouter.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("disabled legacy token returned %d, want 401", recorder.Code)
	}
}

func TestAllNonPublicRoutesAreFailClosed(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service, _, _ := newAuthBoundary(t)
	router := gin.New()
	RouterWithRuntime(router, Runtime{Auth: service})

	public := map[string]struct{}{
		http.MethodGet + " /api/v1/auth/options":       {},
		http.MethodPost + " /api/v1/auth/bootstrap":    {},
		http.MethodPost + " /api/v1/auth/login":        {},
		http.MethodGet + " /api/v1/auth/oidc/start":    {},
		http.MethodGet + " /api/v1/auth/oidc/callback": {},
	}
	parameter := regexp.MustCompile(`:[^/]+`)
	for _, route := range router.Routes() {
		if _, ok := public[route.Method+" "+route.Path]; ok {
			continue
		}
		t.Run(route.Method+" "+route.Path, func(t *testing.T) {
			path := parameter.ReplaceAllString(route.Path, "1")
			path = strings.ReplaceAll(path, "*any", "index.html")
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(route.Method, path, strings.NewReader(`{}`))
			request.Header.Set("Content-Type", "application/json")
			router.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusUnauthorized {
				t.Fatalf("uncredentialed route returned %d, want 401: %s", recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestLoginAndSessionResponsesKeepOpaqueSessionInCookie(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service, store, _ := newAuthBoundary(t)
	password := "correct horse battery staple"
	passwordHash, err := auth.HashPassword(password)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	now := time.Now().UTC()
	localUser := auth.User{
		ID: 99, Username: "local-admin", DisplayName: "Local Administrator",
		PasswordHash: passwordHash, Role: auth.RoleAdmin, AuthSource: "bootstrap",
		Enabled: true, CreatedAt: now, UpdatedAt: now,
	}
	store.mu.Lock()
	store.users[localUser.ID] = localUser
	store.localUser = &localUser
	store.mu.Unlock()

	router := gin.New()
	RouterWithRuntime(router, Runtime{Auth: service})
	loginRecorder := httptest.NewRecorder()
	loginRequest := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login",
		strings.NewReader(`{"username":"local-admin","password":"correct horse battery staple"}`))
	loginRequest.Header.Set("Content-Type", "application/json")
	loginRequest.Header.Set("Origin", authBoundaryOrigin)
	router.ServeHTTP(loginRecorder, loginRequest)
	if loginRecorder.Code != http.StatusOK {
		t.Fatalf("login returned %d: %s", loginRecorder.Code, loginRecorder.Body.String())
	}
	if got := loginRecorder.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("login Cache-Control = %q, want no-store", got)
	}

	var sessionCookie *http.Cookie
	for _, cookie := range loginRecorder.Result().Cookies() {
		if cookie.Name == service.SessionCookieName() {
			sessionCookie = cookie
			break
		}
	}
	if sessionCookie == nil || sessionCookie.Value == "" {
		t.Fatalf("login did not issue %s cookie", service.SessionCookieName())
	}
	if !sessionCookie.HttpOnly || sessionCookie.Path != "/" || sessionCookie.SameSite != http.SameSiteLaxMode || sessionCookie.MaxAge <= 0 {
		t.Fatalf("unsafe session cookie attributes: %#v", sessionCookie)
	}
	if strings.Contains(loginRecorder.Body.String(), sessionCookie.Value) {
		t.Fatal("login response body leaked opaque session token")
	}

	loginResult := decodeSuccessfulResult(t, loginRecorder.Body.Bytes())
	if _, exists := loginResult["token"]; exists {
		t.Fatal("login response exposed a token field")
	}
	if _, exists := loginResult["session_token"]; exists {
		t.Fatal("login response exposed a session_token field")
	}
	csrf, ok := loginResult["csrf_token"].(string)
	if !ok || !service.ValidCSRF(sessionCookie.Value, csrf) {
		t.Fatalf("login returned invalid CSRF token: %#v", loginResult["csrf_token"])
	}

	sessionRecorder := httptest.NewRecorder()
	sessionRequest := httptest.NewRequest(http.MethodGet, "/api/v1/auth/session", nil)
	sessionRequest.AddCookie(sessionCookie)
	router.ServeHTTP(sessionRecorder, sessionRequest)
	if sessionRecorder.Code != http.StatusOK {
		t.Fatalf("session endpoint returned %d: %s", sessionRecorder.Code, sessionRecorder.Body.String())
	}
	if got := sessionRecorder.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("session Cache-Control = %q, want no-store", got)
	}
	if strings.Contains(sessionRecorder.Body.String(), sessionCookie.Value) {
		t.Fatal("session response body leaked opaque session token")
	}
	sessionResult := decodeSuccessfulResult(t, sessionRecorder.Body.Bytes())
	user, ok := sessionResult["user"].(map[string]any)
	if !ok {
		t.Fatalf("session user has unexpected shape: %#v", sessionResult["user"])
	}
	if got := user["id"]; got != "99" {
		t.Fatalf("session user id = %#v, want stable string ID 99", got)
	}
	if got := user["username"]; got != "local-admin" {
		t.Fatalf("session username = %#v", got)
	}
}

func TestPasswordChangeRevokesCurrentSessionAndRequiresNewPassword(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service, _, sessions := newAuthBoundary(t)
	router := gin.New()
	RouterWithRuntime(router, Runtime{Auth: service})

	current := sessions[auth.RoleAdmin]
	change := httptest.NewRecorder()
	router.ServeHTTP(change, authenticatedRequest(t, service, current,
		http.MethodPost, "/api/v1/auth/password",
		`{"current_password":"correct horse battery staple","new_password":"new correct horse battery staple"}`))
	if change.Code != http.StatusOK {
		t.Fatalf("password change returned %d: %s", change.Code, change.Body.String())
	}
	cleared := false
	for _, cookie := range change.Result().Cookies() {
		if cookie.Name == service.SessionCookieName() && cookie.MaxAge < 0 {
			cleared = true
		}
	}
	if !cleared {
		t.Fatal("password change did not clear the browser session cookie")
	}

	oldSession := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/auth/session", nil)
	request.AddCookie(&http.Cookie{Name: service.SessionCookieName(), Value: current.token})
	router.ServeHTTP(oldSession, request)
	if oldSession.Code != http.StatusUnauthorized {
		t.Fatalf("old session status = %d, want 401", oldSession.Code)
	}

	for _, attempt := range []struct {
		password string
		status   int
	}{
		{password: "correct horse battery staple", status: http.StatusUnauthorized},
		{password: "new correct horse battery staple", status: http.StatusOK},
	} {
		recorder := httptest.NewRecorder()
		login := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(
			`{"username":"admin","password":"`+attempt.password+`"}`))
		login.Header.Set("Origin", authBoundaryOrigin)
		login.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(recorder, login)
		if recorder.Code != attempt.status {
			t.Fatalf("login with %q returned %d, want %d: %s", attempt.password, recorder.Code, attempt.status, recorder.Body.String())
		}
	}
}

func decodeSuccessfulResult(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var envelope struct {
		Code   int            `json:"code"`
		Result map[string]any `json:"result"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatalf("decode response: %v; body=%s", err, body)
	}
	if envelope.Code != 1 || envelope.Result == nil {
		t.Fatalf("unexpected response envelope: %#v", envelope)
	}
	return envelope.Result
}
