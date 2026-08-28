package unit_test

import (
	"errors"
	"net"
	"testing"

	"MTL_Scheduler_PII_Test/internals/cache"

	"github.com/redis/go-redis/v9"
)

func TestIsUnavailable(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil error is not unavailable", nil, false},
		{"redis.Nil is not unavailable", redis.Nil, false},
		{"network error is unavailable", &net.DNSError{IsTimeout: true}, true},
		{"generic connection error is unavailable", errors.New("dial tcp: connection refused"), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cache.IsUnavailable(tt.err)
			if got != tt.want {
				t.Errorf("IsUnavailable(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}
