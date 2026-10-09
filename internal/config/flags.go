// Package config holds switches that are read from the environment WHEN ASKED, never at package init: .env is loaded by
// main() after package initialisation, so a value read at init would always be empty.
package config

import "os"

// DebugEndpointsEnabled reports whether the destructive debug endpoints may be registered. It is true ONLY when
// ENABLE_DEBUG_ENDPOINTS is exactly "true"; anything else (unset, "TRUE", "1", "yes") leaves them off.
func DebugEndpointsEnabled() bool {
	return os.Getenv("ENABLE_DEBUG_ENDPOINTS") == "true"
}
