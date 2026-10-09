package config

import "testing"

func TestDebugEndpointsEnabled_OnlyForExactlyTrue(t *testing.T) {
	for value, want := range map[string]bool{"true": true, "": false, "TRUE": false, "True": false, "1": false, "yes": false, " true": false, "false": false} {
		t.Setenv("ENABLE_DEBUG_ENDPOINTS", value)
		if got := DebugEndpointsEnabled(); got != want {
			t.Errorf("ENABLE_DEBUG_ENDPOINTS=%q: got %v, want %v", value, got, want)
		}
	}
}

func TestDebugEndpointsEnabled_IsOffWhenUnset(t *testing.T) {
	t.Setenv("ENABLE_DEBUG_ENDPOINTS", "")
	if DebugEndpointsEnabled() {
		t.Fatal("debug endpoints must be off unless explicitly enabled")
	}
}

// the value is read when asked, so a .env loaded after package init still counts
func TestDebugEndpointsEnabled_ReadsTheEnvironmentWhenCalled(t *testing.T) {
	t.Setenv("ENABLE_DEBUG_ENDPOINTS", "")
	before := DebugEndpointsEnabled()
	t.Setenv("ENABLE_DEBUG_ENDPOINTS", "true")
	if before || !DebugEndpointsEnabled() {
		t.Fatal("the flag must follow the environment at call time")
	}
}
