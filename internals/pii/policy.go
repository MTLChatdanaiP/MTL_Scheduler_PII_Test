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
