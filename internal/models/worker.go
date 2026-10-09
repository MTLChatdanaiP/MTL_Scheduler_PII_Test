package models

import (
	"time"

	"gorm.io/gorm"
)

// RFC-004 §4 Worker Identity: "worker_id, instance_id, hostname, process_id, version, started_at, queues, concurrency, deployment_metadata" — subset implemented here
type Worker struct {
	gorm.Model
	// RFC-004 §4: "worker_id must be unique for concurrently active processes" — reused as the stable name across restarts (see InstanceId for the per-process-run identifier)
	WorkerId string `json:"worker_id"`
	// RFC-004 §4 Worker Identity: distinguishes this specific process run from other runs of the same logical worker
	InstanceId string `json:"instance_id"`

	ComponentType      string    `json:"component_type"`
	Hostname           string    `json:"hostname"`
	StartedAt          time.Time `json:"started_at"`
	ConfiguredCapacity int

	// RFC-005 §7 Component Health Projection: the build this process was started from.
	BuildRevision string `json:"build_revision"`

	// RFC-010 §9: when this process stopped ON PURPOSE (its run loop returned because shutdown was signalled). nil while it runs and
	// for a process that crashed -- a crash leaves no trace, which is exactly the difference between "stopped" and "gone".
	StoppedAt *time.Time `json:"stopped_at"`

	// RFC-010 §9: when this process saw the shutdown signal and stopped claiming new work. It may still be finishing a task. nil
	// while it runs normally. See ComponentInstance.Draining for how long that is believed.
	DrainingAt *time.Time `json:"draining_at"`
}
