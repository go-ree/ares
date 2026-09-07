package webserver

import (
	"context"
	"sync"
	"time"
)

const (
	readinessSuccessTTL = time.Second
	readinessFailureTTL = 250 * time.Millisecond
)

// readinessProbe coalesces concurrent readiness checks and briefly caches the
// result. This keeps a public health endpoint from turning an anonymous HTTP
// flood into an equally large stream of database pings.
type readinessProbe struct {
	mu      sync.Mutex
	check   func(context.Context) error
	ready   bool
	until   time.Time
	loading chan struct{}
	now     func() time.Time
}

func newReadinessProbe(check func(context.Context) error) *readinessProbe {
	return &readinessProbe{check: check, now: time.Now}
}

func (p *readinessProbe) status(ctx context.Context) bool {
	for {
		p.mu.Lock()
		if p.now().Before(p.until) {
			ready := p.ready
			p.mu.Unlock()
			return ready
		}
		if p.loading != nil {
			done := p.loading
			p.mu.Unlock()
			select {
			case <-done:
				continue
			case <-ctx.Done():
				return false
			}
		}
		done := make(chan struct{})
		p.loading = done
		p.mu.Unlock()

		// A canceled public request must not poison readiness for other probes.
		// The database operation has its own strict deadline and only one can be
		// in flight for this process.
		checkContext, cancel := context.WithTimeout(context.Background(), time.Second)
		err := p.check(checkContext)
		cancel()

		p.mu.Lock()
		p.ready = err == nil
		ttl := readinessFailureTTL
		if p.ready {
			ttl = readinessSuccessTTL
		}
		p.until = p.now().Add(ttl)
		p.loading = nil
		close(done)
		ready := p.ready
		p.mu.Unlock()
		return ready
	}
}
