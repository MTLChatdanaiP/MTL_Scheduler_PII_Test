package unit_test

import (
	"context"
	"errors"
	"io"
	"net"
	"testing"

	"github.com/redis/go-redis/v9"

	"MTL_Scheduler_PII_Test/internal/cache"
)

// RFC-003 §13: "Redis unavailability must be distinguishable from application
// job failure." Before this fix both branches of IsUnavailable returned true, so
// EVERY non-nil error -- including a deliberate shutdown -- was reported as an
// infrastructure outage.

func TestIsUnavailable_NotAnOutage(t *testing.T) {
	cases := map[string]error{
		"no error at all":                                          nil,
		"redis.Nil is a legitimate empty result":                   redis.Nil,
		"a cancelled context is our own shutdown, not Redis dying": context.Canceled,
		"a command error means Redis answered -- it is up":         errors.New("WRONGTYPE Operation against a key holding the wrong kind of value"),
	}
	for name, err := range cases {
		if cache.IsUnavailable(err) {
			t.Errorf("%s: should NOT be classified as unavailable", name)
		}
	}
}

func TestIsUnavailable_GenuineTransportFailures(t *testing.T) {
	cases := map[string]error{
		"connection refused":  &net.OpError{Op: "dial", Err: errors.New("connection refused")},
		"deadline exceeded":   context.DeadlineExceeded,
		"connection died":     io.EOF,
		"truncated response":  io.ErrUnexpectedEOF,
		"socket already shut": net.ErrClosed,
	}
	for name, err := range cases {
		if !cache.IsUnavailable(err) {
			t.Errorf("%s: SHOULD be classified as unavailable", name)
		}
	}
}

func TestIsUnavailable_WrappedErrorsAreStillClassified(t *testing.T) {
	// call sites wrap errors with context before passing them here
	wrapped := errors.Join(errors.New("while reading the task stream"), io.EOF)
	if !cache.IsUnavailable(wrapped) {
		t.Error("a wrapped transport error must still be detected")
	}
	wrappedCancel := errors.Join(errors.New("during shutdown"), context.Canceled)
	if cache.IsUnavailable(wrappedCancel) {
		t.Error("a wrapped cancellation must still be recognised as a shutdown, not an outage")
	}
}
