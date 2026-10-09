package unit_test

// RFC-006 §11 IGNORE.

import (
	"MTL_Scheduler_PII_Test/internal/pii"
	"testing"

	"MTL_Scheduler_PII_Test/internal/models"
)

func ruleOf(id, action string) models.PolicyRule {
	return models.PolicyRule{ID: id, Action: models.PolicyAction{Type: action}}
}

func TestDropIgnored_RemovesOnlyIgnoreFindingsAndKeepsOrder(t *testing.T) {
	in := []pii.EvaluatedFinding{
		{Rule: ruleOf("a", "REDACT")}, {Rule: ruleOf("b", "IGNORE")}, {Rule: ruleOf("c", "MASK")},
		{Rule: ruleOf("d", "ignore")}, {Rule: ruleOf("e", "BLOCK")}, {Rule: ruleOf("f", "OBSERVE")},
	}
	got := pii.DropIgnored(in)

	var ids []string
	for _, f := range got {
		ids = append(ids, f.Rule.ID)
	}
	want := []string{"a", "c", "e", "f"}
	if len(ids) != len(want) {
		t.Fatalf("kept %v, want %v", ids, want)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("kept %v, want %v (action matching is case-insensitive and order is preserved)", ids, want)
		}
	}
	if len(pii.DropIgnored(nil)) != 0 {
		t.Fatal("nil must be safe")
	}
}

func ignorePolicy(rules ...models.PolicyRule) models.PIIPolicy {
	var p models.PIIPolicy
	p.Spec.EvaluationMode = "FIRST_MATCH"
	p.Spec.Defaults.Action = "OBSERVE"
	p.Spec.Detectors = []models.DetectorDefinition{
		{ID: "d-email", PIIType: "Email", Type: "REGEX", Enabled: true, Pattern: `[^\s@"]+@[^\s@"]+\.[^\s@"]+`, MinimumConfidence: 1},
		{ID: "d-any", PIIType: "Contact", Type: "REGEX", Enabled: true, Pattern: `[^\s@"]+@[^\s@"]+\.[^\s@"]+`, MinimumConfidence: 1},
	}
	p.Spec.Rules = rules
	return p
}

func scan(payload string, p models.PIIPolicy) string {
	evaluated, _, _ := pii.DetectJSON(payload, p.Spec.Detectors, p, "JOB_PAYLOAD", "T", "")
	evaluated = pii.DropIgnored(pii.ResolveOverlaps(evaluated))
	return pii.ApplyFindingsToJSON(payload, evaluated)
}

// "known-safe fields ... without disabling the detector globally"
func TestIgnore_AFieldScopedRuleSparesOneFieldAndNotAnother(t *testing.T) {
	p := ignorePolicy(
		models.PolicyRule{ID: "ignore-test-account", Priority: 200, Match: models.MatchConditions{PIITypes: []string{"Email"}, FieldPaths: []string{"test_account.email"}}, Action: models.PolicyAction{Type: "IGNORE"}},
		models.PolicyRule{ID: "redact-email", Priority: 100, Match: models.MatchConditions{PIITypes: []string{"Email"}}, Action: models.PolicyAction{Type: "REDACT"}},
	)

	out := scan(`{"test_account":{"email":"qa@example.com"},"customer":{"email":"jane@example.com"}}`, p)

	if want := `{"customer":{"email":"[Email-REDACTED]"},"test_account":{"email":"qa@example.com"}}`; out != want {
		t.Fatalf("the ignored field must be untouched and the other redacted:\n got  %s\n want %s", out, want)
	}
}

func TestIgnore_ABetterRuleOnTheSameTextBeatsAnOverlappingRedact(t *testing.T) {
	// two detectors match the SAME text; the IGNORE rule has the higher priority, so the text is left alone
	p := ignorePolicy(
		models.PolicyRule{ID: "ignore-it", Priority: 200, Match: models.MatchConditions{DetectorIDs: []string{"d-email"}}, Action: models.PolicyAction{Type: "IGNORE"}},
		models.PolicyRule{ID: "redact-it", Priority: 100, Match: models.MatchConditions{DetectorIDs: []string{"d-any"}}, Action: models.PolicyAction{Type: "REDACT"}},
	)
	payload := `{"e":"qa@example.com"}`

	if out := scan(payload, p); out != payload {
		t.Fatalf("an IGNORE with the higher priority must win the text, got %s", out)
	}
}

func TestIgnore_AHigherPriorityRedactStillWinsOverAnIgnore(t *testing.T) {
	p := ignorePolicy(
		models.PolicyRule{ID: "ignore-it", Priority: 100, Match: models.MatchConditions{DetectorIDs: []string{"d-email"}}, Action: models.PolicyAction{Type: "IGNORE"}},
		models.PolicyRule{ID: "redact-it", Priority: 200, Match: models.MatchConditions{DetectorIDs: []string{"d-any"}}, Action: models.PolicyAction{Type: "REDACT"}},
	)

	if out := scan(`{"e":"qa@example.com"}`, p); out != `{"e":"[Contact-REDACTED]"}` {
		t.Fatalf("priority decides, and IGNORE is not special: got %s", out)
	}
}
