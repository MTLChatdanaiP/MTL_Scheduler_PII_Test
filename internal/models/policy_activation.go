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

	// RFC-006 §33: "Policy mutations must be audited with actor, revision,
	// checksum and activation result." Revision, checksum and result already
	// existed; this is the missing actor -- the authenticated principal that asked
	// for the activation, or "system" for startup.
	Actor string
}
