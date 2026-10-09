package pii

// RFC-006 §32 pii.policy_drift_detected: the file on disk no longer matches the policy that is ACTIVE.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"MTL_Scheduler_PII_Test/internal/models"
)

func checksumOf(p models.PIIPolicy) string {
	spec, _ := json.Marshal(p.Spec)
	sum := sha256.Sum256(spec)
	return hex.EncodeToString(sum[:])
}

func writePolicy(t *testing.T, p models.PIIPolicy) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "default.json")
	data, _ := json.Marshal(p)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func activate(t *testing.T, p models.PIIPolicy) {
	t.Helper()
	p.Metadata.Checksum = checksumOf(p)
	prev := LoadedPolicy.Load()
	LoadedPolicy.Store(&p)
	t.Cleanup(func() { LoadedPolicy.Store(prev) })
}

func basePolicy() models.PIIPolicy {
	var p models.PIIPolicy
	p.Metadata.Name = "default"
	p.Spec.EvaluationMode = "FIRST_MATCH"
	p.Spec.Defaults.Action = "OBSERVE"
	p.Spec.Rules = []models.PolicyRule{{ID: "r1", Priority: 1, Action: models.PolicyAction{Type: "REDACT"}}}
	return p
}

func TestCheckPolicyDrift_AFileMatchingTheActivePolicyHasNotDrifted(t *testing.T) {
	p := basePolicy()
	activate(t, p)

	st := CheckPolicyDrift(writePolicy(t, p))

	if st.Drifted || st.FileError != "" || st.FileChecksum != st.ActiveChecksum || st.FileChecksum == "" {
		t.Fatalf("an unchanged file must not report drift: %+v", st)
	}
}

func TestCheckPolicyDrift_AnEditedFileHasDrifted(t *testing.T) {
	p := basePolicy()
	activate(t, p)

	edited := basePolicy()
	edited.Spec.Rules = append(edited.Spec.Rules, models.PolicyRule{ID: "r2", Priority: 2, Action: models.PolicyAction{Type: "MASK"}})

	st := CheckPolicyDrift(writePolicy(t, edited))

	if !st.Drifted || st.FileChecksum == st.ActiveChecksum {
		t.Fatalf("a file edited after activation must report drift: %+v", st)
	}
	if st.FileChecksum != checksumOf(edited) {
		t.Fatal("the reported file checksum must be the one LoadPolicy would verify")
	}
}

func TestCheckPolicyDrift_AnUnreadableOrInvalidFileIsAnErrorNotDrift(t *testing.T) {
	activate(t, basePolicy())

	missing := CheckPolicyDrift(filepath.Join(t.TempDir(), "nope", "default.json"))
	if missing.Drifted || missing.FileError == "" {
		t.Fatalf("a missing file is an error, not drift: %+v", missing)
	}

	bad := filepath.Join(t.TempDir(), "bad.json")
	os.WriteFile(bad, []byte(`{"spec": "this is not a policy jane.doe@example.com"`), 0o644)
	invalid := CheckPolicyDrift(bad)
	if invalid.Drifted || invalid.FileError == "" {
		t.Fatalf("invalid JSON is an error, not drift: %+v", invalid)
	}
	if strings.Contains(invalid.FileError, "jane") || strings.Contains(invalid.FileError, bad) {
		t.Fatalf("the error must never echo the file's contents or its path: %q", invalid.FileError)
	}
}
