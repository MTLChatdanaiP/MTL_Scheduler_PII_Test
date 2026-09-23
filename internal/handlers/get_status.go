package handlers

import (
	"MTL_Scheduler_PII_Test/internal/database"
	"MTL_Scheduler_PII_Test/internal/freshness"
	"MTL_Scheduler_PII_Test/internal/models"
	"MTL_Scheduler_PII_Test/internal/pagination"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type WorkerListItem struct {
	WorkerId           string
	InstanceId         string
	Hostname           string
	StartedAt          time.Time
	ConfiguredCapacity int
	LastHeartbeat      time.Time
	RunningAttempts    int
	Capacity           int
}

type WorkerDetailResponse struct {
	Worker    WorkerDetailItem        `json:"worker"` // or a proper struct
	Freshness freshness.FreshnessInfo `json:"freshness"`
	Live      freshness.LiveInfo      `json:"live"`
}

type WorkerDetailItem struct {
	WorkerListItem
	ActiveAttempts    []models.Attempt `json:"active_attempts"`
	RecentCompletions []models.Attempt `json:"recent_completions"`
	RecentFailures    []models.Attempt `json:"recent_failures"`
	Alerts            []models.Alert   `json:"alerts"`
	ActiveAlertCount  int              `json:"active_alert_count"`
}

func GetWorkers(c *gin.Context) {
	query := database.DB.WithContext(c.Request.Context()).Model(&models.Worker{})

	p, err := pagination.ParseParams(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	query, total, err := pagination.ApplyPagination(query, p, "started_at", &models.Worker{})
	if err != nil {
		fmt.Println("[Database] Failed to paginate workers:", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to paginate workers"})
		return
	}

	var workers []models.Worker
	if err := query.Order("started_at DESC").Find(&workers).Error; err != nil {
		fmt.Println("[Database] Failed to fetch workers:", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch workers"})
		return
	}

	seenWorker := make(map[string]bool)
	latestWorkers := make([]models.Worker, 0, len(workers))
	for _, w := range workers {
		if seenWorker[w.WorkerId] {
			continue
		}
		seenWorker[w.WorkerId] = true
		latestWorkers = append(latestWorkers, w)
	}
	workers = latestWorkers

	workerIDs := make([]string, 0, len(workers))
	for _, worker := range workers {
		workerIDs = append(workerIDs, worker.WorkerId)
	}

	var heartbeats []models.WorkerHeartbeat

	if len(workerIDs) > 0 {
		since := time.Now().UTC().Add(-24 * time.Hour)

		if err := database.DB.WithContext(c.Request.Context()).Where("worker_id IN ? AND occurred_at >= ?", workerIDs, since).Order("occurred_at DESC").Find(&heartbeats).Error; err != nil {
			fmt.Println("[Database] Failed to fetch worker heartbeats:", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch worker heartbeats"})
			return
		}
	}

	latestHeartbeats := make(map[string]models.WorkerHeartbeat)

	for _, heartbeat := range heartbeats {
		if _, seen := latestHeartbeats[heartbeat.WorkerId]; seen {
			continue
		}

		latestHeartbeats[heartbeat.WorkerId] = heartbeat
	}

	var result []WorkerListItem

	for _, worker := range workers {
		item := WorkerListItem{
			WorkerId:           worker.WorkerId,
			InstanceId:         worker.InstanceId,
			Hostname:           worker.Hostname,
			StartedAt:          worker.StartedAt,
			ConfiguredCapacity: worker.ConfiguredCapacity,
		}

		if heartbeat, ok := latestHeartbeats[worker.WorkerId]; ok {
			item.LastHeartbeat = heartbeat.OccurredAt
			item.RunningAttempts = heartbeat.RunningAttempts
			item.Capacity = heartbeat.Capacity
		}

		result = append(result, item)
	}

	var lastTS time.Time
	var lastID uint

	if len(workers) > 0 {
		lastTS = workers[len(workers)-1].StartedAt
		lastID = workers[len(workers)-1].ID
	}

	page := pagination.BuildPageInfo(p, len(workers), lastTS, lastID, total)

	var newestLastEvent time.Time          // newestLastEvent  time.Time
	for _, heartbeat := range heartbeats { // you already have this map
		if heartbeat.OccurredAt.After(newestLastEvent) {
			newestLastEvent = heartbeat.OccurredAt
		}
	}

	watermark, err := freshness.CurrentWatermark(c.Request.Context())
	if err != nil {
		fmt.Println("failed to compute watermark:", err)
	}

	c.JSON(http.StatusOK, gin.H{
		"workers":   result,
		"page":      page,
		"freshness": freshness.FreshnessFrom(newestLastEvent),
		"live":      freshness.LiveInfo{Watermark: watermark},
	})
}

func GetWorkerDetail(c *gin.Context) {
	workerID := c.Param("worker_id")

	var worker models.Worker
	if err := database.DB.WithContext(c.Request.Context()).Where("worker_id = ?", workerID).First(&worker).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "worker not found"})
			return
		}

		fmt.Println("[Database] Failed to fetch worker:", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch worker"})
		return
	}

	var latestHeartbeat models.WorkerHeartbeat
	if err := database.DB.WithContext(c.Request.Context()).Where("worker_id = ?", workerID).Order("occurred_at DESC").First(&latestHeartbeat).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			fmt.Println("[Database] Failed to fetch worker heartbeat:", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch worker heartbeat"})
			return
		}
	}

	var attempts []models.Attempt
	if err := database.DB.WithContext(c.Request.Context()).Where("worker_id = ?", workerID).Order("started_at DESC").Find(&attempts).Error; err != nil {
		fmt.Println("[Database] Failed to fetch worker attempts:", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch worker attempts"})
		return
	}

	var alerts []models.Alert
	if err := database.DB.WithContext(c.Request.Context()).Where("subject_type = ? AND subject_id = ?", "WORKER", workerID).Find(&alerts).Error; err != nil {
		fmt.Println("[Database] Failed to fetch worker alerts:", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch worker alerts"})
		return
	}

	activeAttempts := make([]models.Attempt, 0)
	recentCompletions := make([]models.Attempt, 0)
	recentFailures := make([]models.Attempt, 0)

	for _, attempt := range attempts {
		if attempt.Status == "Claimed" || attempt.Status == "Started" {
			activeAttempts = append(activeAttempts, attempt)
		}

		if attempt.Status == "Succeeded" {
			recentCompletions = append(recentCompletions, attempt)
		}

		if attempt.Status == "Failed" || attempt.Status == "Abandoned" {
			recentFailures = append(recentFailures, attempt)
		}
	}

	activeAlertCount := 0
	for _, alert := range alerts {
		if alert.Status == "OPEN" {
			activeAlertCount++
		}
	}

	freshnessInfo := freshness.FreshnessFrom(latestHeartbeat.OccurredAt)
	watermark, err := freshness.CurrentWatermark(c.Request.Context())
	if err != nil {
		fmt.Println("failed to compute watermark:", err)
	}

	item := WorkerDetailItem{
		WorkerListItem: WorkerListItem{
			WorkerId:           worker.WorkerId,
			InstanceId:         worker.InstanceId,
			Hostname:           worker.Hostname,
			StartedAt:          worker.StartedAt,
			ConfiguredCapacity: worker.ConfiguredCapacity,
			LastHeartbeat:      latestHeartbeat.OccurredAt,
			RunningAttempts:    latestHeartbeat.RunningAttempts,
			Capacity:           latestHeartbeat.Capacity,
		},
		ActiveAttempts:    activeAttempts,
		RecentCompletions: recentCompletions,
		RecentFailures:    recentFailures,
		Alerts:            alerts,
		ActiveAlertCount:  activeAlertCount,
	}

	c.JSON(http.StatusOK, WorkerDetailResponse{
		Worker:    item,
		Freshness: freshnessInfo,
		Live:      freshness.LiveInfo{Watermark: watermark},
	})
}

func GetSchedules(c *gin.Context) {
	query := database.DB.WithContext(c.Request.Context()).Model(&models.ScheduleDefinition{})

	p, err := pagination.ParseParams(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	query = ApplyQueryFilters(c, query, []QueryFilter{
		{Param: "enabled", Column: "enabled"},
		{Param: "schedule_id", Column: "schedule_id"},
	})

	query, total, err := pagination.ApplyPagination(query, p, "created_at", &models.ScheduleDefinition{})
	if err != nil {
		fmt.Println("[Database] Failed to paginate schedules:", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to paginate schedules"})
		return
	}

	var schedules []models.ScheduleDefinition
	if err := query.Find(&schedules).Error; err != nil {
		fmt.Println("[Database] Failed to fetch schedules:", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch schedules"})
		return
	}

	var lastTS time.Time
	var lastID uint

	if len(schedules) > 0 {
		lastTS = schedules[len(schedules)-1].CreatedAt
		lastID = schedules[len(schedules)-1].ID
	}

	page := pagination.BuildPageInfo(p, len(schedules), lastTS, lastID, total)

	var newestLastEvent time.Time

	for _, schedule := range schedules {
		if schedule.UpdatedAt.After(newestLastEvent) {
			newestLastEvent = schedule.UpdatedAt
		}
	}

	watermark, err := freshness.CurrentWatermark(c.Request.Context())
	if err != nil {
		fmt.Println("failed to compute watermark:", err)
	}

	c.JSON(http.StatusOK, gin.H{
		"schedules": schedules,
		"page":      page,
		"freshness": freshness.FreshnessFrom(newestLastEvent),
		"live": freshness.LiveInfo{
			Watermark: watermark,
		},
	})
}

type ScheduleDetailResponse struct {
	Schedule     models.ScheduleDefinition     `json:"schedule"`
	RecentRuns   []ScheduleRunItem             `json:"recent_runs"`
	Annotations  []models.MonitoringAnnotation `json:"annotations"`
	NextExpected *time.Time                    `json:"next_expected_at,omitempty"`

	Freshness freshness.FreshnessInfo `json:"freshness"`
	Live      freshness.LiveInfo      `json:"live"`
}

type ScheduleRunItem struct {
	RunID         string     `json:"run_id"`
	ExpectedAt    time.Time  `json:"expected_at"`
	CreatedAt     time.Time  `json:"created_at"`
	StartedAt     *time.Time `json:"started_at,omitempty"`
	Status        string     `json:"status"`
	CreationDrift float64    `json:"creation_drift_seconds"`
	StartDrift    *float64   `json:"start_drift_seconds,omitempty"`
}

func GetScheduleDetail(c *gin.Context) {
	scheduleID := c.Param("schedule_id")

	var schedule models.ScheduleDefinition
	if err := database.DB.WithContext(c.Request.Context()).Where("schedule_id = ?", scheduleID).First(&schedule).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "schedule not found"})
			return
		}

		fmt.Println("[Database] Failed to fetch schedule:", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch schedule"})
		return
	}

	var tasks []models.Task
	if err := database.DB.WithContext(c.Request.Context()).Where("schedule_id = ?", scheduleID).Order("created_at DESC").Limit(100).Find(&tasks).Error; err != nil {
		fmt.Println("[Database] Failed to fetch schedule runs:", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch schedule runs"})
		return
	}

	jobIDs := make([]string, 0, len(tasks))
	for _, task := range tasks {
		jobIDs = append(jobIDs, task.JobId)
	}

	var projections []models.RunProjection
	if len(jobIDs) > 0 {
		if err := database.DB.WithContext(c.Request.Context()).Where("job_id IN ?", jobIDs).Find(&projections).Error; err != nil {
			fmt.Println("[Database] Failed to fetch run projections:", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch run projections"})
			return
		}
	}

	projectionMap := make(map[string]models.RunProjection, len(projections))
	for _, projection := range projections {
		projectionMap[projection.JobId] = projection
	}

	var annotations []models.MonitoringAnnotation
	if err := database.DB.WithContext(c.Request.Context()).Where("subject_type = ? AND subject_id = ?", "SCHEDULE", scheduleID).Order("derived_at DESC").Find(&annotations).Error; err != nil {
		fmt.Println("[Database] Failed to fetch schedule annotations:", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch schedule annotations"})
		return
	}

	recentRuns := make([]ScheduleRunItem, 0, len(tasks))

	for _, task := range tasks {
		item := ScheduleRunItem{
			RunID:         task.JobId,
			ExpectedAt:    task.ExpectedAt,
			CreatedAt:     task.CreatedAt,
			Status:        task.Status,
			CreationDrift: task.CreatedAt.Sub(task.ExpectedAt).Seconds(),
		}

		if projection, ok := projectionMap[task.JobId]; ok {
			if !projection.StartedAt.IsZero() {
				startedAt := projection.StartedAt
				startDrift := startedAt.Sub(task.ExpectedAt).Seconds()

				item.StartedAt = &startedAt
				item.StartDrift = &startDrift
			}
		}

		recentRuns = append(recentRuns, item)
	}

	var nextExpected *time.Time
	if !schedule.NextRunAt.IsZero() {
		nextExpected = &schedule.NextRunAt
	}

	var newestLastEvent time.Time

	if schedule.UpdatedAt.After(newestLastEvent) {
		newestLastEvent = schedule.UpdatedAt
	}

	for _, task := range tasks {
		if task.CreatedAt.After(newestLastEvent) {
			newestLastEvent = task.CreatedAt
		}
	}

	for _, annotation := range annotations {
		if annotation.DerivedAt.After(newestLastEvent) {
			newestLastEvent = annotation.DerivedAt
		}
	}

	freshnessInfo := freshness.FreshnessFrom(newestLastEvent)

	watermark, err := freshness.CurrentWatermark(c.Request.Context())
	if err != nil {
		fmt.Println("failed to compute watermark:", err)
	}

	c.JSON(http.StatusOK, ScheduleDetailResponse{
		Schedule:     schedule,
		RecentRuns:   recentRuns,
		Annotations:  annotations,
		NextExpected: nextExpected,

		Freshness: freshnessInfo,
		Live: freshness.LiveInfo{
			Watermark: watermark,
		},
	})
}
