package pii

// RFC-006 §10 Field Path Matching, §25 Structured JSON Processing,
// §27 Overlapping Matches.
//
// In-package because fieldPathMatches, getStringAtPath and setStringAtPath are
// unexported.
//
// WHY THIS FILE EXISTS AT ALL: the leaf-relative offset bug survived for weeks
// because every existing test payload was FLAT. In a flat payload the leaf value
// IS the whole payload, so leaf-relative and payload-relative offsets coincide
// and the bug is invisible. Every nested case below is one the old suite could
// not have caught.

import (
	"encoding/json"
	"strings"
	"testing"

	"MTL_Scheduler_PII_Test/internals/models"
)

func TestFieldPathMatches(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		path    string
		want    bool
	}{
		{"exact match", "email", "email", true},
		{"exact nested match", "user.email", "user.email", true},
		{"wildcard matches index 0", "customers[*].phone", "customers[0].phone", true},
		{"wildcard matches higher index", "customers[*].phone", "customers[7].phone", true},

		// The rule that was matching EVERY phone before the ruleMatches fix. If
		// wildcard matching ever over-matches again, these are the cases that
		// catch it.
		{"different field under same array does not match", "customers[*].phone", "customers[0].name", false},
		{"different array does not match", "customers[*].phone", "vendors[0].phone", false},
		{"top-level field does not match a nested pattern", "customers[*].phone", "supportPhone", false},
		{"wildcard does not match a missing segment", "customers[*].phone", "customers[0]", false},
		{"deeper path does not match a shorter pattern", "customers[*].phone", "customers[0].phone.ext", false},

		{"empty path does not match", "customers[*].phone", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := fieldPathMatches(tt.pattern, tt.path); got != tt.want {
				t.Errorf("fieldPathMatches(%q, %q) = %v, want %v", tt.pattern, tt.path, got, tt.want)
			}
		})
	}
}

const nestedPayload = `{"email": "jane.doe@example.com", "ssn": "123-45-6789", "supportPhone": "555-000-1111", "customers": [{"name": "John", "phone": "555-123-4567"}, {"name": "Amy", "phone": "555-987-6543"}]}`

func parsePayload(t *testing.T) map[string]interface{} {
	t.Helper()

	var root map[string]interface{}
	if err := json.Unmarshal([]byte(nestedPayload), &root); err != nil {
		t.Fatalf("test payload is not valid json: %v", err)
	}
	return root
}

func TestGetStringAtPath(t *testing.T) {
	root := parsePayload(t)

	tests := []struct {
		name   string
		path   string
		want   string
		wantOK bool
	}{
		{"top level field", "email", "jane.doe@example.com", true},
		{"array element field", "customers[0].phone", "555-123-4567", true},
		{"second array element", "customers[1].phone", "555-987-6543", true},

		// Every one of these must return false rather than panic. A resolver
		// that panics on a malformed path takes down the whole task-creation
		// request; returning false just skips one transformation.
		{"missing field", "nope", "", false},
		{"index out of range", "customers[9].phone", "", false},
		{"array treated as object", "email[0]", "", false},
		{"non-string value", "customers", "", false},
		{"empty path", "", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := getStringAtPath(root, tt.path)

			if ok != tt.wantOK {
				t.Fatalf("getStringAtPath(%q) ok = %v, want %v", tt.path, ok, tt.wantOK)
			}
			if got != tt.want {
				t.Errorf("getStringAtPath(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

func TestSetStringAtPath(t *testing.T) {
	t.Run("writes into a nested array element", func(t *testing.T) {
		root := parsePayload(t)

		if ok := setStringAtPath(root, "customers[1].phone", "[REDACTED]"); !ok {
			t.Fatal("setStringAtPath reported failure on a valid path")
		}

		got, _ := getStringAtPath(root, "customers[1].phone")
		if got != "[REDACTED]" {
			t.Errorf("after set, value = %q, want %q", got, "[REDACTED]")
		}

		// Writing one leaf must not disturb its siblings. A set that rebuilt the
		// array or the parent object could silently drop neighbouring fields.
		if sibling, _ := getStringAtPath(root, "customers[0].phone"); sibling != "555-123-4567" {
			t.Errorf("sibling element changed to %q", sibling)
		}
		if name, _ := getStringAtPath(root, "customers[1].name"); name != "Amy" {
			t.Errorf("sibling field changed to %q", name)
		}
	})

	t.Run("reports failure without panicking on bad paths", func(t *testing.T) {
		for _, path := range []string{"nope", "customers[9].phone", "email[0]", ""} {
			root := parsePayload(t)
			if setStringAtPath(root, path, "x") {
				t.Errorf("setStringAtPath(%q) reported success on an invalid path", path)
			}
		}
	})
}

// ---------------------------------------------------------------------------
// The offset bug itself.
// ---------------------------------------------------------------------------

func offsetBugPolicy() models.PIIPolicy {
	detectors := []models.DetectorDefinition{
		{ID: "builtin-phone", PIIType: "Phone", Type: "REGEX", Enabled: true, Pattern: `\d{3}-\d{3}-\d{4}`, MinimumConfidence: 1.0},
		{ID: "builtin-ssn", PIIType: "SSN", Type: "REGEX", Enabled: true, Pattern: `\d{3}-\d{2}-\d{4}`, MinimumConfidence: 1.0},
		{ID: "builtin-email", PIIType: "Email", Type: "REGEX", Enabled: true, Pattern: `[^\s@]+@[^\s@]+\.[^\s@]+`, MinimumConfidence: 1.0},
	}

	// Built inline rather than loaded from policies/default.json on purpose:
	// this test must not break every time that file's checksum is regenerated,
	// and it must not depend on which rules happen to be enabled there.
	var policy models.PIIPolicy
	policy.Spec.EvaluationMode = "FIRST_MATCH"
	policy.Spec.Defaults.Action = "OBSERVE"
	policy.Spec.Detectors = detectors
	policy.Spec.Rules = []models.PolicyRule{
		{
			ID:       "redact-everything",
			Priority: 0,
			Match: models.MatchConditions{
				Sources:     []string{"JOB_PAYLOAD"},
				DetectorIDs: []string{"builtin-phone", "builtin-ssn", "builtin-email"},
			},
			Action: models.PolicyAction{Type: "REDACT"},
		},
	}
	return policy
}

func TestApplyFindingsToJSON_RewritesEveryLeaf(t *testing.T) {
	policy := offsetBugPolicy()

	evaluated, failed, ok := DetectJSON(nestedPayload, policy.Spec.Detectors, policy, "JOB_PAYLOAD", "TEST")
	if !ok {
		t.Fatal("DetectJSON reported the payload as non-JSON")
	}
	if len(failed) > 0 {
		t.Fatalf("detectors failed: %v", failed)
	}

	// email, ssn, supportPhone, and two nested customer phones.
	if len(evaluated) != 5 {
		t.Fatalf("expected 5 findings, got %d -- nested leaves are being missed", len(evaluated))
	}

	result := ApplyFindingsToJSON(nestedPayload, evaluated)

	// THE ACTUAL BUG: leaf-relative offsets spliced into the whole payload
	// corrupted the document. Parsing the result is the strongest possible
	// assertion here -- the broken version produced text that was not JSON at all.
	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(result), &parsed); err != nil {
		t.Fatalf("result is not valid JSON (the offset bug is back): %v\ngot: %s", err, result)
	}

	for _, raw := range []string{
		"jane.doe@example.com",
		"123-45-6789",
		"555-000-1111",
		"555-123-4567", // nested
		"555-987-6543", // nested
	} {
		if strings.Contains(result, raw) {
			t.Errorf("raw value %q survived in the rewritten payload: %s", raw, result)
		}
	}

	// Non-PII values must be untouched. The broken version ate the front of the
	// document, taking unrelated keys with it.
	for _, keep := range []string{"John", "Amy", "customers"} {
		if !strings.Contains(result, keep) {
			t.Errorf("non-PII content %q was lost: %s", keep, result)
		}
	}
}

func TestApplyFindingsToJSON_ReturnsPayloadUnchangedOnBadInput(t *testing.T) {
	// A failure here must never produce a half-rewritten document. Worst case is
	// "nothing was redacted", which is visible; "partially corrupted" is not.
	notJSON := "contact me at test@example.com"

	if got := ApplyFindingsToJSON(notJSON, nil); got != notJSON {
		t.Errorf("non-JSON input was modified: got %q, want %q", got, notJSON)
	}
}

func TestResolveOverlaps_DoesNotCompareAcrossFields(t *testing.T) {
	policy := offsetBugPolicy()

	evaluated, _, ok := DetectJSON(nestedPayload, policy.Spec.Detectors, policy, "JOB_PAYLOAD", "TEST")
	if !ok {
		t.Fatal("DetectJSON reported the payload as non-JSON")
	}

	resolved := ResolveOverlaps(evaluated)

	// None of these five findings overlap: they are five different fields. The
	// broken version compared leaf-relative offsets across fields, so findings
	// sitting at 0..12 in their OWN values looked like conflicts and all but one
	// were discarded -- a 5-finding payload collapsed to 1.
	if len(resolved) != len(evaluated) {
		t.Errorf("ResolveOverlaps dropped findings from different fields: %d in, %d out", len(evaluated), len(resolved))
	}
}
