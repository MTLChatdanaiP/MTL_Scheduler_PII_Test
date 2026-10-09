// Package buildinfo reports which build of the program is running (RFC-005 §7 Component Health Projection:
// "version/build revision"). It is a leaf package so both the worker and the health report can use it.
package buildinfo

import (
	"os"
	"regexp"
	"runtime/debug"
	"strings"
)

var safeRevision = regexp.MustCompile(`^[A-Za-z0-9._:+-]{1,64}$`)

// Revision returns the build revision: the BUILD_REVISION environment variable if it is set to something safe to show,
// else the VCS revision Go embeds in a binary built inside a git checkout (shortened to 12 characters), else "unknown".
// (`go run` embeds no revision, so a development run reports "unknown" unless BUILD_REVISION is set.) It is read when
// called, not at startup, so a value that only exists in .env is seen.
func Revision() string {
	if r := strings.TrimSpace(os.Getenv("BUILD_REVISION")); safeRevision.MatchString(r) {
		return r
	}

	if info, ok := debug.ReadBuildInfo(); ok {
		for _, s := range info.Settings {
			if s.Key == "vcs.revision" && s.Value != "" {
				if len(s.Value) > 12 {
					return s.Value[:12]
				}
				return s.Value
			}
		}
	}
	return "unknown"
}
