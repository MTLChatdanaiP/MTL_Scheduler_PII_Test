package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"MTL_Scheduler_PII_Test/internals/cache"
	"MTL_Scheduler_PII_Test/internals/database"
	"MTL_Scheduler_PII_Test/internals/models"

	"github.com/oklog/ulid/v2"
	"gorm.io/gorm"
)

const (
	stuckThreshold                 = 60 * time.Second
	monitoringInterval             = 20 * time.Second
	workerHeartbeatFreshnessWindow = 30 * time.Second
	backlogThreshold               = 20
	missedOccurrenceThreshold      = 5 * time.Minute
)

// ---------- Sweep entry point ----------

func StartMonitoringSweep(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		failedChecks := 0
		checks := []func(context.Context) error{
			checkStuckTasks,
			checkLostTasks,
			checkDuplicateExecution,
			checkScheduleDrift,
			checkQueueBacklog,
		}

		for _, check := range checks {
			if err := check(ctx); err != nil {
				failedChecks++
			}
		}

		totalChecks := 5
		status := "DEGRADED"

		switch {
		case failedChecks == 0:
			status = "COMPLETE"
		case failedChecks == totalChecks:
			status = "UNKNOWN"
		case failedChecks < totalChecks/2:
			status = "PARTIAL"
		default:
			status = "DEGRADED"
		}

		health_sample := models.MonitoringHealth{
			Status:       status,
			FailedChecks: failedChecks,
			SampledAt:    time.Now(),
		}

		if err := database.DB.WithContext(ctx).Create(&health_sample).Error; err != nil {
			fmt.Println("Failed to create monitoring health:", err)

		}

		resolveClearedAnnotations(ctx, "RUN_STUCK")
		resolveClearedAnnotations(ctx, "RUN_LOST")
		resolveDuplicateExecutionAnnotations(ctx)
		resolveScheduleDriftAnnotations(ctx)
		resolveQueueBacklogAnnotations(ctx)

		time.Sleep(monitoringInterval)
	}
}

// ---------- RFC-005 §10 Stuck Detection ----------

func checkStuckTasks(ctx context.Context) error {
	var tasks []models.Task

	err := database.DB.WithContext(ctx).Where("status = ?", "Running").Find(&tasks).Error

	if err != nil {
		fmt.Println("Failed to query running tasks:", err)
		return err
	}

	for _, task := range tasks {
		var latestEvent models.EventEnvelope

		err := database.DB.WithContext(ctx).
			Where("job_id = ?", task.JobId).
			Order("occurred_at DESC").
			First(&latestEvent).Error

		if err != nil {
			fmt.Println("Failed to query latest event for task:", task.JobId, err)
			continue
		}

		var latest_attempt models.Attempt

		err = database.DB.WithContext(ctx).
			Where("job_id = ?", latestEvent.JobId).
			Order("created_at DESC").
			First(&latest_attempt).Error

		if err != nil {
			fmt.Println("Failed to query latest attempt for task:", task.JobId, err)
			continue
		}

		var latest_heartbeat models.WorkerHeartbeat

		err = database.DB.WithContext(ctx).
			Where("worker_id = ?", latest_attempt.WorkerId).
			Order("occurred_at DESC").
			First(&latest_heartbeat).Error

		if err != nil {
			fmt.Println("Failed to query latest heartbeat for worker:", latest_attempt.WorkerId, err)
			continue
		}

		taskIsStale := time.Since(latestEvent.OccurredAt) > stuckThreshold
		workerIsHealthy := time.Since(latest_heartbeat.OccurredAt) < 5*time.Minute

		if taskIsStale && workerIsHealthy {
			var existing models.MonitoringAnnotation
			notFoundErr := database.DB.WithContext(ctx).
				Where("subject_id = ? AND type = ? AND resolved_at IS NULL", task.JobId, "RUN_STUCK").
				First(&existing).Error

			if notFoundErr != nil {
				evidenceMap := map[string]interface{}{
					"last_event_at":     latestEvent.OccurredAt,
					"threshold_seconds": int(stuckThreshold.Seconds()),
				}

				evidenceJSON, _ := json.Marshal(evidenceMap)

				new_annotation := models.MonitoringAnnotation{
					AnnotationID: ulid.Make().String(),
					Type:         "RUN_STUCK",
					SubjectType:  "TASK",
					SubjectID:    task.JobId,
					DerivedAt:    time.Now().UTC(),
					Evidence:     string(evidenceJSON),
				}

				if err := database.DB.WithContext(ctx).Create(&new_annotation).Error; err != nil {
					fmt.Println("Failed to create monitoring annotation for task:", task.JobId, err)
					continue
				}
			}
		}
	}

	return nil
}

// ---------- RFC-005 §11 Lost Detection ----------

func checkLostTasks(ctx context.Context) error {
	var tasks []models.Task

	err := database.DB.WithContext(ctx).Where("status = ?", "Running").Find(&tasks).Error

	if err != nil {
		fmt.Println("Failed to query lost tasks monitoring annotations:", err)
		return err
	}

	for _, task := range tasks {
		var latestEvent models.EventEnvelope

		err := database.DB.WithContext(ctx).
			Where("job_id = ?", task.JobId).
			Order("occurred_at DESC").
			First(&latestEvent).Error

		if err != nil {
			fmt.Println("Failed to query latest event for task:", task.JobId, err)
			continue
		}

		var latest_attempt models.Attempt

		err = database.DB.WithContext(ctx).
			Where("job_id = ?", latestEvent.JobId).
			Order("created_at DESC").
			First(&latest_attempt).Error

		if err != nil {
			fmt.Println("Failed to query latest attempt for task:", task.JobId, err)
			continue
		}

		var latest_heartbeat models.WorkerHeartbeat

		err = database.DB.WithContext(ctx).
			Where("worker_id = ?", latest_attempt.WorkerId).
			Order("occurred_at DESC").
			First(&latest_heartbeat).Error

		if err != nil {
			fmt.Println("Failed to query latest heartbeat for worker:", latest_attempt.WorkerId, err)
		}

		taskIsStale := time.Since(latestEvent.OccurredAt) > stuckThreshold
		workerIsUnhealthy := errors.Is(err, gorm.ErrRecordNotFound) || (err == nil && time.Since(latest_heartbeat.OccurredAt) > workerHeartbeatFreshnessWindow)

		if taskIsStale && workerIsUnhealthy {
			var existing models.MonitoringAnnotation
			notFoundErr := database.DB.WithContext(ctx).
				Where("subject_id = ? AND type = ? AND resolved_at IS NULL", task.JobId, "RUN_LOST").
				First(&existing).Error

			if notFoundErr != nil {
				evidenceMap := map[string]interface{}{
					"last_event_at":     latestEvent.OccurredAt,
					"threshold_seconds": int(stuckThreshold.Seconds()),
				}

				evidenceJSON, _ := json.Marshal(evidenceMap)

				new_annotation := models.MonitoringAnnotation{
					AnnotationID: ulid.Make().String(),
					Type:         "RUN_LOST",
					SubjectType:  "TASK",
					SubjectID:    task.JobId,
					DerivedAt:    time.Now().UTC(),
					Evidence:     string(evidenceJSON),
				}

				if err := database.DB.WithContext(ctx).Create(&new_annotation).Error; err != nil {
					fmt.Println("Failed to create monitoring annotation for task:", task.JobId, err)
					continue
				}
			}
		}
	}
	return nil
}

// ---------- Shared resolver for RUN_STUCK / RUN_LOST ----------

func resolveClearedAnnotations(ctx context.Context, annotationType string) {
	var annotationsHistory []models.MonitoringAnnotation

	err := database.DB.WithContext(ctx).
		Where("type = ? AND resolved_at IS NULL", annotationType).
		Find(&annotationsHistory).Error

	if err != nil {
		fmt.Println("Failed to query unresolved monitoring annotations:", err)
		return
	}

	for _, annotation := range annotationsHistory {
		var task models.Task

		err := database.DB.WithContext(ctx).
			Where("job_id = ?", annotation.SubjectID).
			Order("occurred_at DESC").
			First(&task).Error

		if err != nil {
			fmt.Println("Failed to query task:", annotation.SubjectID, err)
			continue
		}

		var latestEvent models.EventEnvelope

		err = database.DB.WithContext(ctx).
			Where("job_id = ?", annotation.SubjectID).
			Order("occurred_at DESC").
			First(&latestEvent).Error

		if err != nil {
			fmt.Println("Failed to query latest event for task:", annotation.SubjectID, err)
			continue
		}

		isTerminal := task.Status == "Completed" || task.Status == "Failed"
		isFreshAgain := time.Since(latestEvent.OccurredAt) < stuckThreshold

		if isTerminal || isFreshAgain {
			annotation.ResolvedAt = time.Now().UTC()

			if err := database.DB.WithContext(ctx).Save(&annotation).Error; err != nil {
				fmt.Println("Failed to resolve monitoring annotation:", annotation.AnnotationID, err)
				continue
			}
		}
	}
}

// ---------- RFC-005 §12 Duplicate Execution Detection ----------

func checkDuplicateExecution(ctx context.Context) error {
	var results []struct {
		JobId string
		Count int64
	}

	err := database.DB.WithContext(ctx).Model(&models.Attempt{}).
		Select("job_id, count(*) as count").
		Where("status IN ?", []string{"Claimed", "Started"}).
		Group("job_id").
		Having("count(*) > 1").
		Find(&results).Error

	if err != nil {
		fmt.Println("Failed to query lost tasks monitoring annotations:", err)
		return err
	}

	for _, r := range results {
		var existing models.MonitoringAnnotation
		notFoundErr := database.DB.WithContext(ctx).
			Where("subject_id = ? AND type = ? AND resolved_at IS NULL", r.JobId, "RUN_DUPLICATE_SUSPECTED").
			First(&existing).Error

		if notFoundErr != nil {
			evidenceMap := map[string]interface{}{
				"active_attempt_count": r.Count,
			}

			evidenceJSON, _ := json.Marshal(evidenceMap)
			new_annotation := models.MonitoringAnnotation{
				AnnotationID: ulid.Make().String(),
				Type:         "RUN_DUPLICATE_SUSPECTED",
				SubjectType:  "TASK",
				SubjectID:    r.JobId,
				DerivedAt:    time.Now().UTC(),
				Evidence:     string(evidenceJSON),
			}

			if err := database.DB.WithContext(ctx).Create(&new_annotation).Error; err != nil {
				fmt.Println("Failed to create monitoring annotation for task:", r.JobId, err)
				continue
			}
		}
	}
	return nil
}

func resolveDuplicateExecutionAnnotations(ctx context.Context) {
	var annotations []models.MonitoringAnnotation
	database.DB.WithContext(ctx).
		Where("type = ? AND resolved_at IS NULL", "RUN_DUPLICATE_SUSPECTED").
		Find(&annotations)

	for _, a := range annotations {
		var count int64
		database.DB.WithContext(ctx).Model(&models.Attempt{}).
			Where("job_id = ? AND status IN ?", a.SubjectID, []string{"Claimed", "Started"}).
			Count(&count)

		if count <= 1 {
			a.ResolvedAt = time.Now().UTC()
			database.DB.WithContext(ctx).Save(&a)
		}
	}
}

// ---------- RFC-005 §13 Schedule Monitoring ----------

func checkScheduleDrift(ctx context.Context) error {
	var overdueSchedules []models.ScheduleDefinition
	cutoff := time.Now().Add(-missedOccurrenceThreshold)

	err := database.DB.WithContext(ctx).
		Where("enabled = ?", true).
		Where("next_run_at < ?", cutoff).
		Find(&overdueSchedules).Error

	if err != nil {
		fmt.Println("Failed to query lost tasks monitoring annotations:", err)
		return err
	}

	for _, sched := range overdueSchedules {
		var existing models.MonitoringAnnotation
		notFoundErr := database.DB.WithContext(ctx).
			Where("subject_type = ? AND subject_id = ? AND type = ? AND resolved_at IS NULL", "SCHEDULE", sched.ScheduleId, "SCHEDULE_MISSED").
			First(&existing).Error

		if notFoundErr != nil {
			evidenceMap := map[string]interface{}{
				"overdued_next_run_at": time.Since(sched.NextRunAt),
			}

			evidenceJSON, _ := json.Marshal(evidenceMap)
			new_annotation := models.MonitoringAnnotation{
				AnnotationID: ulid.Make().String(),
				Type:         "SCHEDULE_MISSED",
				SubjectType:  "SCHEDULE",
				SubjectID:    sched.ScheduleId,
				DerivedAt:    time.Now().UTC(),
				Evidence:     string(evidenceJSON),
			}

			if err := database.DB.WithContext(ctx).Create(&new_annotation).Error; err != nil {
				fmt.Println("Failed to create monitoring annotation for schedule:", sched.ScheduleId, err)
				continue
			}
		}
	}
	return nil
}

func resolveScheduleDriftAnnotations(ctx context.Context) {
	var annotations []models.MonitoringAnnotation
	database.DB.WithContext(ctx).
		Where("type = ? AND resolved_at IS NULL", "SCHEDULE_MISSED").
		Find(&annotations)

	for _, a := range annotations {
		var sched models.ScheduleDefinition
		database.DB.WithContext(ctx).Where("schedule_id = ?", a.SubjectID).First(&sched)

		if time.Since(sched.NextRunAt) < missedOccurrenceThreshold {
			a.ResolvedAt = time.Now().UTC()
			database.DB.WithContext(ctx).Save(&a)
		}
	}
}

// ---------- RFC-005 §14 Queue Monitoring ----------

func checkQueueBacklog(ctx context.Context) error {
	var latest models.QueueHealth
	err := database.DB.WithContext(ctx).Order("sampled_at DESC").First(&latest).Error

	if err != nil {
		fmt.Println("Failed to query lost tasks monitoring annotations:", err)
		return err
	}

	if latest.PendingCount > backlogThreshold {
		var existing models.MonitoringAnnotation

		notFoundErr := database.DB.WithContext(ctx).
			Where("subject_type = ? AND subject_id = ? AND type = ? AND resolved_at IS NULL", "QUEUE", cache.TaskStream, "QUEUE_BACKLOG").
			First(&existing).Error

		if notFoundErr != nil {
			evidenceMap := map[string]interface{}{
				"pending_count":      latest.PendingCount,
				"pending_oldest_age": latest.OldestPendingAgeSeconds,
			}

			evidenceJSON, _ := json.Marshal(evidenceMap)
			new_annotation := models.MonitoringAnnotation{
				AnnotationID: ulid.Make().String(),
				Type:         "QUEUE_BACKLOG",
				SubjectType:  "QUEUE",
				SubjectID:    cache.TaskStream,
				DerivedAt:    time.Now().UTC(),
				Evidence:     string(evidenceJSON),
			}

			if err := database.DB.WithContext(ctx).Create(&new_annotation).Error; err != nil {
				fmt.Println("Failed to create monitoring annotation for queue:", cache.TaskStream, err)
			}
		}
	}
	return nil
}

func resolveQueueBacklogAnnotations(ctx context.Context) {
	var annotations []models.MonitoringAnnotation
	database.DB.WithContext(ctx).
		Where("type = ? AND resolved_at IS NULL", "QUEUE_BACKLOG").
		Find(&annotations)

	if len(annotations) == 0 {
		return
	}

	var latest models.QueueHealth
	database.DB.WithContext(ctx).Order("sampled_at DESC").First(&latest)

	if latest.PendingCount <= backlogThreshold {
		for _, a := range annotations {
			a.ResolvedAt = time.Now().UTC()
			database.DB.WithContext(ctx).Save(&a)
		}
	}
}
