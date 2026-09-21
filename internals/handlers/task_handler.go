package handlers

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"MTL_Scheduler_PII_Test/internals/database"
	"MTL_Scheduler_PII_Test/internals/freshness"
	"MTL_Scheduler_PII_Test/internals/models"
	"MTL_Scheduler_PII_Test/internals/pagination"
	"MTL_Scheduler_PII_Test/internals/taskservice"
)

// PRD §10.1 Job Submission / §10.2 Immediate Jobs / §10.3 Scheduled Jobs — handles both immediate and scheduled tasks depending on whether RunAt is provided
// RFC-001 §4 Domain Model: this is where a JobRun-equivalent (Task) is created
func CreateTask(c *gin.Context) { // RFC-001 §9 Commands: CreateInitialRun
	var task models.Task

	ctx := c.Request.Context()

	if err := c.ShouldBindJSON(&task); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": err.Error(),
		})
		return
	}

	result_reply := taskservice.CreateTask_Direct(ctx, task)
	c.JSON(http.StatusCreated, result_reply)
}

func GetTask(c *gin.Context) {
	query := database.DB.WithContext(c.Request.Context()).Model(&models.Task{})

	// RFC-008 §7 Filters
	query = ApplyQueryFilters(c, query, []QueryFilter{
		// Direct Task filters
		{Param: "run_id", Column: "job_id"},
		{Param: "execution_chain_id", Column: "execution_chain_id"},
		{Param: "parent_run_id", Column: "parent_run_id"},
		{Param: "job_type", Column: "task_type"},
		{Param: "execution_state", Column: "status"},
		{Param: "retry_index", Column: "retry_index"},
		{Param: "schedule_id", Column: "schedule_id"},
		{Param: "pii_scan_status", Column: "scan_status"},
		{Param: "queue", Column: "queue"},
	})

	query = ApplyQueryRangeFilters(c, query, []QueryRangeFilter{
		{Param: "created_from", Column: "created_at", Op: ">="},
		{Param: "created_to", Column: "created_at", Op: "<="},
	})

	query, err := applyRunDerivedFilters(c, query)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	// RFC-008 §12 Pagination
	p, err := pagination.ParseParams(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	query, total, err := pagination.ApplyPagination(query, p, "created_at", &models.Task{})
	if err != nil {
		fmt.Println("[Database] Failed to paginate runs:", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to paginate runs"})
		return
	}

	var tasks []models.Task
	if err := query.Find(&tasks).Error; err != nil {
		fmt.Println("[Database] Failed to fetch runs:", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch runs"})
		return
	}

	var projections []models.RunProjection
	var totalAttempts []models.Attempt
	var piiFindings []models.PIIRecord
	var alerts []models.Alert
	var schedules []models.ScheduleDefinition
	var annotations []models.MonitoringAnnotation
	var chainJobs []models.Task

	scheduleIDs := make([]string, 0)
	jobToChain := make(map[string]string) // job_id -> execution_chain_id, used to key PII findings by chain

	for _, task := range tasks {
		if task.ScheduleId != "" {
			scheduleIDs = append(scheduleIDs, task.ScheduleId)
		}
	}

	if len(tasks) > 0 {
		jobIDs := make([]string, 0, len(tasks))
		for _, task := range tasks {
			jobIDs = append(jobIDs, task.JobId)
		}

		if err := database.DB.WithContext(c.Request.Context()).Where("job_id IN ?", jobIDs).Find(&projections).Error; err != nil {
			fmt.Println("[Database] Failed to fetch run projections:", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch run projections"})
			return
		}

		if err := database.DB.WithContext(c.Request.Context()).Where("job_id IN ?", jobIDs).Order("attempt_number ASC").Find(&totalAttempts).Error; err != nil {
			fmt.Println("[Database] Failed to fetch run attempts:", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch run attempts"})
			return
		}

		// --- CHAIN-SCOPED PII LOOKUP ---
		// if has execution chainPII record data i
		chainIDs := make([]string, 0)
		seenChain := make(map[string]bool)
		for _, task := range tasks {
			if task.ExecutionChainId != "" && !seenChain[task.ExecutionChainId] {
				seenChain[task.ExecutionChainId] = true
				chainIDs = append(chainIDs, task.ExecutionChainId)
			}
		}

		if len(chainIDs) > 0 {
			if err := database.DB.WithContext(c.Request.Context()).
				Select("job_id, execution_chain_id").
				Where("execution_chain_id IN ?", chainIDs).
				Find(&chainJobs).Error; err != nil {
				fmt.Println("[Database] Failed to fetch chain members:", err)
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch chain members"})
				return
			}
		}

		chainJobIDs := make([]string, 0, len(chainJobs))
		for _, t := range chainJobs {
			chainJobIDs = append(chainJobIDs, t.JobId)
			jobToChain[t.JobId] = t.ExecutionChainId
		}

		if len(chainJobIDs) > 0 {
			if err := database.DB.WithContext(c.Request.Context()).
				Where("job_id IN ?", chainJobIDs).
				Find(&piiFindings).Error; err != nil {
				fmt.Println("[Database] Failed to fetch PII findings:", err)
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch PII findings"})
				return
			}
		}
		// --- END CHAIN-SCOPED PII LOOKUP ---

		if err := database.DB.WithContext(c.Request.Context()).Where("subject_type = ? AND subject_id IN ?", "RUN", jobIDs).Find(&alerts).Error; err != nil {
			fmt.Println("[Database] Failed to fetch run alerts:", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch run alerts"})
			return
		}

		if len(scheduleIDs) > 0 {
			if err := database.DB.WithContext(c.Request.Context()).
				Where("schedule_id IN ?", scheduleIDs).
				Find(&schedules).Error; err != nil {
				fmt.Println("[Database] Failed to fetch schedules:", err)
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch schedules"})
				return
			}
		}

		if err := database.DB.WithContext(c.Request.Context()).Where("subject_type = ? AND subject_id IN ?", "RUN", jobIDs).Find(&annotations).Error; err != nil {
			fmt.Println("[Database] Failed to fetch run annotations:", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch run annotations"})
			return
		}
	}

	projectionMap := make(map[string]models.RunProjection, len(projections))

	for _, projection := range projections {
		projectionMap[projection.JobId] = projection
	}

	attemptMap := make(map[string][]models.Attempt, len(totalAttempts))

	for _, attempt := range totalAttempts {
		attemptMap[attempt.JobId] = append(
			attemptMap[attempt.JobId],
			attempt,
		)
	}

	// piiMap is keyed by EXECUTION CHAIN ID now, not job_id — see the fetch above
	piiMap := make(map[string][]PIIFindingItem, len(piiFindings))

	for _, finding := range piiFindings {
		chainID, ok := jobToChain[finding.JobID]
		if !ok {
			continue // orphaned finding, no chain to attach it to — skip rather than guess
		}
		piiMap[chainID] = append(
			piiMap[chainID],
			PIIFindingItem{
				Type:         finding.Type,
				DetectorID:   finding.DetectorID,
				Confidence:   finding.Confidence,
				Source:       finding.Source,
				Index:        finding.Index,
				PolicyAction: finding.PolicyAction,
			},
		)
	}

	alertMap := make(map[string][]models.Alert, len(alerts))

	for _, alert := range alerts {
		alertMap[alert.SubjectID] = append(
			alertMap[alert.SubjectID],
			alert,
		)
	}

	scheduleMap := make(map[string]models.ScheduleDefinition, len(schedules))

	for _, schedule := range schedules {
		scheduleMap[schedule.ScheduleId] = schedule
	}

	annotationMap := make(map[string][]models.MonitoringAnnotation, len(annotations))

	for _, annotation := range annotations {
		annotationMap[annotation.SubjectID] = append(
			annotationMap[annotation.SubjectID],
			annotation,
		)
	}

	runs := make([]RunListItem, 0, len(tasks))

	for _, task := range tasks {
		item := RunListItem{
			Task: task,
		}

		if projection, ok := projectionMap[task.JobId]; ok {
			item.CurrentStatus = projection.CurrentStatus
			item.QueuedAt = projection.QueuedAt
			item.StartedAt = projection.StartedAt
			item.CompletedAt = projection.CompletedAt
			item.RecoveryStarted = projection.RecoveryStarted
			item.PIIFindingCount = projection.PIIFindingCount
			item.WasReclaimed = projection.WasReclaimed
			item.LastEventAt = projection.LastEventAt
		}

		if attempts, ok := attemptMap[task.JobId]; ok {
			item.Attempts = attempts
			item.AttemptCount = len(attempts)
			if len(attempts) > 0 {

				latest := attempts[len(attempts)-1]
				item.LatestWorker = latest.WorkerId
				item.Duration = latest.FinishedAt.Sub(latest.StartedAt)
			}
		}

		// was: piiMap[task.JobId] — now looked up by chain, see note above
		if findings, ok := piiMap[task.ExecutionChainId]; ok {
			item.PIIFindings = findings
		}

		if runAlerts, ok := alertMap[task.JobId]; ok {
			item.ActiveAlertCount = 0

			for _, alert := range runAlerts {
				if alert.Status == "OPEN" {
					item.ActiveAlertCount++
				}
			}
		}

		if schedule, ok := scheduleMap[task.ScheduleId]; ok {
			item.Schedule = &schedule
		}

		if runAnnotations, ok := annotationMap[task.JobId]; ok {
			item.Annotations = runAnnotations
		}

		runs = append(runs, item)
	}

	var lastTS time.Time
	var lastID uint

	if len(tasks) > 0 {
		lastTS = tasks[len(tasks)-1].CreatedAt
		lastID = tasks[len(tasks)-1].ID
	}

	page := pagination.BuildPageInfo(
		p,
		len(tasks),
		lastTS,
		lastID,
		total,
	)

	var newestLastEvent time.Time        // newestLastEvent  time.Time
	for _, proj := range projectionMap { // you already have this map
		if proj.LastEventAt.After(newestLastEvent) {
			newestLastEvent = proj.LastEventAt
		}
	}

	watermark, err := freshness.CurrentWatermark(c.Request.Context())
	if err != nil {
		fmt.Println("failed to compute watermark:", err)
	}

	c.JSON(http.StatusOK, gin.H{
		"runs":      runs,
		"page":      page,
		"freshness": freshness.FreshnessFrom(newestLastEvent),
		"live":      freshness.LiveInfo{Watermark: watermark},
	})
}

type RunDetailResponse struct {
	Run              RunListItem             `json:"run"`
	Freshness        freshness.FreshnessInfo `json:"freshness"`
	Live             freshness.LiveInfo      `json:"live"`
	PayloadSizeBytes int                     `json:"payload_size_bytes"`
}

func GetTaskDetail(c *gin.Context) {
	jobID := c.Param("run_id")

	query := database.DB.WithContext(c.Request.Context()).Where("job_id = ?", jobID)

	var task models.Task
	if err := query.First(&task).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "run not found"})
			return
		}

		fmt.Println("[Database] Failed to fetch run:", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch run"})
		return
	}

	var projection models.RunProjection
	var attempts []models.Attempt
	var piiFindings []models.PIIRecord
	var alerts []models.Alert
	var schedule models.ScheduleDefinition
	var annotations []models.MonitoringAnnotation

	if err := database.DB.WithContext(c.Request.Context()).Where("job_id = ?", jobID).First(&projection).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			fmt.Println("[Database] Failed to fetch run projection:", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch run projection"})
			return
		}
	}

	if err := database.DB.WithContext(c.Request.Context()).Where("job_id = ?", jobID).Order("attempt_number ASC").Find(&attempts).Error; err != nil {
		fmt.Println("[Database] Failed to fetch run attempts:", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch run attempts"})
		return
	}

	if err := database.DB.WithContext(c.Request.Context()).Where("job_id = ?", jobID).Find(&piiFindings).Error; err != nil {
		fmt.Println("[Database] Failed to fetch PII findings:", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch PII findings"})
		return
	}

	if err := database.DB.WithContext(c.Request.Context()).Where("subject_type = ? AND subject_id = ?", "RUN", jobID).Find(&alerts).Error; err != nil {
		fmt.Println("[Database] Failed to fetch run alerts:", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch run alerts"})
		return
	}

	if task.ScheduleId != "" {
		if err := database.DB.WithContext(c.Request.Context()).Where("schedule_id = ?", task.ScheduleId).First(&schedule).Error; err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				fmt.Println("[Database] Failed to fetch schedule:", err)
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch schedule"})
				return
			}
		}
	}

	if err := database.DB.WithContext(c.Request.Context()).Where("subject_type = ? AND subject_id = ?", "RUN", jobID).Find(&annotations).Error; err != nil {
		fmt.Println("[Database] Failed to fetch run annotations:", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch run annotations"})
		return
	}

	item := RunListItem{
		Task: task,
	}

	item.CurrentStatus = projection.CurrentStatus
	item.QueuedAt = projection.QueuedAt
	item.StartedAt = projection.StartedAt
	item.CompletedAt = projection.CompletedAt
	item.RecoveryStarted = projection.RecoveryStarted
	item.PIIFindingCount = projection.PIIFindingCount
	item.WasReclaimed = projection.WasReclaimed
	item.LastEventAt = projection.LastEventAt

	item.Attempts = attempts
	item.AttemptCount = len(attempts)

	if len(attempts) > 0 {
		latest := attempts[len(attempts)-1]
		item.LatestWorker = latest.WorkerId
		if !projection.StartedAt.IsZero() && !projection.CompletedAt.IsZero() {
			item.Duration = projection.CompletedAt.Sub(projection.StartedAt)
		}
	}

	for _, finding := range piiFindings {
		item.PIIFindings = append(item.PIIFindings, PIIFindingItem{
			Type:         finding.Type,
			DetectorID:   finding.DetectorID,
			Confidence:   finding.Confidence,
			Source:       finding.Source,
			Index:        finding.Index,
			PolicyAction: finding.PolicyAction,
		})
	}

	for _, alert := range alerts {
		if alert.Status == "OPEN" {
			item.ActiveAlertCount++
		}
	}

	if task.ScheduleId != "" && schedule.ScheduleId != "" {
		item.Schedule = &schedule
	}

	item.Annotations = annotations

	freshnessInfo := freshness.FreshnessFrom(projection.LastEventAt)
	watermark, err := freshness.CurrentWatermark(c.Request.Context())
	if err != nil {
		fmt.Println("failed to compute watermark:", err)
	}

	c.JSON(http.StatusOK, RunDetailResponse{
		Run:              item,
		Freshness:        freshnessInfo,
		Live:             freshness.LiveInfo{Watermark: watermark},
		PayloadSizeBytes: len(task.Payload),
	})
}

type ToggleScheduleRequest struct {
	Enabled bool `json:"enabled"`
}

func ToggleSchedule(c *gin.Context) {
	scheduleId := c.Param("schedule_id")
	ctx := c.Request.Context()

	var req ToggleScheduleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var def models.ScheduleDefinition

	results := database.DB.WithContext(ctx).
		Where("schedule_id = ?", scheduleId).
		First(&def)

	if results.Error != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "schedule not found, u goofed up"})
		return
	}

	def.Enabled = req.Enabled
	database.DB.WithContext(ctx).Save(&def)

	c.JSON(http.StatusOK, gin.H{
		"schedule_id": def.ScheduleId,
		"enabled":     def.Enabled,
	})
}

func RerunTaskPost(c *gin.Context) {
	jobId := c.Param("job_id")
	ctx := c.Request.Context()

	result_reply, err := taskservice.RerunTask(ctx, jobId)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, result_reply)
}
