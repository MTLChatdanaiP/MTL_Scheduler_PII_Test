package handlers

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"

	"MTL_Scheduler_PII_Test/internals/database"
	"MTL_Scheduler_PII_Test/internals/models"
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
	fmt.Println("[Database] Fetching all tasks")

	var Tasks []models.Task

	database.DB.WithContext(c.Request.Context()).Find(&Tasks)

	c.JSON(http.StatusOK, Tasks)
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
