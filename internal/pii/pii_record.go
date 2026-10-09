package pii

import (
	"context"

	"gorm.io/gorm"

	"MTL_Scheduler_PII_Test/internal/models"
)

// RFC-000 §5: the PII Policy Engine OWNS findings. Everything that stores one goes through RecordFinding, so there is one place that
// decides what a finding record contains (and, above all, what it never contains: the raw value).

// FindingInput is everything a finding record is made from.
type FindingInput struct {
	JobID     string
	AttemptID string // only a scan of what a run produced has one; the pre-execution scans leave it empty
	Source    string // JOB_PAYLOAD, JOB_METADATA, STRUCTURED_LOG, JOB_RESULT, ERROR_MESSAGE ...
	Index     int    // the finding's number among those of the same type in this scan
	Finding   Finding
	Rule      models.PolicyRule
	Policy    models.PIIPolicy
}

// RecordFinding builds the PIIRecord for one finding and saves it. The record holds the finding's fingerprint (a keyed hash), never
// its value, and the rule and policy version that produced it. MaskStrategy is set only for a MASK rule. It returns the saved record
// and the database error, if any: the caller decides how to fail (task creation blocks the task, an artifact scan logs it).
func RecordFinding(ctx context.Context, db *gorm.DB, in FindingInput) (models.PIIRecord, error) {
	record := models.PIIRecord{
		JobID:            in.JobID,
		AttemptID:        in.AttemptID,
		Type:             string(in.Finding.Type),
		DetectorID:       in.Finding.DetectorID,
		FingerprintValue: Fingerprint(in.Finding.Match),
		Index:            in.Index,
		Source:           in.Source,
		Confidence:       1.0,
		PolicyAction:     in.Rule.Action.Type,
		FieldPath:        in.Finding.FieldPath,
		RuleID:           in.Rule.ID,
		PolicyName:       in.Policy.Metadata.Name,
		PolicyVersion:    in.Policy.Metadata.Version,
		PolicyChecksum:   in.Policy.Metadata.Checksum,
	}
	if in.Rule.Action.Type == "MASK" {
		record.MaskStrategy = in.Rule.Action.Mask.Strategy
	}

	err := db.WithContext(ctx).Create(&record).Error
	return record, err
}
