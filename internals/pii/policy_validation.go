package pii

import (
	"fmt"
	"log/slog"
	"regexp"

	"MTL_Scheduler_PII_Test/internals/models"
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
		for _, detid := range rule.DetectorIDs {
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
	}
	return problems
}
