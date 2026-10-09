package pii

// RFC-006 §13 (mask validation at activation), §33 (keys read outside the policy
// document, validated at startup).

import (
	"strings"
	"sync"
	"testing"

	"MTL_Scheduler_PII_Test/internal/models"
)

func ruleWith(id string, priority int, action models.PolicyAction) models.PolicyRule {
	return models.PolicyRule{ID: id, Priority: priority, Action: action}
}

func policyOf(rules ...models.PolicyRule) models.PIIPolicy {
	var p models.PIIPolicy
	p.Spec.EvaluationMode = "FIRST_MATCH"
	p.Spec.Defaults.Action = "OBSERVE"
	p.Spec.Rules = rules
	return p
}

func TestValidatePolicy_AcceptsEveryConfigThatWorkedBefore(t *testing.T) {
	// Leniency matters as much as strictness: a policy that loaded before this
	// change must still load, or the app refuses to start.
	policy := policyOf(
		ruleWith("redact", 1, models.PolicyAction{Type: "REDACT"}),
		ruleWith("observe", 2, models.PolicyAction{Type: "OBSERVE"}),
		ruleWith("mask-full", 3, models.PolicyAction{Type: "MASK", Mask: models.MaskConfig{Strategy: "FULL", MaskCharacter: "*"}}),
		ruleWith("mask-email", 4, models.PolicyAction{Type: "MASK", Mask: models.MaskConfig{Strategy: "EMAIL", VisibleCharacters: 2, MaskCharacter: "*", DomainMode: "PRESERVE"}}),
		ruleWith("mask-no-char", 5, models.PolicyAction{Type: "MASK", Mask: models.MaskConfig{Strategy: "KEEP_SUFFIX", VisibleCharacters: 4}}), // maskCharacter omitted: defaults to "*"
		ruleWith("mask-fixed-token", 6, models.PolicyAction{Type: "MASK", Mask: models.MaskConfig{Strategy: "FIXED", MaskCharacter: "[MASKED]"}}),
		ruleWith("lowercase-ok", 7, models.PolicyAction{Type: "mask", Mask: models.MaskConfig{Strategy: "keep_prefix", VisibleCharacters: 2, MaskCharacter: "#", DomainMode: "mask"}}),
		ruleWith("local-prefix", 8, models.PolicyAction{Type: "MASK", Mask: models.MaskConfig{Strategy: "EMAIL", LocalVisiblePrefix: 2, MaskCharacter: "*"}}),
	)

	if problems := ValidatePolicy(policy); len(problems) != 0 {
		t.Fatalf("a valid policy was rejected:\n%s", strings.Join(problems, "\n"))
	}
}

func TestValidatePolicy_RejectsEachBadActionWithExactlyOneMessage(t *testing.T) {
	tests := []struct {
		name     string
		action   models.PolicyAction
		contains string
	}{
		{"no action type", models.PolicyAction{}, "no action type"},
		{"MASK with no strategy", models.PolicyAction{Type: "MASK", Mask: models.MaskConfig{MaskCharacter: "*"}}, "no strategy"},
		{"misspelled strategy", models.PolicyAction{Type: "MASK", Mask: models.MaskConfig{Strategy: "EMAIL2", MaskCharacter: "*"}}, `"EMAIL2"`},
		{"multi-character maskCharacter", models.PolicyAction{Type: "MASK", Mask: models.MaskConfig{Strategy: "FULL", MaskCharacter: "**"}}, "single character"},
		{"negative visibleCharacters", models.PolicyAction{Type: "MASK", Mask: models.MaskConfig{Strategy: "KEEP_PREFIX", VisibleCharacters: -1, MaskCharacter: "*"}}, "visibleCharacters"},
		{"negative localVisiblePrefix", models.PolicyAction{Type: "MASK", Mask: models.MaskConfig{Strategy: "EMAIL", LocalVisiblePrefix: -1, MaskCharacter: "*"}}, "localVisiblePrefix"},
		{"bad domainMode", models.PolicyAction{Type: "MASK", Mask: models.MaskConfig{Strategy: "EMAIL", MaskCharacter: "*", DomainMode: "HIDE"}}, "domainMode"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			problems := ValidatePolicy(policyOf(ruleWith("r1", 1, tt.action)))
			if len(problems) != 1 {
				t.Fatalf("expected exactly 1 problem, got %d: %v", len(problems), problems)
			}
			if !strings.Contains(problems[0], tt.contains) || !strings.Contains(problems[0], `"r1"`) {
				t.Errorf("message %q should name the rule and mention %q", problems[0], tt.contains)
			}
		})
	}
}

// BLOCK used to be rejected as "not implemented". It is implemented now (RFC-006 §11): a task whose scan hits a BLOCK
// rule is stored as Blocked and never queued.
func TestValidatePolicy_BlockIsAnImplementedAction(t *testing.T) {
	p := policyOf(ruleWith("block-ssn", 1, models.PolicyAction{Type: "BLOCK"}))
	if problems := ValidatePolicy(p); len(problems) != 0 {
		t.Fatalf("BLOCK must be a valid action now: %v", problems)
	}
	lower := policyOf(ruleWith("block-ssn", 1, models.PolicyAction{Type: "block"}))
	if problems := ValidatePolicy(lower); len(problems) != 0 {
		t.Fatalf("action types are case-insensitive: %v", problems)
	}
}

func TestValidatePolicy_IgnoreIsAnImplementedAction(t *testing.T) {
	if problems := ValidatePolicy(policyOf(ruleWith("known-safe", 1, models.PolicyAction{Type: "IGNORE"}))); len(problems) != 0 {
		t.Fatalf("IGNORE must be a valid action now (RFC-006 §11): %v", problems)
	}
}

func TestValidatePolicy_StillRejectsAnActionNothingImplements(t *testing.T) {
	// FAIL_CLOSED appears in the RFC's scan-error discussion but is not a rule action; a typo is the same case
	for _, bad := range []string{"FAIL_CLOSED", "REDAC", "ALLOW"} {
		problems := ValidatePolicy(policyOf(ruleWith("r1", 1, models.PolicyAction{Type: bad})))
		if len(problems) != 1 || !strings.Contains(problems[0], "not implemented") {
			t.Fatalf("%s must be rejected loudly, got %v", bad, problems)
		}
	}
}

func TestValidatePolicy_FixedMayUseALongTokenButOtherStrategiesMayNot(t *testing.T) {
	ok := policyOf(ruleWith("fixed", 1, models.PolicyAction{Type: "MASK", Mask: models.MaskConfig{Strategy: "FIXED", MaskCharacter: "[REDACTED]"}}))
	if p := ValidatePolicy(ok); len(p) != 0 {
		t.Fatalf("FIXED with a long token should be valid: %v", p)
	}
}

func TestValidatePolicy_IgnoresMaskSettingsOnNonMaskRules(t *testing.T) {
	// garbage in a mask block is irrelevant when the action is REDACT
	p := policyOf(ruleWith("redact", 1, models.PolicyAction{Type: "REDACT", Mask: models.MaskConfig{Strategy: "NONSENSE", VisibleCharacters: -9}}))
	if problems := ValidatePolicy(p); len(problems) != 0 {
		t.Fatalf("a REDACT rule's unused mask block must not be validated: %v", problems)
	}
}

func TestValidatePolicy_ExistingChecksStillWork(t *testing.T) {
	// the new validation sits beside the old checks, it does not replace them
	p := policyOf(
		ruleWith("a", 1, models.PolicyAction{Type: "REDACT"}),
		ruleWith("b", 1, models.PolicyAction{Type: "REDACT"}),
	)
	problems := ValidatePolicy(p)
	if len(problems) != 1 || !strings.Contains(problems[0], "priority 1") {
		t.Fatalf("duplicate priority should still be reported, got %v", problems)
	}
}

func TestDryRun_ReportsPolicyProblemsBeforeActivation(t *testing.T) {
	bad := policyOf(ruleWith("typo", 1, models.PolicyAction{Type: "MASK", Mask: models.MaskConfig{Strategy: "KEEP_SUFIX", MaskCharacter: "*"}}))

	res := DryRun("anything", bad, "JOB_PAYLOAD", "T", "")

	if len(res.PolicyProblems) != 1 {
		t.Fatalf("dry run should surface the bad mask strategy, got %v", res.PolicyProblems)
	}
}

// ---------------------------------------------------------------------------
// Keys (RFC-006 §33)
// ---------------------------------------------------------------------------

func resetKeys() {
	keysOnce = sync.Once{}
	fingerprintKey = nil
	encryptKey = nil
}

const goodKeyA = "0123456789abcdef0123456789abcdef"
const goodKeyB = "fedcba9876543210fedcba9876543210"

func TestKeys_ASetAfterPackageInitIsStillUsed(t *testing.T) {
	// The bug: keys were read in package-level vars, i.e. before main() could load
	// a .env file. Here the environment is set only AFTER the package has
	// initialised, which is exactly what godotenv.Load() does -- and the key must
	// still be picked up.
	resetKeys()
	t.Cleanup(resetKeys)
	t.Setenv("PII_ENCR_KEY", goodKeyA)
	t.Setenv("PII_FINGERPRINT_KEY", goodKeyB)

	ct, err := Encrypt("jane@example.com")
	if err != nil {
		t.Fatalf("Encrypt failed even though PII_ENCR_KEY is set: %v", err)
	}
	pt, err := Decrypt(ct)
	if err != nil || pt != "jane@example.com" {
		t.Fatalf("round trip failed: %q, %v", pt, err)
	}

	// A round trip alone also works with an empty key, so assert the key that was
	// loaded is the one set after init, not an empty one.
	if string(encryptKey) != goodKeyA || string(fingerprintKey) != goodKeyB {
		t.Fatalf("keys set after package init were not picked up: enc=%d bytes, fingerprint=%d bytes", len(encryptKey), len(fingerprintKey))
	}
}

func TestKeys_CiphertextIsBoundToTheConfiguredKey(t *testing.T) {
	// A round trip alone proves nothing: an EMPTY key also round-trips with itself,
	// which is exactly the silent failure this fixes. What distinguishes a real key
	// is that data sealed under key A cannot be opened under key B.
	resetKeys()
	t.Cleanup(resetKeys)
	t.Setenv("PII_ENCR_KEY", goodKeyA)
	sealed, err := Encrypt("jane@example.com")
	if err != nil {
		t.Fatal(err)
	}

	resetKeys()
	t.Setenv("PII_ENCR_KEY", goodKeyB)
	if pt, err := Decrypt(sealed); err == nil {
		t.Fatalf("data sealed under one key opened under another (got %q): the key is not actually being used", pt)
	}
}

func TestKeys_FingerprintIsKeyed(t *testing.T) {
	resetKeys()
	t.Setenv("PII_ENCR_KEY", goodKeyA)
	t.Setenv("PII_FINGERPRINT_KEY", goodKeyA)
	first := Fingerprint("555-123-4567")

	resetKeys()
	t.Cleanup(resetKeys)
	t.Setenv("PII_FINGERPRINT_KEY", goodKeyB)
	second := Fingerprint("555-123-4567")

	if first == second {
		t.Fatal("two different fingerprint keys produced the same fingerprint, so it is not actually keyed")
	}
	if first == "" || second == "" {
		t.Fatal("fingerprint should never be empty")
	}
}

func TestKeys_MissingEncryptionKeyFailsClosed(t *testing.T) {
	resetKeys()
	t.Cleanup(resetKeys)
	t.Setenv("PII_ENCR_KEY", "")
	t.Setenv("PII_FINGERPRINT_KEY", goodKeyB)

	if _, err := Encrypt("secret"); err == nil {
		t.Fatal("Encrypt must refuse to run without a key, not silently use sha256(\"\")")
	}
	if _, err := Decrypt("anything"); err == nil {
		t.Fatal("Decrypt must refuse to run without a key")
	}
}

func TestValidateKeys(t *testing.T) {
	tests := []struct {
		name        string
		fingerprint string
		encrypt     string
		wantErr     string
	}{
		{"both good", goodKeyA, goodKeyB, ""},
		{"both missing", "", "", "PII_FINGERPRINT_KEY"},
		{"encryption key missing", goodKeyA, "", "PII_ENCR_KEY"},
		{"fingerprint key too short", "short", goodKeyB, "PII_FINGERPRINT_KEY must be at least 16"},
		{"encryption key too short", goodKeyA, "short", "PII_ENCR_KEY must be at least 16"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetKeys()
			t.Cleanup(resetKeys)
			t.Setenv("PII_FINGERPRINT_KEY", tt.fingerprint)
			t.Setenv("PII_ENCR_KEY", tt.encrypt)

			err := ValidateKeys()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("expected valid, got %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("expected an error mentioning %q, got %v", tt.wantErr, err)
			}
		})
	}
}
