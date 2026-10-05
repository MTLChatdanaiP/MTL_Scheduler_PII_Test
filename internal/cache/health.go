package cache

import (
	"context"
	"errors"
	"io"
	"net"

	"github.com/redis/go-redis/v9"
)

// RFC-003 §13 Redis Outage: "Redis unavailability must be distinguishable
// from application job failure."
func IsUnavailable(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, redis.Nil) {
		return false // a legitimate empty result, not a failure
	}

	if errors.Is(err, context.Canceled) {
		return false
	}

	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	if errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, io.EOF) ||
		errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, net.ErrClosed) {
		return true
	}

	return false
}
