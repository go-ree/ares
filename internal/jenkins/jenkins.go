package jenkins

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bndr/gojenkins"
	"github.com/go-ree/ares/internal/upstreamhttp"
)

const (
	defaultTimeout                   = 15 * time.Second
	maxJenkinsJSONResponseBytes      = 1 << 20
	jenkinsProgressiveTextPathSuffix = "/logText/progressiveText"
)

// RuntimeConfig is the validated Jenkins configuration used by one immutable
// runtime snapshot. It is populated from database-backed Web settings.
type RuntimeConfig struct {
	Address  string
	Username string
	Token    string
	Timeout  time.Duration
}

// Runtime keeps a client and the exact credentials used to construct it
// together, so an in-flight request never observes half of a hot reload.
type Runtime struct {
	Client *gojenkins.Jenkins
	Config RuntimeConfig
}

// ClientSnapshot pins one immutable runtime for a complete executor operation.
// Hot-reloading Jenkins swaps the global pointer, but an already acquired
// snapshot never mixes the old trigger with a new server address/status call.
type ClientSnapshot struct {
	runtime *Runtime
}

var activeRuntime atomic.Pointer[Runtime]
var runtimeGate sync.RWMutex

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func rejectRedirect(*http.Request, []*http.Request) error {
	return errors.New("Jenkins redirects are not allowed")
}

type jenkinsResponseLimitTransport struct {
	base    http.RoundTripper
	limited http.RoundTripper
}

func limitJenkinsResponses(base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return &jenkinsResponseLimitTransport{
		base: base, limited: upstreamhttp.LimitResponses(base, maxJenkinsJSONResponseBytes),
	}
}

func (transport *jenkinsResponseLimitTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	// progressiveText is intentionally read in 256 KiB pieces by
	// getProgressiveText. Jenkins commonly advertises a body larger than one
	// piece, so the generic Content-Length precheck must not change that cursor
	// protocol's existing bounded-prefix behavior.
	if request != nil && request.URL != nil && strings.HasSuffix(request.URL.Path, jenkinsProgressiveTextPathSuffix) {
		return transport.base.RoundTrip(request)
	}
	return transport.limited.RoundTrip(request)
}

func BuildRuntime(ctx context.Context, cfg RuntimeConfig) (*Runtime, error) {
	var err error
	cfg.Address, err = NormalizeAddress(cfg.Address)
	if err != nil {
		return nil, err
	}
	cfg.Username = strings.TrimSpace(cfg.Username)
	if cfg.Timeout <= 0 {
		cfg.Timeout = defaultTimeout
	}
	if ctx == nil {
		ctx = context.Background()
	}
	baseTransport := http.DefaultTransport
	limitedTransport := limitJenkinsResponses(baseTransport)

	// gojenkins v1.1.0 accepts a context but does not attach it to the HTTP
	// request. Inject it for the connection probe, then switch the retained
	// runtime client back to a normal timeout-bound transport.
	probeClient := &http.Client{
		Timeout:       cfg.Timeout,
		CheckRedirect: rejectRedirect,
		Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
			return limitedTransport.RoundTrip(request.Clone(ctx))
		}),
	}
	client := gojenkins.CreateJenkins(
		probeClient,
		cfg.Address,
		cfg.Username,
		cfg.Token,
	)
	if _, err := client.Init(ctx); err != nil {
		return nil, fmt.Errorf("connect to Jenkins: %w", err)
	}
	if err := verifyJenkinsProbe(ctx, probeClient, cfg); err != nil {
		return nil, fmt.Errorf("connect to Jenkins: %w", err)
	}
	client.Requester.Client = &http.Client{
		Timeout: cfg.Timeout, CheckRedirect: rejectRedirect,
		Transport: limitJenkinsResponses(baseTransport),
	}
	return &Runtime{Client: client, Config: cfg}, nil
}

func verifyJenkinsProbe(ctx context.Context, client *http.Client, cfg RuntimeConfig) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(cfg.Address, "/")+"/api/json", nil)
	if err != nil {
		return errors.New("create Jenkins verification request")
	}
	request.SetBasicAuth(cfg.Username, cfg.Token)
	request.Header.Set("Accept", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("Jenkins verification failed with HTTP status %d", response.StatusCode)
	}
	decoder := json.NewDecoder(response.Body)
	var payload map[string]json.RawMessage
	if err := decoder.Decode(&payload); err != nil {
		if errors.Is(err, upstreamhttp.ErrResponseTooLarge) {
			return upstreamhttp.ErrResponseTooLarge
		}
		return errors.New("Jenkins verification returned invalid JSON")
	}
	if payload == nil {
		return errors.New("Jenkins verification returned invalid JSON")
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if errors.Is(err, upstreamhttp.ErrResponseTooLarge) {
			return upstreamhttp.ErrResponseTooLarge
		}
		return errors.New("Jenkins verification returned invalid trailing data")
	}
	return nil
}

// NormalizeAddress validates a Jenkins base URL without contacting it.
func NormalizeAddress(address string) (string, error) {
	address = strings.TrimRight(strings.TrimSpace(address), "/")
	parsed, err := url.Parse(address)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return "", fmt.Errorf("jenkins address must be a valid HTTP or HTTPS URL")
	}
	if parsed.User != nil {
		return "", fmt.Errorf("jenkins address must not contain embedded credentials")
	}
	if parsed.Scheme == "http" && !isLoopbackHostname(parsed.Hostname()) {
		return "", fmt.Errorf("jenkins address must use HTTPS unless it targets loopback development")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("jenkins address must not contain a query or fragment")
	}
	return address, nil
}

func isLoopbackHostname(hostname string) bool {
	if strings.EqualFold(strings.TrimSuffix(hostname, "."), "localhost") {
		return true
	}
	address, err := netip.ParseAddr(hostname)
	return err == nil && address.IsLoopback()
}

func Activate(runtime *Runtime) {
	runtimeGate.Lock()
	defer runtimeGate.Unlock()
	activeRuntime.Store(runtime)
}

func Disable() {
	runtimeGate.Lock()
	defer runtimeGate.Unlock()
	activeRuntime.Store(nil)
}

// CommitRuntime serializes a configuration commit against executor operations.
// The callback runs while new Start/Reconcile calls are paused; callers may
// perform a final database guard and only return the runtime after persistence
// succeeds. A nil runtime disables Jenkins.
func CommitRuntime(commit func() (*Runtime, error)) error {
	runtimeGate.Lock()
	defer runtimeGate.Unlock()
	next, err := commit()
	if err != nil {
		return err
	}
	activeRuntime.Store(next)
	return nil
}

func Current() *Runtime {
	return activeRuntime.Load()
}

func Acquire() *ClientSnapshot {
	runtimeGate.RLock()
	defer runtimeGate.RUnlock()
	runtime := Current()
	if runtime == nil {
		return nil
	}
	return &ClientSnapshot{runtime: runtime}
}

// AcquireForOperation pins the active runtime and prevents an address switch
// until the returned release function is called. Callers must always defer it.
func AcquireForOperation() (*ClientSnapshot, func()) {
	runtimeGate.RLock()
	runtime := Current()
	if runtime == nil {
		runtimeGate.RUnlock()
		return nil, func() {}
	}
	return &ClientSnapshot{runtime: runtime}, runtimeGate.RUnlock
}

func (s *ClientSnapshot) Address() string {
	if s == nil || s.runtime == nil {
		return ""
	}
	return s.runtime.Config.Address
}

// clientForContext creates a lightweight gojenkins client whose transport
// binds every request to ctx. gojenkins v1.1.0 accepts context parameters but
// builds requests with http.NewRequest, so the transport is the only reliable
// place to propagate cancellation without mutating the shared hot-reload
// runtime client.
func clientForContext(runtime *Runtime, ctx context.Context) *gojenkins.Jenkins {
	if ctx == nil {
		ctx = context.Background()
	}
	baseClient := runtime.Client.Requester.Client
	client := *baseClient
	transport := baseClient.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	client.Transport = roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		return transport.RoundTrip(request.Clone(ctx))
	})
	return gojenkins.CreateJenkins(
		&client,
		runtime.Config.Address,
		runtime.Config.Username,
		runtime.Config.Token,
	)
}

func IsConfigured() bool {
	return Current() != nil
}
