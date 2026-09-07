package controller

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-ree/ares/internal/auth"
	"golang.org/x/time/rate"
)

type rateLimitAuditStore struct {
	auth.Store
	events []auth.AuditEvent
}

type optionsCountingStore struct {
	auth.Store
	calls   atomic.Int64
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (s *optionsCountingStore) BootstrapAvailable(ctx context.Context) (bool, error) {
	s.calls.Add(1)
	if s.started != nil {
		s.once.Do(func() { close(s.started) })
	}
	if s.release != nil {
		select {
		case <-s.release:
		case <-ctx.Done():
			return false, ctx.Err()
		}
	}
	return true, nil
}

type blockingReadCloser struct {
	started chan struct{}
	release chan struct{}
	read    atomic.Bool
	once    sync.Once
}

func (r *blockingReadCloser) Read([]byte) (int, error) {
	r.read.Store(true)
	if r.started != nil {
		r.once.Do(func() { close(r.started) })
	}
	if r.release != nil {
		<-r.release
	}
	return 0, io.EOF
}

func (*blockingReadCloser) Close() error { return nil }

func (s *rateLimitAuditStore) AppendAudit(_ context.Context, event auth.AuditEvent) error {
	s.events = append(s.events, event)
	return errors.New("audit unavailable")
}

func TestPublicAuthenticationRateLimitsFailClosedWhenAuditIsUnavailable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := &rateLimitAuditStore{}
	service, err := auth.NewService(store, auth.Config{
		RootKey:   "0123456789abcdef0123456789abcdef",
		PublicURL: "http://localhost:8080",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	controller := NewAuthController(service)
	controller.bootstrapGuard = newPublicAuthGuard(0, 0, 1, 1)
	controller.loginGuard = newPublicAuthGuard(0, 0, 1, 1)
	controller.oidcStartGuard = newPublicAuthGuard(0, 0, 1, 1)
	controller.oidcCallbackGuard = newPublicAuthGuard(0, 0, 1, 1)

	router := gin.New()
	router.POST("/api/v1/auth/bootstrap", controller.Bootstrap)
	router.POST("/api/v1/auth/login", controller.Login)
	router.GET("/api/v1/auth/oidc/start", controller.OIDCStart)
	router.GET("/api/v1/auth/oidc/callback", controller.OIDCCallback)

	tests := []struct {
		name   string
		method string
		target string
		action string
		body   string
	}{
		{name: "bootstrap", method: http.MethodPost, target: "/api/v1/auth/bootstrap", action: "auth.bootstrap", body: `{}`},
		{name: "local login", method: http.MethodPost, target: "/api/v1/auth/login", action: "auth.login", body: `{}`},
		{name: "OIDC start", method: http.MethodGet, target: "/api/v1/auth/oidc/start", action: "auth.oidc.start"},
		{name: "OIDC callback", method: http.MethodGet, target: "/api/v1/auth/oidc/callback?state=state&code=code", action: "auth.oidc.callback"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			eventsBefore := len(store.events)
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(test.method, test.target, strings.NewReader(test.body))
			if test.method == http.MethodPost {
				request.Header.Set("Origin", "http://localhost:8080")
				request.Header.Set("Content-Type", "application/json")
			}
			if test.name == "OIDC callback" {
				request.AddCookie(&http.Cookie{Name: service.FlowCookieName(), Value: "flow-binding"})
			}
			router.ServeHTTP(recorder, request)

			if recorder.Code != http.StatusTooManyRequests {
				t.Fatalf("status = %d, want 429: %s", recorder.Code, recorder.Body.String())
			}
			if got := recorder.Header().Get("Retry-After"); got != "1" {
				t.Fatalf("Retry-After = %q, want 1", got)
			}
			if got := recorder.Header().Get("Cache-Control"); got != "no-store" {
				t.Fatalf("Cache-Control = %q, want no-store", got)
			}
			if len(store.events) != eventsBefore+1 {
				t.Fatalf("rate-limit audit events = %d, want %d", len(store.events), eventsBefore+1)
			}
			last := store.events[len(store.events)-1]
			if last.Action != test.action || last.Result != "denied" || last.HTTPStatus != http.StatusTooManyRequests {
				t.Fatalf("rate-limit audit event = %+v", last)
			}
		})
	}
	if len(store.events) != len(tests) {
		t.Fatalf("audit events = %d, want %d", len(store.events), len(tests))
	}
}

func TestAuthOptionsAdmissionRejectsBeforeDatabaseLookup(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := &optionsCountingStore{}
	service, err := auth.NewService(store, auth.Config{
		RootKey: "0123456789abcdef0123456789abcdef", PublicURL: "http://localhost:8080",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	controller := NewAuthController(service)
	controller.optionsGuard = newPublicAuthGuard(0, 0, 1, 1)
	controller.anonymousAuditLimiter = rate.NewLimiter(0, 0)
	router := gin.New()
	router.GET("/options", controller.Options)

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/options", nil))
	if recorder.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", recorder.Code)
	}
	if got := store.calls.Load(); got != 0 {
		t.Fatalf("database lookups = %d, want zero", got)
	}
}

func TestAuthOptionsCacheCoalescesConcurrentDatabaseLookups(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := &optionsCountingStore{started: make(chan struct{}), release: make(chan struct{})}
	service, err := auth.NewService(store, auth.Config{
		RootKey: "0123456789abcdef0123456789abcdef", PublicURL: "http://localhost:8080",
		BootstrapEnabled: true, BootstrapToken: "0123456789abcdef0123456789abcdef",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	controller := NewAuthController(service)
	controller.optionsGuard = &publicAuthGuard{concurrency: newConcurrentAdmission(64, 64)}
	router := gin.New()
	router.GET("/options", controller.Options)

	const requests = 16
	statuses := make(chan int, requests)
	for index := 0; index < requests; index++ {
		go func() {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, "/options", nil)
			request.RemoteAddr = "192.0.2.10:1234"
			router.ServeHTTP(recorder, request)
			statuses <- recorder.Code
		}()
	}
	select {
	case <-store.started:
	case <-time.After(time.Second):
		t.Fatal("options database lookup did not start")
	}
	time.Sleep(20 * time.Millisecond)
	if got := store.calls.Load(); got != 1 {
		t.Fatalf("concurrent database lookups = %d, want one", got)
	}
	close(store.release)
	for index := 0; index < requests; index++ {
		if status := <-statuses; status != http.StatusOK {
			t.Fatalf("response status = %d, want 200", status)
		}
	}

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/options", nil))
	if got := store.calls.Load(); got != 1 {
		t.Fatalf("cached database lookups = %d, want one", got)
	}
	controller.invalidateOptionsCache()
	recorder = httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/options", nil))
	if got := store.calls.Load(); got != 2 {
		t.Fatalf("database lookups after invalidation = %d, want two", got)
	}
}

func TestRateLimitedAnonymousAuditWritesAreBounded(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := &rateLimitAuditStore{}
	service, err := auth.NewService(store, auth.Config{
		RootKey:   "0123456789abcdef0123456789abcdef",
		PublicURL: "http://localhost:8080",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	controller := NewAuthController(service)
	controller.loginGuard = newPublicAuthGuard(0, 0, 1, 1)
	controller.anonymousAuditLimiter = rate.NewLimiter(0, 0)
	router := gin.New()
	router.POST("/api/v1/auth/login", controller.Login)

	for requestNumber := 0; requestNumber < 3; requestNumber++ {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{}`))
		request.Header.Set("Origin", "http://localhost:8080")
		request.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusTooManyRequests {
			t.Fatalf("request %d status = %d, want 429", requestNumber, recorder.Code)
		}
	}
	if len(store.events) != 0 {
		t.Fatalf("exhausted anonymous audit budget wrote %d events, want 0", len(store.events))
	}
}

func TestPreflightAuditFloodCannotSuppressAdmittedAuthenticationFailure(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := &rateLimitAuditStore{}
	service, err := auth.NewService(store, auth.Config{
		RootKey: "0123456789abcdef0123456789abcdef", PublicURL: "http://localhost:8080",
		BootstrapEnabled: true, BootstrapToken: "0123456789abcdef0123456789abcdef",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	controller := NewAuthController(service)
	controller.anonymousAuditLimiter = rate.NewLimiter(0, 0)
	controller.authFailureAuditLimit = rate.NewLimiter(0, 1)
	router := gin.New()
	router.POST("/bootstrap", controller.Bootstrap)

	preflight := httptest.NewRequest(http.MethodPost, "/bootstrap", strings.NewReader(`{}`))
	preflight.Header.Set("Origin", "https://attacker.example")
	preflight.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(httptest.NewRecorder(), preflight)
	if len(store.events) != 0 {
		t.Fatalf("exhausted preflight budget wrote an event: %#v", store.events)
	}

	attempt := httptest.NewRequest(http.MethodPost, "/bootstrap", strings.NewReader(
		`{"bootstrap_token":"wrong","username":"admin","display_name":"Admin","password":"correct horse battery staple"}`))
	attempt.Header.Set("Origin", "http://localhost:8080")
	attempt.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(httptest.NewRecorder(), attempt)
	if len(store.events) != 1 || store.events[0].Action != "auth.bootstrap" || store.events[0].Result != "failed" {
		t.Fatalf("admitted authentication failure audit = %#v", store.events)
	}
}

func TestPublicAuthenticationValidatesCheapInputsBeforeAdmission(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := &rateLimitAuditStore{}
	service, err := auth.NewService(store, auth.Config{
		RootKey: "0123456789abcdef0123456789abcdef", PublicURL: "http://localhost:8080",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	controller := NewAuthController(service)
	controller.loginGuard = newPublicAuthGuard(0, 0, 1, 1)
	controller.oidcStartGuard = newPublicAuthGuard(0, 0, 1, 1)
	controller.oidcCallbackGuard = newPublicAuthGuard(0, 0, 1, 1)
	router := gin.New()
	router.POST("/login", controller.Login)
	router.GET("/start", controller.OIDCStart)
	router.GET("/callback", controller.OIDCCallback)

	request := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(`{}`))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("invalid origin status = %d, want 403", recorder.Code)
	}
	recorder = httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/start?unknown=value", nil))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("invalid OIDC start query status = %d, want 400", recorder.Code)
	}
	recorder = httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/callback?state=state&code=code", nil))
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("missing OIDC flow cookie status = %d, want 401", recorder.Code)
	}
}

func TestPublicAuthenticationGuardIsolatesClientsAndBoundsConcurrency(t *testing.T) {
	guard := newPublicAuthGuard(rate.Every(time.Hour), 1, 3, 2)
	releaseA1, ok := guard.acquire("192.0.2.1")
	if !ok {
		t.Fatal("first client attempt was rejected")
	}
	if _, ok := guard.acquire("192.0.2.1"); ok {
		t.Fatal("one client bypassed its rate budget")
	}
	releaseB, ok := guard.acquire("192.0.2.2")
	if !ok {
		t.Fatal("one exhausted client starved another client")
	}
	releaseA1()
	releaseB()

	concurrencyOnly := &publicAuthGuard{concurrency: newConcurrentAdmission(2, 1)}
	releaseA, ok := concurrencyOnly.acquire("192.0.2.1")
	if !ok {
		t.Fatal("first concurrent attempt was rejected")
	}
	if _, ok := concurrencyOnly.acquire("192.0.2.1"); ok {
		t.Fatal("one client bypassed the concurrent ceiling")
	}
	releaseB, ok = concurrencyOnly.acquire("192.0.2.2")
	if !ok {
		t.Fatal("second client did not receive its fair concurrent slot")
	}
	if _, ok := concurrencyOnly.acquire("192.0.2.3"); ok {
		t.Fatal("process-wide concurrent ceiling was bypassed")
	}
	releaseA()
	releaseB()
}

func TestKeyedRateLimiterKeepsBoundedStableEntriesAtCapacity(t *testing.T) {
	clock := time.Unix(1_700_000_000, 0)
	limiter := newKeyedRateLimiter(rate.Every(time.Hour), 1, 2, time.Minute)
	limiter.now = func() time.Time { return clock }
	if !limiter.allow("192.0.2.1") || !limiter.allow("192.0.2.2") {
		t.Fatal("initial client entries were rejected")
	}
	_ = limiter.allow("192.0.2.3")
	if len(limiter.entries) != 2 || limiter.entries["192.0.2.1"] == nil || limiter.entries["192.0.2.2"] == nil {
		t.Fatalf("capacity handling evicted stable client state: %#v", limiter.entries)
	}
	for index := 4; index < 100; index++ {
		_ = limiter.allow("198.51.100." + strconv.Itoa(index))
	}
	if len(limiter.entries) != 2 {
		t.Fatalf("rotating clients grew limiter table to %d entries", len(limiter.entries))
	}

	clock = clock.Add(2 * time.Minute)
	if !limiter.allow("203.0.113.1") {
		t.Fatal("expired entries were not reclaimed")
	}
	if len(limiter.entries) != 1 || limiter.entries["203.0.113.1"] == nil {
		t.Fatalf("expired limiter entries were not replaced: %#v", limiter.entries)
	}
}

func TestLoginAdmissionCoversSlowRequestBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := &rateLimitAuditStore{}
	service, err := auth.NewService(store, auth.Config{
		RootKey: "0123456789abcdef0123456789abcdef", PublicURL: "http://localhost:8080",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	controller := NewAuthController(service)
	controller.loginGuard = &publicAuthGuard{concurrency: newConcurrentAdmission(4, 1)}
	controller.anonymousAuditLimiter = rate.NewLimiter(0, 0)
	router := gin.New()
	router.POST("/login", controller.Login)

	firstBody := &blockingReadCloser{started: make(chan struct{}), release: make(chan struct{})}
	firstRequest := httptest.NewRequest(http.MethodPost, "/login", nil)
	firstRequest.Body = firstBody
	firstRequest.Header.Set("Origin", "http://localhost:8080")
	firstRequest.Header.Set("Content-Type", "application/json")
	firstDone := make(chan struct{})
	go func() {
		router.ServeHTTP(httptest.NewRecorder(), firstRequest)
		close(firstDone)
	}()
	select {
	case <-firstBody.started:
	case <-time.After(time.Second):
		t.Fatal("first request never entered body decoding")
	}

	secondBody := &blockingReadCloser{}
	secondRequest := httptest.NewRequest(http.MethodPost, "/login", nil)
	secondRequest.Body = secondBody
	secondRequest.Header.Set("Origin", "http://localhost:8080")
	secondRequest.Header.Set("Content-Type", "application/json")
	secondRecorder := httptest.NewRecorder()
	router.ServeHTTP(secondRecorder, secondRequest)
	if secondRecorder.Code != http.StatusTooManyRequests {
		t.Fatalf("second request status = %d, want 429", secondRecorder.Code)
	}
	if secondBody.read.Load() {
		t.Fatal("rejected concurrent request body was read before admission")
	}
	close(firstBody.release)
	select {
	case <-firstDone:
	case <-time.After(time.Second):
		t.Fatal("first request did not release its admission slot")
	}
}

func TestFailedAuthenticationAuditsShareBoundedAnonymousBudget(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := &rateLimitAuditStore{}
	service, err := auth.NewService(store, auth.Config{
		RootKey: "0123456789abcdef0123456789abcdef", PublicURL: "http://localhost:8080",
		BootstrapEnabled: true, BootstrapToken: "0123456789abcdef0123456789abcdef",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	controller := NewAuthController(service)
	controller.authFailureAuditLimit = rate.NewLimiter(0, 0)
	router := gin.New()
	router.POST("/bootstrap", controller.Bootstrap)
	router.POST("/login", controller.Login)
	router.GET("/oidc/start", controller.OIDCStart)
	router.GET("/oidc/callback", controller.OIDCCallback)

	requests := []*http.Request{
		httptest.NewRequest(http.MethodPost, "/bootstrap", strings.NewReader(
			`{"bootstrap_token":"wrong","username":"admin","display_name":"Admin","password":"correct horse battery staple"}`)),
		httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(`{"username":"missing","password":"wrong"}`)),
		httptest.NewRequest(http.MethodGet, "/oidc/start", nil),
		httptest.NewRequest(http.MethodGet, "/oidc/callback?state="+strings.Repeat("A", 43)+"&code=fake", nil),
	}
	requests[0].Header.Set("Content-Type", "application/json")
	requests[0].Header.Set("Origin", "http://localhost:8080")
	requests[1].Header.Set("Content-Type", "application/json")
	requests[1].Header.Set("Origin", "http://localhost:8080")
	requests[3].AddCookie(&http.Cookie{Name: service.FlowCookieName(), Value: strings.Repeat("A", 43)})
	for _, request := range requests {
		router.ServeHTTP(httptest.NewRecorder(), request)
	}
	if len(store.events) != 0 {
		t.Fatalf("exhausted anonymous failure budget wrote %d audit events: %#v", len(store.events), store.events)
	}
}

func TestSuccessfulAuthenticationAuditIsNotSuppressedByAnonymousFailureBudget(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := &rateLimitAuditStore{}
	service, err := auth.NewService(store, auth.Config{
		RootKey:   "0123456789abcdef0123456789abcdef",
		PublicURL: "http://localhost:8080",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	controller := NewAuthController(service)
	controller.anonymousAuditLimiter = rate.NewLimiter(0, 0)
	recorder := httptest.NewRecorder()
	ginContext, _ := gin.CreateTestContext(recorder)
	ginContext.Request = httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)

	controller.auditAuthenticationBestEffort(ginContext, "auth.login", auth.Principal{
		UserID: 7, Username: "admin", DisplayName: "Admin", AuthSource: "bootstrap",
	}, "succeeded", http.StatusOK)
	if len(store.events) != 1 || store.events[0].Action != "auth.login" || store.events[0].Result != "succeeded" {
		t.Fatalf("successful authentication audit = %#v", store.events)
	}
}
