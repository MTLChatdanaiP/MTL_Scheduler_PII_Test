package models

import (
	"time"

	"gorm.io/gorm"
)

// RFC-007 §7 Alert Model.
type Alert struct {
	gorm.Model

	AlertID   string `json:"alert_id"`
	AlertType string `json:"alert_type"` // RFC-007 §4 vocabulary: RUN_STUCK, QUEUE_BACKLOG, PII_DETECTED, ...

	// RFC-007 §5: "Severity is policy, not intrinsic truth"
	Severity string `json:"severity"` // INFO | WARNING | CRITICAL

	// RFC-007 §6: explicit status, not inferred from whether a timestamp is null.
	Status string `json:"status"` // OPEN | ACKNOWLEDGED | RESOLVED

	OpenedAt       time.Time  `json:"opened_at"`
	AcknowledgedAt *time.Time `json:"acknowledged_at,omitempty"`
	ResolvedAt     *time.Time `json:"resolved_at,omitempty"`

	// RFC-007 §15 Q1 asks whether acknowledgement requires user identity.
	AcknowledgedBy string `json:"acknowledged_by,omitempty"`

	// RFC-007 §7 subject vocabulary: RUN, ATTEMPT, QUEUE, WORKER, SCHEDULE, PII_FINDING, PLATFORM.
	SubjectType string `json:"subject_type"`
	SubjectID   string `json:"subject_id"`

	// RFC-007 §7 / §8: which rule fired, and which version of it. Recorded so an
	// alert stays explainable after its rule is later edited
	RuleID      string `json:"rule_id"`
	RuleVersion int    `json:"rule_version"`

	// RFC-007 §10: JSON blob explaining why this alert exists.
	// RFC-007 §13: must never contain raw PII -- type, source and field path only.
	Evidence string `json:"evidence"`

	// RFC-007 §13: human-readable one-liner. Same PII constraint as Evidence.
	Summary string `json:"summary"`

	// Link back to the RFC-005 fact that triggered this, when there was one.
	// Empty for alerts with a non-monitoring origin (e.g. the PII alert types).
	SourceAnnotationID string `json:"source_annotation_id,omitempty"`
}

// RFC-007 §8 Rule Model.
type AlertRules struct {
	APIVersion string
	Kind       string
	Metadata   struct {
		Name     string
		Version  int
		Checksum string
	}
	Spec struct {
		Rules []AlertRule
	}
}

type AlertRule struct {
	ID             string
	Enabled        bool
	Source         string
	AnnotationType string
	Metric         string
	Operator       string
	Threshold      float64
	TextValue      string
	AlertType      string
	Severity       string
	Scope          RuleScope
	Priority       int
}

type RuleScope struct {
	// GLOBAL | QUEUE | JOB_TYPE | SCHEDULE | PII_CATEGORY
	Type   string
	Values []string
}
