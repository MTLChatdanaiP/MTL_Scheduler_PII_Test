package models

import (
	"time"

	"gorm.io/gorm"
)

type QueueHealth struct {
	gorm.Model

	StreamLength            int64
	PendingCount            int64
	OldestPendingAgeSeconds int64
	ConsumerCount           int
	SampledAt               time.Time
}
