package models

import "gorm.io/gorm"

// RFC-006 §17 Finding Model: "finding_id, run_id, attempt_id?, source,
// field_path?, pii_type, confidence, detector_id, detector_version,
// policy_name, policy_version, policy_checksum, rule_id, policy_action,
// mask_strategy?, detected_at, value_fingerprint?"
//
// NOT ADDED: detector_version -- DetectorDefinition has no Version field
// anywhere in the policy schema, so there is nothing to populate this from
// yet. Would need a schema change to the policy JSON itself, not just this
// struct. AttemptID -- always empty for pre-execution scans (this record is
// created before any Attempt exists); becomes real once RFC-006 §24
// post-execution scanning is unblocked.
type PIIRecord struct {
	gorm.Model

	// RFC-006 §17: run_id correlation field.
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

	// --- NEW, RFC-006 §17 ---

	// Where in the payload this finding was found. Empty for a flat
	// (non-JSON) payload scanned via the plain-text fallback path -- there is
	// no field path to speak of in that case, matching the RFC's field_path?
	// being optional.
	FieldPath string

	// The specific policy rule that decided this finding's action, or
	// "default" if no rule matched and the policy's default action applied.
	RuleID string

	// Only set when PolicyAction == "MASK". Empty for REDACT/OBSERVE/BLOCK,
	// since there is no masking strategy to record for those.
	MaskStrategy string

	// The active policy's identity AT THE TIME this finding was created --
	// not looked up later, so a finding's provenance stays accurate even
	// after the policy is reloaded to a new version.
	PolicyName     string
	PolicyVersion  int
	PolicyChecksum string
}
