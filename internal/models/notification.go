package models

import (
	"time"

	"gorm.io/gorm"
)

type Notification struct {
	gorm.Model

	NotificationID string `json:"notification_id"`

	AlertID string `json:"alert_id"`

	Channel string `json:"channel"` // WEBHOOK | EMAIL | SLACK | PAGERDUTY

	Status string `json:"status"` // PENDING | SENT | FAILED

	AttemptCount int `json:"attempt_count"`

	LastAttemptAt *time.Time `json:"last_attempt_at"`
	SentAt        *time.Time `json:"sent_at"`

	LastError string `json:"last_error"`

	Target string `json:"target"`
}
