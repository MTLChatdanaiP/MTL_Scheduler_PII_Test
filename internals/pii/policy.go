package pii

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"MTL_Scheduler_PII_Test/internals/database"
	"MTL_Scheduler_PII_Test/internals/events"
	"MTL_Scheduler_PII_Test/internals/models"
)

type EvaluatedFinding struct {
	Finding Finding
	Rule    models.PolicyRule // the resolved rule for this finding
}

var LoadedPolicy atomic.Pointer[models.PIIPolicy]

func GetLoadedPolicy() models.PIIPolicy {
	return *LoadedPolicy.Load()
}

func LoadPolicy(path string) (models.PIIPolicy, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return models.PIIPolicy{}, err
	}

	var policy models.PIIPolicy
	if err := json.Unmarshal(data, &policy); err != nil {
		return models.PIIPolicy{}, err
	}

	policySpec, err := json.Marshal(policy.Spec)
	if err != nil {
		return models.PIIPolicy{}, err
	}

	hash := sha256.Sum256(policySpec)

	result := hex.EncodeToString(hash[:])

	if result != policy.Metadata.Checksum {
		return models.PIIPolicy{}, fmt.Errorf("policy checksum mismatch: expected %s, computed %s", policy.Metadata.Checksum, result)
	}

	problems := ValidatePolicy(policy)

	if len(problems) > 0 {
		return models.PIIPolicy{}, fmt.Errorf("policy validation failed:\n%s", strings.Join(problems, "\n"))
	}

	return policy, nil
}

func ResolveRule(detectorID string, policy models.PIIPolicy) models.PolicyRule {
	rules := make([]models.PolicyRule, len(policy.Spec.Rules))
	copy(rules, policy.Spec.Rules)

	sort.Slice(rules, func(i, j int) bool {
		return rules[i].Priority > rules[j].Priority
	})

	for _, rule := range rules {
		for _, id := range rule.DetectorIDs {
			if id == detectorID {
				return rule
			}
		}
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

func EvaluatePolicy(findings []Finding, policy models.PIIPolicy) []EvaluatedFinding {
	evaluated := []EvaluatedFinding{}

	for _, finding := range findings {
		rule := ResolveRule(finding.DetectorID, policy)
		evaluated = append(evaluated, EvaluatedFinding{Finding: finding, Rule: rule})
	}

	return evaluated
}

func ActivatePolicy(ctx context.Context, path string, trigger string) (models.PIIPolicy, error) {
	policy, err := LoadPolicy(path)

	activation := models.PolicyActivation{
		ActivatedAt: time.Now().UTC(),
	}

	if err != nil {
		activation.Result = "FAILED"
		activation.FailureReason = err.Error()
		database.DB.WithContext(ctx).Create(&activation)
		events.LogEvent(ctx, "system", "pii.policy_reload_failed", "api")
		return models.PIIPolicy{}, err
	}

	activation.PolicyName = policy.Metadata.Name
	activation.PolicyVersion = policy.Metadata.Version
	activation.Checksum = policy.Metadata.Checksum
	activation.Result = "SUCCESS"
	activation.Trigger = trigger
	database.DB.WithContext(ctx).Create(&activation)
	events.LogEvent(ctx, "system", "pii.policy_activated", "api")

	return policy, nil
}
