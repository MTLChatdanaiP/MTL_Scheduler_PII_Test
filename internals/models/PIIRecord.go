package models

import "gorm.io/gorm"

// RFC-006 §8 Finding Model: "finding_id, run_id, attempt_id?, source, field_path?, pii_type, confidence, detector_id, policy_action, detected_at, value_fingerprint?"
// PRD §33 PII Finding Model
type PIIRecord struct {
	gorm.Model
	// RFC-006 §8: run_id correlation field (named JobId here to match this project's task identifier)
	JobId string // which task this PII came from
	Type  string // Email, Phone, SSN, CreditCard — matches pii.PIIType
	// RFC-006 §9 Raw Value Handling: "Default: store_raw_value = false." This project deviates and stores the raw value directly rather than a keyed fingerprint — documented simplification
	Value string // the actual sensitive value
	Index int
	// RFC-006 §4 Scan Sources: JOB_PAYLOAD / JOB_METADATA / JOB_RESULT / ERROR_MESSAGE / STRUCTURED_LOG — only JOB_PAYLOAD is currently produced
	Source string
	// RFC-006 §8 Finding Model: confidence score — regex detector always reports 1.0 (no partial-certainty concept for pattern matching)
	Confidence float64
}
