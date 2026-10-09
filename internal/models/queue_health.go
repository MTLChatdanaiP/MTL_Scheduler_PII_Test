package models

import (
	"time"

	"gorm.io/gorm"
)

type QueueHealth struct {
	gorm.Model

	QueueName               string
	StreamLength            int64
	PendingCount            int64
	OldestPendingAgeSeconds int64
	ConsumerCount           int
	SampledAt               time.Time

	// RFC-005 §7 Queue Projection "throughput estimates": runs that finished (completed or failed) per minute over the last
	// five minutes, counted from the event log when the sample is taken.
	ThroughputPerMinute float64
}
