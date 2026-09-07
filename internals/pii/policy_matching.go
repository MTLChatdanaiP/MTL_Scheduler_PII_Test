package pii

import (
	"slices"
	"sort"

	"MTL_Scheduler_PII_Test/internals/models"
)

type MatchContext struct {
	Source     string
	JobType    string
	PIIType    string
	FieldPath  string // empty until §10 exists
	Confidence float64
	DetectorID string
}

func ruleMatches(rule models.PolicyRule, ctx MatchContext) bool {

	match := rule.Match

	if len(match.Sources) > 0 && !slices.Contains(match.Sources, ctx.Source) {
		return false
	}
	if len(match.JobTypes) > 0 && !slices.Contains(match.JobTypes, ctx.JobType) {
		return false
	}
	if len(match.PIITypes) > 0 && !slices.Contains(match.PIITypes, ctx.PIIType) {
		return false
	}
	if match.MinimumConfidence > 0 && ctx.Confidence < match.MinimumConfidence {
		return false
	}
	return true
}

func ResolveRule(ctx MatchContext, policy models.PIIPolicy) models.PolicyRule {
	rules := make([]models.PolicyRule, len(policy.Spec.Rules))
	copy(rules, policy.Spec.Rules)

	sort.Slice(rules, func(i, j int) bool {
		return rules[i].Priority > rules[j].Priority
	})

	for _, rule := range rules {
		if !slices.Contains(rule.DetectorIDs, ctx.DetectorID) {
			continue
		}
		if !ruleMatches(rule, ctx) {
			continue
		}
		return rule
	}

	return models.PolicyRule{
		ID:     "default",
		Action: policy.Spec.Defaults.Action,
		// NOTE: if Defaults.Action is ever set to "MASK" with no matching rule,
		// this synthetic default rule has no Mask config, so Mask() would
		// silently return an empty string. Not currently reachable since
		// Defaults.Action is "OBSERVE" in the live policy — documented, not fixed.
	}
}

func ResolveAction(detectorID string, policy models.PIIPolicy) string {

	rules := make([]models.PolicyRule, len(policy.Spec.Rules))
	copy(rules, policy.Spec.Rules)

	sort.Slice(rules, func(i, j int) bool {
		return rules[i].Priority > rules[j].Priority
	})

	for _, rule := range rules {
		for _, id := range rule.DetectorIDs {
			if id == detectorID {
				return rule.Action
			}
		}
	}

	return policy.Spec.Defaults.Action
}
