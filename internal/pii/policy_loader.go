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

	"MTL_Scheduler_PII_Test/internal/database"
	"MTL_Scheduler_PII_Test/internal/events"
	"MTL_Scheduler_PII_Test/internal/models"
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

func EvaluatePolicy(findings []Finding, policy models.PIIPolicy, source string, jobType string, queue string) []EvaluatedFinding {
	evaluated := []EvaluatedFinding{}

	for _, finding := range findings {
		ctx := MatchContext{
			Source:     source,
			JobType:    jobType,
			Queue:      queue,
			PIIType:    string(finding.Type),
			DetectorID: finding.DetectorID,
			Confidence: 1.0, // TODO: does Finding carry a real confidence value anywhere yet, or is this still always 1.0?
		}
		rule := ResolveRule(ctx, policy)
		evaluated = append(evaluated, EvaluatedFinding{Finding: finding, Rule: rule})
	}

	return evaluated
}

// ActivatePolicy is ActivatePolicyAs with the actor defaulted to the source, so
// startup (source "system") and every existing caller keep working unchanged.
func ActivatePolicy(ctx context.Context, path string, trigger string, source string) (models.PIIPolicy, error) {
	return ActivatePolicyAs(ctx, path, trigger, source, source)
}

// ActivatePolicyAs loads and activates a policy file, recording who asked.
//
// RFC-006 §33 wants every policy mutation audited with actor, revision, checksum
// and result. A failed activation now records the trigger and actor as well, which
// it did not before, so "who tried to reload a broken policy" is answerable.
func ActivatePolicyAs(ctx context.Context, path string, trigger string, source string, actor string) (models.PIIPolicy, error) {
	if actor == "" {
		actor = source
	}

	policy, err := LoadPolicy(path)

	activation := models.PolicyActivation{
		ActivatedAt: time.Now().UTC(),
		Trigger:     trigger,
		Actor:       actor,
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
	database.DB.WithContext(ctx).Create(&activation)

	LoadedPolicy.Store(&policy)
	events.LogEvent(ctx, "system", "pii.policy_activated", source)

	return policy, nil
}
