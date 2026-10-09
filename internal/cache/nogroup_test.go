package cache

import (
	"errors"
	"testing"
)

func TestIsNoGroup(t *testing.T) {
	if !IsNoGroup(errors.New("NOGROUP No such key 'tasks:stream' or consumer group 'WorkerG_A' in XREADGROUP with GROUP option")) {
		t.Fatal("the NOGROUP reply must be recognised")
	}
	for _, other := range []error{nil, errors.New("dial tcp 127.0.0.1:6379: connection refused"), errors.New("BUSYGROUP Consumer Group name already exists"), errors.New("WRONGPASS invalid username-password pair")} {
		if IsNoGroup(other) {
			t.Errorf("%v is not a missing-group error", other)
		}
	}
}
