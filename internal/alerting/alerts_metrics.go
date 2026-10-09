package alerts

import (
	"MTL_Scheduler_PII_Test/internal/database"
	"MTL_Scheduler_PII_Test/internal/models"
	"context"
	"strconv"
	"time"
)

const (
	heartbeatQueryWindow  = 1 * time.Hour
	queueQueryWindow      = 1 * time.Hour
	monitoringQueryWindow = 1 * time.Hour

	piiRecordWindow = 15 * time.Minute

	platformSubjectID = "platform"
)

type MetricSample struct {
	SubjectType string // RUN | QUEUE | WORKER | SCHEDULE | PII_FINDING
	SubjectID   string
	ScopeValue  string // job type / pii category — what non-ID scopes match on
	Numeric     float64
	Text        string
	Evidence    map[string]interface{}
}

type MetricResolver func(context.Context) ([]MetricSample, error)

var MetricResolvers = map[string]MetricResolver{
	"worker.heartbeat_age": resolveWorkerHeartbeatAge,
	"worker.capacity_used": resolveWorkerCapacityUsed,
	"queue.oldest_age":     resolveQueueOldestAge,
	"queue.pending_count":  resolveQueuePendingCount,
	"queue.consumer_count": resolveQueueConsumerCount,
	"monitoring.status":    resolveMonitoringStatus,
	"pii.policy_action":    resolvePIIPolicyAction,

	// RFC-007 §4: read from monitoring's own event log (event_metrics.go)
	"run.failed_age":             resolveEventAge("task.failed", "RUN"),
	"run.retries_exhausted_age":  resolveEventAge("task.retries_exhausted", "RUN"),
	"run.timed_out_age":          resolveEventAge("task.timed_out", "RUN"),
	"pii.scan_failed_age":        resolveEventAge("pii.scan_failed", "RUN"),
	"schedule.creation_lateness": resolveScheduleCreationLateness,
}

func formatNumeric(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

func resolveWorkerHeartbeatAge(ctx context.Context) ([]MetricSample, error) {
	var heartbeats []models.WorkerHeartbeat

	cutoff := time.Now().UTC().Add(-heartbeatQueryWindow)

	err := database.DB.WithContext(ctx).
		Where("occurred_at >= ?", cutoff).
		Order("occurred_at DESC").
		Find(&heartbeats).Error

	if err != nil {
		return nil, err
	}

	// RFC-010 §9: an instance that shut down on purpose is not a missing worker, so it must not raise a heartbeat-age alert.
	// A draining instance (RFC-010 §9) has stopped heartbeating on purpose for at most models.DrainWindow, so it is skipped the same way.
	var stoppedIDs []string
	database.DB.WithContext(ctx).Model(&models.Worker{}).
		Where("stopped_at IS NOT NULL OR (draining_at IS NOT NULL AND draining_at > ?)", time.Now().UTC().Add(-models.DrainWindow)).
		Pluck("instance_id", &stoppedIDs)
	stopped := make(map[string]bool, len(stoppedIDs))
	for _, id := range stoppedIDs {
		stopped[id] = true
	}

	seen := make(map[string]bool)
	samples := make([]MetricSample, 0)

	for _, hb := range heartbeats {
		if seen[hb.WorkerId] {
			continue
		}
		if stopped[hb.InstanceId] {
			seen[hb.WorkerId] = true // the newest heartbeat of this worker id belongs to a deliberately stopped instance
			continue
		}

		seen[hb.WorkerId] = true

		age := time.Since(hb.OccurredAt).Seconds()

		samples = append(samples, MetricSample{
			SubjectType: "WORKER",
			SubjectID:   hb.WorkerId,
			ScopeValue:  "",
			Numeric:     age,
			Text:        formatNumeric(age),
			Evidence: map[string]interface{}{
				"age_seconds":      age,
				"instance_id":      hb.InstanceId,
				"occurred_at":      hb.OccurredAt,
				"running_attempts": hb.RunningAttempts,
				"capacity":         hb.Capacity,
			},
		})
	}

	return samples, nil
}

func resolveWorkerCapacityUsed(ctx context.Context) ([]MetricSample, error) {
	var heartbeats []models.WorkerHeartbeat

	cutoff := time.Now().UTC().Add(-heartbeatQueryWindow)

	err := database.DB.WithContext(ctx).
		Where("occurred_at >= ?", cutoff).
		Order("occurred_at DESC").
		Find(&heartbeats).Error

	if err != nil {
		return nil, err
	}

	seen := make(map[string]bool)
	samples := make([]MetricSample, 0)

	for _, hb := range heartbeats {
		if seen[hb.WorkerId] {
			continue
		}
		seen[hb.WorkerId] = true
		if hb.Capacity <= 0 {
			continue
		}

		used := (float64(hb.RunningAttempts) / float64(hb.Capacity)) * 100

		samples = append(samples, MetricSample{
			SubjectType: "WORKER",
			SubjectID:   hb.WorkerId,
			ScopeValue:  "",
			Numeric:     used,
			Text:        formatNumeric(used),
			Evidence: map[string]interface{}{
				"capacity_used_percent": used,
				"running_attempts":      hb.RunningAttempts,
				"capacity":              hb.Capacity,
				"instance_id":           hb.InstanceId,
				"occurred_at":           hb.OccurredAt,
			},
		})
	}

	return samples, nil
}

func latestQueueHealth(ctx context.Context) ([]models.QueueHealth, error) {
	var rows []models.QueueHealth

	cutoff := time.Now().UTC().Add(-queueQueryWindow)

	err := database.DB.WithContext(ctx).
		Where("sampled_at >= ?", cutoff).
		Order("sampled_at DESC").
		Find(&rows).Error

	if err != nil {
		return nil, err
	}

	seen := make(map[string]bool)
	latest := make([]models.QueueHealth, 0)

	for _, row := range rows {
		if seen[row.QueueName] {
			continue
		}
		seen[row.QueueName] = true
		latest = append(latest, row)
	}

	return latest, nil
}

func resolveQueueOldestAge(ctx context.Context) ([]MetricSample, error) {
	rows, err := latestQueueHealth(ctx)
	if err != nil {
		return nil, err
	}

	samples := make([]MetricSample, 0, len(rows))

	for _, row := range rows {
		age := float64(row.OldestPendingAgeSeconds)

		samples = append(samples, MetricSample{
			SubjectType: "QUEUE",
			SubjectID:   row.QueueName,
			Numeric:     age,
			Text:        formatNumeric(age),
			Evidence: map[string]interface{}{
				"oldest_pending_age_seconds": row.OldestPendingAgeSeconds,
				"pending_count":              row.PendingCount,
				"sampled_at":                 row.SampledAt,
			},
		})
	}

	return samples, nil
}

func resolveQueuePendingCount(ctx context.Context) ([]MetricSample, error) {
	rows, err := latestQueueHealth(ctx)
	if err != nil {
		return nil, err
	}

	samples := make([]MetricSample, 0, len(rows))

	for _, row := range rows {
		pending := float64(row.PendingCount)

		samples = append(samples, MetricSample{
			SubjectType: "QUEUE",
			SubjectID:   row.QueueName,
			Numeric:     pending,
			Text:        formatNumeric(pending),
			Evidence: map[string]interface{}{
				"pending_count":              row.PendingCount,
				"stream_length":              row.StreamLength,
				"oldest_pending_age_seconds": row.OldestPendingAgeSeconds,
				"sampled_at":                 row.SampledAt,
			},
		})
	}

	return samples, nil
}

func resolveQueueConsumerCount(ctx context.Context) ([]MetricSample, error) {
	rows, err := latestQueueHealth(ctx)
	if err != nil {
		return nil, err
	}

	samples := make([]MetricSample, 0, len(rows))

	for _, row := range rows {
		consumers := float64(row.ConsumerCount)

		samples = append(samples, MetricSample{
			SubjectType: "QUEUE",
			SubjectID:   row.QueueName,
			Numeric:     consumers,
			Text:        formatNumeric(consumers),
			Evidence: map[string]interface{}{
				"consumer_count": row.ConsumerCount,
				"pending_count":  row.PendingCount,
				"stream_length":  row.StreamLength,
				"sampled_at":     row.SampledAt,
			},
		})
	}

	return samples, nil
}

func resolveMonitoringStatus(ctx context.Context) ([]MetricSample, error) {
	var row models.MonitoringHealth

	cutoff := time.Now().UTC().Add(-monitoringQueryWindow)

	err := database.DB.WithContext(ctx).
		Where("sampled_at >= ?", cutoff).
		Order("sampled_at DESC").
		First(&row).Error

	if err != nil {
		return []MetricSample{}, nil
	}

	failed := float64(row.FailedChecks)

	return []MetricSample{
		{
			SubjectType: "PLATFORM",
			SubjectID:   platformSubjectID,
			Numeric:     failed,
			Text:        row.Status, // COMPLETE | DEGRADED | PARTIAL | UNKNOWN
			Evidence: map[string]interface{}{
				"status":        row.Status,
				"failed_checks": row.FailedChecks,
				"sampled_at":    row.SampledAt,
			},
		},
	}, nil
}

func resolvePIIPolicyAction(ctx context.Context) ([]MetricSample, error) {
	var records []models.PIIRecord

	cutoff := time.Now().UTC().Add(-piiRecordWindow)

	err := database.DB.WithContext(ctx).
		Where("created_at >= ?", cutoff).
		Order("created_at DESC").
		Find(&records).Error

	if err != nil {
		return nil, err
	}

	samples := make([]MetricSample, 0, len(records))

	for _, record := range records {
		samples = append(samples, MetricSample{
			SubjectType: "PII_FINDING",

			SubjectID: strconv.FormatUint(uint64(record.ID), 10),

			// RFC-007 §8 scopes by PII category
			ScopeValue: record.Type,

			Numeric: record.Confidence,
			Text:    record.PolicyAction, // OBSERVE | MASK | REDACT

			// RFC-007 §13 PII SAFETY
			Evidence: map[string]interface{}{
				"pii_type":      record.Type,
				"policy_action": record.PolicyAction,
				"detector_id":   record.DetectorID,
				"source":        record.Source,
				"job_id":        record.JobID,
				"confidence":    record.Confidence,
				"detected_at":   record.CreatedAt,
			},
		})
	}

	return samples, nil
}
