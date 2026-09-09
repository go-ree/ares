package job

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

const (
	defaultLegacyPollInterval = 10 * time.Second
	defaultDrainTimeout       = 20 * time.Second
	maximumCleanupAllowance   = time.Second
)

var (
	ErrAlreadyStarted = errors.New("background job manager already started")
	ErrNotStarted     = errors.New("background job manager is not started")
	ErrDrainTimeout   = errors.New("background job drain timed out")
)

// Runner is implemented by the v2 workflow worker and the integration
// revision synchronizer. Run must stop accepting new work when ctx is canceled
// and return after its own in-flight cleanup is complete.
type Runner interface {
	Run(context.Context) error
}

type RunnerFunc func(context.Context) error

func (fn RunnerFunc) Run(ctx context.Context) error {
	if fn == nil {
		return errors.New("background runner function is nil")
	}
	return fn(ctx)
}

// LegacyRunner performs one leader-gated v1 drain round. Manager owns the
// context-aware polling interval so the compatibility state machine is never
// launched from a context-free cron callback.
type LegacyRunner interface {
	RunOnce(context.Context) error
}

type Options struct {
	LegacyPollInterval time.Duration
	DrainTimeout       time.Duration
}

type namedRunner struct {
	name   string
	runner Runner
}

// managedContext keeps the shutdown deadline anchored to the instant Manager
// begins stopping. Runners may opt into this additional method without
// coupling the generic job package to a concrete worker implementation.
type managedContext struct {
	context.Context
	manager *Manager
}

func (c managedContext) ShutdownDeadline() (time.Time, bool) {
	if c.manager == nil {
		return time.Time{}, false
	}
	c.manager.mu.Lock()
	defer c.manager.mu.Unlock()
	if c.manager.stoppedAt.IsZero() {
		return time.Time{}, false
	}
	return c.manager.stoppedAt.Add(c.manager.drainTimeout), true
}

// Manager owns all process background loops and their shutdown boundary.
// Manager is intentionally single-use.
type Manager struct {
	runners      []namedRunner
	legacy       LegacyRunner
	legacyPoll   time.Duration
	drainTimeout time.Duration

	mu        sync.Mutex
	started   bool
	cancel    context.CancelFunc
	stopOnce  sync.Once
	stoppedAt time.Time
	stopping  chan struct{}
	done      chan struct{}
	wait      sync.WaitGroup
	errMu     sync.Mutex
	err       error
}

func NewManager(v2 Runner, integrationSync Runner, legacy LegacyRunner, options Options) (*Manager, error) {
	if options.LegacyPollInterval == 0 {
		options.LegacyPollInterval = defaultLegacyPollInterval
	}
	if options.DrainTimeout == 0 {
		options.DrainTimeout = defaultDrainTimeout
	}
	if options.LegacyPollInterval < 0 {
		return nil, errors.New("legacy poll interval must be positive")
	}
	if options.DrainTimeout < 0 {
		return nil, errors.New("drain timeout must be positive")
	}
	manager := &Manager{
		legacy: legacy, legacyPoll: options.LegacyPollInterval, drainTimeout: options.DrainTimeout,
		stopping: make(chan struct{}), done: make(chan struct{}),
	}
	if v2 != nil {
		manager.runners = append(manager.runners, namedRunner{name: "workflow", runner: v2})
	}
	if integrationSync != nil {
		manager.runners = append(manager.runners, namedRunner{name: "integration_sync", runner: integrationSync})
	}
	return manager, nil
}

// Run is a convenience for callers that do not need to start the web server
// between background startup and shutdown waiting.
func (m *Manager) Run(ctx context.Context) error {
	if err := m.Start(ctx); err != nil {
		return err
	}
	return m.Wait()
}

func (m *Manager) Start(parent context.Context) error {
	if m == nil {
		return ErrNotStarted
	}
	if parent == nil {
		return errors.New("background job context is nil")
	}
	m.mu.Lock()
	if m.started {
		m.mu.Unlock()
		return ErrAlreadyStarted
	}
	// Parent cancellation is observed by the watcher below so markStopping can
	// publish one absolute shutdown deadline before runners see cancellation.
	// WithoutCancel retains request-scoped values while Manager owns Done.
	baseCtx, cancel := context.WithCancel(context.WithoutCancel(parent))
	ctx := managedContext{Context: baseCtx, manager: m}
	m.started = true
	m.cancel = cancel
	m.wait.Add(len(m.runners))
	if m.legacy != nil {
		m.wait.Add(1)
	}
	m.mu.Unlock()

	go func() {
		select {
		case <-parent.Done():
			m.Stop()
		case <-ctx.Done():
			m.markStopping()
		}
		m.wait.Wait()
		close(m.done)
	}()
	// Do not rely on the watcher being scheduled before runners when Start is
	// handed an already-cancelled parent.
	if parent.Err() != nil {
		m.Stop()
	}
	for _, component := range m.runners {
		go m.runComponent(ctx, component)
	}
	if m.legacy != nil {
		go m.runLegacy(ctx)
	}
	return nil
}

// Stop is idempotent. It cancels the service context, which tells workers to
// stop acquiring; a v2 Worker may keep its already acquired task contexts alive
// while it performs its own graceful drain.
func (m *Manager) Stop() {
	if m == nil {
		return
	}
	m.mu.Lock()
	cancel := m.cancel
	started := m.started
	m.mu.Unlock()
	if !started {
		return
	}
	m.markStopping()
	cancel()
}

func (m *Manager) Done() <-chan struct{} {
	if m == nil {
		closed := make(chan struct{})
		close(closed)
		return closed
	}
	return m.done
}

// Stopping closes as soon as shutdown begins, before in-flight runners have
// drained. A server composition can use it to stop accepting HTTP requests
// when a critical background runner exits unexpectedly.
func (m *Manager) Stopping() <-chan struct{} {
	if m == nil {
		closed := make(chan struct{})
		close(closed)
		return closed
	}
	return m.stopping
}

// Wait waits for a stop signal and then applies the configured upper bound.
// A timed-out goroutine is not allowed to hold process shutdown indefinitely;
// the database lease/fencing boundary rejects any late writes after takeover.
func (m *Manager) Wait() error {
	if m == nil {
		return ErrNotStarted
	}
	m.mu.Lock()
	started := m.started
	m.mu.Unlock()
	if !started {
		return ErrNotStarted
	}

	if done, result := m.doneResult(); done {
		return result
	}
	<-m.stopping
	// done and stopping may both already be closed when Wait is called late.
	// Recheck done outside a competing select so a completed drain always wins.
	if done, result := m.doneResult(); done {
		return result
	}
	m.mu.Lock()
	stoppedAt := m.stoppedAt
	m.mu.Unlock()
	remaining := time.Until(stoppedAt.Add(m.drainTimeout + cleanupAllowance(m.drainTimeout)))
	if remaining <= 0 {
		if done, result := m.doneResult(); done {
			return result
		}
		return m.drainTimeoutResult()
	}
	timer := time.NewTimer(remaining)
	defer timer.Stop()
	select {
	case <-m.done:
		return m.result()
	case <-timer.C:
		if done, result := m.doneResult(); done {
			return result
		}
		return m.drainTimeoutResult()
	}
}

func (m *Manager) doneResult() (bool, error) {
	select {
	case <-m.done:
		return true, m.result()
	default:
		return false, nil
	}
}

func (m *Manager) drainTimeoutResult() error {
	timeoutErr := fmt.Errorf("%w after %s", ErrDrainTimeout, m.drainTimeout)
	return errors.Join(m.result(), timeoutErr)
}

func cleanupAllowance(drainTimeout time.Duration) time.Duration {
	allowance := drainTimeout / 10
	if allowance > maximumCleanupAllowance {
		return maximumCleanupAllowance
	}
	return allowance
}

func (m *Manager) runComponent(ctx context.Context, component namedRunner) {
	defer m.wait.Done()
	err := component.runner.Run(ctx)
	if ctx.Err() != nil {
		if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			wrapped := fmt.Errorf("%s runner during drain: %w", component.name, err)
			m.recordError(wrapped)
			slog.Warn("后台组件在服务排空期间退出", "component", component.name, "error_type", fmt.Sprintf("%T", err))
		}
		return
	}
	if err == nil {
		err = errors.New("runner stopped before service shutdown")
	}
	m.recordError(fmt.Errorf("%s runner: %w", component.name, err))
	m.Stop()
}

func (m *Manager) runLegacy(ctx context.Context) {
	defer m.wait.Done()
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		if err := m.legacy.RunOnce(ctx); err != nil && ctx.Err() == nil {
			slog.Warn("旧版任务排空轮次失败，稍后重试", "error_type", fmt.Sprintf("%T", err))
		}
		timer.Reset(m.legacyPoll)
	}
}

func (m *Manager) markStopping() {
	m.stopOnce.Do(func() {
		m.mu.Lock()
		m.stoppedAt = time.Now()
		m.mu.Unlock()
		close(m.stopping)
	})
}

func (m *Manager) recordError(err error) {
	if err == nil {
		return
	}
	m.errMu.Lock()
	m.err = errors.Join(m.err, err)
	m.errMu.Unlock()
}

func (m *Manager) result() error {
	m.errMu.Lock()
	defer m.errMu.Unlock()
	return m.err
}
