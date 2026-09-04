package controller

import (
	"hash/maphash"
	"net"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/time/rate"
)

const (
	publicAuthClientLimitEntries = 4096
	publicAuthClientLimitIdle    = 15 * time.Minute
	publicAuthOverflowBuckets    = 64
)

type clientLimitEntry struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// keyedRateLimiter isolates anonymous authentication budgets by the client IP
// resolved by Gin's configured trusted-proxy policy. Its bounded table prevents
// a rotating source-address flood from growing process memory without limit.
type keyedRateLimiter struct {
	mu         sync.Mutex
	limit      rate.Limit
	burst      int
	maxEntries int
	idle       time.Duration
	entries    map[string]*clientLimitEntry
	overflow   []*rate.Limiter
	hashSeed   maphash.Seed
	nextSweep  time.Time
	now        func() time.Time
}

func newKeyedRateLimiter(limit rate.Limit, burst, maxEntries int, idle time.Duration) *keyedRateLimiter {
	limiter := &keyedRateLimiter{
		limit: limit, burst: burst, maxEntries: maxEntries, idle: idle,
		entries: make(map[string]*clientLimitEntry), hashSeed: maphash.MakeSeed(), now: time.Now,
	}
	limiter.overflow = make([]*rate.Limiter, publicAuthOverflowBuckets)
	for index := range limiter.overflow {
		limiter.overflow[index] = rate.NewLimiter(limit, burst)
	}
	return limiter
}

func (l *keyedRateLimiter) allow(key string) bool {
	if l == nil {
		return true
	}
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	if entry := l.entries[key]; entry != nil {
		entry.lastSeen = now
		return entry.limiter.AllowN(now, 1)
	}
	if len(l.entries) >= l.maxEntries && (l.nextSweep.IsZero() || !now.Before(l.nextSweep)) {
		l.removeExpired(now)
		sweepInterval := l.idle / 4
		if sweepInterval < time.Second {
			sweepInterval = time.Second
		}
		if sweepInterval > time.Minute {
			sweepInterval = time.Minute
		}
		l.nextSweep = now.Add(sweepInterval)
	}
	if len(l.entries) >= l.maxEntries {
		return l.allowOverflow(key, now)
	}
	entry := &clientLimitEntry{limiter: rate.NewLimiter(l.limit, l.burst), lastSeen: now}
	l.entries[key] = entry
	return entry.limiter.AllowN(now, 1)
}

func (l *keyedRateLimiter) allowOverflow(key string, now time.Time) bool {
	if len(l.overflow) == 0 {
		return false
	}
	index := maphash.String(l.hashSeed, key) % uint64(len(l.overflow))
	return l.overflow[index].AllowN(now, 1)
}

func (l *keyedRateLimiter) removeExpired(now time.Time) {
	for key, entry := range l.entries {
		if now.Sub(entry.lastSeen) >= l.idle {
			delete(l.entries, key)
		}
	}
}

// concurrentAdmission applies both a process-wide ceiling and a per-principal
// ceiling. acquire is non-blocking so an exhausted endpoint cannot accumulate
// waiting goroutines or pin HTTP connections indefinitely.
type concurrentAdmission struct {
	mu            sync.Mutex
	maximumTotal  int
	maximumPerKey int
	activeTotal   int
	activeByKey   map[string]int
}

func newConcurrentAdmission(maximumTotal, maximumPerKey int) *concurrentAdmission {
	return &concurrentAdmission{
		maximumTotal: maximumTotal, maximumPerKey: maximumPerKey,
		activeByKey: make(map[string]int),
	}
}

func (a *concurrentAdmission) acquire(key string) (func(), bool) {
	if a == nil {
		return func() {}, true
	}
	a.mu.Lock()
	if key == "" || a.maximumTotal <= 0 || a.maximumPerKey <= 0 ||
		a.activeTotal >= a.maximumTotal || a.activeByKey[key] >= a.maximumPerKey {
		a.mu.Unlock()
		return nil, false
	}
	a.activeTotal++
	a.activeByKey[key]++
	a.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			a.mu.Lock()
			defer a.mu.Unlock()
			a.activeTotal--
			a.activeByKey[key]--
			if a.activeByKey[key] == 0 {
				delete(a.activeByKey, key)
			}
		})
	}, true
}

type publicAuthGuard struct {
	clients     *keyedRateLimiter
	concurrency *concurrentAdmission
}

func newPublicAuthGuard(requestRate rate.Limit, burst, maximumConcurrent, maximumPerClient int) *publicAuthGuard {
	return &publicAuthGuard{
		clients:     newKeyedRateLimiter(requestRate, burst, publicAuthClientLimitEntries, publicAuthClientLimitIdle),
		concurrency: newConcurrentAdmission(maximumConcurrent, maximumPerClient),
	}
}

func (g *publicAuthGuard) acquire(clientKey string) (func(), bool) {
	if g == nil {
		return func() {}, true
	}
	if g.clients != nil && !g.clients.allow(clientKey) {
		return nil, false
	}
	return g.concurrency.acquire(clientKey)
}

func publicAuthClientKey(c *gin.Context) string {
	if c == nil {
		return "unknown"
	}
	if address := net.ParseIP(c.ClientIP()); address != nil {
		return address.String()
	}
	return "unknown"
}
