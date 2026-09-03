package models

import (
	"time"

	"gorm.io/gorm"
)

type PolicyActivation struct {
	gorm.Model

	PolicyName    string
	PolicyVersion int
	Checksum      string
	ActivatedAt   time.Time
	Result        string // "SUCCESS" or "FAILED"
	FailureReason string // empty on success
	Trigger       string
}
