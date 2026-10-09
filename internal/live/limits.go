package live

import (
	"os"
	"strconv"
	"sync"
)

// RFC-010 §19 subscription limits.
//
// A browser tab stuck in a reconnect loop, or a script that never closes its streams, can open connections faster than the hub
// can serve them. Each API key may therefore hold only a bounded number of live connections at once; the next is refused with 429
// before any stream starts. The limit is read when asked, never at package init, because .env is loaded after packages initialise.

// DefaultMaxConnectionsPerKey is used when LIVE_MAX_CONNECTIONS_PER_KEY is unset or not a positive whole number.
const DefaultMaxConnectionsPerKey = 10

// MaxConnectionsPerKey returns the current per-key cap.
func MaxConnectionsPerKey() int {
	if v := os.Getenv("LIVE_MAX_CONNECTIONS_PER_KEY"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return DefaultMaxConnectionsPerKey
}

// ConnLimiter counts open live connections per caller.
type ConnLimiter struct {
	mu     sync.Mutex
	active map[string]int
}

// NewConnLimiter returns an empty limiter.
func NewConnLimiter() *ConnLimiter { return &ConnLimiter{active: map[string]int{}} }

// Limiter is the process-wide limiter the live handler uses.
var Limiter = NewConnLimiter()

// Acquire takes a slot for caller if it has fewer than max open connections, and reports whether it got one.
// A refusal is counted in the pipeline stats.
func (l *ConnLimiter) Acquire(caller string, max int) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.active[caller] >= max {
		CountRejectedSubscription()
		return false
	}
	l.active[caller]++
	return true
}

// Release gives a slot back. Releasing more than was acquired never goes below zero, so a double release is harmless.
func (l *ConnLimiter) Release(caller string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.active[caller] <= 1 {
		delete(l.active, caller)
		return
	}
	l.active[caller]--
}

// Open returns how many connections caller currently holds.
func (l *ConnLimiter) Open(caller string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.active[caller]
}
