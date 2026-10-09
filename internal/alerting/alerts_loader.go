package alerts

import (
	"MTL_Scheduler_PII_Test/internal/events"
	"MTL_Scheduler_PII_Test/internal/models"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
)

var LoadedAlertRules atomic.Pointer[models.AlertRules]

func GetLoadedRules() models.AlertRules {
	return *LoadedAlertRules.Load()
}

func LoadRules(path string) (models.AlertRules, error) {

	data, err := os.ReadFile(path)
	if err != nil {
		return models.AlertRules{}, err
	}

	var alert models.AlertRules
	if err := json.Unmarshal(data, &alert); err != nil {
		return models.AlertRules{}, err
	}

	alertSpec, err := json.Marshal(alert.Spec)
	if err != nil {
		return models.AlertRules{}, err
	}

	hash := sha256.Sum256(alertSpec)

	result := hex.EncodeToString(hash[:])

	if result != alert.Metadata.Checksum {
		return models.AlertRules{}, fmt.Errorf("alerts checksum mismatch: expected %s, computed %s", alert.Metadata.Checksum, result)
	}

	problems := ValidateRules(alert)

	if len(problems) > 0 {
		return models.AlertRules{}, fmt.Errorf("alert rules validation failed:\n%s", strings.Join(problems, "\n"))
	}
	return alert, nil
}

var validSeverities = map[string]struct{}{
	"INFO":     {},
	"WARNING":  {},
	"CRITICAL": {},
}

var validScopeTypes = map[string]struct{}{
	"":             {},
	"GLOBAL":       {},
	"QUEUE":        {},
	"JOB_TYPE":     {},
	"SCHEDULE":     {},
	"PII_CATEGORY": {},
}

func ValidateRules(alertRules models.AlertRules) []string {
	var problems []string

	validRules := make(map[string]bool)

	for i, rule := range alertRules.Spec.Rules {
		if rule.ID == "" {
			problems = append(problems, fmt.Sprintf("ruleID at index %d is nil", i))
			continue
		}
		if _, exists := validRules[rule.ID]; exists {
			problems = append(problems, fmt.Sprintf("rule at index %d with ID %q already exists", i, rule.ID))
			continue
		}
		validRules[rule.ID] = true
		if rule.Source != "ANNOTATION" && rule.Source != "METRIC" {
			problems = append(problems,
				fmt.Sprintf(
					"rule %q: Source must be ANNOTATION or METRIC, got %q",
					rule.ID,
					rule.Source,
				),
			)
		}
		// A typo in a metric name or an operator did not fail anywhere: the rule simply never fired.
		problems = append(problems, validateRuleCondition(rule)...)
		if _, ok := validSeverities[rule.Severity]; !ok {
			problems = append(problems,
				fmt.Sprintf(
					"rule %q: Severity must be INFO, WARNING, or CRITICAL | Got: %q",
					rule.ID, rule.Severity,
				),
			)
		}
		if rule.AlertType == "" {
			problems = append(problems,
				fmt.Sprintf(
					"rule %q: AlertType is required",
					rule.ID,
				),
			)
		}
		if _, ok := validScopeTypes[rule.Scope.Type]; !ok {
			problems = append(problems,
				fmt.Sprintf(
					"rule %q: invalid Scope.Type",
					rule.ID,
				),
			)
		} else {
			if (rule.Scope.Type != "GLOBAL" && rule.Scope.Type != "") &&
				len(rule.Scope.Values) == 0 {
				problems = append(problems,
					fmt.Sprintf(
						"rule %q: Scope.Values cannot be empty for %q scope",
						rule.ID,
						rule.Scope.Type,
					),
				)
			}
		}
	}

	return problems
}

func ActivateRules(ctx context.Context, path string, source string) (models.AlertRules, error) {
	rules, err := LoadRules(path)

	if err != nil {
		events.LogEvent(ctx, "system", "alerting.alert_reload_failed", source)
		return models.AlertRules{}, err
	}

	LoadedAlertRules.Store(&rules)
	events.LogEvent(ctx, "system", "alerting.alert_activated", source)

	return rules, nil
}

// validOperators are the comparison operators conditionMatches understands.
var validOperators = map[string]bool{"GT": true, "GTE": true, "LT": true, "LTE": true, "EQ": true, "NEQ": true}

// validateRuleCondition takes one rule and returns a message for every reason its condition could never work:
// a METRIC rule naming a metric no resolver provides or an operator conditionMatches does not know (or EQ/NEQ
// with nothing to compare to), and an ANNOTATION rule with no annotation type.
func validateRuleCondition(rule models.AlertRule) []string {
	var problems []string

	switch rule.Source {
	case "METRIC":
		if _, ok := MetricResolvers[rule.Metric]; !ok {
			problems = append(problems, fmt.Sprintf("rule %q: unknown metric %q, so it can never fire", rule.ID, rule.Metric))
		}
		switch {
		case !validOperators[rule.Operator]:
			problems = append(problems, fmt.Sprintf("rule %q: unknown operator %q (use GT, GTE, LT, LTE, EQ or NEQ), so it can never fire", rule.ID, rule.Operator))
		case (rule.Operator == "EQ" || rule.Operator == "NEQ") && rule.TextValue == "":
			problems = append(problems, fmt.Sprintf("rule %q: operator %s needs a textValue to compare to", rule.ID, rule.Operator))
		}
	case "ANNOTATION":
		if rule.AnnotationType == "" {
			problems = append(problems, fmt.Sprintf("rule %q: an ANNOTATION rule needs an annotationType", rule.ID))
		}
	}

	return problems
}
