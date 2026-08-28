package models

import (
	"time"

	"gorm.io/gorm"
)

type MonitoringAnnotation struct {
	gorm.Model

	AnnotationID string
	Type         string
	SubjectType  string
	SubjectID    string
	DerivedAt    time.Time
	Evidence     string
	ResolvedAt   *time.Time
}
