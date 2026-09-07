package auth

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

type OIDCConfig struct {
	IssuerURL                string
	ClientID                 string
	ClientSecret             string
	RedirectURL              string
	Scopes                   []string
	RequireVerifiedEmail     bool
	AllowedSigningAlgorithms []string
	MaxClockSkew             time.Duration
	HTTPTimeout              time.Duration
}

type oidcClient struct {
	config OIDCConfig
	client *http.Client

	mu           sync.Mutex
	oauth        *oauth2.Config
	verifier     *oidc.IDTokenVerifier
	initializing chan struct{}
	initErr      error
	initRetryAt  time.Time
}

const (
	oidcInitializationFailureBackoff = 5 * time.Second
	maxOIDCResponseBodyBytes         = int64(1024 * 1024)
)

var errOIDCResponseTooLarge = errors.New("OIDC response exceeds the allowed size")

type boundedOIDCTransport struct {
	base http.RoundTripper
}

func (t boundedOIDCTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	response, err := base.RoundTrip(request)
	if err != nil {
		return nil, err
	}
	if response.Body == nil {
		return response, nil
	}
	if response.ContentLength > maxOIDCResponseBodyBytes {
		_ = response.Body.Close()
		return nil, errOIDCResponseTooLarge
	}
	response.Body = &boundedOIDCResponseBody{
		reader: response.Body, closer: response.Body, remaining: maxOIDCResponseBodyBytes,
	}
	return response, nil
}

type boundedOIDCResponseBody struct {
	reader    io.Reader
	closer    io.Closer
	remaining int64
}

func (b *boundedOIDCResponseBody) Read(buffer []byte) (int, error) {
	if b.remaining <= 0 {
		var probe [1]byte
		read, err := b.reader.Read(probe[:])
		if read > 0 {
			return 0, errOIDCResponseTooLarge
		}
		if err != nil {
			return 0, err
		}
		return 0, io.ErrNoProgress
	}
	if int64(len(buffer)) > b.remaining {
		buffer = buffer[:b.remaining]
	}
	read, err := b.reader.Read(buffer)
	b.remaining -= int64(read)
	return read, err
}

func (b *boundedOIDCResponseBody) Close() error {
	return b.closer.Close()
}

func NewOIDCClient(config OIDCConfig) (OIDCClient, error) {
	issuer, err := validateOIDCEndpoint("issuer", config.IssuerURL)
	if err != nil {
		return nil, err
	}
	redirect, err := validateOIDCEndpoint("redirect", config.RedirectURL)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(config.ClientID) == "" || strings.TrimSpace(config.ClientSecret) == "" {
		return nil, errors.New("OIDC client ID and client secret are required")
	}
	config.IssuerURL = issuer.String()
	config.RedirectURL = redirect.String()
	if len(config.Scopes) == 0 {
		config.Scopes = []string{oidc.ScopeOpenID, "profile", "email"}
	}
	if !slices.Contains(config.Scopes, oidc.ScopeOpenID) {
		return nil, errors.New("OIDC scopes must contain openid")
	}
	if len(config.AllowedSigningAlgorithms) == 0 {
		config.AllowedSigningAlgorithms = []string{"RS256"}
	}
	for _, algorithm := range config.AllowedSigningAlgorithms {
		switch algorithm {
		case "RS256", "RS384", "RS512", "ES256", "ES384", "ES512", "EdDSA":
		default:
			return nil, fmt.Errorf("OIDC signing algorithm %q is not allowed", algorithm)
		}
	}
	if config.MaxClockSkew <= 0 || config.MaxClockSkew > 5*time.Minute {
		config.MaxClockSkew = time.Minute
	}
	if config.HTTPTimeout <= 0 || config.HTTPTimeout > time.Minute {
		config.HTTPTimeout = 10 * time.Second
	}
	return &oidcClient{config: config, client: &http.Client{
		Timeout: config.HTTPTimeout, Transport: boundedOIDCTransport{base: http.DefaultTransport},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errors.New("OIDC HTTP redirects are not allowed")
		},
	}}, nil
}

func (c *oidcClient) Issuer() string { return c.config.IssuerURL }

func validateOIDCEndpoint(name, raw string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, fmt.Errorf("invalid OIDC %s URL", name)
	}
	if parsed.Scheme != "https" && !(parsed.Scheme == "http" &&
		(parsed.Hostname() == "localhost" || parsed.Hostname() == "127.0.0.1" || parsed.Hostname() == "::1")) {
		return nil, fmt.Errorf("OIDC %s URL must use HTTPS except on loopback", name)
	}
	return parsed, nil
}

func validateDiscoveredOIDCEndpoint(name, raw string, required, allowLoopbackHTTP bool) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" && !required {
		return "", nil
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
		return "", fmt.Errorf("invalid OIDC %s URL", name)
	}
	loopback := parsed.Hostname() == "localhost" || parsed.Hostname() == "127.0.0.1" || parsed.Hostname() == "::1"
	if parsed.Scheme != "https" && !(allowLoopbackHTTP && parsed.Scheme == "http" && loopback) {
		return "", fmt.Errorf("OIDC %s URL must use HTTPS except on loopback", name)
	}
	return parsed.String(), nil
}

func (c *oidcClient) initialize(ctx context.Context) (*oauth2.Config, *oidc.IDTokenVerifier, error) {
	for {
		c.mu.Lock()
		if c.oauth != nil && c.verifier != nil {
			oauthConfig, verifier := c.oauth, c.verifier
			c.mu.Unlock()
			return oauthConfig, verifier, nil
		}
		if c.initializing == nil && c.initErr != nil && time.Now().Before(c.initRetryAt) {
			err := c.initErr
			c.mu.Unlock()
			return nil, nil, err
		}
		done := c.initializing
		if done == nil {
			done = make(chan struct{})
			c.initializing = done
			go c.initializeProvider(done)
		}
		c.mu.Unlock()

		select {
		case <-done:
			// Re-enter under the mutex to read the completed result or its
			// bounded negative-cache state.
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		}
	}
}

func (c *oidcClient) initializeProvider(done chan struct{}) {
	discoveryContext, cancel := context.WithTimeout(context.Background(), c.config.HTTPTimeout)
	defer cancel()
	discoveryContext = oidc.ClientContext(discoveryContext, c.client)
	provider, err := oidc.NewProvider(discoveryContext, c.config.IssuerURL)
	if err == nil {
		allowLoopbackHTTP := strings.HasPrefix(c.config.IssuerURL, "http://")
		endpoint := provider.Endpoint()
		authorizationEndpoint, validationErr := validateDiscoveredOIDCEndpoint(
			"authorization endpoint", endpoint.AuthURL, true, allowLoopbackHTTP,
		)
		if validationErr == nil {
			endpoint.AuthURL = authorizationEndpoint
			endpoint.TokenURL, validationErr = validateDiscoveredOIDCEndpoint(
				"token endpoint", endpoint.TokenURL, true, allowLoopbackHTTP,
			)
		}
		var metadata struct {
			JWKSURI          string `json:"jwks_uri"`
			UserInfoEndpoint string `json:"userinfo_endpoint"`
		}
		if validationErr == nil {
			validationErr = provider.Claims(&metadata)
		}
		if validationErr == nil {
			_, validationErr = validateDiscoveredOIDCEndpoint("JWKS endpoint", metadata.JWKSURI, true, allowLoopbackHTTP)
		}
		if validationErr == nil {
			_, validationErr = validateDiscoveredOIDCEndpoint(
				"userinfo endpoint", metadata.UserInfoEndpoint, false, allowLoopbackHTTP,
			)
		}
		if validationErr != nil {
			c.finishInitialization(done, nil, nil, fmt.Errorf("validate OIDC provider metadata: %w", validationErr))
			return
		}
		oauthConfig := &oauth2.Config{
			ClientID: c.config.ClientID, ClientSecret: c.config.ClientSecret,
			RedirectURL: c.config.RedirectURL, Endpoint: endpoint,
			Scopes: append([]string(nil), c.config.Scopes...),
		}
		// Use a long-lived context containing only the bounded HTTP client. A
		// request-scoped cancellation must not poison the cached JWKS verifier.
		verifierContext := oidc.ClientContext(context.Background(), c.client)
		verifier := provider.VerifierContext(verifierContext, &oidc.Config{
			ClientID:             c.config.ClientID,
			SupportedSigningAlgs: append([]string(nil), c.config.AllowedSigningAlgorithms...),
			Now:                  time.Now,
		})
		c.finishInitialization(done, oauthConfig, verifier, nil)
		return
	}
	c.finishInitialization(done, nil, nil, fmt.Errorf("discover OIDC provider: %w", err))
}

func (c *oidcClient) finishInitialization(
	done chan struct{},
	oauthConfig *oauth2.Config,
	verifier *oidc.IDTokenVerifier,
	err error,
) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.initializing != done {
		return
	}
	c.oauth, c.verifier, c.initErr = oauthConfig, verifier, err
	if err != nil {
		c.initRetryAt = time.Now().Add(oidcInitializationFailureBackoff)
	} else {
		c.initRetryAt = time.Time{}
	}
	c.initializing = nil
	close(done)
}

func (c *oidcClient) AuthorizationURL(ctx context.Context, state, nonce, verifier string) (string, error) {
	oauthConfig, _, err := c.initialize(ctx)
	if err != nil {
		return "", err
	}
	return oauthConfig.AuthCodeURL(state, oidc.Nonce(nonce), oauth2.S256ChallengeOption(verifier)), nil
}

func (c *oidcClient) ExchangeAndVerify(ctx context.Context, code, verifier string, expectedNonceHash []byte) (OIDCClaims, error) {
	if strings.TrimSpace(code) == "" || len(code) > 4096 || !validOpaqueToken(verifier) || len(expectedNonceHash) != 32 {
		return OIDCClaims{}, ErrInvalidOIDCFlow
	}
	oauthConfig, tokenVerifier, err := c.initialize(ctx)
	if err != nil {
		return OIDCClaims{}, err
	}
	exchangeContext, cancel := context.WithTimeout(ctx, c.config.HTTPTimeout)
	defer cancel()
	exchangeContext = context.WithValue(exchangeContext, oauth2.HTTPClient, c.client)
	oauthToken, err := oauthConfig.Exchange(exchangeContext, code, oauth2.VerifierOption(verifier))
	if err != nil {
		return OIDCClaims{}, errors.New("OIDC authorization code exchange failed")
	}
	rawIDToken, ok := oauthToken.Extra("id_token").(string)
	if !ok || rawIDToken == "" {
		return OIDCClaims{}, errors.New("OIDC token response omitted id_token")
	}
	idToken, err := tokenVerifier.Verify(exchangeContext, rawIDToken)
	if err != nil {
		return OIDCClaims{}, errors.New("OIDC ID token validation failed")
	}
	if idToken.Issuer != c.config.IssuerURL || idToken.Subject == "" ||
		len(idToken.Nonce) > 512 || subtle.ConstantTimeCompare(tokenHash(idToken.Nonce), expectedNonceHash) != 1 {
		return OIDCClaims{}, errors.New("OIDC ID token identity or nonce validation failed")
	}
	now := time.Now()
	if idToken.IssuedAt.IsZero() || idToken.IssuedAt.After(now.Add(c.config.MaxClockSkew)) || !now.Before(idToken.Expiry) {
		return OIDCClaims{}, errors.New("OIDC ID token time validation failed")
	}
	var claims struct {
		Email             string `json:"email"`
		EmailVerified     bool   `json:"email_verified"`
		Name              string `json:"name"`
		PreferredUsername string `json:"preferred_username"`
		AuthorizedParty   string `json:"azp"`
		NotBefore         int64  `json:"nbf"`
	}
	if err := idToken.Claims(&claims); err != nil {
		return OIDCClaims{}, errors.New("OIDC ID token claims are invalid")
	}
	if claims.NotBefore != 0 && now.Add(c.config.MaxClockSkew).Before(time.Unix(claims.NotBefore, 0)) {
		return OIDCClaims{}, errors.New("OIDC ID token is not active")
	}
	if (claims.AuthorizedParty != "" && claims.AuthorizedParty != c.config.ClientID) ||
		(len(idToken.Audience) > 1 && claims.AuthorizedParty != c.config.ClientID) {
		return OIDCClaims{}, errors.New("OIDC authorized party is invalid")
	}
	if c.config.RequireVerifiedEmail && (strings.TrimSpace(claims.Email) == "" || !claims.EmailVerified) {
		return OIDCClaims{}, errors.New("OIDC verified email is required")
	}
	if len(idToken.Issuer) > 2048 || len(idToken.Subject) > 255 || len(claims.Email) > 320 ||
		len(claims.Name) > 1024 || len(claims.PreferredUsername) > 255 {
		return OIDCClaims{}, errors.New("OIDC identity claims exceed allowed length")
	}
	return OIDCClaims{
		Issuer: idToken.Issuer, Subject: idToken.Subject,
		Email: strings.TrimSpace(claims.Email), EmailVerified: claims.EmailVerified,
		Name: strings.TrimSpace(claims.Name), PreferredUsername: strings.TrimSpace(claims.PreferredUsername),
	}, nil
}
