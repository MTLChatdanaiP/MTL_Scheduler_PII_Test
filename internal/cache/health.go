package cache

import (
	"errors"
	"net"

	"github.com/redis/go-redis/v9"
)

// RFC-003 §13 Redis Outage: "Redis unavailability must be distinguishable
// from application job failure." This is the shared classifier every Redis
// call site checks before logging a generic error.
func IsUnavailable(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, redis.Nil) {
		return false // a legitimate empty result, not a failure
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	return true
}
