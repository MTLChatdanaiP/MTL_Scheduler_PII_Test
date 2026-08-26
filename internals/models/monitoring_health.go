package models

import (
	"time"

	"gorm.io/gorm"
)

type MonitoringHealth struct {
	gorm.Model

	Status       string // "COMPLETE", "DEGRADED", "PARTIAL", "UNKNOWN"
	FailedChecks int    // how many of the sweep's checks errored this cycle
	SampledAt    time.Time
}
