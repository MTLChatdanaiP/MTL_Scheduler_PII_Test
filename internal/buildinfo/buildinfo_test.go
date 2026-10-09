package buildinfo

import (
	"regexp"
	"testing"
)

func TestRevision_TheEnvironmentVariableWinsAndIsReadWhenCalled(t *testing.T) {
	t.Setenv("BUILD_REVISION", "abc123def456")
	if got := Revision(); got != "abc123def456" {
		t.Fatalf("got %q: BUILD_REVISION set after startup must be seen", got)
	}
}

func TestRevision_AnUnsafeValueIsNeverShown(t *testing.T) {
	// it is displayed in the UI and returned by the API, so it must not be a way to inject text
	for _, bad := range []string{"has space", "jane.doe@example.com", "line\nbreak", "<script>", "x/y"} {
		t.Setenv("BUILD_REVISION", bad)
		if got := Revision(); got == bad {
			t.Errorf("unsafe value %q was returned", bad)
		}
	}
}

func TestRevision_AlwaysReturnsSomethingSafe(t *testing.T) {
	t.Setenv("BUILD_REVISION", "")
	if got := Revision(); !regexp.MustCompile(`^[A-Za-z0-9._:+-]{1,64}$`).MatchString(got) {
		t.Fatalf("revision %q is not safe to display", got)
	}
}
