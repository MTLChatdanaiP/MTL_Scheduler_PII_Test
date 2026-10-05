package models

import (
	"time"

	"gorm.io/gorm"
)

// RFC-006 §24 Post-Execution Scanning, the "safe monitoring persistence" stage.
//
// What a worker produces after running a task -- its result, its error message,
// its log output -- may contain PII that was never in the original payload, so
// it cannot be stored as-is. RFC-006 §24 is explicit that "raw artifact bodies
// are transient at this boundary and must not be sent directly to Monitoring,
// Job Run Lifecycle, or a general log sink", and that Job Run Lifecycle "may
// receive only a sanitized summary and a protected artifact reference".
//
// This row IS that sanitized summary plus that reference. The raw body never
// reaches it: scanning and transformation happen first, in memory, and only the
// transformed text is written here.
type ExecutionArtifact struct {
	gorm.Model

	// Correlation. AttemptID is what makes this different from a pre-execution
	// PIIRecord: this artifact belongs to one specific run of the task.
	JobID     string `json:"job_id"`
	AttemptID string `json:"attempt_id"`

	// RFC-006 §4 source classes. Post-execution artifacts are JOB_RESULT,
	// ERROR_MESSAGE or STRUCTURED_LOG -- never JOB_PAYLOAD, which is scanned
	// pre-execution by a different path.
	Source string `json:"source"`

	// SanitizedBody is the artifact AFTER policy transformation, truncated to
	// MaxArtifactBytes. "Bounded persistence" in the RFC's own words: an
	// artifact store with no size limit becomes the accidental log sink the
	// section exists to prevent.
	SanitizedBody string `json:"sanitized_body"`
	Truncated     bool   `json:"truncated"`
	OriginalBytes int    `json:"original_bytes"`

	// Scan outcome, mirroring Task.ScanStatus: CLEAN, DETECTED or SCAN_ERROR.
	// Kept separate from the run's own success/failure, because a scan failing
	// is not the job failing (RFC-006 §2).
	ScanStatus   string `json:"scan_status"`
	FindingCount int    `json:"finding_count"`

	// Which policy judged it, so a finding can always be traced back to the
	// rules in force at the time.
	PolicyName     string `json:"policy_name"`
	PolicyVersion  int    `json:"policy_version"`
	PolicyChecksum string `json:"policy_checksum"`

	ProducedAt time.Time `json:"produced_at"`
}
