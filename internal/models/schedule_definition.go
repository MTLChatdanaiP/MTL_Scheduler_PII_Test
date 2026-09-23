package models

import (
	"time"

	"gorm.io/gorm"
)

type ScheduleDefinition struct {
	gorm.Model

	ScheduleId      string // ULID
	TaskName        string
	TaskType        string
	Payload         string
	IntervalSeconds int       // only meaningful if RECURRING — e.g. 60 = every minute
	NextRunAt       time.Time // when this should next fire
	Enabled         bool
}
