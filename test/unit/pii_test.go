package unit_test

import (
	"strings"
	"testing"

	"MTL_Scheduler_PII_Test/internals/pii"
)

func TestDetect(t *testing.T) {
	// Path is relative to THIS package's directory (test/unit/), not the repo
	// root -- Go runs each test with its own package dir as the working
	// directory. The previous "policies/default.json" silently resolved to
	// test/unit/policies/default.json, which does not exist.
	//
	// t.Fatalf rather than t.Errorf: if the policy fails to load there are no
	// detectors, every subcase reports "0 findings, want 4", and the real cause
	// (missing file) is buried under three misleading failures.
	policy, err := pii.LoadPolicy("../../policies/default.json")
	if err != nil {
		t.Fatalf("failed to load policy: %v", err)
	}

	pii.LoadedPolicy.Store(&policy)

	// No explicit LoadedPolicy.Store here -- ActivatePolicy installs it itself.
	// Storing again would work but implies the function doesn't, which is
	// exactly the confusion that let PostReloadPolicy ship without a Store.

	if len(policy.Spec.Detectors) == 0 {
		t.Fatal("policy loaded but has no detectors -- wrong file?")
	}

	tests := []struct {
		name    string
		input   string
		wantLen int
	}{
		{"finds all four types", "Email me@x.com call 081-234-5678 ssn 123-45-6789 card 4111-1111-1111-1111", 4},
		{"no pii returns empty", "just a normal sentence with nothing sensitive", 0},
		{"two emails not deduplicated", "contact me@x.com or also you@y.com", 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			findings, failed := pii.Detect(tt.input, pii.GetLoadedPolicy().Spec.Detectors)

			if len(failed) > 0 {
				t.Fatalf("detectors failed: %v", failed)
			}

			if len(findings) != tt.wantLen {
				t.Errorf("Detect(%q) returned %d findings, want %d", tt.input, len(findings), tt.wantLen)
			}
		})
	}
}

func TestReplacer(t *testing.T) {
	tests := []struct {
		name         string
		payload      string
		match        string
		piiType      pii.PIIType
		index        string
		wantContains string
	}{
		{"replaces email with placeholder", "contact me@x.com now", "me@x.com", pii.PIIType("Email"), "1", "[Email-1]"},
		{"replaces phone with placeholder", "call 081-234-5678 today", "081-234-5678", pii.PIIType("Phone"), "1", "[Phone-1]"},
		{"replaces second occurrence with correct index", "ssn 123-45-6789 again", "123-45-6789", pii.PIIType("SSN"), "2", "[SSN-2]"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			replaced := pii.ReplacerMatch(tt.payload, tt.match, tt.piiType, tt.index)

			if !strings.Contains(replaced, tt.wantContains) {
				t.Errorf("ReplacerMatch(%q) = %q, want to contain %q", tt.payload, replaced, tt.wantContains)
			}

			// The raw value must be gone, not merely accompanied by a placeholder.
			// Asserting only "contains [Email-1]" would pass on a result that
			// appended the placeholder and left the original in place.
			if strings.Contains(replaced, tt.match) {
				t.Errorf("ReplacerMatch(%q) = %q, still contains raw value %q", tt.payload, replaced, tt.match)
			}
		})
	}
}
