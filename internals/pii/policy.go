package pii

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sort"

	"MTL_Scheduler_PII_Test/internals/models"
)

var LoadedPolicy models.PIIPolicy

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

	return policy, nil
}

func ResolveAction(detectorID string, policy models.PIIPolicy) string {

	rules := make([]models.PolicyRule, len(policy.Spec.Rules))
	copy(rules, policy.Spec.Rules)

	sort.Slice(rules, func(i, j int) bool {
		return rules[i].Priority < rules[j].Priority
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

	// TODO 1: build a set of valid detector IDs from policy.Spec.Detectors
	// (a map[string]bool works well for "does this ID exist" lookups)

	// TODO 2: for each detector, if Type == "REGEX", try regexp.Compile(det.Pattern)
	// — if it fails, append a problem describing WHICH detector and WHY

	// TODO 3: for each rule, for each id in rule.DetectorIDs, check that
	// id actually exists in the set from TODO 1 — if not, append a
	// problem describing which rule references a nonexistent detector

	// TODO 4: check for duplicate rule Priority values — RFC-006's
	// FIRST_MATCH semantics implicitly assume priority creates a clear
	// order; two rules sharing the same priority is at least worth
	// flagging as ambiguous, even if you don't hard-fail on it

	return problems
}
