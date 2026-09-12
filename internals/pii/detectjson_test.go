package pii

// TEMP test, delete once the offset/overlap bug is fixed.
// Run with: go test ./internals/pii/ -run TestDetectJSON_OffsetBug -v -count=1
// -count=1 disables Go's test cache so every rerun actually re-executes.

import (
	"fmt"
	"testing"

	"MTL_Scheduler_PII_Test/internals/models"
)

func TestDetectJSON_OffsetBug(t *testing.T) {
	detectors := []models.DetectorDefinition{
		{ID: "builtin-email", PIIType: "Email", Type: "REGEX", Enabled: true, Pattern: `[^\s@]+@[^\s@]+\.[^\s@]+`, MinimumConfidence: 1.0},
		{ID: "builtin-phone", PIIType: "Phone", Type: "REGEX", Enabled: true, Pattern: `\d{3}-\d{3}-\d{4}`, MinimumConfidence: 1.0},
		{ID: "builtin-ssn", PIIType: "SSN", Type: "REGEX", Enabled: true, Pattern: `\d{3}-\d{2}-\d{4}`, MinimumConfidence: 1.0},
		{ID: "builtin-creditcard", PIIType: "CreditCard", Type: "REGEX", Enabled: true, Pattern: `\d{4}-\d{4}-\d{4}-\d{4}`, MinimumConfidence: 1.0},
	}

	// Policy built inline, not loaded from policies/default.json, so this test
	// never breaks because of a checksum edit -- it's testing the pipeline,
	// not the file.
	var policy models.PIIPolicy
	policy.Spec.EvaluationMode = "FIRST_MATCH"
	policy.Spec.Defaults.Action = "OBSERVE"
	policy.Spec.Detectors = detectors
	policy.Spec.Rules = []models.PolicyRule{
		{
			ID:       "redact-phone-ssn",
			Priority: 0,
			Match: models.MatchConditions{
				Sources:     []string{"JOB_PAYLOAD"},
				PIITypes:    []string{"Phone", "SSN"},
				DetectorIDs: []string{"builtin-phone", "builtin-ssn"},
			},
			Action: models.PolicyAction{Type: "REDACT"},
		},
		{
			ID:       "mask-creditcard",
			Priority: 1,
			Match: models.MatchConditions{
				Sources:     []string{"JOB_PAYLOAD"},
				PIITypes:    []string{"CreditCard"},
				DetectorIDs: []string{"builtin-creditcard"},
			},
			Action: models.PolicyAction{Type: "MASK", Mask: models.MaskConfig{Strategy: "FULL", MaskCharacter: "*"}},
		},
	}

	payload := `{"email": "jane.doe@example.com", "ssn": "123-45-6789", "creditcard": "4111-1111-1111-1111", "supportPhone": "555-000-1111", "customers": [{"name": "John", "phone": "555-123-4567"}, {"name": "Amy", "phone": "555-987-6543"}]}`

	evaluated, failed, ok := DetectJSON(payload, detectors, policy, "JOB_PAYLOAD", "CUSTOMER_UPDATE")
	if !ok {
		t.Fatalf("payload should be valid JSON, DetectJSON returned ok=false")
	}
	if len(failed) > 0 {
		t.Fatalf("unexpected failed detectors: %v", failed)
	}

	t.Logf("--- raw findings BEFORE ResolveOverlaps: %d ---", len(evaluated))
	for _, e := range evaluated {
		t.Logf("  %-10s %-20s path=%-25s start=%-3d end=%-3d match=%-20q action=%s",
			e.Finding.Type, e.Finding.DetectorID, e.Finding.FieldPath,
			e.Finding.Start, e.Finding.End, e.Finding.Match, e.Rule.Action.Type)
	}

	// This is the real production call path -- create.go runs ResolveOverlaps
	// right after DetectJSON, same as here.
	resolved := ResolveOverlaps(evaluated)
	t.Logf("--- findings AFTER ResolveOverlaps: %d (started with %d) ---", len(resolved), len(evaluated))

	var transformations []Transformation
	for _, e := range resolved {
		switch e.Rule.Action.Type {
		case "REDACT":
			transformations = append(transformations, Transformation{
				Start: e.Finding.Start, End: e.Finding.End,
				Replacement: fmt.Sprintf("[%s-REDACTED]", e.Finding.Type),
			})
		case "MASK":
			transformations = append(transformations, Transformation{
				Start: e.Finding.Start, End: e.Finding.End,
				Replacement: Mask(e.Finding.Match, e.Rule.Action.Mask),
			})
		}
	}

	final := ApplyTransformations(payload, transformations)
	t.Logf("ORIGINAL: %s", payload)
	t.Logf("FINAL:    %s", final)

	// Expected once fixed: 6 findings survive ResolveOverlaps (email, ssn,
	// creditcard, 3x phone -- none of these six actually overlap each other,
	// they're six different fields), and FINAL still parses as valid JSON
	// with every matched value replaced and nothing else touched.
	if len(resolved) != 6 {
		t.Errorf("expected 6 surviving findings (email, ssn, creditcard, 3x phone), got %d -- ResolveOverlaps is likely comparing leaf-relative offsets across different fields as if they overlap", len(resolved))
	}
}
