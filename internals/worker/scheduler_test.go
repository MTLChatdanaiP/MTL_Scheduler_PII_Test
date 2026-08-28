package worker

import (
	"context"
	"testing"
	"time"

	"MTL_Scheduler_PII_Test/internals/database"
	"MTL_Scheduler_PII_Test/internals/models"

	"github.com/oklog/ulid/v2"
)

func TestScheduler_FiresRecurringSchedule(t *testing.T) {
	ctx := context.Background()

	tname := "test_recurring_fire"
	tt := "dummy"
	testStartTime := time.Now()

	sched := models.ScheduleDefinition{
		ScheduleId:      ulid.Make().String(),
		TaskName:        tname,
		TaskType:        tt,
		Payload:         "scheduled task test payload",
		Enabled:         true,
		IntervalSeconds: 60,
		NextRunAt:       time.Now().Add(-1 * time.Minute), // already due
	}
	database.DB.Create(&sched)

	t.Cleanup(func() {
		database.DB.Where("schedule_id = ?", sched.ScheduleId).Delete(&models.ScheduleDefinition{})
	})

	fireRecurringSchedules(ctx)

	var tasks []models.Task
	database.DB.Where("task_name = ? AND task_type = ? AND created_at > ?", tname, tt, testStartTime).Find(&tasks)

	if len(tasks) != 1 {
		t.Errorf("expected 1 task created from recurring schedule, got %d", len(tasks))
	}
}
func TestToggleSchedule_DisabledScheduleNotPickedUp(t *testing.T) {
	// TODO: insert an Enabled: false ScheduleDefinition with a past NextRunAt
	// run the same extracted schedule-check function
	// assert NO new Task was created
	ctx := context.Background()

	tname := "test_disabled_schedule"
	tt := "dummy"

	sched := models.ScheduleDefinition{
		ScheduleId:      ulid.Make().String(),
		TaskName:        tname,
		TaskType:        tt,
		Payload:         "should never fire",
		Enabled:         false, // the whole point of this test
		IntervalSeconds: 60,
		NextRunAt:       time.Now().Add(-1 * time.Minute), // also overdue, but disabled
	}
	database.DB.Create(&sched)

	t.Cleanup(func() {
		database.DB.Where("schedule_id = ?", sched.ScheduleId).Delete(&models.ScheduleDefinition{})
	})

	fireRecurringSchedules(ctx)

	var tasks []models.Task
	database.DB.Where("task_name = ? AND task_type = ?", tname, tt).Find(&tasks)

	if len(tasks) != 0 {
		t.Errorf("expected 0 tasks created from disabled schedule, got %d", len(tasks))
	}
}
