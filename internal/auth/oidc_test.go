package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
	"golang.org/x/oauth2"
)

type oidcTestIdentity struct {
	issuer    string
	subject   string
	audience  []string
	nonce     string
	azp       string
	email     string
	verified  bool
	issuedAt  time.Time
	expiresAt time.Time
}

type oidcTestProvider struct {
	t        *testing.T
	server   *httptest.Server
	key      *rsa.PrivateKey
	keyID    string
	client   string
	mu       sync.Mutex
	verifier string
	identity oidcTestIdentity
}

func newOIDCTestProvider(t *testing.T) *oidcTestProvider {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate OIDC signing key: %v", err)
	}
	provider := &oidcTestProvider{t: t, key: key, keyID: "test-key", client: "ares-test-client"}
	provider.server = httptest.NewServer(http.HandlerFunc(provider.serveHTTP))
	t.Cleanup(provider.server.Close)
	return provider
}

func (p *oidcTestProvider) serveHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/.well-known/openid-configuration":
		writeOIDCTestJSON(w, map[string]any{
			"issuer":                                p.server.URL,
			"authorization_endpoint":                p.server.URL + "/authorize",
			"token_endpoint":                        p.server.URL + "/token",
			"jwks_uri":                              p.server.URL + "/keys",
			"response_types_supported":              []string{"code"},
			"subject_types_supported":               []string{"public"},
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	case "/keys":
		writeOIDCTestJSON(w, map[string]any{"keys": []jose.JSONWebKey{{
			Key: &p.key.PublicKey, KeyID: p.keyID, Algorithm: string(jose.RS256), Use: "sig",
		}}})
	case "/token":
		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid form", http.StatusBadRequest)
			return
		}
		p.mu.Lock()
		expectedVerifier := p.verifier
		identity := p.identity
		p.mu.Unlock()
		if r.Form.Get("grant_type") != "authorization_code" || r.Form.Get("code") == "" ||
			r.Form.Get("code_verifier") != expectedVerifier {
			http.Error(w, "invalid authorization code request", http.StatusBadRequest)
			return
		}
		idToken, err := p.sign(identity)
		if err != nil {
			p.t.Errorf("sign ID token: %v", err)
			http.Error(w, "signing failure", http.StatusInternalServerError)
			return
		}
		writeOIDCTestJSON(w, map[string]any{
			"access_token": "unused-access-token", "token_type": "Bearer",
			"expires_in": 300, "id_token": idToken,
		})
	default:
		http.NotFound(w, r)
	}
}

func (p *oidcTestProvider) sign(identity oidcTestIdentity) (string, error) {
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: p.key},
		(&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", p.keyID))
	if err != nil {
		return "", err
	}
	claims := jwt.Claims{
		Issuer: identity.issuer, Subject: identity.subject,
		Audience: jwt.Audience(identity.audience),
		IssuedAt: jwt.NewNumericDate(identity.issuedAt),
		Expiry:   jwt.NewNumericDate(identity.expiresAt),
	}
	privateClaims := struct {
		Nonce             string `json:"nonce"`
		AuthorizedParty   string `json:"azp,omitempty"`
		Email             string `json:"email"`
		EmailVerified     bool   `json:"email_verified"`
		Name              string `json:"name"`
		PreferredUsername string `json:"preferred_username"`
	}{
		Nonce: identity.nonce, AuthorizedParty: identity.azp,
		Email: identity.email, EmailVerified: identity.verified,
		Name: "OIDC User", PreferredUsername: "oidc-user",
	}
	return jwt.Signed(signer).Claims(claims).Claims(privateClaims).Serialize()
}

func (p *oidcTestProvider) configure(verifier string, identity oidcTestIdentity) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.verifier = verifier
	p.identity = identity
}

func writeOIDCTestJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}

func TestOIDCDiscoveryPKCEAndIDTokenBoundary(t *testing.T) {
	provider := newOIDCTestProvider(t)
	client, err := NewOIDCClient(OIDCConfig{
		IssuerURL: provider.server.URL, ClientID: provider.client, ClientSecret: "client-secret",
		RedirectURL: provider.server.URL + "/callback", RequireVerifiedEmail: true,
		AllowedSigningAlgorithms: []string{"RS256"}, MaxClockSkew: time.Minute,
	})
	if err != nil {
		t.Fatalf("create OIDC client: %v", err)
	}

	state := strings.Repeat("s", 43)
	nonce := strings.Repeat("n", 43)
	verifier := strings.Repeat("v", 43)
	authorizationURL, err := client.AuthorizationURL(t.Context(), state, nonce, verifier)
	if err != nil {
		t.Fatalf("build authorization URL: %v", err)
	}
	parsed, err := url.Parse(authorizationURL)
	if err != nil {
		t.Fatalf("parse authorization URL: %v", err)
	}
	query := parsed.Query()
	if parsed.Scheme+"://"+parsed.Host+parsed.Path != provider.server.URL+"/authorize" {
		t.Fatalf("authorization endpoint = %q", parsed.String())
	}
	for name, want := range map[string]string{
		"response_type": "code", "client_id": provider.client,
		"redirect_uri": provider.server.URL + "/callback", "state": state,
		"nonce": nonce, "code_challenge_method": "S256",
		"code_challenge": oauth2.S256ChallengeFromVerifier(verifier),
	} {
		if got := query.Get(name); got != want {
			t.Errorf("authorization query %s = %q, want %q", name, got, want)
		}
	}
	if strings.Contains(authorizationURL, verifier) {
		t.Fatal("authorization URL leaked the raw PKCE verifier")
	}

	now := time.Now().UTC()
	validIdentity := oidcTestIdentity{
		issuer: provider.server.URL, subject: "subject-123", audience: []string{provider.client},
		nonce: nonce, email: "user@example.com", verified: true,
		issuedAt: now.Add(-time.Minute), expiresAt: now.Add(5 * time.Minute),
	}
	provider.configure(verifier, validIdentity)
	claims, err := client.ExchangeAndVerify(t.Context(), "authorization-code", verifier, tokenHash(nonce))
	if err != nil {
		t.Fatalf("exchange valid OIDC response: %v", err)
	}
	if claims.Issuer != provider.server.URL || claims.Subject != "subject-123" ||
		claims.Email != "user@example.com" || !claims.EmailVerified || claims.PreferredUsername != "oidc-user" {
		t.Fatalf("unexpected verified claims: %#v", claims)
	}

	tests := []struct {
		name   string
		mutate func(*oidcTestIdentity)
	}{
		{
			name:   "nonce mismatch",
			mutate: func(identity *oidcTestIdentity) { identity.nonce = strings.Repeat("x", 43) },
		},
		{
			name:   "issuer mismatch",
			mutate: func(identity *oidcTestIdentity) { identity.issuer = provider.server.URL + "/other" },
		},
		{
			name:   "audience mismatch",
			mutate: func(identity *oidcTestIdentity) { identity.audience = []string{"another-client"} },
		},
		{
			name:   "single audience rejects mismatched azp",
			mutate: func(identity *oidcTestIdentity) { identity.azp = "another-client" },
		},
		{
			name: "multiple audiences require matching azp",
			mutate: func(identity *oidcTestIdentity) {
				identity.audience = []string{provider.client, "another-client"}
				identity.azp = "another-client"
			},
		},
		{
			name:   "unverified email",
			mutate: func(identity *oidcTestIdentity) { identity.verified = false },
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			identity := validIdentity
			test.mutate(&identity)
			provider.configure(verifier, identity)
			if _, verifyErr := client.ExchangeAndVerify(t.Context(), "authorization-code", verifier, tokenHash(nonce)); verifyErr == nil {
				t.Fatal("invalid ID token was accepted")
			}
		})
	}
}

func TestOIDCInitializationIsSharedAndWaitersHonorContext(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var startOnce sync.Once
	var discoveryRequests atomic.Int32
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/openid-configuration" {
			http.NotFound(w, r)
			return
		}
		discoveryRequests.Add(1)
		startOnce.Do(func() { close(started) })
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		writeOIDCTestJSON(w, map[string]any{
			"issuer":                                server.URL,
			"authorization_endpoint":                server.URL + "/authorize",
			"token_endpoint":                        server.URL + "/token",
			"jwks_uri":                              server.URL + "/keys",
			"response_types_supported":              []string{"code"},
			"subject_types_supported":               []string{"public"},
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	}))
	t.Cleanup(server.Close)

	client, err := NewOIDCClient(OIDCConfig{
		IssuerURL: server.URL, ClientID: "ares-test-client", ClientSecret: "client-secret",
		RedirectURL: server.URL + "/callback", HTTPTimeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	firstResult := make(chan error, 1)
	go func() {
		_, firstErr := client.AuthorizationURL(context.Background(), "state", "nonce", "verifier")
		firstResult <- firstErr
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("OIDC discovery did not start")
	}

	waiterContext, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := client.AuthorizationURL(waiterContext, "state", "nonce", "verifier"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("waiting initialization error = %v, want context deadline", err)
	}
	if got := discoveryRequests.Load(); got != 1 {
		t.Fatalf("concurrent initialization made %d discovery requests, want 1", got)
	}

	close(release)
	select {
	case err := <-firstResult:
		if err != nil {
			t.Fatalf("shared discovery failed: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("shared discovery did not finish")
	}
	if _, err := client.AuthorizationURL(t.Context(), "state", "nonce", "verifier"); err != nil {
		t.Fatalf("cached OIDC initialization failed: %v", err)
	}
	if got := discoveryRequests.Load(); got != 1 {
		t.Fatalf("cached initialization made %d discovery requests, want 1", got)
	}
}

func TestOIDCInitializationFailureIsTemporarilyCached(t *testing.T) {
	var discoveryRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		discoveryRequests.Add(1)
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	t.Cleanup(server.Close)
	client, err := NewOIDCClient(OIDCConfig{
		IssuerURL: server.URL, ClientID: "ares-test-client", ClientSecret: "client-secret",
		RedirectURL: server.URL + "/callback", HTTPTimeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		if _, err := client.AuthorizationURL(t.Context(), "state", "nonce", "verifier"); err == nil {
			t.Fatal("failed OIDC discovery was accepted")
		}
	}
	if got := discoveryRequests.Load(); got != 1 {
		t.Fatalf("negative cache made %d discovery requests, want 1", got)
	}
}

func TestOIDCDiscoveryRejectsOversizedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(strings.Repeat("x", int(maxOIDCResponseBodyBytes)+1)))
	}))
	t.Cleanup(server.Close)
	client, err := NewOIDCClient(OIDCConfig{
		IssuerURL: server.URL, ClientID: "ares-test-client", ClientSecret: "client-secret",
		RedirectURL: server.URL + "/callback", HTTPTimeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.AuthorizationURL(t.Context(), "state", "nonce", "verifier"); err == nil || !strings.Contains(err.Error(), errOIDCResponseTooLarge.Error()) {
		t.Fatalf("oversized OIDC discovery error = %v", err)
	}
}

func TestOIDCRejectsDiscoveryRedirectWithoutContactingTarget(t *testing.T) {
	var targetRequests atomic.Int64
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		targetRequests.Add(1)
	}))
	t.Cleanup(target.Close)
	issuer := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		http.Redirect(response, request, target.URL, http.StatusTemporaryRedirect)
	}))
	t.Cleanup(issuer.Close)

	client, err := NewOIDCClient(OIDCConfig{
		IssuerURL: issuer.URL, ClientID: "ares-test-client", ClientSecret: "client-secret",
		RedirectURL: issuer.URL + "/callback", HTTPTimeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.AuthorizationURL(t.Context(), "state", "nonce", "verifier"); err == nil ||
		!strings.Contains(err.Error(), "redirects are not allowed") {
		t.Fatalf("discovery redirect error = %v", err)
	}
	if got := targetRequests.Load(); got != 0 {
		t.Fatalf("discovery redirect target received %d request(s), want zero", got)
	}
}

func TestOIDCRejectsInsecureDiscoveredEndpoints(t *testing.T) {
	for _, field := range []string{"authorization_endpoint", "token_endpoint", "jwks_uri", "userinfo_endpoint"} {
		t.Run(field, func(t *testing.T) {
			var issuer *httptest.Server
			issuer = httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				if request.URL.Path != "/.well-known/openid-configuration" {
					http.NotFound(response, request)
					return
				}
				metadata := map[string]any{
					"issuer": issuer.URL, "authorization_endpoint": issuer.URL + "/authorize",
					"token_endpoint": issuer.URL + "/token", "jwks_uri": issuer.URL + "/keys",
					"userinfo_endpoint":        issuer.URL + "/userinfo",
					"response_types_supported": []string{"code"}, "subject_types_supported": []string{"public"},
					"id_token_signing_alg_values_supported": []string{"RS256"},
				}
				metadata[field] = "http://identity.example.test/unsafe"
				writeOIDCTestJSON(response, metadata)
			}))
			t.Cleanup(issuer.Close)
			client, err := NewOIDCClient(OIDCConfig{
				IssuerURL: issuer.URL, ClientID: "ares-test-client", ClientSecret: "client-secret",
				RedirectURL: issuer.URL + "/callback", HTTPTimeout: time.Second,
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := client.AuthorizationURL(t.Context(), "state", "nonce", "verifier"); err == nil ||
				!strings.Contains(err.Error(), "must use HTTPS") {
				t.Fatalf("insecure %s error = %v", field, err)
			}
		})
	}
}

func TestHTTPSOIDCIssuerCannotDowngradeDiscoveredEndpointToLoopbackHTTP(t *testing.T) {
	var issuer *httptest.Server
	issuer = httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/.well-known/openid-configuration" {
			http.NotFound(response, request)
			return
		}
		writeOIDCTestJSON(response, map[string]any{
			"issuer": issuer.URL, "authorization_endpoint": issuer.URL + "/authorize",
			"token_endpoint": "http://127.0.0.1:1/token", "jwks_uri": issuer.URL + "/keys",
			"response_types_supported": []string{"code"}, "subject_types_supported": []string{"public"},
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	}))
	t.Cleanup(issuer.Close)
	originalTransport := http.DefaultTransport
	http.DefaultTransport = issuer.Client().Transport
	t.Cleanup(func() { http.DefaultTransport = originalTransport })

	client, err := NewOIDCClient(OIDCConfig{
		IssuerURL: issuer.URL, ClientID: "ares-test-client", ClientSecret: "client-secret",
		RedirectURL: issuer.URL + "/callback", HTTPTimeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.AuthorizationURL(t.Context(), "state", "nonce", "verifier"); err == nil ||
		!strings.Contains(err.Error(), "must use HTTPS") {
		t.Fatalf("HTTPS issuer loopback downgrade error = %v", err)
	}
}

func TestOIDCTokenRedirectDoesNotForwardCodeOrClientSecret(t *testing.T) {
	var targetRequests atomic.Int64
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		targetRequests.Add(1)
	}))
	t.Cleanup(target.Close)
	var issuer *httptest.Server
	issuer = httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/.well-known/openid-configuration":
			writeOIDCTestJSON(response, map[string]any{
				"issuer": issuer.URL, "authorization_endpoint": issuer.URL + "/authorize",
				"token_endpoint": issuer.URL + "/token", "jwks_uri": issuer.URL + "/keys",
				"response_types_supported": []string{"code"}, "subject_types_supported": []string{"public"},
				"id_token_signing_alg_values_supported": []string{"RS256"},
			})
		case "/token":
			response.Header().Set("Location", target.URL)
			response.WriteHeader(http.StatusTemporaryRedirect)
		default:
			http.NotFound(response, request)
		}
	}))
	t.Cleanup(issuer.Close)
	client, err := NewOIDCClient(OIDCConfig{
		IssuerURL: issuer.URL, ClientID: "ares-test-client", ClientSecret: "client-secret",
		RedirectURL: issuer.URL + "/callback", HTTPTimeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.ExchangeAndVerify(
		t.Context(), "authorization-code", strings.Repeat("v", 43), tokenHash(strings.Repeat("n", 43)),
	); err == nil {
		t.Fatal("token redirect was accepted")
	}
	if got := targetRequests.Load(); got != 0 {
		t.Fatalf("token redirect target received %d request(s), want zero", got)
	}
}
