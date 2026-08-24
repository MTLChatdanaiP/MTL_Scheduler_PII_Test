package handlers

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/oklog/ulid/v2"
	"gorm.io/gorm/clause"

	"MTL_Scheduler_PII_Test/internals/database"
	"MTL_Scheduler_PII_Test/internals/events"
	"MTL_Scheduler_PII_Test/internals/models"
	"MTL_Scheduler_PII_Test/internals/pii"
)

// NOTE: duplicate of internals/pii.PIIType — unused in this file, left over from an earlier draft before pii.AllTypes/pii.Detect existed
type PIIType string

const (
	Email      PIIType = "Email"
	Phone      PIIType = "Phone"
	SSN        PIIType = "SSN"
	CreditCard PIIType = "CreditCard"
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
	// PRD §9 Job Identity Requirements: stable, sortable identifier generated at creation time, before the row is persisted
	task.JobId = ulid.Make().String()
	task.ExecutionChainId = task.JobId

	var exeChain = models.ExecutionChain{
		ExecutionChainId: task.ExecutionChainId,
	}

	err := database.DB.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns: []clause.Column{
				{Name: "execution_chain_id"},
			},
			DoNothing: true,
		}).
		Create(&exeChain).Error

	if err != nil {
		fmt.Println("FAILED TO UPSERT EXECUTION CHAIN: ", err)
	}

	temp := make(map[pii.PIIType]int)

	for _, piiType := range pii.AllTypes {
		temp[piiType] = 0
	}

	// RFC-006 §12 Pre-Execution Scanning: "an implementation may scan... before publishing to Redis... The chosen boundary affects whether raw PII enters Redis." This project scans and redacts before the task is ever saved or published, so raw PII never enters Postgres or the Redis stream
	for _, value := range pii.Detect(task.Payload) {
		temp[value.Type] += 1

		// RFC-006 §7 Scan Model — Policy Evaluation stage, REDACT branch: finding is persisted separately (Claim Check) before the payload is rewritten
		record := models.PIIRecord{JobId: task.JobId, Type: string(value.Type), Value: value.Match, Index: temp[value.Type], Source: "JOB_PAYLOAD", Confidence: 1.0}
		database.DB.WithContext(ctx).Create(&record)
		fmt.Println(record)

		// RFC-006 §14 PII-Safe Logging: payload is rewritten so no downstream system (Redis, worker logs, monitoring) ever sees the raw value
		task.Payload = pii.Replacer(task.Payload, value.Match, value.Type, strconv.Itoa(temp[value.Type]))

		fmt.Println(task.Payload)
	}
	fmt.Println("Final: ", task.Payload)

	// RFC-002 §12 Timezone Requirements / §4 Domain Model (expected_at): normalizes an unset or past-due RunAt to "now", meaning immediate tasks flow through the same scheduler poll loop as scheduled ones (RFC-002 §7 Scheduling Flow) rather than publishing directly here
	if task.RunAt.Before(time.Now()) {
		task.RunAt = time.Now()
	}

	// RFC-000 §5.3 Domain Events Are Facts: run.created-equivalent event
	database.DB.WithContext(c.Request.Context()).Create(&task)
	events.LogEvent(ctx, task.JobId, "task.created", "api")

	c.JSON(http.StatusCreated, task)
}

func GetTask(c *gin.Context) {
	fmt.Println("[Database] Fetching all tasks")

	var Tasks []models.Task

	database.DB.WithContext(c.Request.Context()).Find(&Tasks)

	c.JSON(http.StatusOK, Tasks)
}
