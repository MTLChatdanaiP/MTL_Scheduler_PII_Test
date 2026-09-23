package models

import (
	"time"

	"gorm.io/gorm"
)

type WorkerHeartbeat struct {
	gorm.Model

	WorkerId        string    `json:"worker_id"`
	InstanceId      string    `json:"instance_id"`
	OccurredAt      time.Time `json:"occurred_at"`
	RunningAttempts int       `json:"running_attempts"`
	Capacity        int       `json:"capacity"`
	// Version         string    `json:"version"`
}
