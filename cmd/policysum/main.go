// policysum checks (and optionally fixes) a PII policy file before you reload it.
//
//	go run ./cmd/policysum policies/default.json        report only
//	go run ./cmd/policysum -w policies/default.json     rewrite the checksum in place
//
// LoadPolicy refuses a policy whose "checksum" is not sha256(json.Marshal(Spec)),
// so every hand edit to a policy file needs the checksum regenerated. This does it
// with the same code path LoadPolicy uses, and also runs ValidatePolicy so mask
// and action mistakes are reported here instead of at startup.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"regexp"

	"MTL_Scheduler_PII_Test/internal/models"
	"MTL_Scheduler_PII_Test/internal/pii"
)

var checksumField = regexp.MustCompile(`("(?i:checksum)"\s*:\s*")[^"]*(")`)

func main() {
	write := flag.Bool("w", false, "rewrite the checksum in the file")
	flag.Parse()

	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: policysum [-w] <policy.json>")
		os.Exit(2)
	}
	path := flag.Arg(0)

	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "cannot read policy:", err)
		os.Exit(1)
	}

	var policy models.PIIPolicy
	if err := json.Unmarshal(data, &policy); err != nil {
		fmt.Fprintln(os.Stderr, "policy is not valid JSON for this schema:", err)
		os.Exit(1)
	}

	spec, err := json.Marshal(policy.Spec)
	if err != nil {
		fmt.Fprintln(os.Stderr, "cannot marshal spec:", err)
		os.Exit(1)
	}
	sum := sha256.Sum256(spec)
	computed := hex.EncodeToString(sum[:])

	fmt.Println("file checksum:    ", policy.Metadata.Checksum)
	fmt.Println("computed checksum:", computed)

	problems := pii.ValidatePolicy(policy)
	if len(problems) > 0 {
		fmt.Println("\nVALIDATION PROBLEMS (LoadPolicy would refuse this policy):")
		for _, p := range problems {
			fmt.Println("  -", p)
		}
	} else {
		fmt.Println("validation:        OK")
	}

	if policy.Metadata.Checksum == computed {
		fmt.Println("checksum:          OK")
	} else if *write {
		if !checksumField.Match(data) {
			fmt.Fprintln(os.Stderr, `no "checksum" field found to rewrite`)
			os.Exit(1)
		}
		updated := checksumField.ReplaceAll(data, []byte("${1}"+computed+"${2}"))
		if err := os.WriteFile(path, updated, 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "cannot write policy:", err)
			os.Exit(1)
		}
		fmt.Println("checksum:          REWRITTEN")
	} else {
		fmt.Println("checksum:          MISMATCH (run again with -w to fix)")
		os.Exit(1)
	}

	if len(problems) > 0 {
		os.Exit(1)
	}
}
