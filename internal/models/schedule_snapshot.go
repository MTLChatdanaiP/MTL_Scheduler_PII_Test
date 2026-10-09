package models

import "gorm.io/gorm"

// RFC-002 §11: what the scheduler last announced about each schedule definition.
//
// Schedules can only be created or edited by SQL (there is no schedules API), so a created, updated, enabled
// or disabled event cannot come from an API write. The scheduler compares each definition to this snapshot on
// every poll and announces the difference. DefinitionHash covers the task name, task type, payload and interval;
// the raw values are never stored here.
type ScheduleSnapshot struct {
	gorm.Model

	ScheduleID     string `gorm:"uniqueIndex"`
	DefinitionHash string
	Enabled        bool
}
