package routes

import (
	"github.com/gin-gonic/gin"

	"MTL_Scheduler_PII_Test/internals/handlers"
)

func SetupRouter() *gin.Engine {
	r := gin.Default()

	// --- Task lifecycle ---
	// PRD §10.1 Job Submission
	r.POST("/tasks", handlers.CreateTask)
	r.GET("/tasks", handlers.GetTask)

	// --- PII ---
	// RFC-008 §5.7 PII Findings: "Raw values should not be shown by default."
	// This endpoint returns fingerprints only, matching that principle —
	// raw values are only reachable via the gated /admin/pii-vault endpoint below.
	r.GET("/pii/:job_id", handlers.GetPIIByJobId)
	// RFC-006 §17 Security: gated behind X-Admin-Key — placeholder auth,
	// not a real permission-scope system yet (see RFC-006 §17 Open Questions)
	r.GET("/admin/pii-vault/:job_id", handlers.GetDecryptedPII)

	// --- Events & Monitoring ---
	// RFC-008 §6 Timeline: "The timeline should merge facts from different
	// contexts." — this endpoint returns the raw event log for one job, an
	// early version of the RFC-008 timeline concept
	r.GET("/events/:job_id", handlers.GetEventByJobId)
	// RFC-008 §5.3 Run Detail / RFC-005 §7 Run Projection
	r.GET("/projection/:job_id", handlers.GetRunProjectionByJobId)
	r.GET("/runs/:job_id/metrics", handlers.GetRunMetrics)
	r.GET("/runs/:job_id/chain", handlers.GetRunChain)
	r.GET("/metrics", handlers.GetMetrics)

	// --- Scheduling ---
	r.PATCH("/schedules/:schedule_id/toggle", handlers.ToggleSchedule)

	// --- Debug / Dev tools ---
	r.DELETE("/debug/reset", handlers.NUKE_THE_FUCKER)
	return r
}
