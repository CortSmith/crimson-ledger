package apiserver

import (
	"context"
	"sync"
	"time"
)

// Limiter decides whether one more request for a key is allowed.
type Limiter interface {
	// Allow reports whether one more request for key is allowed now, else how long to wait.
	Allow(ctx context.Context, key string, limit int, per time.Duration) (ok bool, retryAfter time.Duration, err error)
}

const (
	// burstDivisor makes the burst allowance one sixth of the per-window limit (120/min -> 20 at once).
	burstDivisor = 6
	// maxBuckets bounds the memory a flood of distinct keys or IPs can take.
	maxBuckets = 10000

	// Failed-auth lockout (PLAN.md section 7): 10 failures in 5 minutes lock the IP for 5 minutes.
	failedAuthMax     = 10
	failedAuthWindow  = 5 * time.Minute
	failedAuthLockout = 5 * time.Minute
)

type bucket struct {
	tokens float64
	last   time.Time
}

// MemoryLimiter is a token bucket per key held in memory (local mode and one Lambda container).
type MemoryLimiter struct {
	mu      sync.Mutex
	now     func() time.Time
	buckets map[string]*bucket
}

// NewMemoryLimiter makes a limiter that reads time from now.
func NewMemoryLimiter(now func() time.Time) *MemoryLimiter {
	return &MemoryLimiter{now: now, buckets: map[string]*bucket{}}
}

// Allow implements Limiter.
func (l *MemoryLimiter) Allow(_ context.Context, key string, limit int, per time.Duration) (bool, time.Duration, error) {
	burst := float64(max(1, limit/burstDivisor))
	rate := float64(limit) / per.Seconds()
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	b := l.buckets[key]
	if b == nil {
		l.makeRoom(now, per)
		b = &bucket{tokens: burst, last: now}
		l.buckets[key] = b
	}
	b.tokens = min(burst, b.tokens+now.Sub(b.last).Seconds()*rate)
	b.last = now
	if b.tokens >= 1 {
		b.tokens--
		return true, 0, nil
	}
	return false, time.Duration((1 - b.tokens) / rate * float64(time.Second)), nil
}

// makeRoom keeps the map under maxBuckets: idle buckets are full again, so dropping them loses nothing.
func (l *MemoryLimiter) makeRoom(now time.Time, per time.Duration) {
	if len(l.buckets) < maxBuckets {
		return
	}
	for k, b := range l.buckets {
		if now.Sub(b.last) > per {
			delete(l.buckets, k)
		}
	}
	for k := range l.buckets {
		if len(l.buckets) < maxBuckets {
			return
		}
		delete(l.buckets, k)
	}
}

func (l *MemoryLimiter) size() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.buckets)
}

type failState struct {
	count       int
	windowStart time.Time
	lockedUntil time.Time
}

// failedAuthTracker counts failed authentications per client IP and locks an IP that fails too often.
type failedAuthTracker struct {
	mu  sync.Mutex
	now func() time.Time
	m   map[string]*failState
}

func newFailedAuthTracker(now func() time.Time) *failedAuthTracker {
	return &failedAuthTracker{now: now, m: map[string]*failState{}}
}

// locked reports whether ip is locked out and for how much longer.
func (t *failedAuthTracker) locked(ip string) (bool, time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()
	s := t.m[ip]
	if s == nil {
		return false, 0
	}
	if wait := s.lockedUntil.Sub(t.now()); wait > 0 {
		return true, wait
	}
	return false, 0
}

// record notes one failed authentication from ip.
func (t *failedAuthTracker) record(ip string) {
	now := t.now()
	t.mu.Lock()
	defer t.mu.Unlock()
	s := t.m[ip]
	if s == nil {
		t.makeRoom(now)
		s = &failState{windowStart: now}
		t.m[ip] = s
	}
	if now.Sub(s.windowStart) > failedAuthWindow {
		s.count, s.windowStart = 0, now
	}
	s.count++
	if s.count >= failedAuthMax {
		s.lockedUntil = now.Add(failedAuthLockout)
		s.count, s.windowStart = 0, now
	}
}

func (t *failedAuthTracker) makeRoom(now time.Time) {
	if len(t.m) < maxBuckets {
		return
	}
	for ip, s := range t.m {
		if now.After(s.lockedUntil) && now.Sub(s.windowStart) > failedAuthWindow {
			delete(t.m, ip)
		}
	}
	for ip := range t.m {
		if len(t.m) < maxBuckets {
			return
		}
		delete(t.m, ip)
	}
}
