package config

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

const (
	defaultWebReadHeaderTimeout = 5 * time.Second
	defaultWebReadTimeout       = 15 * time.Second
	defaultWebWriteTimeout      = 30 * time.Second
	defaultWebIdleTimeout       = 60 * time.Second
	defaultWebMaxHeaderBytes    = 64 * 1024
	defaultWebMaxJSONBodyBytes  = int64(1024 * 1024)

	defaultSSEHeartbeatInterval = 15 * time.Second
	defaultSSEReauthInterval    = 30 * time.Second
	defaultSSEWriteTimeout      = 10 * time.Second
	defaultSSEIdleTimeout       = 2 * time.Minute
	defaultSSEMaxDuration       = 15 * time.Minute

	defaultOIDCFlowTTL          = 10 * time.Minute
	defaultOIDCHTTPTimeout      = 10 * time.Second
	defaultOIDCMaxClockSkew     = time.Minute
	defaultSessionIdleTimeout   = 30 * time.Minute
	defaultSessionAbsolute      = 8 * time.Hour
	defaultSessionTouchInterval = 5 * time.Minute

	maxSecretFileBytes = 64 * 1024
)

type WebConfig struct {
	Address           string    `yaml:"address"`
	PublicURL         string    `yaml:"public_url"`
	TrustedProxyCIDRs []string  `yaml:"trusted_proxy_cidrs"`
	ReadHeaderTimeout string    `yaml:"read_header_timeout"`
	ReadTimeout       string    `yaml:"read_timeout"`
	WriteTimeout      string    `yaml:"write_timeout"`
	IdleTimeout       string    `yaml:"idle_timeout"`
	MaxHeaderBytes    int       `yaml:"max_header_bytes"`
	MaxJSONBodyBytes  int64     `yaml:"max_json_body_bytes"`
	SSE               SSEConfig `yaml:"sse"`
}

type SSEConfig struct {
	HeartbeatInterval string `yaml:"heartbeat_interval"`
	ReauthInterval    string `yaml:"reauth_interval"`
	WriteTimeout      string `yaml:"write_timeout"`
	IdleTimeout       string `yaml:"idle_timeout"`
	MaxDuration       string `yaml:"max_duration"`
}

type AuthConfig struct {
	RootKey          string                 `yaml:"root_key"`
	RootKeyFile      string                 `yaml:"root_key_file"`
	OIDC             OIDCConfig             `yaml:"oidc"`
	Session          SessionConfig          `yaml:"session"`
	LocalLogin       LocalLoginConfig       `yaml:"local_login"`
	Bootstrap        BootstrapConfig        `yaml:"bootstrap"`
	LegacyAdminToken LegacyAdminTokenConfig `yaml:"legacy_admin_token"`
}

type OIDCConfig struct {
	Enabled                  bool     `yaml:"enabled"`
	IssuerURL                string   `yaml:"issuer_url"`
	ClientID                 string   `yaml:"client_id"`
	ClientSecret             string   `yaml:"client_secret"`
	ClientSecretFile         string   `yaml:"client_secret_file"`
	Scopes                   []string `yaml:"scopes"`
	RequireVerifiedEmail     bool     `yaml:"require_verified_email"`
	AutoProvision            bool     `yaml:"auto_provision"`
	FlowTTL                  string   `yaml:"flow_ttl"`
	HTTPTimeout              string   `yaml:"http_timeout"`
	MaxClockSkew             string   `yaml:"max_clock_skew"`
	AllowedSigningAlgorithms []string `yaml:"allowed_signing_algorithms"`
}

type SessionConfig struct {
	IdleTimeout     string `yaml:"idle_timeout"`
	AbsoluteTimeout string `yaml:"absolute_timeout"`
	TouchInterval   string `yaml:"touch_interval"`
}

type LocalLoginConfig struct {
	Enabled bool `yaml:"enabled"`
}

type BootstrapConfig struct {
	Enabled   bool   `yaml:"enabled"`
	Token     string `yaml:"token"`
	TokenFile string `yaml:"token_file"`
}

type LegacyAdminTokenConfig struct {
	Enabled   bool   `yaml:"enabled"`
	Token     string `yaml:"token"`
	TokenFile string `yaml:"token_file"`
	SunsetAt  string `yaml:"sunset_at"`
}

type resolvedSecrets struct {
	settingsAdminToken    string
	settingsEncryptionKey string
	authRootKey           string
	oidcClientSecret      string
	bootstrapToken        string
	legacyAdminToken      string
}

func overrideSecretSource(directName, fileName string, direct, file *string) error {
	directValue, directSet := os.LookupEnv(directName)
	fileValue, fileSet := os.LookupEnv(fileName)
	if directSet && fileSet {
		return fmt.Errorf("%s and %s are mutually exclusive", directName, fileName)
	}
	if directSet {
		*direct = directValue
	}
	if fileSet {
		*file = fileValue
	}
	return nil
}

func splitCommaSeparated(value string) []string {
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if value := strings.TrimSpace(part); value != "" {
			result = append(result, value)
		}
	}
	return result
}

func normalizeAndValidateSecurityConfig(cfg *Config) error {
	if cfg == nil {
		return errors.New("config is nil")
	}

	applySecurityDefaults(cfg)
	if err := validateWebConfig(&cfg.Web); err != nil {
		return err
	}
	if err := resolveConfiguredSecrets(cfg); err != nil {
		return err
	}
	if err := validateAuthConfig(cfg); err != nil {
		return err
	}
	if mustDuration(cfg.Web.SSE.ReauthInterval, defaultSSEReauthInterval) >
		mustDuration(cfg.Auth.Session.IdleTimeout, defaultSessionIdleTimeout) {
		return errors.New("web.sse.reauth_interval must not exceed auth.session.idle_timeout")
	}
	return nil
}

func applySecurityDefaults(cfg *Config) {
	setDurationDefault(&cfg.Web.ReadHeaderTimeout, defaultWebReadHeaderTimeout)
	setDurationDefault(&cfg.Web.ReadTimeout, defaultWebReadTimeout)
	setDurationDefault(&cfg.Web.WriteTimeout, defaultWebWriteTimeout)
	setDurationDefault(&cfg.Web.IdleTimeout, defaultWebIdleTimeout)
	if cfg.Web.MaxHeaderBytes == 0 {
		cfg.Web.MaxHeaderBytes = defaultWebMaxHeaderBytes
	}
	if cfg.Web.MaxJSONBodyBytes == 0 {
		cfg.Web.MaxJSONBodyBytes = defaultWebMaxJSONBodyBytes
	}
	setDurationDefault(&cfg.Web.SSE.HeartbeatInterval, defaultSSEHeartbeatInterval)
	setDurationDefault(&cfg.Web.SSE.ReauthInterval, defaultSSEReauthInterval)
	setDurationDefault(&cfg.Web.SSE.WriteTimeout, defaultSSEWriteTimeout)
	setDurationDefault(&cfg.Web.SSE.IdleTimeout, defaultSSEIdleTimeout)
	setDurationDefault(&cfg.Web.SSE.MaxDuration, defaultSSEMaxDuration)

	setDurationDefault(&cfg.Auth.OIDC.FlowTTL, defaultOIDCFlowTTL)
	setDurationDefault(&cfg.Auth.OIDC.HTTPTimeout, defaultOIDCHTTPTimeout)
	setDurationDefault(&cfg.Auth.OIDC.MaxClockSkew, defaultOIDCMaxClockSkew)
	setDurationDefault(&cfg.Auth.Session.IdleTimeout, defaultSessionIdleTimeout)
	setDurationDefault(&cfg.Auth.Session.AbsoluteTimeout, defaultSessionAbsolute)
	setDurationDefault(&cfg.Auth.Session.TouchInterval, defaultSessionTouchInterval)
	if cfg.Auth.OIDC.Enabled && len(cfg.Auth.OIDC.Scopes) == 0 {
		cfg.Auth.OIDC.Scopes = []string{"openid", "profile", "email"}
	}
	if cfg.Auth.OIDC.Enabled && len(cfg.Auth.OIDC.AllowedSigningAlgorithms) == 0 {
		cfg.Auth.OIDC.AllowedSigningAlgorithms = []string{"RS256"}
	}
}

func setDurationDefault(target *string, value time.Duration) {
	if strings.TrimSpace(*target) == "" {
		*target = value.String()
	}
}

func validateWebConfig(web *WebConfig) error {
	var err error
	web.PublicURL, err = validatePublicURL(web.PublicURL)
	if err != nil {
		return fmt.Errorf("web.public_url: %w", err)
	}
	if err := validateTrustedProxyCIDRs(web); err != nil {
		return err
	}

	for _, duration := range []struct {
		name  string
		value string
		max   time.Duration
	}{
		{"web.read_header_timeout", web.ReadHeaderTimeout, time.Minute},
		{"web.read_timeout", web.ReadTimeout, 10 * time.Minute},
		{"web.write_timeout", web.WriteTimeout, 10 * time.Minute},
		{"web.idle_timeout", web.IdleTimeout, 10 * time.Minute},
		{"web.sse.heartbeat_interval", web.SSE.HeartbeatInterval, 5 * time.Minute},
		{"web.sse.reauth_interval", web.SSE.ReauthInterval, time.Minute},
		{"web.sse.write_timeout", web.SSE.WriteTimeout, time.Minute},
		{"web.sse.idle_timeout", web.SSE.IdleTimeout, time.Hour},
		{"web.sse.max_duration", web.SSE.MaxDuration, 24 * time.Hour},
	} {
		if _, err := parseBoundedDuration(duration.name, duration.value, duration.max); err != nil {
			return err
		}
	}
	if web.MaxHeaderBytes < 1024 || web.MaxHeaderBytes > 1024*1024 {
		return fmt.Errorf("web.max_header_bytes must be between 1024 and 1048576")
	}
	if web.MaxJSONBodyBytes < 1024 || web.MaxJSONBodyBytes > 64*1024*1024 {
		return fmt.Errorf("web.max_json_body_bytes must be between 1024 and 67108864")
	}
	heartbeat := mustDuration(web.SSE.HeartbeatInterval, defaultSSEHeartbeatInterval)
	idle := mustDuration(web.SSE.IdleTimeout, defaultSSEIdleTimeout)
	maximum := mustDuration(web.SSE.MaxDuration, defaultSSEMaxDuration)
	if idle < 30*time.Second {
		return fmt.Errorf("web.sse.idle_timeout must be at least 30s")
	}
	if heartbeat >= idle {
		return fmt.Errorf("web.sse.heartbeat_interval must be shorter than web.sse.idle_timeout")
	}
	if idle > maximum {
		return fmt.Errorf("web.sse.idle_timeout must not exceed web.sse.max_duration")
	}
	if mustDuration(web.SSE.ReauthInterval, defaultSSEReauthInterval) > maximum {
		return fmt.Errorf("web.sse.reauth_interval must not exceed web.sse.max_duration")
	}
	return nil
}

func validatePublicURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", errors.New("must be an absolute HTTP or HTTPS URL")
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return "", errors.New("must use HTTP or HTTPS")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("must not contain credentials, query, or fragment")
	}
	if parsed.Path != "" && parsed.Path != "/" {
		return "", errors.New("must not contain a path")
	}
	if parsed.Scheme == "http" && !isLoopbackHostname(parsed.Hostname()) {
		return "", errors.New("HTTP is allowed only for loopback development")
	}
	canonicalHost, err := canonicalOriginHost(parsed)
	if err != nil {
		return "", err
	}
	return parsed.Scheme + "://" + canonicalHost, nil
}

func canonicalOriginHost(parsed *url.URL) (string, error) {
	hostname := strings.ToLower(parsed.Hostname())
	if hostname == "" || strings.HasSuffix(parsed.Host, ":") {
		return "", errors.New("must contain a valid host and port")
	}
	port := parsed.Port()
	if port != "" {
		number, err := strconv.Atoi(port)
		if err != nil || number < 1 || number > 65535 {
			return "", errors.New("port must be between 1 and 65535")
		}
		if (parsed.Scheme == "https" && number == 443) || (parsed.Scheme == "http" && number == 80) {
			port = ""
		}
	}
	if port != "" {
		return net.JoinHostPort(hostname, port), nil
	}
	if strings.Contains(hostname, ":") {
		return "[" + hostname + "]", nil
	}
	return hostname, nil
}

func isLoopbackHostname(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func validateTrustedProxyCIDRs(web *WebConfig) error {
	seen := make(map[string]struct{}, len(web.TrustedProxyCIDRs))
	normalized := make([]string, 0, len(web.TrustedProxyCIDRs))
	for _, raw := range web.TrustedProxyCIDRs {
		value := strings.TrimSpace(raw)
		if value == "" {
			continue
		}
		_, network, err := net.ParseCIDR(value)
		if err != nil {
			return fmt.Errorf("web.trusted_proxy_cidrs contains invalid CIDR %q", value)
		}
		canonical := network.String()
		if _, exists := seen[canonical]; exists {
			continue
		}
		seen[canonical] = struct{}{}
		normalized = append(normalized, canonical)
	}
	web.TrustedProxyCIDRs = normalized
	return nil
}

func resolveConfiguredSecrets(cfg *Config) error {
	var err error
	if cfg.resolvedSecrets.settingsAdminToken, err = resolveSecret(
		"settings.admin_token", cfg.Settings.AdminToken, cfg.Settings.AdminTokenFile,
	); err != nil {
		return err
	}
	if cfg.resolvedSecrets.settingsEncryptionKey, err = resolveSecret(
		"settings.encryption_key", cfg.Settings.EncryptionKey, cfg.Settings.EncryptionKeyFile,
	); err != nil {
		return err
	}
	if cfg.resolvedSecrets.authRootKey, err = resolveSecret(
		"auth.root_key", cfg.Auth.RootKey, cfg.Auth.RootKeyFile,
	); err != nil {
		return err
	}
	if cfg.resolvedSecrets.oidcClientSecret, err = resolveSecret(
		"auth.oidc.client_secret", cfg.Auth.OIDC.ClientSecret, cfg.Auth.OIDC.ClientSecretFile,
	); err != nil {
		return err
	}
	if cfg.resolvedSecrets.bootstrapToken, err = resolveSecret(
		"auth.bootstrap.token", cfg.Auth.Bootstrap.Token, cfg.Auth.Bootstrap.TokenFile,
	); err != nil {
		return err
	}
	if cfg.resolvedSecrets.legacyAdminToken, err = resolveSecret(
		"auth.legacy_admin_token.token", cfg.Auth.LegacyAdminToken.Token, cfg.Auth.LegacyAdminToken.TokenFile,
	); err != nil {
		return err
	}
	return nil
}

func resolveSecret(name, direct, file string) (string, error) {
	file = strings.TrimSpace(file)
	if direct != "" && file != "" {
		return "", fmt.Errorf("%s and %s_file are mutually exclusive", name, name)
	}
	if file == "" {
		if len(direct) > maxSecretFileBytes {
			return "", fmt.Errorf("%s exceeds %d bytes", name, maxSecretFileBytes)
		}
		return direct, nil
	}
	secret, err := readSecretFile(file)
	if err != nil {
		return "", fmt.Errorf("read %s_file %q: %w", name, filepath.Clean(file), err)
	}
	if secret == "" {
		return "", fmt.Errorf("%s_file must not be empty", name)
	}
	return secret, nil
}

func readSecretFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxSecretFileBytes+1))
	if err != nil {
		return "", err
	}
	if len(data) > maxSecretFileBytes {
		return "", fmt.Errorf("secret file exceeds %d bytes", maxSecretFileBytes)
	}
	data = []byte(strings.TrimSuffix(strings.TrimSuffix(string(data), "\n"), "\r"))
	return string(data), nil
}

func validateAuthConfig(cfg *Config) error {
	authEnabled := cfg.Auth.OIDC.Enabled || cfg.Auth.LocalLogin.Enabled || cfg.Auth.Bootstrap.Enabled
	if authEnabled {
		if cfg.Web.PublicURL == "" {
			return errors.New("web.public_url is required when browser authentication is enabled")
		}
		if len(cfg.resolvedSecrets.authRootKey) < 32 {
			return errors.New("auth.root_key must contain at least 32 bytes when browser authentication is enabled")
		}
	}

	idle, err := parseBoundedDuration("auth.session.idle_timeout", cfg.Auth.Session.IdleTimeout, 24*time.Hour)
	if err != nil {
		return err
	}
	absolute, err := parseBoundedDuration("auth.session.absolute_timeout", cfg.Auth.Session.AbsoluteTimeout, 30*24*time.Hour)
	if err != nil {
		return err
	}
	touch, err := parseBoundedDuration("auth.session.touch_interval", cfg.Auth.Session.TouchInterval, time.Hour)
	if err != nil {
		return err
	}
	if idle > absolute {
		return errors.New("auth.session.idle_timeout must not exceed auth.session.absolute_timeout")
	}
	if touch >= idle {
		return errors.New("auth.session.touch_interval must be shorter than auth.session.idle_timeout")
	}

	if cfg.Auth.OIDC.Enabled {
		if err := validateOIDCConfig(cfg); err != nil {
			return err
		}
		if !cfg.Auth.OIDC.AutoProvision {
			return errors.New("auth.oidc.auto_provision must be true until identity pre-provisioning is supported")
		}
	}
	if cfg.Auth.Bootstrap.Enabled {
		if !cfg.Auth.LocalLogin.Enabled {
			return errors.New("auth.local_login.enabled must be true when bootstrap is enabled")
		}
		if len(cfg.resolvedSecrets.bootstrapToken) < 32 {
			return errors.New("auth.bootstrap.token must contain at least 32 bytes")
		}
	}
	if cfg.Auth.LegacyAdminToken.Enabled {
		legacyToken := cfg.resolvedSecrets.legacyAdminToken
		if legacyToken == "" {
			legacyToken = cfg.resolvedSecrets.settingsAdminToken
		}
		if len(legacyToken) < 32 {
			return errors.New("auth.legacy_admin_token.token must contain at least 32 bytes when enabled")
		}
		if value := strings.TrimSpace(cfg.Auth.LegacyAdminToken.SunsetAt); value != "" {
			if _, err := time.Parse(time.RFC3339, value); err != nil {
				return errors.New("auth.legacy_admin_token.sunset_at must use RFC3339")
			}
		}
	}
	return nil
}

func validateOIDCConfig(cfg *Config) error {
	issuer := strings.TrimSpace(cfg.Auth.OIDC.IssuerURL)
	parsed, err := url.Parse(issuer)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return errors.New("auth.oidc.issuer_url must be an absolute HTTP or HTTPS URL")
	}
	if parsed.Scheme != "https" && !(parsed.Scheme == "http" && isLoopbackHostname(parsed.Hostname())) {
		return errors.New("auth.oidc.issuer_url must use HTTPS except for loopback development")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("auth.oidc.issuer_url must not contain credentials, query, or fragment")
	}
	cfg.Auth.OIDC.IssuerURL = issuer
	cfg.Auth.OIDC.ClientID = strings.TrimSpace(cfg.Auth.OIDC.ClientID)
	if cfg.Auth.OIDC.ClientID == "" {
		return errors.New("auth.oidc.client_id is required when OIDC is enabled")
	}
	if cfg.resolvedSecrets.oidcClientSecret == "" {
		return errors.New("auth.oidc.client_secret is required when OIDC is enabled")
	}
	flowTTL, err := parseBoundedDuration("auth.oidc.flow_ttl", cfg.Auth.OIDC.FlowTTL, 30*time.Minute)
	if err != nil {
		return err
	}
	if flowTTL < time.Minute {
		return errors.New("auth.oidc.flow_ttl must be at least 1m")
	}
	if _, err := parseBoundedDuration("auth.oidc.http_timeout", cfg.Auth.OIDC.HTTPTimeout, time.Minute); err != nil {
		return err
	}
	if _, err := parseBoundedDuration("auth.oidc.max_clock_skew", cfg.Auth.OIDC.MaxClockSkew, 5*time.Minute); err != nil {
		return err
	}
	normalizedScopes := make([]string, 0, len(cfg.Auth.OIDC.Scopes))
	seenScopes := make(map[string]struct{}, len(cfg.Auth.OIDC.Scopes))
	for _, scope := range cfg.Auth.OIDC.Scopes {
		scope = strings.TrimSpace(scope)
		if scope == "" {
			return errors.New("auth.oidc.scopes must not contain empty values")
		}
		if _, exists := seenScopes[scope]; exists {
			return errors.New("auth.oidc.scopes must not contain duplicates")
		}
		seenScopes[scope] = struct{}{}
		normalizedScopes = append(normalizedScopes, scope)
	}
	cfg.Auth.OIDC.Scopes = normalizedScopes
	if !slices.Contains(cfg.Auth.OIDC.Scopes, "openid") {
		return errors.New("auth.oidc.scopes must include openid")
	}
	allowedAlgorithms := map[string]struct{}{
		"RS256": {}, "RS384": {}, "RS512": {},
		"ES256": {}, "ES384": {}, "ES512": {},
		"EdDSA": {},
	}
	seen := make(map[string]struct{}, len(cfg.Auth.OIDC.AllowedSigningAlgorithms))
	normalizedAlgorithms := make([]string, 0, len(cfg.Auth.OIDC.AllowedSigningAlgorithms))
	for _, algorithm := range cfg.Auth.OIDC.AllowedSigningAlgorithms {
		algorithm = strings.TrimSpace(algorithm)
		if _, allowed := allowedAlgorithms[algorithm]; !allowed {
			return errors.New("auth.oidc.allowed_signing_algorithms contains an unsupported algorithm")
		}
		if _, exists := seen[algorithm]; exists {
			return errors.New("auth.oidc.allowed_signing_algorithms must not contain duplicates")
		}
		seen[algorithm] = struct{}{}
		normalizedAlgorithms = append(normalizedAlgorithms, algorithm)
	}
	if len(seen) == 0 {
		return errors.New("auth.oidc.allowed_signing_algorithms must not be empty")
	}
	cfg.Auth.OIDC.AllowedSigningAlgorithms = normalizedAlgorithms
	return nil
}

func parseBoundedDuration(name, value string, maximum time.Duration) (time.Duration, error) {
	parsed, err := time.ParseDuration(strings.TrimSpace(value))
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("invalid positive duration for %s: %q", name, value)
	}
	if parsed > maximum {
		return 0, fmt.Errorf("%s must not exceed %s", name, maximum)
	}
	return parsed, nil
}

func mustDuration(value string, fallback time.Duration) time.Duration {
	parsed, err := time.ParseDuration(strings.TrimSpace(value))
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func WebReadHeaderTimeout() time.Duration {
	if Main == nil {
		return defaultWebReadHeaderTimeout
	}
	return mustDuration(Main.Web.ReadHeaderTimeout, defaultWebReadHeaderTimeout)
}

func WebReadTimeout() time.Duration {
	if Main == nil {
		return defaultWebReadTimeout
	}
	return mustDuration(Main.Web.ReadTimeout, defaultWebReadTimeout)
}

func WebWriteTimeout() time.Duration {
	if Main == nil {
		return defaultWebWriteTimeout
	}
	return mustDuration(Main.Web.WriteTimeout, defaultWebWriteTimeout)
}

func WebIdleTimeout() time.Duration {
	if Main == nil {
		return defaultWebIdleTimeout
	}
	return mustDuration(Main.Web.IdleTimeout, defaultWebIdleTimeout)
}

func WebMaxHeaderBytes() int {
	if Main == nil || Main.Web.MaxHeaderBytes <= 0 {
		return defaultWebMaxHeaderBytes
	}
	return Main.Web.MaxHeaderBytes
}

func WebMaxJSONBodyBytes() int64 {
	if Main == nil || Main.Web.MaxJSONBodyBytes <= 0 {
		return defaultWebMaxJSONBodyBytes
	}
	return Main.Web.MaxJSONBodyBytes
}

func WebTrustedProxyCIDRs() []string {
	if Main == nil {
		return nil
	}
	return slices.Clone(Main.Web.TrustedProxyCIDRs)
}

func SSEHeartbeatInterval() time.Duration {
	if Main == nil {
		return defaultSSEHeartbeatInterval
	}
	return mustDuration(Main.Web.SSE.HeartbeatInterval, defaultSSEHeartbeatInterval)
}

func SSEReauthInterval() time.Duration {
	if Main == nil {
		return defaultSSEReauthInterval
	}
	return mustDuration(Main.Web.SSE.ReauthInterval, defaultSSEReauthInterval)
}

func SSEWriteTimeout() time.Duration {
	if Main == nil {
		return defaultSSEWriteTimeout
	}
	return mustDuration(Main.Web.SSE.WriteTimeout, defaultSSEWriteTimeout)
}

func SSEIdleTimeout() time.Duration {
	if Main == nil {
		return defaultSSEIdleTimeout
	}
	return mustDuration(Main.Web.SSE.IdleTimeout, defaultSSEIdleTimeout)
}

func SSEMaxDuration() time.Duration {
	if Main == nil {
		return defaultSSEMaxDuration
	}
	return mustDuration(Main.Web.SSE.MaxDuration, defaultSSEMaxDuration)
}

func OIDCFlowTTL() time.Duration {
	if Main == nil {
		return defaultOIDCFlowTTL
	}
	return mustDuration(Main.Auth.OIDC.FlowTTL, defaultOIDCFlowTTL)
}

func OIDCHTTPTimeout() time.Duration {
	if Main == nil {
		return defaultOIDCHTTPTimeout
	}
	return mustDuration(Main.Auth.OIDC.HTTPTimeout, defaultOIDCHTTPTimeout)
}

func OIDCMaxClockSkew() time.Duration {
	if Main == nil {
		return defaultOIDCMaxClockSkew
	}
	return mustDuration(Main.Auth.OIDC.MaxClockSkew, defaultOIDCMaxClockSkew)
}

func WebPublicURL() string {
	if Main == nil {
		return ""
	}
	return Main.Web.PublicURL
}

func AuthCookieSecure() bool {
	if Main == nil {
		return false
	}
	parsed, err := url.Parse(Main.Web.PublicURL)
	return err == nil && parsed.Scheme == "https"
}

func OIDCRedirectURL() string {
	if Main == nil || Main.Web.PublicURL == "" {
		return ""
	}
	return strings.TrimRight(Main.Web.PublicURL, "/") + "/api/v1/auth/oidc/callback"
}

func SessionIdleTimeout() time.Duration {
	if Main == nil {
		return defaultSessionIdleTimeout
	}
	return mustDuration(Main.Auth.Session.IdleTimeout, defaultSessionIdleTimeout)
}

func SessionAbsoluteTimeout() time.Duration {
	if Main == nil {
		return defaultSessionAbsolute
	}
	return mustDuration(Main.Auth.Session.AbsoluteTimeout, defaultSessionAbsolute)
}

func SessionTouchInterval() time.Duration {
	if Main == nil {
		return defaultSessionTouchInterval
	}
	return mustDuration(Main.Auth.Session.TouchInterval, defaultSessionTouchInterval)
}

func AuthRootKey() string {
	if Main == nil {
		return ""
	}
	if Main.resolvedSecrets.authRootKey != "" {
		return Main.resolvedSecrets.authRootKey
	}
	return Main.Auth.RootKey
}

func OIDCClientSecret() string {
	if Main == nil {
		return ""
	}
	if Main.resolvedSecrets.oidcClientSecret != "" {
		return Main.resolvedSecrets.oidcClientSecret
	}
	return Main.Auth.OIDC.ClientSecret
}

func BootstrapToken() string {
	if Main == nil {
		return ""
	}
	if Main.resolvedSecrets.bootstrapToken != "" {
		return Main.resolvedSecrets.bootstrapToken
	}
	return Main.Auth.Bootstrap.Token
}

func LegacyAdminToken() string {
	if Main == nil {
		return ""
	}
	if Main.resolvedSecrets.legacyAdminToken != "" {
		return Main.resolvedSecrets.legacyAdminToken
	}
	if Main.Auth.LegacyAdminToken.Token != "" {
		return Main.Auth.LegacyAdminToken.Token
	}
	return SettingsAdminToken()
}
