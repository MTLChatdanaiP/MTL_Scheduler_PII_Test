package pii

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"

	"MTL_Scheduler_PII_Test/internal/models"
)

// RFC-006 §32 pii.policy_drift_detected.
//
// "Drift" here is the file on disk no longer matching the policy that is ACTIVE in memory: someone edited
// policies/default.json and has not reloaded it, so the engine is still enforcing the old rules while the file says
// something else.

// DefaultPolicyPath is where the policy file lives. It is the path the reload endpoint uses.
const DefaultPolicyPath = "policies/default.json"

// DriftStatus is the result of comparing the active policy with the file.
type DriftStatus struct {
	Drifted        bool   `json:"drifted"`
	ActiveChecksum string `json:"active_checksum"`
	FileChecksum   string `json:"file_checksum"`
	// FileError is set when the file could not be read or parsed. It never contains the file's contents.
	FileError string `json:"file_error,omitempty"`
}

// CheckPolicyDrift takes a policy file path and returns whether that file's policy differs from the active one, by
// computing the checksum of the file's spec (the same way LoadPolicy verifies it) and comparing it with the active
// policy's. An unreadable or invalid file is reported in FileError and is not counted as drift: nothing can be said
// about content that cannot be read.
func CheckPolicyDrift(path string) DriftStatus {
	status := DriftStatus{ActiveChecksum: GetLoadedPolicy().Metadata.Checksum}

	data, err := os.ReadFile(path)
	if err != nil {
		status.FileError = "policy file could not be read"
		return status
	}

	var policy models.PIIPolicy
	if err := json.Unmarshal(data, &policy); err != nil {
		status.FileError = "policy file is not valid JSON for this schema"
		return status
	}

	spec, err := json.Marshal(policy.Spec)
	if err != nil {
		status.FileError = "policy spec could not be hashed"
		return status
	}
	sum := sha256.Sum256(spec)

	status.FileChecksum = hex.EncodeToString(sum[:])
	status.Drifted = status.FileChecksum != status.ActiveChecksum
	return status
}
