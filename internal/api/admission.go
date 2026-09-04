package api

import (
	"hash/maphash"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

const (
	requestAdmissionEntries         = 4096
	requestAdmissionOverflowBuckets = 64
	requestAdmissionIdle            = 15 * time.Minute
)

type admissionRateEntry struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// boundedKeyRateLimiter provides per-session or per-principal fairness without
// allowing attacker-controlled keys to grow process memory without bound.
type boundedKeyRateLimiter struct {
	mu        sync.Mutex
	limit     rate.Limit
	burst     int
	entries   map[string]*admissionRateEntry
	overflow  []*rate.Limiter
	seed      maphash.Seed
	nextSweep time.Time
}

func newBoundedKeyRateLimiter(limit rate.Limit, burst int) *boundedKeyRateLimiter {
	result := &boundedKeyRateLimiter{
		limit: limit, burst: burst, entries: make(map[string]*admissionRateEntry), seed: maphash.MakeSeed(),
	}
	result.overflow = make([]*rate.Limiter, requestAdmissionOverflowBuckets)
	for index := range result.overflow {
		result.overflow[index] = rate.NewLimiter(limit, burst)
	}
	return result
}

func (l *boundedKeyRateLimiter) allow(key string, now time.Time) bool {
	_, ok := l.reserve(key, now)
	return ok
}

// reserve consumes one immediately available token and returns a cancellation
// function that can restore it when a later, process-wide admission check
// rejects the same request. This prevents one already-throttled key from
// draining the shared budget used by other principals.
func (l *boundedKeyRateLimiter) reserve(key string, now time.Time) (func(), bool) {
	if l == nil {
		return func() {}, true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if entry := l.entries[key]; entry != nil {
		entry.lastSeen = now
		return reserveImmediate(entry.limiter, now)
	}
	if len(l.entries) >= requestAdmissionEntries && (l.nextSweep.IsZero() || !now.Before(l.nextSweep)) {
		for existingKey, entry := range l.entries {
			if now.Sub(entry.lastSeen) >= requestAdmissionIdle {
				delete(l.entries, existingKey)
			}
		}
		l.nextSweep = now.Add(time.Minute)
	}
	if len(l.entries) >= requestAdmissionEntries {
		bucket := maphash.String(l.seed, key) % uint64(len(l.overflow))
		return reserveImmediate(l.overflow[bucket], now)
	}
	entry := &admissionRateEntry{limiter: rate.NewLimiter(l.limit, l.burst), lastSeen: now}
	l.entries[key] = entry
	return reserveImmediate(entry.limiter, now)
}

func reserveImmediate(limiter *rate.Limiter, now time.Time) (func(), bool) {
	reservation := limiter.ReserveN(now, 1)
	if !reservation.OK() || reservation.DelayFrom(now) > 0 {
		reservation.CancelAt(now)
		return nil, false
	}
	var once sync.Once
	return func() {
		once.Do(func() { reservation.CancelAt(now) })
	}, true
}

type admissionConcurrency struct {
	mu            sync.Mutex
	maximumTotal  int
	maximumPerKey int
	activeTotal   int
	activeByKey   map[string]int
}

func newAdmissionConcurrency(maximumTotal, maximumPerKey int) *admissionConcurrency {
	return &admissionConcurrency{
		maximumTotal: maximumTotal, maximumPerKey: maximumPerKey, activeByKey: make(map[string]int),
	}
}

func (a *admissionConcurrency) acquire(keys ...string) (func(), bool) {
	if a == nil {
		return func() {}, true
	}
	keys = uniqueAdmissionKeys(keys)
	a.mu.Lock()
	if len(keys) == 0 || a.activeTotal >= a.maximumTotal {
		a.mu.Unlock()
		return nil, false
	}
	for _, key := range keys {
		if a.activeByKey[key] >= a.maximumPerKey {
			a.mu.Unlock()
			return nil, false
		}
	}
	a.activeTotal++
	for _, key := range keys {
		a.activeByKey[key]++
	}
	a.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			a.mu.Lock()
			defer a.mu.Unlock()
			a.activeTotal--
			for _, key := range keys {
				a.activeByKey[key]--
				if a.activeByKey[key] == 0 {
					delete(a.activeByKey, key)
				}
			}
		})
	}, true
}

type requestAdmission struct {
	mu          sync.Mutex
	global      *rate.Limiter
	perKey      *boundedKeyRateLimiter
	concurrency *admissionConcurrency
}

func newRequestAdmission(globalLimit rate.Limit, globalBurst int, perKeyLimit rate.Limit, perKeyBurst, maximumTotal, maximumPerKey int) *requestAdmission {
	return &requestAdmission{
		global:      rate.NewLimiter(globalLimit, globalBurst),
		perKey:      newBoundedKeyRateLimiter(perKeyLimit, perKeyBurst),
		concurrency: newAdmissionConcurrency(maximumTotal, maximumPerKey),
	}
}

func (a *requestAdmission) acquire(keys ...string) (func(), bool) {
	if a == nil {
		return func() {}, true
	}
	keys = uniqueAdmissionKeys(keys)
	if len(keys) == 0 {
		return nil, false
	}
	// Keep the multi-dimensional token decision atomic. In particular, no
	// concurrent reservation can prevent restoration when a later dimension or
	// the global limiter rejects this request.
	a.mu.Lock()
	defer a.mu.Unlock()
	now := time.Now()
	reservations := make([]func(), 0, len(keys))
	for _, key := range keys {
		if a.perKey == nil {
			continue
		}
		cancel, ok := a.perKey.reserve(key, now)
		if !ok {
			cancelAdmissionReservations(reservations)
			return nil, false
		}
		reservations = append(reservations, cancel)
	}
	if a.global != nil && !a.global.AllowN(now, 1) {
		cancelAdmissionReservations(reservations)
		return nil, false
	}
	return a.concurrency.acquire(keys...)
}

func cancelAdmissionReservations(reservations []func()) {
	for index := len(reservations) - 1; index >= 0; index-- {
		reservations[index]()
	}
}

func uniqueAdmissionKeys(keys []string) []string {
	unique := make([]string, 0, len(keys))
	seen := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		if key == "" {
			return nil
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		unique = append(unique, key)
	}
	return unique
}

type denialAdmission struct {
	mu     sync.Mutex
	global *rate.Limiter
	perKey *boundedKeyRateLimiter
}

func newDenialAdmission(globalLimit rate.Limit, globalBurst int, perKeyLimit rate.Limit, perKeyBurst int) *denialAdmission {
	return &denialAdmission{
		global: rate.NewLimiter(globalLimit, globalBurst),
		perKey: newBoundedKeyRateLimiter(perKeyLimit, perKeyBurst),
	}
}

func (a *denialAdmission) allow(key string) bool {
	if a == nil {
		return true
	}
	if key == "" {
		return false
	}
	// Reserve the subject-specific budget before the shared budget. Otherwise
	// one principal that is already over its own limit can keep consuming every
	// global token and suppress denial events from unrelated principals.
	a.mu.Lock()
	defer a.mu.Unlock()
	now := time.Now()
	cancel := func() {}
	if a.perKey != nil {
		var ok bool
		cancel, ok = a.perKey.reserve(key, now)
		if !ok {
			return false
		}
	}
	if a.global != nil && !a.global.AllowN(now, 1) {
		cancel()
		return false
	}
	return true
}
