package models

import "gorm.io/gorm"

// RFC-006 §8 Finding Model: "finding_id, run_id, attempt_id?, source, field_path?, pii_type, confidence, detector_id, policy_action, detected_at, value_fingerprint?"
// PRD §33 PII Finding Model
type PIIRecord struct {
	gorm.Model

	// RFC-006 §8: run_id correlation field.
	JobID string
	Type  string

	// Detection metadata.
	DetectorID string
	Confidence float64 // regex detector always reports 1.0

	// RFC-006 §4: JOB_PAYLOAD, JOB_METADATA, JOB_RESULT,
	// ERROR_MESSAGE, STRUCTURED_LOG.
	// Currently only JOB_PAYLOAD is used.
	Source string
	Index  int

	// RFC-006 §9 recommends storing a fingerprint by default.
	// This project intentionally stores the raw value as a
	// documented simplification.
	FingerprintValue string

	// Policy outcome.
	PolicyAction string
}
