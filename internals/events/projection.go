package events

import (
	"MTL_Scheduler_PII_Test/internals/database"
	"MTL_Scheduler_PII_Test/internals/models"
	"context"
	"fmt"
	"time"

	"gorm.io/gorm/clause"
)

// RFC-005 §7 Projections — Run Projection: "current execution state, latest attempt, attempt count, timestamps, ... PII summary, alert summary."
// Only the Run Projection is implemented; Worker/Queue/Schedule/Component Health projections are commented out in models.RunProjection pending their prerequisite RFCs (004 heartbeats, 003 queue sampling, 002 schedule model).
func UpdateProjection(ctx context.Context, jobId string, eventType string, occurredAt time.Time) {

	var field string
	var value any
	var status string

	// RFC-005 §8 Fact vs Interpretation: CurrentStatus here is the current authoritative fact, derived by replaying events — not a monitoring interpretation like RUN_LOST
	switch eventType {
	case "task.created":
		status = "Pending"
	case "task.queued":
		field = "queued_at"
		value = time.Now()
		status = "Queued"
	case "task.started":
		field = "started_at"
		value = time.Now()
		status = "Running"
	case "task.completed":
		field = "completed_at"
		value = time.Now()
		status = "Completed"
	case "task.recovery_started":
		field = "was_reclaimed"
		value = true
		status = "Queued" //idk if it queued or Running
	}

	// RFC-005 §7 Run Projection: current_status field
	updates := map[string]interface{}{"current_status": status}
	if field != "" {
		updates[field] = value
	}

	projection := models.RunProjection{JobId: jobId}

	// Upsert pattern: RFC-005 §7 describes projections as continuously-updated current-state views built from the event stream, not append-only rows
	err := database.DB.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "job_id"}},
		DoUpdates: clause.Assignments(updates),
	}).Create(&projection).Error

	if err != nil {
		fmt.Println("FAILED TO UPSERT PROJECTION: ", err)
		//add a metric or counter or something idk
	}

}
