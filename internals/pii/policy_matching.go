package pii

import (
	"slices"
	"sort"

	"MTL_Scheduler_PII_Test/internals/models"
)

type MatchContext struct {
	Source     string
	JobType    string
	Queue      string
	PIIType    string
	FieldPath  string // empty until §10 exists
	Confidence float64
	DetectorID string
	Labels     []string
}

func ruleMatches(rule models.PolicyRule, ctx MatchContext) bool {
	match := rule.Match

	if len(match.Sources) > 0 && !slices.Contains(match.Sources, ctx.Source) {
		return false
	}

	if len(match.JobTypes) > 0 && !slices.Contains(match.JobTypes, ctx.JobType) {
		return false
	}

	//if len(match.PIITypes) > 0 && !slices.Contains(match.PIITypes, ctx.PIIType) {
	//	return false
	//}

	if len(match.DetectorIDs) > 0 && !slices.Contains(match.DetectorIDs, ctx.DetectorID) {
		return false
	}

	if match.MinimumConfidence > 0 && ctx.Confidence < match.MinimumConfidence {
		return false
	}

	if len(match.FieldPaths) > 0 {
		matched := false

		for _, pattern := range match.FieldPaths {
			if fieldPathMatches(pattern, ctx.FieldPath) {
				matched = true
				break
			}
		}

		if !matched {
			return false
		}
	}

	//if len(match.Labels) > 0 {
	//	for _, requiredLabel := range match.Labels {
	//		if !slices.Contains(ctx.Labels, requiredLabel) {
	//			return false
	//		}
	//	}
	//}

	return true
}

func ResolveRule(ctx MatchContext, policy models.PIIPolicy) models.PolicyRule {
	rules := make([]models.PolicyRule, len(policy.Spec.Rules))
	copy(rules, policy.Spec.Rules)

	sort.Slice(rules, func(i, j int) bool {
		return rules[i].Priority > rules[j].Priority
	})

	for _, rule := range rules {
		DetectorIDs := rule.Match.DetectorIDs
		if !slices.Contains(DetectorIDs, ctx.DetectorID) {
			continue
		}
		if !ruleMatches(rule, ctx) {
			continue
		}
		return rule
	}

	return models.PolicyRule{
		ID: "default",
		Action: models.PolicyAction{
			Type: policy.Spec.Defaults.Action,
		},
	}
}

func ResolveAction(detectorID string, policy models.PIIPolicy) string {

	rules := make([]models.PolicyRule, len(policy.Spec.Rules))
	copy(rules, policy.Spec.Rules)

	sort.Slice(rules, func(i, j int) bool {
		return rules[i].Priority > rules[j].Priority
	})

	for _, rule := range rules {
		DetectorIDs := rule.Match.DetectorIDs
		for _, id := range DetectorIDs {
			if id == detectorID {
				return rule.Action.Type
			}
		}
	}

	return policy.Spec.Defaults.Action
}
