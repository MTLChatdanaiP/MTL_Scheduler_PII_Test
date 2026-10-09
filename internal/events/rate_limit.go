package events

import (
	"context"
	"sync"
	"time"
)

// RedisDownEventEvery is how often a "redis.unavailable" event may be recorded per producer.
//
// ReadStream records one on every failed read and sleeps one second, so a long outage wrote
// roughly 3600 event rows an hour, and the queue monitor and reclaimer added more.
const RedisDownEventEvery = time.Minute

var (
	limiterMu  sync.Mutex
	lastLogged = map[string]time.Time{}
	nowFunc    = time.Now // replaced by a test so it does not have to sleep
)

// LogEventEvery takes the arguments of LogEvent plus a window, and records the event only if
// the same (event type, producer) has not been recorded within that window. It returns nothing.
func LogEventEvery(ctx context.Context, every time.Duration, subject string, eventType string, producer string) {
	key := eventType + "|" + producer

	limiterMu.Lock()
	now := nowFunc()
	last, seen := lastLogged[key]
	if seen && now.Sub(last) < every {
		limiterMu.Unlock()
		return
	}
	lastLogged[key] = now
	limiterMu.Unlock()

	LogEvent(ctx, subject, eventType, producer)
}

// ResetRateLimitsForTest forgets every remembered event, so a test starts from a clean slate.
func ResetRateLimitsForTest() {
	limiterMu.Lock()
	defer limiterMu.Unlock()
	lastLogged = map[string]time.Time{}
}
