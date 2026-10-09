package alerts

import (
	"context"
	"time"

	"MTL_Scheduler_PII_Test/internal/database"
	"MTL_Scheduler_PII_Test/internal/models"
)

// RFC-007 §4 Alert Types. RUN_FAILED, RUN_RETRY_EXHAUSTED, RUN_TIMEOUT, PII_SCAN_FAILED and SCHEDULE_DELAYED
// had no data source at all, although the events for three of them were already being written.
//
// Monitoring builds from its own immutable event log (RFC-005 §4), so each of these is a metric that reads
// that log and reports HOW LONG AGO the thing happened. A rule says "alert while it is recent"
// (operator LTE, threshold 900 = 15 minutes). When the event ages past the threshold the alert resolves by
// itself, with no change to the annotation machinery (which would have resolved a terminal run instantly).

const (
	// eventMetricWindow bounds how far back the event log is read.
	eventMetricWindow = 24 * time.Hour
	// eventMetricLimit bounds how many rows one resolver reads.
	eventMetricLimit = 2000

	scheduleLatenessWindow = 1 * time.Hour
)

// resolveEventAge takes an event type and the subject type of its subject, and returns a resolver that
// reports, for each subject that had that event in the last 24 hours, how many seconds ago the newest one
// happened. The evidence holds ids and times only, never payload or message text.
func resolveEventAge(eventType string, subjectType string) MetricResolver {
	return func(ctx context.Context) ([]MetricSample, error) {
		var rows []models.EventEnvelope

		cutoff := time.Now().UTC().Add(-eventMetricWindow)

		err := database.DB.WithContext(ctx).
			Where("event_type = ? AND occurred_at >= ?", eventType, cutoff).
			Order("occurred_at DESC").
			Limit(eventMetricLimit).
			Find(&rows).Error
		if err != nil {
			return nil, err
		}

		seen := make(map[string]bool)
		samples := make([]MetricSample, 0)

		for _, e := range rows {
			if seen[e.JobId] {
				continue // the newest event per subject only
			}
			seen[e.JobId] = true

			age := time.Since(e.OccurredAt).Seconds()

			samples = append(samples, MetricSample{
				SubjectType: subjectType,
				SubjectID:   e.JobId,
				Numeric:     age,
				Text:        formatNumeric(age),
				Evidence: map[string]interface{}{
					"event_type":         eventType,
					"event_id":           e.EventID,
					"occurred_at":        e.OccurredAt,
					"execution_chain_id": e.ExecutionChainID,
					"age_seconds":        age,
				},
			})
		}

		return samples, nil
	}
}

// resolveScheduleCreationLateness reports, for each schedule that produced a run in the last hour, the worst
// gap in seconds between when an occurrence was EXPECTED and when its run was actually created. That is the
// "how late was run creation" measure RFC-002 §14 asks for and the data behind SCHEDULE_DELAYED.
func resolveScheduleCreationLateness(ctx context.Context) ([]MetricSample, error) {
	var tasks []models.Task

	cutoff := time.Now().UTC().Add(-scheduleLatenessWindow)

	err := database.DB.WithContext(ctx).
		Where("schedule_id <> '' AND created_at >= ? AND expected_at > ?", cutoff, time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)).
		Find(&tasks).Error
	if err != nil {
		return nil, err
	}

	worst := make(map[string]models.Task)
	lateness := make(map[string]float64)

	for _, task := range tasks {
		l := task.CreatedAt.Sub(task.ExpectedAt).Seconds()
		if l < 0 {
			l = 0
		}
		if prev, ok := lateness[task.ScheduleId]; !ok || l > prev {
			lateness[task.ScheduleId] = l
			worst[task.ScheduleId] = task
		}
	}

	samples := make([]MetricSample, 0, len(lateness))
	for scheduleID, l := range lateness {
		task := worst[scheduleID]
		samples = append(samples, MetricSample{
			SubjectType: "SCHEDULE",
			SubjectID:   scheduleID,
			Numeric:     l,
			Text:        formatNumeric(l),
			Evidence: map[string]interface{}{
				"lateness_seconds": l,
				"run_id":           task.JobId,
				"expected_at":      task.ExpectedAt,
				"created_at":       task.CreatedAt,
			},
		})
	}
	return samples, nil
}
