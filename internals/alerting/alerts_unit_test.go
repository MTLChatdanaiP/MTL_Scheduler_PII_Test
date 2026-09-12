package alerts

// RFC-007 §5, §7, §8. In-package because severityFor, subjectTypeFor and
// summaryFor are unexported.
//
// Everything here is pure -- no database, no Redis, no sweep. That is
// deliberate: these run in CI's unit step in milliseconds, and a failure points
// at one function rather than at "something in the pipeline".

import (
	"strings"
	"testing"

	"MTL_Scheduler_PII_Test/internals/models"
)

func TestSubjectTypeFor(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		// The only value that actually differs between the two vocabularies:
		// monitoring writes TASK, RFC-007 §7's subject list says RUN.
		{"TASK translates to RUN", "TASK", "RUN"},

		// These already agree, so they pass through untouched.
		{"QUEUE passes through", "QUEUE", "QUEUE"},
		{"SCHEDULE passes through", "SCHEDULE", "SCHEDULE"},

		// Not produced by anything yet, but the passthrough default means they
		// will already be correct when they are. This is why no explicit case is
		// needed for them.
		{"WORKER passes through", "WORKER", "WORKER"},
		{"PII_FINDING passes through", "PII_FINDING", "PII_FINDING"},

		{"empty passes through", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := subjectTypeFor(tt.in); got != tt.want {
				t.Errorf("subjectTypeFor(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestSummaryFor(t *testing.T) {
	got := summaryFor("RUN_STUCK", "RUN", "01M22TAAJJF37365S3H0Z5E4PA")

	for _, want := range []string{"RUN_STUCK", "RUN", "01M22TAAJJF37365S3H0Z5E4PA"} {
		if !strings.Contains(got, want) {
			t.Errorf("summaryFor() = %q, expected it to contain %q", got, want)
		}
	}
}

func TestSummaryFor_DoesNotDoubleTranslate(t *testing.T) {
	// summaryFor receives an ALREADY-translated subject type from the creation
	// site. If it translated again internally, passing "TASK" here would produce
	// "RUN" -- and the summary would disagree with the SubjectType column
	// whenever the caller's and the function's translation state differed.
	got := summaryFor("QUEUE_BACKLOG", "TASK", "job-1")

	if !strings.Contains(got, "TASK") {
		t.Errorf("summaryFor() = %q -- it translated TASK internally, but the caller already translates", got)
	}
}

// ---------------------------------------------------------------------------
// RFC-007 §8 rule validation
//
// A rules file is the one place a silent mistake is most expensive: an invalid
// rule that loads cleanly simply never fires, and nothing tells you. Every case
// below is a rule that would otherwise do exactly that.
// ---------------------------------------------------------------------------

// validRule returns a rule with nothing wrong with it, for tests to break one
// field at a time. Building each case from a known-good baseline means a
// failure points at the field that was changed rather than at whichever check
// happens to run first.
func validRule() models.AlertRule {
	return models.AlertRule{
		ID:        "valid-rule",
		Enabled:   true,
		Source:    "ANNOTATION",
		AlertType: "RUN_STUCK",
		Severity:  "CRITICAL",
		Scope:     models.RuleScope{Type: "GLOBAL"},
	}
}

func rulesDoc(rules ...models.AlertRule) models.AlertRules {
	var doc models.AlertRules
	doc.Spec.Rules = rules
	return doc
}

func TestValidateRules_AcceptsValidRules(t *testing.T) {
	problems := ValidateRules(rulesDoc(validRule()))

	if len(problems) != 0 {
		t.Errorf("valid rule reported %d problems: %v", len(problems), problems)
	}
}

func TestValidateRules_AcceptsAbsentScope(t *testing.T) {
	// An omitted scope means GLOBAL, following the same
	// empty-means-no-restriction convention as MatchConditions. If "" is missing
	// from validScopeTypes, every rule that leaves scope out fails validation --
	// which would make the common case the broken one.
	r := validRule()
	r.Scope = models.RuleScope{}

	if problems := ValidateRules(rulesDoc(r)); len(problems) != 0 {
		t.Errorf("rule with no scope reported problems: %v", problems)
	}
}

func TestValidateRules_RejectsBrokenRules(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*models.AlertRule)
	}{
		{"empty ID", func(r *models.AlertRule) { r.ID = "" }},
		{"invalid source", func(r *models.AlertRule) { r.Source = "SOMETHING_ELSE" }},
		{"empty source", func(r *models.AlertRule) { r.Source = "" }},
		{"invalid severity", func(r *models.AlertRule) { r.Severity = "URGENT" }},
		{"empty severity", func(r *models.AlertRule) { r.Severity = "" }},
		{"empty alert type", func(r *models.AlertRule) { r.AlertType = "" }},
		{"invalid scope type", func(r *models.AlertRule) { r.Scope = models.RuleScope{Type: "PLANET"} }},
		// A non-GLOBAL scope with no values can never match anything. This is
		// the queues/labels failure from RFC-006 in a new place: the rule loads,
		// looks scoped, and is actually disabled.
		{"non-global scope with empty values", func(r *models.AlertRule) {
			r.Scope = models.RuleScope{Type: "QUEUE", Values: nil}
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := validRule()
			tt.mutate(&r)

			if problems := ValidateRules(rulesDoc(r)); len(problems) == 0 {
				t.Errorf("expected a problem for %s, got none", tt.name)
			}
		})
	}
}

func TestValidateRules_RejectsDuplicateIDs(t *testing.T) {
	a := validRule()
	b := validRule() // same ID

	problems := ValidateRules(rulesDoc(a, b))

	if len(problems) == 0 {
		t.Fatal("duplicate rule IDs were accepted")
	}

	// Duplicate IDs are not cosmetic: the §9 dedup key is built from rule_id, so
	// two rules sharing one ID produce alerts that look identical to the dedup
	// query, and one silently suppresses the other's alerts.
	joined := strings.Join(problems, " ")
	if !strings.Contains(joined, a.ID) {
		t.Errorf("duplicate-ID problem does not name the offending ID: %v", problems)
	}
}

func TestValidateRules_ReportsEveryProblemInOnePass(t *testing.T) {
	// The whole reason ValidateRules returns a slice rather than an error:
	// fixing a rules file one problem per restart is miserable. If this ever
	// regresses to stopping at the first failure, this test catches it.
	badSeverity := validRule()
	badSeverity.ID = "bad-severity"
	badSeverity.Severity = "NOPE"

	badSource := validRule()
	badSource.ID = "bad-source"
	badSource.Source = "NOPE"

	noAlertType := validRule()
	noAlertType.ID = "no-alert-type"
	noAlertType.AlertType = ""

	problems := ValidateRules(rulesDoc(badSeverity, badSource, noAlertType))

	if len(problems) < 3 {
		t.Errorf("expected at least 3 problems across 3 broken rules, got %d: %v", len(problems), problems)
	}
}
