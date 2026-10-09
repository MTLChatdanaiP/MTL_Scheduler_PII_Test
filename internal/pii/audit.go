package pii

import (
	"context"
	"time"

	"MTL_Scheduler_PII_Test/internal/database"
	"MTL_Scheduler_PII_Test/internal/models"
)

// Audit actions.
const AuditPIIRawValueRead = "PII_RAW_VALUE_READ"

// RecordAccess takes who did something sensitive, what they did, to what, and how
// many values were involved, and returns an error only if the row could not be
// stored, by writing one AuditRecord. It never stores the values themselves.
func RecordAccess(ctx context.Context, actor string, action string, subjectType string, subjectID string, count int) error {
	if actor == "" {
		actor = "unknown"
	}

	record := models.AuditRecord{
		Actor:       actor,
		Action:      action,
		SubjectType: subjectType,
		SubjectID:   subjectID,
		Count:       count,
		OccurredAt:  time.Now().UTC(),
	}

	return database.DB.WithContext(ctx).Create(&record).Error
}
