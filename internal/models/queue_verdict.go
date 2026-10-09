package models

import "fmt"

// QueueDegradedPendingThreshold is the pending-message count above which a queue is DEGRADED. It lives here, once: the monitoring sweep
// (queue.degraded events), the overview count, GET /queues and the dashboard (which shows what the server says) all read it, so the
// browser can never disagree with the server about what "degraded" means (RFC-009 §2, §25).
const QueueDegradedPendingThreshold = 20

// QueueVerdict is the server's judgement of one queue sample: HEALTHY or DEGRADED, with the numbers behind it (RFC-010 §9 evidence).
func QueueVerdict(q QueueHealth) (health, reason string) {
	if q.PendingCount > QueueDegradedPendingThreshold {
		return "DEGRADED", fmt.Sprintf("%d messages pending (degraded above %d)", q.PendingCount, QueueDegradedPendingThreshold)
	}
	return "HEALTHY", fmt.Sprintf("%d messages pending (degraded above %d)", q.PendingCount, QueueDegradedPendingThreshold)
}
