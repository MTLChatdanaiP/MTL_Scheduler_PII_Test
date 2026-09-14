package routes

import (
	"github.com/gin-gonic/gin"

	"MTL_Scheduler_PII_Test/internals/auth"
	"MTL_Scheduler_PII_Test/internals/handlers"
)

func SetupRouter() *gin.Engine {
	r := gin.Default()

	// --- Tasks & Runs ---
	r.POST("/tasks", handlers.CreateTask)                           // PRD §10.1
	r.GET("/runs", auth.RequireScope("job.read"), handlers.GetTask) // RFC-008 §5.2, §13
	r.POST("/runs/:job_id/rerun", handlers.RerunTaskPost)

	// --- PII ---
	r.GET("/admin/pii-vault/:job_id", auth.RequireScope("pii.raw_value.read"), handlers.GetDecryptedPII) // RFC-006 §17
	r.POST("/pii/dry-run", auth.RequireScope("pii.policy.validate"), handlers.PostDryRun)                // RFC-006 §21
	r.GET("/pii/findings", auth.RequireScope("pii.findings.read"), handlers.SearchPII)                   // RFC-008 §5.7, §13

	// --- PII Policy ---
	r.GET("/pii/policy", auth.RequireScope("pii.policy.read"), handlers.GetActivePolicy)              // RFC-006 §16, RFC-008 §13
	r.POST("/pii/policy/reload", auth.RequireScope("pii.policy.activate"), handlers.PostReloadPolicy) // RFC-006 §16

	// --- Runs & Monitoring ---
	r.GET("/runs/:run_id/timeline", auth.RequireScope("job.logs.read"), handlers.GetEventByJobId) // RFC-008 §6, §13
	r.GET("/projection/:job_id", auth.RequireScope("job.read"), handlers.GetRunProjectionByJobId) // RFC-008 §5.3
	r.GET("/runs/:run_id/metrics", auth.RequireScope("job.read"), handlers.GetRunMetrics)         // RFC-008 §13
	r.GET("/execution-chains/:id", auth.RequireScope("job.read"), handlers.GetRunChain)           // RFC-008 §13
	r.GET("/metrics", auth.RequireScope("job.read"), handlers.GetSystemMetrics)                   // RFC-008 §5.1

	// --- Scheduling ---
	r.PATCH("/schedules/:schedule_id/toggle", handlers.ToggleSchedule)

	// --- Alerts ---
	r.GET("/alerts", auth.RequireScope("alerts.read"), handlers.GetAlerts)                                          // RFC-007 §13, RFC-008 §13
	r.POST("/alerts/:alert_id/acknowledge", auth.RequireScope("alerts.acknowledge"), handlers.PostAcknowledgeAlert) // RFC-007 §13
	r.POST("/alerts/rules/reload", auth.RequireScope("alerts.rules.reload"), handlers.PostReloadRules)              // RFC-007 §13

	// --- Debug ---
	r.DELETE("/debug/reset", handlers.NUKE_THE_FUCKER)

	return r
}
