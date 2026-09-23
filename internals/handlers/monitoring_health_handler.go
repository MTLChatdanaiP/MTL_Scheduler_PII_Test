package handlers

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	alerts "MTL_Scheduler_PII_Test/internals/alerting"
	"MTL_Scheduler_PII_Test/internals/database"
	"MTL_Scheduler_PII_Test/internals/freshness"
	"MTL_Scheduler_PII_Test/internals/models"
	pii "MTL_Scheduler_PII_Test/internals/pii"
)

// SubsystemFreshness is one row of "when did this subsystem last observe
// something." Available=false means there is genuinely no data source for
// this subsystem yet -- not an error, a real known gap (alert evaluation
// lag today; see UnavailableReason).
type SubsystemFreshness struct {
	Subsystem         string     `json:"subsystem"`
	LastObservedAt    *time.Time `json:"last_observed_at,omitempty"`
	LagSeconds        *float64   `json:"lag_seconds,omitempty"`
	Available         bool       `json:"available"`
	UnavailableReason string     `json:"unavailable_reason,omitempty"`
}

type MonitoringHealthResponse struct {
	// The anomaly-detection sweep's own health -- a DIFFERENT question from
	// subsystem freshness below. This answers "did checkStuckTasks /
	// checkLostTasks / checkDuplicateExecution / checkScheduleDrift run
	// cleanly this cycle," not "is data fresh."
	SweepStatus       string    `json:"sweep_status"`
	SweepFailedChecks int       `json:"sweep_failed_checks"`
	SweepSampledAt    time.Time `json:"sweep_sampled_at"`

	Subsystems []SubsystemFreshness `json:"subsystems"`

	ActivePolicyName     string `json:"active_policy_name"`
	ActivePolicyVersion  int    `json:"active_policy_version"`
	ActivePolicyChecksum string `json:"active_policy_checksum"`

	LastReloadResult        string    `json:"last_reload_result"`
	LastReloadAt            time.Time `json:"last_reload_at"`
	LastReloadFailureReason string    `json:"last_reload_failure_reason,omitempty"`

	// Always "N/A" today -- there is exactly one scanner process
	// (pii.LoadedPolicy is a single atomic.Pointer, process-wide), so there
	// is nothing for a version to drift FROM. This field exists so the UI
	// can say so explicitly rather than silently omitting the row.
	ScannerDrift string `json:"scanner_drift"`

	KnownGaps []string `json:"known_gaps"`

	Freshness freshness.FreshnessInfo `json:"freshness"`
	Live      freshness.LiveInfo      `json:"live"`
}

func newestTimestamp(ctx *gin.Context, model interface{}, column string) (*time.Time, error) {
	var result *time.Time
	err := database.DB.WithContext(ctx.Request.Context()).
		Model(model).
		Select(column).
		Order(column + " DESC").
		Limit(1).
		Scan(&result).Error
	return result, err
}

func subsystemRow(name string, lastObserved *time.Time, err error) SubsystemFreshness {
	if err != nil || lastObserved == nil || lastObserved.IsZero() {
		return SubsystemFreshness{
			Subsystem:         name,
			Available:         false,
			UnavailableReason: "no observations recorded yet",
		}
	}

	lag := time.Since(*lastObserved).Seconds()
	return SubsystemFreshness{
		Subsystem:      name,
		LastObservedAt: lastObserved,
		LagSeconds:     &lag,
		Available:      true,
	}
}

func GetMonitoringHealth(c *gin.Context) {
	ctx := c.Request.Context()

	// --- Sweep's own health, most recent sample ---
	var sweepHealth models.MonitoringHealth
	database.DB.WithContext(ctx).Order("sampled_at DESC").Limit(1).First(&sweepHealth)

	// --- Subsystem freshness rows ---
	eventIngest, eventErr := newestTimestamp(c, &models.EventEnvelope{}, "ingested_at")
	projection, projErr := newestTimestamp(c, &models.RunProjection{}, "last_event_at")
	queueObs, queueErr := newestTimestamp(c, &models.QueueHealth{}, "sampled_at")
	// PII scan activity: newest Task row, not newest PIIRecord -- the scan
	// runs synchronously on EVERY task creation regardless of whether it
	// found anything, so this stays accurate during a run of all-clean
	// payloads. PIIRecord's own CreatedAt would falsely look stale then.
	piiScan, piiErr := newestTimestamp(c, &models.Task{}, "created_at")

	alertLastRun := alerts.LastSweepAt()
	var alertLastRunPtr *time.Time
	if !alertLastRun.IsZero() {
		alertLastRunPtr = &alertLastRun
	}

	subsystems := []SubsystemFreshness{
		subsystemRow("Event Ingestion", eventIngest, eventErr),
		subsystemRow("Projection", projection, projErr),
		subsystemRow("Redis Queue Inspection", queueObs, queueErr),
		subsystemRow("Alert Evaluation", alertLastRunPtr, nil), // CHANGED
		subsystemRow("PII Scanner", piiScan, piiErr),
	}

	// --- Active policy identity ---
	policy := pii.GetLoadedPolicy()

	// --- Most recent reload attempt ---
	var lastActivation models.PolicyActivation
	database.DB.WithContext(ctx).Order("activated_at DESC").Limit(1).First(&lastActivation)

	knownGaps := []string{
		"Scanner policy-version drift does not apply -- this deployment runs a single scanner instance.",
		"MonitoringHealth.FailedChecks is a count only; it does not record WHICH of the 4 checks failed.",
		"Post-execution PII scanning (job results, error messages, logs) is blocked pending RFC-005 monitoring persistence.",
	}

	watermark, _ := freshness.CurrentWatermark(ctx)

	var newestOverall time.Time
	for _, s := range subsystems {
		if s.Available && s.LastObservedAt != nil && s.LastObservedAt.After(newestOverall) {
			newestOverall = *s.LastObservedAt
		}
	}

	c.JSON(http.StatusOK, MonitoringHealthResponse{
		SweepStatus:       sweepHealth.Status,
		SweepFailedChecks: sweepHealth.FailedChecks,
		SweepSampledAt:    sweepHealth.SampledAt,

		Subsystems: subsystems,

		ActivePolicyName:     policy.Metadata.Name,
		ActivePolicyVersion:  policy.Metadata.Version,
		ActivePolicyChecksum: policy.Metadata.Checksum,

		LastReloadResult:        lastActivation.Result,
		LastReloadAt:            lastActivation.ActivatedAt,
		LastReloadFailureReason: lastActivation.FailureReason,

		ScannerDrift: "N/A",
		KnownGaps:    knownGaps,

		Freshness: freshness.FreshnessFrom(newestOverall),
		Live:      freshness.LiveInfo{Watermark: watermark},
	})
}
