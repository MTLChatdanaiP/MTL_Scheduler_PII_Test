package alerts

import (
	"MTL_Scheduler_PII_Test/internals/models"
	"slices"
)

func annotationFindRuleFromType(rules models.AlertRules, annotationType string) (models.AlertRule, bool) {

	var found models.AlertRule
	matched := false

	for _, rule := range rules.Spec.Rules {
		if !rule.Enabled || rule.Source != "ANNOTATION" || rule.AnnotationType != annotationType {
			continue
		}
		if !matched || winsOver(rule, found) {
			found = rule
			matched = true
		}
	}

	return found, matched
}

func winsOver(a, b models.AlertRule) bool {
	if a.Priority != b.Priority {
		return a.Priority > b.Priority
	}

	return a.ID < b.ID
}

func scopeMatches(scope models.RuleScope, sample MetricSample) bool {
	switch scope.Type {
	case "", "GLOBAL":
		return true
	case "QUEUE", "SCHEDULE":
		return slices.Contains(scope.Values, sample.SubjectID)
	case "JOB_TYPE", "PII_CATEGORY":
		return slices.Contains(scope.Values, sample.ScopeValue)
	}
	return false
}

func conditionMatches(rule models.AlertRule, sample MetricSample) bool {
	switch rule.Operator {
	case "GT":
		return sample.Numeric > rule.Threshold
	case "GTE":
		return sample.Numeric >= rule.Threshold
	case "LT":
		return sample.Numeric < rule.Threshold
	case "LTE":
		return sample.Numeric <= rule.Threshold
	case "EQ":
		return sample.Text == rule.TextValue
	case "NEQ":
		return sample.Text != rule.TextValue
	}
	return false
}
