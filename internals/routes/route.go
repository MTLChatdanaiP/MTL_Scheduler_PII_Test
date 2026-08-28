package routes

import (
	"github.com/gin-gonic/gin"

	"MTL_Scheduler_PII_Test/internals/handlers"
)

func SetupRouter() *gin.Engine {
	r := gin.Default()

	// PRD §10.1 Job Submission
	r.POST("/tasks", handlers.CreateTask)
	r.GET("/tasks", handlers.GetTask)
	// RFC-008 §5.7 PII Findings: "Raw values should not be shown by default." — this endpoint currently returns raw Value, a documented deviation from RFC-008's intended PII-safe read model
	r.GET("/pii/:job_id", handlers.GetPIIByJobId)
	// RFC-008 §6 Timeline: "The timeline should merge facts from different contexts." — this endpoint returns the raw event log for one job, an early version of the RFC-008 timeline concept
	r.GET("/events/:job_id", handlers.GetEventByJobId)
	// RFC-008 §5.3 Run Detail / RFC-005 §7 Run Projection
	r.GET("/projection/:job_id", handlers.GetRunProjectionByJobId)

	r.DELETE("/debug/reset", handlers.NUKE_THE_FUCKER)

	r.GET("/runs/:job_id/metrics", handlers.GetRunMetrics)
	r.GET("/runs/:job_id/chain", handlers.GetRunChain)

	r.PATCH("/schedules/:schedule_id/:flip", handlers.GetTask)

	r.GET("/metrics", handlers.GetMetrics)

	return r
}
