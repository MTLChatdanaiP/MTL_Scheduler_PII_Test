package integration_test

// RFC-007 §12 Notification Adapters — lifecycle against the real database.
//
// These use a stub adapter rather than the webhook one: the point is the
// notification row's lifecycle, not HTTP. The webhook adapter itself is
// covered by the unit tests in internals/alerting.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"

	alerts "MTL_Scheduler_PII_Test/internals/alerting"
	"MTL_Scheduler_PII_Test/internals/database"
	"MTL_Scheduler_PII_Test/internals/models"
)

// stubAdapter lets a test decide whether delivery succeeds, without a network.
type stubAdapter struct {
	channel   string
	failWith  error
	sendCount int
}

func (s *stubAdapter) Channel() string { return s.channel }
func (s *stubAdapter) Target() string  { return "stub://" + s.channel }

func (s *stubAdapter) Send(ctx context.Context, alert models.Alert) error {
	s.sendCount++
	return s.failWith
}

// useStubAdapter swaps the registry for one stub and restores it afterwards.
//
// NotificationAdapters is package-level state, so leaving a stub registered
// would silently change every later test in the package -- exactly the kind of
// cross-test leak the transformations bug was.
func useStubAdapter(t *testing.T, failWith error) *stubAdapter {
	t.Helper()

	original := alerts.NotificationAdapters
	stub := &stubAdapter{channel: "STUB", failWith: failWith}

	alerts.NotificationAdapters = map[string]alerts.NotificationAdapter{
		"STUB": stub,
	}

	t.Cleanup(func() {
		alerts.NotificationAdapters = original
	})

	return stub
}

// seedAlert inserts an alert directly, bypassing the open passes, so a test can
// control severity precisely.
func seedAlert(t *testing.T, severity string) models.Alert {
	t.Helper()

	alert := models.Alert{
		AlertID:     ulid.Make().String(),
		AlertType:   "WORKER_OFFLINE",
		Severity:    severity,
		Status:      "OPEN",
		OpenedAt:    time.Now().UTC(),
		SubjectType: "WORKER",
		SubjectID:   "test-worker-" + ulid.Make().String(),
		Summary:     "test alert",
		RuleID:      "test-rule",
		RuleVersion: 1,
		Evidence:    `{"age_seconds":91}`,
	}

	if err := database.DB.Create(&alert).Error; err != nil {
		t.Fatalf("failed to seed alert: %v", err)
	}

	t.Cleanup(func() {
		database.DB.Where("alert_id = ?", alert.AlertID).Delete(&models.Notification{})
		database.DB.Where("alert_id = ?", alert.AlertID).Delete(&models.Alert{})
	})

	return alert
}

func notificationsFor(t *testing.T, alertID string) []models.Notification {
	t.Helper()

	var found []models.Notification
	database.DB.Where("alert_id = ?", alertID).Find(&found)
	return found
}

func seedNotification(t *testing.T, alert models.Alert, channel string) models.Notification {
	t.Helper()

	n := models.Notification{
		NotificationID: ulid.Make().String(),
		AlertID:        alert.AlertID,
		Channel:        channel,
		Status:         "PENDING",
		Target:         "stub://" + channel,
	}

	if err := database.DB.Create(&n).Error; err != nil {
		t.Fatalf("failed to seed notification: %v", err)
	}
	return n
}

func TestNotification_SendMarksSent(t *testing.T) {
	ctx := context.Background()
	stub := useStubAdapter(t, nil)

	alert := seedAlert(t, "CRITICAL")
	seedNotification(t, alert, "STUB")

	alerts.RunNotificationPass(ctx)

	if stub.sendCount != 1 {
		t.Errorf("adapter called %d times, want 1", stub.sendCount)
	}

	rows := notificationsFor(t, alert.AlertID)
	if len(rows) != 1 {
		t.Fatalf("expected 1 notification, got %d", len(rows))
	}

	if rows[0].Status != "SENT" {
		t.Errorf("status = %q, want SENT", rows[0].Status)
	}
	if rows[0].SentAt == nil {
		t.Error("sent_at is nil")
	}
	if rows[0].AttemptCount != 1 {
		t.Errorf("attempt_count = %d, want 1", rows[0].AttemptCount)
	}
}

// THE TEST THAT PROVES RFC-007 §12's ACTUAL REQUIREMENT.
//
// "Alert state must survive notification delivery failure", with the example
// alert.status = OPEN alongside notification.status = FAILED. If any code path
// in the notification flow ever writes to an alert, this fails.
func TestNotification_FailedSendLeavesAlertUntouched(t *testing.T) {
	ctx := context.Background()
	useStubAdapter(t, errors.New("delivery refused"))

	alert := seedAlert(t, "CRITICAL")
	seedNotification(t, alert, "STUB")

	alerts.RunNotificationPass(ctx)

	rows := notificationsFor(t, alert.AlertID)
	if len(rows) != 1 {
		t.Fatalf("expected 1 notification, got %d", len(rows))
	}
	if rows[0].Status != "FAILED" {
		t.Errorf("notification status = %q, want FAILED", rows[0].Status)
	}
	if rows[0].LastError == "" {
		t.Error("last_error is empty -- a FAILED row that does not say why is useless")
	}

	var after models.Alert
	database.DB.Where("alert_id = ?", alert.AlertID).First(&after)

	if after.Status != "OPEN" {
		t.Errorf("alert status = %q, want OPEN -- a delivery failure must not touch the alert", after.Status)
	}
	if after.ResolvedAt != nil {
		t.Error("alert was resolved by a notification failure")
	}
	if after.AcknowledgedAt != nil {
		t.Error("alert was acknowledged by a notification failure")
	}
}

func TestNotification_MissingAlertDoesNotStayPending(t *testing.T) {
	ctx := context.Background()
	useStubAdapter(t, nil)

	alert := seedAlert(t, "CRITICAL")
	n := seedNotification(t, alert, "STUB")

	// The alert vanishes before the send pass runs.
	database.DB.Where("alert_id = ?", alert.AlertID).Delete(&models.Alert{})

	alerts.RunNotificationPass(ctx)

	var after models.Notification
	database.DB.Where("notification_id = ?", n.NotificationID).First(&after)

	// Left PENDING, this row would be re-queried every tick forever and never
	// reach a terminal state.
	if after.Status != "FAILED" {
		t.Errorf("status = %q, want FAILED -- a row whose alert is gone must not stay PENDING", after.Status)
	}

	database.DB.Where("notification_id = ?", n.NotificationID).Delete(&models.Notification{})
}

func TestNotification_UnknownChannelDoesNotStayPending(t *testing.T) {
	ctx := context.Background()
	useStubAdapter(t, nil) // registers STUB only

	alert := seedAlert(t, "CRITICAL")
	n := seedNotification(t, alert, "SLACK") // no adapter for this channel

	alerts.RunNotificationPass(ctx)

	var after models.Notification
	database.DB.Where("notification_id = ?", n.NotificationID).First(&after)

	if after.Status != "FAILED" {
		t.Errorf("status = %q, want FAILED -- a channel with no adapter must not stay PENDING", after.Status)
	}
}

func TestNotification_StopsRetryingAtMaxAttempts(t *testing.T) {
	ctx := context.Background()
	stub := useStubAdapter(t, errors.New("still down"))

	alert := seedAlert(t, "CRITICAL")
	n := seedNotification(t, alert, "STUB")

	// One attempt per pass is all the retry delay allows, so drive the row to
	// its limit directly and confirm the query stops selecting it.
	database.DB.Model(&models.Notification{}).
		Where("notification_id = ?", n.NotificationID).
		Updates(map[string]interface{}{
			"status":          "FAILED",
			"attempt_count":   3,
			"last_attempt_at": time.Now().UTC().Add(-1 * time.Hour), // old enough to retry
		})

	stub.sendCount = 0
	alerts.RunNotificationPass(ctx)

	if stub.sendCount != 0 {
		t.Errorf("adapter was called %d times for a row at max attempts, want 0", stub.sendCount)
	}
}

func TestNotification_RetriesFailedRowAfterDelay(t *testing.T) {
	ctx := context.Background()
	stub := useStubAdapter(t, nil) // succeeds this time

	alert := seedAlert(t, "CRITICAL")
	n := seedNotification(t, alert, "STUB")

	// A previous attempt failed, long enough ago to be eligible again.
	database.DB.Model(&models.Notification{}).
		Where("notification_id = ?", n.NotificationID).
		Updates(map[string]interface{}{
			"status":          "FAILED",
			"attempt_count":   1,
			"last_attempt_at": time.Now().UTC().Add(-1 * time.Hour),
		})

	stub.sendCount = 0
	alerts.RunNotificationPass(ctx)

	if stub.sendCount != 1 {
		t.Fatalf("adapter called %d times, want 1 -- an eligible FAILED row was not retried", stub.sendCount)
	}

	var after models.Notification
	database.DB.Where("notification_id = ?", n.NotificationID).First(&after)

	if after.Status != "SENT" {
		t.Errorf("status = %q, want SENT after a successful retry", after.Status)
	}
	if after.AttemptCount != 2 {
		t.Errorf("attempt_count = %d, want 2", after.AttemptCount)
	}
}

func TestNotification_DoesNotRetryBeforeDelayElapses(t *testing.T) {
	ctx := context.Background()
	stub := useStubAdapter(t, errors.New("down"))

	alert := seedAlert(t, "CRITICAL")
	n := seedNotification(t, alert, "STUB")

	// Failed seconds ago. Retrying every tick would mean 180 requests an hour
	// against an endpoint that is already down.
	database.DB.Model(&models.Notification{}).
		Where("notification_id = ?", n.NotificationID).
		Updates(map[string]interface{}{
			"status":          "FAILED",
			"attempt_count":   1,
			"last_attempt_at": time.Now().UTC(),
		})

	stub.sendCount = 0
	alerts.RunNotificationPass(ctx)

	if stub.sendCount != 0 {
		t.Errorf("adapter called %d times, want 0 -- the retry delay was ignored", stub.sendCount)
	}
}

func TestNotification_SentRowIsNeverResent(t *testing.T) {
	ctx := context.Background()
	stub := useStubAdapter(t, nil)

	alert := seedAlert(t, "CRITICAL")
	seedNotification(t, alert, "STUB")

	alerts.RunNotificationPass(ctx)
	alerts.RunNotificationPass(ctx)
	alerts.RunNotificationPass(ctx)

	// SENT is terminal. Without that, every successful notification would be
	// re-delivered on every tick for the life of the system.
	if stub.sendCount != 1 {
		t.Errorf("adapter called %d times across 3 passes, want 1 -- SENT is not terminal", stub.sendCount)
	}
}
