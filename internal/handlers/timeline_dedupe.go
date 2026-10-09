package handlers

import "MTL_Scheduler_PII_Test/internal/models"

// RFC-005 §5 / RFC-010 §7: attempt.claimed, attempt.started and the attempt outcome are now REAL events in
// event_envelopes. The timeline also synthesizes the same three entries from the attempts table, so without
// this every attempt of a new run would appear twice.

// attemptKind takes an event type and returns which lifecycle step of an attempt it is: "claimed",
// "started" or "finished", or "" if it is not one of the three (a heartbeat covers nothing).
func attemptKind(eventType string) string {
	switch eventType {
	case "attempt.claimed":
		return "claimed"
	case "attempt.started":
		return "started"
	case "attempt.finished", "attempt.succeeded", "attempt.failed", "attempt.abandoned":
		return "finished"
	}
	return ""
}

func attemptIDOf(e TimelineEntry) string {
	id, _ := e.Detail["attempt_id"].(string)
	return id
}

// dropCoveredAttemptEntries takes the entries built from stored events and the entries synthesized from the
// attempts table, and returns the synthesized ones minus any that a real event already covers (same attempt,
// same lifecycle step). A run from before the real events existed has nothing covering it, so its synthesized
// entries are all kept.
func dropCoveredAttemptEntries(eventEntries []TimelineEntry, attemptEntries []TimelineEntry) []TimelineEntry {
	covered := make(map[string]bool)
	for _, e := range eventEntries {
		kind, id := attemptKind(e.EventType), attemptIDOf(e)
		if kind != "" && id != "" {
			covered[id+"|"+kind] = true
		}
	}

	kept := make([]TimelineEntry, 0, len(attemptEntries))
	for _, a := range attemptEntries {
		kind, id := attemptKind(a.EventType), attemptIDOf(a)
		if kind != "" && id != "" && covered[id+"|"+kind] {
			continue
		}
		kept = append(kept, a)
	}
	return kept
}

// enrichAttemptEvents takes the entries built from stored events and the attempts of the same runs, and adds
// "attempt_number" to every entry that names an attempt, in place. A real attempt event carries the attempt's id and
// worker but not its number (the synthesized entries always had it), and "attempt 2" is what an operator reads.
func enrichAttemptEvents(eventEntries []TimelineEntry, attempts []models.Attempt) {
	numbers := make(map[string]int, len(attempts))
	for _, a := range attempts {
		numbers[a.AttemptId] = a.AttemptNumber
	}
	for i := range eventEntries {
		id := attemptIDOf(eventEntries[i])
		if id == "" {
			continue
		}
		if n, ok := numbers[id]; ok {
			eventEntries[i].Detail["attempt_number"] = n
		}
	}
}
