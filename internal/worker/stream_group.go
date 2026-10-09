package worker

import (
	"context"
	"math/rand"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// sleepCtx waits for d, or until ctx is cancelled. It returns true if the whole wait happened and false if it was cut short, so a
// loop can stop at once on shutdown instead of finishing a long time.Sleep first.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// ensureGroup creates the consumer group (and the stream, if it does not exist). A group that already exists (BUSYGROUP) is
// SUCCESS: that is the normal case for every worker after the first.
func ensureGroup(ctx context.Context, rdb redis.Cmdable, stream string, group string) error {
	err := rdb.XGroupCreateMkStream(ctx, stream, group, "$").Err()
	if err != nil && !strings.HasPrefix(err.Error(), "BUSYGROUP") {
		return err
	}
	return nil
}

// backoffConfig controls retryWithBackoff. sleep is injected so a test does not wait real seconds.
type backoffConfig struct {
	initial time.Duration
	max     time.Duration
	jitter  float64 // each wait gets up to this fraction ADDED, so many workers do not retry in lock-step
	sleep   func(ctx context.Context, d time.Duration) bool
}

func defaultBackoff() backoffConfig {
	return backoffConfig{initial: 500 * time.Millisecond, max: 30 * time.Second, jitter: 0.2, sleep: sleepCtx}
}

// retryWithBackoff calls op until it succeeds or ctx is cancelled. The wait starts at cfg.initial, doubles after every failure and
// is capped at cfg.max. It returns nil on success and ctx's error if it was cancelled first.
func retryWithBackoff(ctx context.Context, op func() error, cfg backoffConfig) error {
	delay := cfg.initial
	for {
		if err := op(); err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}

		wait := delay
		if cfg.jitter > 0 {
			wait += time.Duration(rand.Float64() * cfg.jitter * float64(delay))
		}
		if !cfg.sleep(ctx, wait) {
			return ctx.Err()
		}

		delay *= 2
		if delay > cfg.max {
			delay = cfg.max
		}
	}
}
