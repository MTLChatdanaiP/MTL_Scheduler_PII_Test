// Package bootstrap holds the checks that decide whether the process may start at all.
package bootstrap

import (
	"fmt"
	"os"
	"strings"

	"MTL_Scheduler_PII_Test/internal/pii"
)

// requiredEnv are the settings without which the database cannot be reached. DB_PORT is not listed: an empty port falls back
// to Postgres's default.
var requiredEnv = []string{"DB_HOST", "DB_USER", "DB_PASSWORD", "DB_NAME"}

// StartupChecks returns an error if the process must not start. main() calls it right after loading .env and exits on an
// error, so a weak PII key stops the program instead of silently weakening every fingerprint and vault entry.
//
// The error names the SETTING and the requirement. It never contains a value (a key's length is reported, not its content).
// Everything is read from the environment when called, never at package init.
func StartupChecks() error {
	var problems []string

	if err := pii.ValidateKeyValues(os.Getenv("PII_FINGERPRINT_KEY"), os.Getenv("PII_ENCR_KEY")); err != nil {
		problems = append(problems, err.Error())
	}

	var missing []string
	for _, name := range requiredEnv {
		if os.Getenv(name) == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		problems = append(problems, "required environment variables are not set: "+strings.Join(missing, ", "))
	}

	if len(problems) > 0 {
		return fmt.Errorf("startup refused: %s", strings.Join(problems, " | "))
	}
	return nil
}
