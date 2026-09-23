package routes

import (
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"

	"MTL_Scheduler_PII_Test/internal/auth"
	"MTL_Scheduler_PII_Test/internal/handlers"
)

// Response shape convention for this API, RFC-008 §13:
//
// Errors:     { "error": "<message>" }
//
// Lists:      { "<plural_resource>": [...], "page": {...} }
//             add "freshness" and "live" as extra sibling keys when the
//             data comes from a projection/health table, not from the
//             source-of-truth table itself.
//
// Details:    { "<resource>": {...}, "freshness": {...}, "live": {...} }
//             (skip freshness/live if the endpoint reads a source-of-truth
//             table directly, e.g. PIIRecord)
//
// Paths mostly match RFC-008 §13's example list. Where a route differs,
// that's a deliberate choice — the RFC states paths are non-normative.

func SetupRouter() *gin.Engine {
	r := gin.Default()

	r.Use(cors.New(cors.Config{
		AllowOrigins:     []string{"http://localhost:5173"},
		AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Accept", "X-API-Key"},
		AllowCredentials: false,
	}))

	// --- Timeline ---
	r.GET("/execution-chains/:execution_chain_id/timeline", auth.RequireScope("job.logs.read"), handlers.GetChainTimeline)
	r.GET("/runs/:run_id/timeline", auth.RequireScope("job.logs.read"), handlers.GetRunTimeline)

	// --- Tasks & Runs ---
	r.POST("/tasks", auth.RequireScope("job.write"), handlers.CreateTask) // PRD §10.1
	r.GET("/runs", auth.RequireScope("job.read"), handlers.GetTask)       // RFC-008 §5.2, §13
	r.GET("/runs/:run_id", auth.RequireScope("job.read"), handlers.GetTaskDetail)
	r.POST("/runs/:job_id/rerun", auth.RequireScope("job.read"), handlers.RerunTaskPost)

	// --- Worker ---
	r.GET("/workers", auth.RequireScope("job.read"), handlers.GetWorkers)
	r.GET("/workers/:worker_id", auth.RequireScope("job.read"), handlers.GetWorkerDetail)

	// --- Schedule ---
	r.GET("/schedules", auth.RequireScope("job.read"), handlers.GetSchedules)
	r.GET("/schedules/:schedule_id", auth.RequireScope("job.read"), handlers.GetScheduleDetail)

	// --- Queue ---
	r.GET("/queues", auth.RequireScope("queue.read"), handlers.GetQueues)
	r.GET("/queues/:queue_name", auth.RequireScope("queue.read"), handlers.GetQueue)

	// --- PII ---
	r.GET("/admin/pii-vault/:job_id", auth.RequireScope("pii.raw_value.read"), handlers.GetDecryptedPII) // RFC-006 §17
	r.POST("/pii/dry-run", auth.RequireScope("pii.policy.validate"), handlers.PostDryRun)                // RFC-006 §21
	r.GET("/pii/findings", auth.RequireScope("pii.findings.read"), handlers.SearchPII)                   // RFC-008 §5.7, §13

	// --- PII Policy ---
	r.GET("/pii/policy", auth.RequireScope("pii.policy.read"), handlers.GetActivePolicy)              // RFC-006 §16, RFC-008 §13
	r.POST("/pii/policy/reload", auth.RequireScope("pii.policy.activate"), handlers.PostReloadPolicy) // RFC-006 §16

	// --- Runs & Monitoring ---
	//r.GET("/runs/:run_id/events", auth.RequireScope("job.logs.read"), handlers.GetEventByJobId)         // RFC-008 §6, §13 RETIRED
	r.GET("/projection/:job_id", auth.RequireScope("job.read"), handlers.GetRunProjectionByJobId)       // RFC-008 §5.3
	r.GET("/runs/:run_id/metrics", auth.RequireScope("job.read"), handlers.GetRunMetrics)               // RFC-008 §13
	r.GET("/execution-chains/:execution_chain_id", auth.RequireScope("job.read"), handlers.GetRunChain) // RFC-008 §13
	r.GET("/metrics", auth.RequireScope("job.read"), handlers.GetSystemMetrics)
	r.GET("/monitoring/health", auth.RequireScope("job.read"), handlers.GetMonitoringHealth) // RFC-008 §5.1
	r.GET("/overview", auth.RequireScope("job.read"), handlers.GetOverview)

	// --- Scheduling ---
	r.PATCH("/schedules/:schedule_id/toggle", auth.RequireScope("job.read"), handlers.ToggleSchedule)

	// --- Alerts ---
	r.GET("/alerts", auth.RequireScope("alerts.read"), handlers.GetAlerts)                                          // RFC-007 §13, RFC-008 §13
	r.POST("/alerts/:alert_id/acknowledge", auth.RequireScope("alerts.acknowledge"), handlers.PostAcknowledgeAlert) // RFC-007 §13
	r.POST("/alerts/rules/reload", auth.RequireScope("alerts.rules.reload"), handlers.PostReloadRules)              // RFC-007 §13

	// --- Debug ---
	r.DELETE("/debug/reset", handlers.NUKE_THE_FUCKER)

	return r
}
