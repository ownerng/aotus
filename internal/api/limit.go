package api

import (
	"sync"
	"time"
)

// Defaults for limiting failed attempts (Config.MaxFailures, FailureWindow).
const (
	defaultMaxFailures   = 10
	defaultFailureWindow = time.Minute
	// maxTracked bounds the memory a flood of distinct callers can take.
	maxTracked = 10000
)

// failLimiter counts failed attempts per caller in a fixed window. Once a
// caller reaches the limit, its further failures are answered with 429 and
// are not audited or logged one by one, so nobody can flood the audit log, the
// logs or the store. Successful requests are never limited (docs/SECURITY.md,
// Accepted risks).
type failLimiter struct {
	mu     sync.Mutex
	max    int
	window time.Duration
	now    func() time.Time
	m      map[string]*failCount
}

type failCount struct {
	n     int
	start time.Time
}

func newFailLimiter(max int, window time.Duration) *failLimiter {
	if max <= 0 {
		max = defaultMaxFailures
	}
	if window <= 0 {
		window = defaultFailureWindow
	}
	return &failLimiter{max: max, window: window, now: time.Now, m: map[string]*failCount{}}
}

// blocked reports whether key has used up its failures, and for how long.
func (l *failLimiter) blocked(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	c := l.current(key)
	if c == nil || c.n < l.max {
		return false, 0
	}
	return true, c.start.Add(l.window).Sub(l.now())
}

// fail records a failed attempt and reports whether it was the one that
// reached the limit.
func (l *failLimiter) fail(key string) (reached bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	c := l.current(key)
	if c == nil {
		if len(l.m) >= maxTracked {
			l.prune()
		}
		c = &failCount{start: l.now()}
		l.m[key] = c
	}
	c.n++
	return c.n == l.max
}

// current returns the count of key in the running window, dropping a stale one.
func (l *failLimiter) current(key string) *failCount {
	c := l.m[key]
	if c != nil && l.now().Sub(c.start) >= l.window {
		delete(l.m, key)
		return nil
	}
	return c
}

// prune drops stale counts; if the table is still full of live ones, it starts
// over rather than grow.
func (l *failLimiter) prune() {
	now := l.now()
	for k, c := range l.m {
		if now.Sub(c.start) >= l.window {
			delete(l.m, k)
		}
	}
	if len(l.m) >= maxTracked {
		l.m = map[string]*failCount{}
	}
}
