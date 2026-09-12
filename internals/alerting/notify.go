package alerts

import (
	"MTL_Scheduler_PII_Test/internals/database"
	"MTL_Scheduler_PII_Test/internals/events"
	"MTL_Scheduler_PII_Test/internals/models"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/oklog/ulid/v2"
)

const (
	webhookTimeout     = 5 * time.Second
	maxLastErrorLength = 256
)

type webhookBody struct {
	AlertID     string                 `json:"alert_id"`
	AlertType   string                 `json:"alert_type"`
	Severity    string                 `json:"severity"`
	Status      string                 `json:"status"`
	Summary     string                 `json:"summary"`
	SubjectType string                 `json:"subject_type"`
	SubjectID   string                 `json:"subject_id"`
	Evidence    map[string]interface{} `json:"evidence,omitempty"`
}

type NotificationAdapter interface {
	Channel() string
	Target() string
	Send(ctx context.Context, alert models.Alert) error
}

type WebhookAdapter struct {
	URL    string
	Client *http.Client
}

var NotificationAdapters = map[string]NotificationAdapter{}

func NewWebhookAdapter(url string) *WebhookAdapter {
	return &WebhookAdapter{
		URL: url,
		Client: &http.Client{
			Timeout: webhookTimeout,
		},
	}
}

func (w *WebhookAdapter) Channel() string {
	return "WEBHOOK"
}

func (w *WebhookAdapter) Target() string {
	return w.URL
}

func (w *WebhookAdapter) Send(
	ctx context.Context,
	alert models.Alert,
) error {

	body, err := buildWebhookBody(alert)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		w.URL,
		bytes.NewReader(body),
	)
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := w.Client.Do(req)
	if err != nil {
		return err
	}

	defer resp.Body.Close()

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		errorBody := string(responseBody)

		if len(errorBody) > maxLastErrorLength {
			errorBody = errorBody[:maxLastErrorLength]
		}

		return fmt.Errorf(
			"webhook returned status %d: %s",
			resp.StatusCode,
			errorBody,
		)
	}

	return nil
}

func buildWebhookBody(
	alert models.Alert,
) ([]byte, error) {

	payload := webhookBody{
		AlertID:     alert.AlertID,
		AlertType:   alert.AlertType,
		Severity:    alert.Severity,
		Status:      alert.Status,
		Summary:     alert.Summary,
		SubjectType: alert.SubjectType,
		SubjectID:   alert.SubjectID,
	}

	if alert.Evidence != "" {
		_ = json.Unmarshal(
			[]byte(alert.Evidence),
			&payload.Evidence,
		)
	}

	return json.Marshal(payload)
}

func shouldNotify(severity string) bool {
	switch severity {
	case "WARNING", "CRITICAL":
		return true
	}
	return false
}

func createNotifications(ctx context.Context, alert models.Alert) {
	if !shouldNotify(alert.Severity) {
		return
	}

	for _, adapter := range NotificationAdapters {

		notification := models.Notification{
			NotificationID: ulid.Make().String(),
			AlertID:        alert.AlertID,
			Channel:        adapter.Channel(),
			Status:         "PENDING",
			Target:         adapter.Target(),
		}

		if err := database.DB.WithContext(ctx).Create(&notification).Error; err != nil {
			fmt.Println("alerting: failed to create notification:", err)
			continue
		}

		events.LogEvent(ctx, alert.AlertID, "alert.notification_requested", "alerting")
	}
}

func failNotification(ctx context.Context, n models.Notification, reason string) {
	n.Status = "FAILED"
	if len(reason) > maxLastErrorLength {
		reason = reason[:maxLastErrorLength]
	}
	n.LastError = reason
	database.DB.WithContext(ctx).Save(&n)
	events.LogEvent(ctx, n.AlertID, "alert.notification_failed", "alerting")
}

func sendPendingNotifications(ctx context.Context) {
	var pending []models.Notification

	retryBefore := time.Now().UTC().Add(-notificationRetryDelay)

	if err := database.DB.WithContext(ctx).
		Where("status = ? OR (status = ? AND attempt_count < ? AND (last_attempt_at IS NULL OR last_attempt_at < ?))",
			"PENDING", "FAILED", maxNotificationAttempts, retryBefore).
		Limit(notificationBatchLimit).
		Find(&pending).Error; err != nil {
		fmt.Println("alerting: failed to query pending notifications:", err)
		return
	}

	for _, n := range pending {
		var alert models.Alert
		if err := database.DB.WithContext(ctx).Where("alert_id = ?", n.AlertID).First(&alert).Error; err != nil {
			failNotification(ctx, n, "alert no longer exists: "+n.AlertID)
			continue
		}

		adapter, ok := NotificationAdapters[n.Channel]
		if !ok {
			failNotification(ctx, n, "no adapter for channel "+n.Channel)
			continue
		}

		n.AttemptCount++
		now := time.Now().UTC()
		n.LastAttemptAt = &now
		database.DB.WithContext(ctx).Save(&n)
		err := adapter.Send(ctx, alert)
		if err != nil {
			failNotification(ctx, n, err.Error())
			continue
		}
		n.Status = "SENT"
		now = time.Now().UTC()
		n.SentAt = &now
		database.DB.WithContext(ctx).Save(&n)
		events.LogEvent(ctx, n.AlertID, "alert.notification_sent", "alerting")
	}
}
