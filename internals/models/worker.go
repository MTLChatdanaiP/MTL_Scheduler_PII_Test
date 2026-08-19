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

	Hostname  string    `json:"hostname"`
	StartedAt time.Time `json:"started_at"`
}
