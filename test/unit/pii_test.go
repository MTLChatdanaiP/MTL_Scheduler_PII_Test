package unit_test

import (
	"MTL_Scheduler_PII_Test/internals/pii"
	"strings"
	"testing"
)

func TestDetect(t *testing.T) {
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
			findings := pii.Detect(tt.input, pii.LoadedPolicy.Spec.Detectors)
			if len(findings) != tt.wantLen {
				t.Errorf("Detect(%q) returned %d findings, want %d", tt.input, len(findings), tt.wantLen)
			}
		})
	}
}

func TestReplacer(t *testing.T) {
	// TODO: table of {payload, match, piiType, index, wantContains}
	// call Replacer(payload, match, piiType, index)
	// assert result contains "[Email-1]" (or whatever the expected placeholder is)
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
			replaced := pii.Replacer(tt.payload, tt.match, tt.piiType, tt.index)

			if !strings.Contains(replaced, tt.wantContains) {
				t.Errorf("Replacer(%q) = %q, want to contain %q",
					tt.payload, replaced, tt.wantContains)
			}
		})
	}
}
