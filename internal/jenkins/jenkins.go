package jenkins

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/bndr/gojenkins"
)

const defaultTimeout = 15 * time.Second

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

var activeRuntime atomic.Pointer[Runtime]

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
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

	// gojenkins v1.1.0 accepts a context but does not attach it to the HTTP
	// request. Inject it for the connection probe, then switch the retained
	// runtime client back to a normal timeout-bound transport.
	probeClient := &http.Client{
		Timeout: cfg.Timeout,
		Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
			return http.DefaultTransport.RoundTrip(request.Clone(ctx))
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
	client.Requester.Client = &http.Client{Timeout: cfg.Timeout}
	return &Runtime{Client: client, Config: cfg}, nil
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
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("jenkins address must not contain a query or fragment")
	}
	return address, nil
}

func Activate(runtime *Runtime) {
	activeRuntime.Store(runtime)
}

func Disable() {
	activeRuntime.Store(nil)
}

func Current() *Runtime {
	return activeRuntime.Load()
}

func IsConfigured() bool {
	return Current() != nil
}
