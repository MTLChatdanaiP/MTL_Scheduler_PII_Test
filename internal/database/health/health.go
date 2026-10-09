// Package health builds the component health report behind GET /components (RFC-005 §7 Component Health Projection).
//
// Before this there was no server-side component projection at all: the Components page assembled one in the browser
// from five other endpoints. This returns, for each component instance of THIS project, its id, type, build revision,
// start time, last heartbeat, derived health with its evidence, and a summary of what the platform depends on.
package health

import (
	"context"
	"time"

	"MTL_Scheduler_PII_Test/internal/buildinfo"
	"MTL_Scheduler_PII_Test/internal/cache"
	"MTL_Scheduler_PII_Test/internal/database"
	"MTL_Scheduler_PII_Test/internal/models"
	"MTL_Scheduler_PII_Test/internal/pii"
	"MTL_Scheduler_PII_Test/internal/worker"
)

// ComponentHealth is one component instance's health.
type ComponentHealth struct {
	ComponentType string     `json:"component_type"`
	InstanceID    string     `json:"component_instance_id"`
	DisplayName   string     `json:"display_name"`
	BuildRevision string     `json:"build_revision"`
	StartedAt     time.Time  `json:"started_at"`
	LastHeartbeat *time.Time `json:"last_heartbeat"` // nil: never reported one
	Health        string     `json:"health"`         // HEALTHY | DEGRADED | OFFLINE | UNKNOWN
	Reason        string     `json:"reason"`
	// Evidence is what the verdict was computed from, so it can be checked rather than trusted.
	Evidence map[string]interface{} `json:"evidence"`
}

// DependencyStatus is the state of something the platform depends on, AS OBSERVED BY THE PROCESS SERVING THE REPORT.
// Detail never contains an error message: those carry hostnames, users and database names.
type DependencyStatus struct {
	Name   string `json:"name"`
	Status string `json:"status"` // OK | UNAVAILABLE
	Detail string `json:"detail"`
}

type ObservedBy struct {
	BuildRevision string `json:"build_revision"`
}

// Report is the whole component health projection.
type Report struct {
	Components   []ComponentHealth  `json:"components"`
	Dependencies []DependencyStatus `json:"dependencies"`
	ObservedBy   ObservedBy         `json:"observed_by"`
	GeneratedAt  time.Time          `json:"generated_at"`
}

// ComponentHealthOf takes one instance, the time and the two thresholds, and returns its health, purely.
func ComponentHealthOf(inst models.ComponentInstance, now time.Time, degradedAfter, offlineAfter time.Duration) ComponentHealth {
	verdict, reason := models.InstanceHealth(inst, now, degradedAfter, offlineAfter)

	evidence := map[string]interface{}{
		"degraded_after_seconds": int(degradedAfter.Seconds()),
		"offline_after_seconds":  int(offlineAfter.Seconds()),
	}

	out := ComponentHealth{
		ComponentType: inst.ComponentType, InstanceID: inst.InstanceID, DisplayName: inst.DisplayName,
		BuildRevision: inst.BuildRevision, StartedAt: inst.StartedAt, Health: verdict, Reason: reason, Evidence: evidence,
	}
	if !inst.LastSeenAt.IsZero() && inst.LastSeenAt.Year() > 1 {
		seen := inst.LastSeenAt
		out.LastHeartbeat = &seen
		evidence["last_seen_age_seconds"] = int(now.Sub(inst.LastSeenAt).Seconds())
	}
	return out
}

// CheckDependencies takes a context and returns the state of the database, Redis and the active PII policy, by pinging the
// first two and checking the third is loaded.
func CheckDependencies(ctx context.Context) []DependencyStatus {
	deps := make([]DependencyStatus, 0, 3)

	pg := DependencyStatus{Name: "postgres", Status: "OK", Detail: "answered a ping"}
	if sqlDB, err := database.DB.DB(); err != nil || sqlDB.PingContext(ctx) != nil {
		pg.Status, pg.Detail = "UNAVAILABLE", "ping failed"
	}
	deps = append(deps, pg)

	rd := DependencyStatus{Name: "redis", Status: "OK", Detail: "answered a ping"}
	if cache.Client == nil || cache.Client.Ping(ctx).Err() != nil {
		rd.Status, rd.Detail = "UNAVAILABLE", "ping failed"
	}
	deps = append(deps, rd)

	policy := DependencyStatus{Name: "pii_policy", Status: "UNAVAILABLE", Detail: "no policy is active"}
	if loaded := pii.LoadedPolicy.Load(); loaded != nil && loaded.Metadata.Checksum != "" {
		policy.Status, policy.Detail = "OK", loaded.Metadata.Name+" is active"
	}
	deps = append(deps, policy)

	return deps
}

// Build takes a context and returns the report: every component instance with its derived health, the dependency
// summary, and the build revision of the process that produced it.
func Build(ctx context.Context) (Report, error) {
	instances, err := worker.GetComponentInstances(ctx)
	if err != nil {
		return Report{}, err
	}

	now := time.Now().UTC()
	components := make([]ComponentHealth, 0, len(instances))
	for _, inst := range instances {
		components = append(components, ComponentHealthOf(inst, now, models.HeartbeatDegradedAfter, models.HeartbeatOfflineAfter))
	}

	return Report{
		Components:   components,
		Dependencies: CheckDependencies(ctx),
		ObservedBy:   ObservedBy{BuildRevision: buildinfo.Revision()},
		GeneratedAt:  now,
	}, nil
}
