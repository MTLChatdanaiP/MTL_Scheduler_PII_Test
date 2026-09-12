package pii

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
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

func EvaluatePolicy(findings []Finding, policy models.PIIPolicy, source string, jobType string) []EvaluatedFinding {
	evaluated := []EvaluatedFinding{}

	for _, finding := range findings {
		ctx := MatchContext{
			Source:     source,
			JobType:    jobType,
			PIIType:    string(finding.Type),
			DetectorID: finding.DetectorID,
			Confidence: 1.0, // TODO: does Finding carry a real confidence value anywhere yet, or is this still always 1.0?
		}
		rule := ResolveRule(ctx, policy)
		evaluated = append(evaluated, EvaluatedFinding{Finding: finding, Rule: rule})
	}

	return evaluated
}

func ActivatePolicy(ctx context.Context, path string, trigger string, source string) (models.PIIPolicy, error) {
	policy, err := LoadPolicy(path)

	activation := models.PolicyActivation{
		ActivatedAt: time.Now().UTC(),
	}

	if err != nil {
		activation.Result = "FAILED"
		activation.FailureReason = err.Error()
		database.DB.WithContext(ctx).Create(&activation)
		events.LogEvent(ctx, "system", "pii.policy_reload_failed", source)
		return models.PIIPolicy{}, err
	}

	activation.PolicyName = policy.Metadata.Name
	activation.PolicyVersion = policy.Metadata.Version
	activation.Checksum = policy.Metadata.Checksum
	activation.Result = "SUCCESS"
	activation.Trigger = trigger
	database.DB.WithContext(ctx).Create(&activation)

	LoadedPolicy.Store(&policy)
	events.LogEvent(ctx, "system", "pii.policy_activated", source)

	return policy, nil
}
