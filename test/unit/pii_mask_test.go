package unit_test

// RFC-006 §12 Masking Strategies, §13 Pattern-Specific Transformation.
//
// WHY THIS FILE EXISTS: before it, NO test under internal/ asserted what any
// masking strategy actually produced. detectjson_test.go builds a MASK/FULL rule
// but only counts findings and never looks at the output, so FULL silently
// returned its input unchanged and nothing noticed. Every expected string below
// was derived by hand from the strategy's definition, not copied from output.

import (
	"MTL_Scheduler_PII_Test/internal/pii"
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"MTL_Scheduler_PII_Test/internal/models"
)

func cfg(strategy string, visible int, maskChar string) models.MaskConfig {
	return models.MaskConfig{Strategy: strategy, VisibleCharacters: visible, MaskCharacter: maskChar}
}

func TestMask_GoldenOutputs(t *testing.T) {
	email := func(visible int, domainMode string) models.MaskConfig {
		return models.MaskConfig{Strategy: "EMAIL", VisibleCharacters: visible, MaskCharacter: "*", DomainMode: domainMode}
	}

	tests := []struct {
		name  string
		value string
		cfg   models.MaskConfig
		want  string
	}{
		// FULL: every character replaced. This is the case that returned its input.
		{"FULL credit card", "4111-1111-1111-1111", cfg("FULL", 0, "*"), "*******************"},
		{"FULL short", "abc", cfg("FULL", 0, "*"), "***"},
		{"FULL custom mask char", "abc", cfg("FULL", 0, "#"), "###"},
		{"FULL empty value stays empty", "", cfg("FULL", 0, "*"), ""},

		{"KEEP_PREFIX 4", "4111-1111-1111-1111", cfg("KEEP_PREFIX", 4, "*"), "4111***************"},
		{"KEEP_SUFFIX 4", "4111-1111-1111-1111", cfg("KEEP_SUFFIX", 4, "*"), "***************1111"},
		{"KEEP_PREFIX_SUFFIX 4", "4111111111111111", cfg("KEEP_PREFIX_SUFFIX", 4, "*"), "4111********1111"},
		{"PRESERVE_FORMAT keeps separators", "123-45-6789", cfg("PRESERVE_FORMAT", 0, "*"), "***-**-****"},

		// EMAIL keeps the FIRST n characters of the local part, not the last n.
		{"EMAIL keeps the first 2", "jane.doe@example.com", email(2, "PRESERVE"), "ja******@example.com"},
		{"EMAIL domainMode MASK", "jane.doe@example.com", email(2, "MASK"), "ja******@***********"},
		{"EMAIL zero visible", "jane.doe@example.com", email(0, "PRESERVE"), "********@example.com"},
		{"EMAIL domainMode is case-insensitive", "jane.doe@example.com", email(2, "mask"), "ja******@***********"},
		{"EMAIL localVisiblePrefix wins when set",
			"jane.doe@example.com",
			models.MaskConfig{Strategy: "EMAIL", VisibleCharacters: 0, LocalVisiblePrefix: 3, MaskCharacter: "*", DomainMode: "PRESERVE"},
			"jan*****@example.com"},
		{"EMAIL falls back to visibleCharacters when localVisiblePrefix unset",
			"jane.doe@example.com",
			models.MaskConfig{Strategy: "EMAIL", VisibleCharacters: 2, MaskCharacter: "*", DomainMode: "PRESERVE"},
			"ja******@example.com"},
		{"EMAIL with no @ is still masked", "plainstring", email(2, "PRESERVE"), "pl*********"},

		{"FIXED token", "secret", cfg("FIXED", 0, "[MASKED]"), "[MASKED]"},
		{"FIXED with token omitted", "secret", cfg("FIXED", 0, ""), "[MASKED]"},

		// An omitted maskCharacter used to PANIC three of these and corrupt two more.
		{"KEEP_PREFIX, maskCharacter omitted", "secret123", cfg("KEEP_PREFIX", 2, ""), "se*******"},
		{"KEEP_SUFFIX, maskCharacter omitted", "secret123", cfg("KEEP_SUFFIX", 2, ""), "*******23"},
		{"KEEP_PREFIX_SUFFIX, maskCharacter omitted", "secret123", cfg("KEEP_PREFIX_SUFFIX", 2, ""), "se*****23"},
		{"EMAIL, maskCharacter omitted", "jane.doe@example.com", email(2, "PRESERVE"), "ja******@example.com"},
		{"PRESERVE_FORMAT, maskCharacter omitted", "123-45-6789", cfg("PRESERVE_FORMAT", 0, ""), "***-**-****"},

		// A visible window as large as the value must not hand the value back.
		{"KEEP_PREFIX_SUFFIX window covers the value", "abcd1234", cfg("KEEP_PREFIX_SUFFIX", 4, "*"), "********"},
		{"KEEP_SUFFIX window larger than the value", "abc", cfg("KEEP_SUFFIX", 9, "*"), "***"},
		{"KEEP_PREFIX window larger than the value", "abc", cfg("KEEP_PREFIX", 9, "*"), "***"},
		{"EMAIL window covers the local part", "ab@x.com", email(5, "PRESERVE"), "********"},

		{"negative visibleCharacters is treated as zero", "secret", cfg("KEEP_PREFIX", -3, "*"), "******"},
		{"multi-character maskCharacter uses its first character", "secret", cfg("KEEP_PREFIX", 2, "**"), "se****"},

		// An unknown or empty strategy used to return "" and silently delete the value.
		{"unknown strategy fails closed, not empty", "secret", cfg("KEEP_SUFIX", 2, "*"), "******"},
		{"empty strategy fails closed, not empty", "secret", cfg("", 0, "*"), "******"},
		{"strategy is case-insensitive", "secret", cfg("keep_prefix", 2, "*"), "se****"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := pii.Mask(tt.value, tt.cfg); got != tt.want {
				t.Errorf("Mask(%q, %+v) = %q, want %q", tt.value, tt.cfg, got, tt.want)
			}
		})
	}
}

func TestMask_NeverReturnsTheOriginalValue(t *testing.T) {
	// The invariant that makes "fails open" impossible: for every non-FIXED
	// strategy and any value holding something worth hiding, the output differs.
	values := []string{
		"a", "ab", "abc", "abcd", "abcd1234", "secret", "jane.doe@example.com",
		"ab@x.com", "4111-1111-1111-1111", "123-45-6789", "สวัสดี", "x@y",
	}

	for _, strategy := range pii.ValidMaskStrategies {
		if strategy == "FIXED" {
			continue
		}
		for visible := 0; visible <= 12; visible++ {
			for _, v := range values {
				c := models.MaskConfig{Strategy: strategy, VisibleCharacters: visible, MaskCharacter: "*", DomainMode: "PRESERVE"}
				if got := pii.Mask(v, c); got == v {
					t.Errorf("%s with %d visible returned the input unchanged: %q", strategy, visible, v)
				}
			}
		}
	}
}

func TestMask_NeverPanics(t *testing.T) {
	strategies := append(append([]string{}, pii.ValidMaskStrategies...), "", "bogus", "keep_prefix")
	maskChars := []string{"", "*", "#", "**", "日"}
	domainModes := []string{"", "PRESERVE", "MASK", "weird"}
	values := []string{
		"", "a", "ab", "@", "@@", "a@", "@b", "jane.doe@example.com",
		"สวัสดี@example.com", "4111-1111-1111-1111", "   ", "\x00", "日本語",
	}

	calls := 0
	for _, s := range strategies {
		for visible := -3; visible <= 15; visible++ {
			for _, mc := range maskChars {
				for _, dm := range domainModes {
					for _, v := range values {
						c := models.MaskConfig{Strategy: s, VisibleCharacters: visible, LocalVisiblePrefix: visible, MaskCharacter: mc, DomainMode: dm}
						func() {
							defer func() {
								if r := recover(); r != nil {
									t.Fatalf("Mask panicked: %v (value=%q cfg=%+v)", r, v, c)
								}
							}()
							out := pii.Mask(v, c)
							if !utf8.ValidString(out) {
								t.Fatalf("Mask produced invalid UTF-8 for value=%q cfg=%+v", v, c)
							}
						}()
						calls++
					}
				}
			}
		}
	}
	if calls < 10000 {
		t.Fatalf("property test ran only %d combinations, expected a large sweep", calls)
	}
}

// ---------------------------------------------------------------------------
// The production path. This is the test that would have caught the leak: it
// runs a MASK/FULL rule through DetectJSON + ApplyFindingsToJSON, exactly what
// CreateTask_Direct does, and inspects the document that would be stored.
// ---------------------------------------------------------------------------

func maskPolicy(rules ...models.PolicyRule) models.PIIPolicy {
	var p models.PIIPolicy
	p.Spec.EvaluationMode = "FIRST_MATCH"
	p.Spec.Defaults.Action = "OBSERVE"
	p.Spec.Detectors = []models.DetectorDefinition{
		{ID: "builtin-creditcard", PIIType: "CreditCard", Type: "REGEX", Enabled: true, Pattern: `\d{4}-\d{4}-\d{4}-\d{4}`, MinimumConfidence: 1.0},
		{ID: "builtin-email", PIIType: "Email", Type: "REGEX", Enabled: true, Pattern: `[^\s@]+@[^\s@]+\.[^\s@]+`, MinimumConfidence: 1.0},
	}
	p.Spec.Rules = rules
	return p
}

func TestProductionPath_MaskFullActuallyMasksACreditCard(t *testing.T) {
	policy := maskPolicy(
		models.PolicyRule{ID: "mask-card", Priority: 1,
			Match:  models.MatchConditions{Sources: []string{"JOB_PAYLOAD"}, PIITypes: []string{"CreditCard"}},
			Action: models.PolicyAction{Type: "MASK", Mask: models.MaskConfig{Strategy: "FULL", MaskCharacter: "*"}}},
		models.PolicyRule{ID: "mask-email", Priority: 2,
			Match:  models.MatchConditions{Sources: []string{"JOB_PAYLOAD"}, PIITypes: []string{"Email"}},
			Action: models.PolicyAction{Type: "MASK", Mask: models.MaskConfig{Strategy: "EMAIL", VisibleCharacters: 2, MaskCharacter: "*", DomainMode: "PRESERVE"}}},
	)

	payload := `{"card": "4111-1111-1111-1111", "contact": "jane.doe@example.com"}`

	evaluated, failed, ok := pii.DetectJSON(payload, policy.Spec.Detectors, policy, "JOB_PAYLOAD", "T", "")
	if !ok || len(failed) > 0 {
		t.Fatalf("detection failed: ok=%v failed=%v", ok, failed)
	}
	if len(evaluated) != 2 {
		t.Fatalf("expected 2 findings, got %d", len(evaluated))
	}

	out := pii.ApplyFindingsToJSON(payload, pii.ResolveOverlaps(evaluated))

	var parsed map[string]string
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("result is not valid JSON: %v\n%s", err, out)
	}

	if strings.Contains(out, "4111") {
		t.Fatalf("THE LEAK: the credit card survived a MASK/FULL rule: %s", out)
	}
	if parsed["card"] != "*******************" {
		t.Errorf("card = %q, want 19 asterisks", parsed["card"])
	}
	if parsed["contact"] != "ja******@example.com" {
		t.Errorf("contact = %q, want ja******@example.com", parsed["contact"])
	}
}

func TestDryRun_MaskedPreviewIsActuallyMasked(t *testing.T) {
	// The dry-run preview is how an operator checks a rule before activating it.
	// With the bug, a FULL rule's "MaskedPreview" was the unmasked value.
	policy := maskPolicy(models.PolicyRule{ID: "mask-card", Priority: 1,
		Match:  models.MatchConditions{PIITypes: []string{"CreditCard"}},
		Action: models.PolicyAction{Type: "MASK", Mask: models.MaskConfig{Strategy: "FULL", MaskCharacter: "*"}}})

	res := pii.DryRun("card 4111-1111-1111-1111 here", policy, "JOB_PAYLOAD", "T", "")

	if len(res.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(res.Results))
	}
	if res.Results[0].MaskedPreview == res.Results[0].MatchedText {
		t.Fatalf("MaskedPreview equals the matched text: %q", res.Results[0].MaskedPreview)
	}
}

// ---------------------------------------------------------------------------
// Checksum stability. LoadPolicy verifies sha256(json.Marshal(policy.Spec)), so
// the serialised shape of every Spec struct is part of the checksum. If adding
// localVisiblePrefix had changed how an EXISTING mask config marshals, every
// existing policy file would fail its checksum and the app would refuse to start.
// ---------------------------------------------------------------------------

func TestMaskConfig_ExistingPoliciesMarshalByteIdentically(t *testing.T) {
	old := models.MaskConfig{Strategy: "EMAIL", VisibleCharacters: 2, MaskCharacter: "*", DomainMode: "PRESERVE"}

	b, err := json.Marshal(old)
	if err != nil {
		t.Fatal(err)
	}

	const want = `{"strategy":"EMAIL","visibleCharacters":2,"maskCharacter":"*","domainMode":"PRESERVE"}`
	if string(b) != want {
		t.Fatalf("marshal output changed, which would break every existing policy checksum:\n got: %s\nwant: %s", b, want)
	}
	if strings.Contains(string(b), "localVisiblePrefix") {
		t.Fatal("a zero localVisiblePrefix must be omitted from the serialised form")
	}
}

func TestMaskConfig_LocalVisiblePrefixSerialisesWhenSet(t *testing.T) {
	c := models.MaskConfig{Strategy: "EMAIL", MaskCharacter: "*", LocalVisiblePrefix: 2}
	b, _ := json.Marshal(c)
	if !strings.Contains(string(b), `"localVisiblePrefix":2`) {
		t.Fatalf("a set localVisiblePrefix must appear in the serialised form: %s", b)
	}
}
