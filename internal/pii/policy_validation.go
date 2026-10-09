package pii

import (
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"unicode/utf8"

	"MTL_Scheduler_PII_Test/internal/models"
)

func ValidatePolicy(policy models.PIIPolicy) []string {
	var problems []string

	valid_det := make(map[string]bool)

	for _, det := range policy.Spec.Detectors {
		if !det.Enabled {
			valid_det[det.ID] = false
			continue
		}

		if det.Type == "REGEX" {
			_, err := regexp.Compile(det.Pattern)
			if err != nil {
				valid_det[det.ID] = false
				problems = append(problems, fmt.Sprintf("detector %q has invalid regex pattern: %v", det.ID, err))
				slog.Error("invalid detector pattern, skipping", "detector_id", det.ID, "error", err)
				continue
			}

			valid_det[det.ID] = true
		} else {
			valid_det[det.ID] = true //placeholder since we only have regex, so set to true so i dont have to append error
		}
	}

	validPiorities := map[int]bool{}

	for _, rule := range policy.Spec.Rules {
		DetectorIDs := rule.Match.DetectorIDs
		for _, detid := range DetectorIDs {
			value, exists := valid_det[detid]
			if exists {
				if !value {
					problems = append(problems, fmt.Sprintf("rule %q references detector %q, which is disabled or invalid", rule.ID, detid))
				}
			} else {
				problems = append(problems, fmt.Sprintf("rule %q references unknown detector %q", rule.ID, detid))
			}
		}

		_, exists := validPiorities[rule.Priority]
		if exists {
			problems = append(problems, fmt.Sprintf("rule %q has priority %d, which is already used by another rule", rule.ID, rule.Priority))
		} else {
			validPiorities[rule.Priority] = true
		}

		// RFC-006 §13: a bad action or mask setting must fail HERE, when the policy
		// is loaded, not later when a customer's data first hits the rule.
		problems = append(problems, validateRuleAction(rule)...)
	}
	return problems
}

// implementedActions are the action types that have real behaviour: all five RFC-006 §11 names. BLOCK stores a task as
// Blocked and never queues it; IGNORE suppresses a match and leaves no trace. Anything else (FAIL_CLOSED, a typo) is
// rejected loudly, because a rule with an action nothing implements used to load fine and silently behave as OBSERVE.
var implementedActions = []string{"OBSERVE", "REDACT", "MASK", "BLOCK", "IGNORE"}

func containsFold(list []string, v string) bool {
	for _, item := range list {
		if strings.EqualFold(item, v) {
			return true
		}
	}
	return false
}

// validateRuleAction takes one rule and returns a readable message for every
// problem with its action, by checking the action type is one that is implemented
// and, for MASK, that the strategy and its settings are ones Mask can honour.
// An empty list means the rule's action is valid.
//
// It is deliberately lenient wherever Mask already has a safe default (an omitted
// maskCharacter becomes "*"), so a policy that worked before keeps loading.
func validateRuleAction(rule models.PolicyRule) []string {
	var problems []string

	actionType := strings.ToUpper(strings.TrimSpace(rule.Action.Type))
	if actionType == "" {
		return append(problems, fmt.Sprintf("rule %q has no action type", rule.ID))
	}
	if !containsFold(implementedActions, actionType) {
		return append(problems, fmt.Sprintf("rule %q uses action %q, which is not implemented (use one of %s)",
			rule.ID, rule.Action.Type, strings.Join(implementedActions, ", ")))
	}
	if actionType != "MASK" {
		return problems
	}

	mask := rule.Action.Mask
	strategy := strings.ToUpper(strings.TrimSpace(mask.Strategy))

	switch {
	case strategy == "":
		problems = append(problems, fmt.Sprintf("rule %q is a MASK rule with no strategy (use one of %s)", rule.ID, strings.Join(ValidMaskStrategies, ", ")))
	case !containsFold(ValidMaskStrategies, strategy):
		problems = append(problems, fmt.Sprintf("rule %q has mask strategy %q, which is not one of %s", rule.ID, mask.Strategy, strings.Join(ValidMaskStrategies, ", ")))
	}

	if strategy != "FIXED" && utf8.RuneCountInString(mask.MaskCharacter) > 1 {
		problems = append(problems, fmt.Sprintf("rule %q has maskCharacter %q, which must be a single character", rule.ID, mask.MaskCharacter))
	}
	if mask.VisibleCharacters < 0 {
		problems = append(problems, fmt.Sprintf("rule %q has visibleCharacters %d, which cannot be negative", rule.ID, mask.VisibleCharacters))
	}
	if mask.LocalVisiblePrefix < 0 {
		problems = append(problems, fmt.Sprintf("rule %q has localVisiblePrefix %d, which cannot be negative", rule.ID, mask.LocalVisiblePrefix))
	}
	if dm := strings.ToUpper(strings.TrimSpace(mask.DomainMode)); dm != "" && dm != "PRESERVE" && dm != "MASK" {
		problems = append(problems, fmt.Sprintf("rule %q has domainMode %q, which must be PRESERVE or MASK", rule.ID, mask.DomainMode))
	}

	return problems
}
