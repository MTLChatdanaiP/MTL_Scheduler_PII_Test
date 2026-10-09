package taskservice

// RFC-006 §32: pii.scan_started, pii.scan_completed and pii.policy_applied, with the run's lineage on events written
// BEFORE the task row exists.

import (
	"strings"
	"testing"

	"MTL_Scheduler_PII_Test/internal/database"
	"MTL_Scheduler_PII_Test/internal/models"
)

func firstEvent(t *testing.T, jobID, eventType string) models.EventEnvelope {
	t.Helper()
	var e models.EventEnvelope
	if err := database.DB.Where("job_id = ? AND event_type = ?", jobID, eventType).Order("id ASC").First(&e).Error; err != nil {
		t.Fatalf("no %s event for %s: %v", eventType, jobID, err)
	}
	return e
}

func redactPhone() models.PolicyRule {
	return models.PolicyRule{ID: "redact-phone", Priority: 100,
		Match:  models.MatchConditions{Sources: []string{"JOB_PAYLOAD"}, PIITypes: []string{"Phone"}},
		Action: models.PolicyAction{Type: "REDACT"}}
}

func TestPIIScanEvents_EveryTaskGetsAStartAndACompletionWithItsLineage(t *testing.T) {
	useTestPolicy(t, testPolicy(redactPhone()))

	for name, payload := range map[string]string{"with a finding": `{"p":"555-123-4567"}`, "clean": `{"ok":"fine"}`} {
		t.Run(name, func(t *testing.T) {
			got := create(t, models.Task{TaskName: "n", TaskType: "Dummy", Payload: payload, TraceID: "trace-scan-1"})

			if eventsOf(t, got.JobId, "pii.scan_started") != 1 || eventsOf(t, got.JobId, "pii.scan_completed") != 1 {
				t.Fatalf("expected exactly one scan_started and one scan_completed")
			}
			for _, et := range []string{"pii.scan_started", "pii.scan_completed"} {
				e := firstEvent(t, got.JobId, et)
				if e.ExecutionChainID != got.ExecutionChainId || e.CorrelationID != got.ExecutionChainId || e.TraceID != "trace-scan-1" {
					t.Fatalf("%s was written before the task row existed, yet must carry the run's chain and trace ids: %+v", et, e)
				}
			}

			started, completed, created := firstEvent(t, got.JobId, "pii.scan_started"), firstEvent(t, got.JobId, "pii.scan_completed"), firstEvent(t, got.JobId, "task.created")
			if !(started.ID < completed.ID && completed.ID < created.ID) {
				t.Fatal("the scan brackets what it finds, and finishes before the task is created")
			}
		})
	}
}

func TestPIIScanEvents_AFindingCarriesTheRunsLineageToo(t *testing.T) {
	useTestPolicy(t, testPolicy(redactPhone()))

	got := create(t, models.Task{TaskName: "n", TaskType: "Dummy", Payload: `{"p":"555-123-4567"}`, TraceID: "trace-scan-2"})

	e := firstEvent(t, got.JobId, "pii.detected")
	if e.ExecutionChainID != got.ExecutionChainId || e.TraceID != "trace-scan-2" {
		t.Fatalf("pii.detected was also written before the task existed and used to carry no lineage: %+v", e)
	}
}

func TestPolicyApplied_OncePerTaskWhenAnActionWasApplied(t *testing.T) {
	t.Run("redact in the payload", func(t *testing.T) {
		useTestPolicy(t, testPolicy(redactPhone()))
		got := create(t, models.Task{TaskName: "n", TaskType: "Dummy", Payload: `{"a":"555-123-4567","b":"555-987-6543"}`})
		if n := eventsOf(t, got.JobId, "pii.policy_applied"); n != 1 {
			t.Fatalf("two redactions in one task are still ONE policy_applied, got %d", n)
		}
	})
	t.Run("payload and task name both applied", func(t *testing.T) {
		useTestPolicy(t, testPolicy(append(standardRules(), redactPhone())...))
		got := create(t, models.Task{TaskName: "for z.w@example.com", TaskType: "Dummy", Payload: `{"p":"555-123-4567"}`})
		if n := eventsOf(t, got.JobId, "pii.policy_applied"); n != 1 {
			t.Fatalf("expected one policy_applied for the whole task, got %d", n)
		}
	})
	t.Run("only the task name was masked", func(t *testing.T) {
		useTestPolicy(t, testPolicy(standardRules()...))
		got := create(t, models.Task{TaskName: "for z.w@example.com", TaskType: "Dummy", Payload: `{"ok":"fine"}`})
		if n := eventsOf(t, got.JobId, "pii.policy_applied"); n != 1 {
			t.Fatalf("an action applied to the NAME counts too, got %d", n)
		}
	})
	t.Run("a block counts as an applied action", func(t *testing.T) {
		useTestPolicy(t, testPolicy(blockRule("block-phone", 100, "JOB_PAYLOAD", "Phone")))
		got := create(t, models.Task{TaskName: "n", TaskType: "Dummy", Payload: `{"p":"555-123-4567"}`})
		if n := eventsOf(t, got.JobId, "pii.policy_applied"); n != 1 {
			t.Fatalf("got %d", n)
		}
	})
}

func TestPolicyApplied_NeverWhenNothingWasChanged(t *testing.T) {
	t.Run("a clean task", func(t *testing.T) {
		useTestPolicy(t, testPolicy(redactPhone()))
		got := create(t, models.Task{TaskName: "n", TaskType: "Dummy", Payload: `{"ok":"fine"}`})
		if n := eventsOf(t, got.JobId, "pii.policy_applied"); n != 0 {
			t.Fatalf("nothing was applied, got %d", n)
		}
	})
	t.Run("OBSERVE only records, it applies nothing", func(t *testing.T) {
		useTestPolicy(t, testPolicy(models.PolicyRule{ID: "observe-phone", Priority: 100,
			Match:  models.MatchConditions{Sources: []string{"JOB_PAYLOAD"}, PIITypes: []string{"Phone"}},
			Action: models.PolicyAction{Type: "OBSERVE"}}))
		got := create(t, models.Task{TaskName: "n", TaskType: "Dummy", Payload: `{"p":"555-123-4567"}`})
		if got.Payload != `{"p":"555-123-4567"}` || eventsOf(t, got.JobId, "pii.policy_applied") != 0 {
			t.Fatalf("OBSERVE must change nothing and apply nothing: %s", got.Payload)
		}
	})
	t.Run("an ignored match", func(t *testing.T) {
		useTestPolicy(t, testPolicy(ignoreRule("ignore-qa", 200, "JOB_PAYLOAD", "qa.email")))
		got := create(t, models.Task{TaskName: "n", TaskType: "Dummy", Payload: `{"qa":{"email":"qa@example.com"}}`})
		if eventsOf(t, got.JobId, "pii.policy_applied") != 0 {
			t.Fatal("IGNORE applies nothing")
		}
	})
}

func TestPIIScanEvents_CarryNoValues(t *testing.T) {
	useTestPolicy(t, testPolicy(redactPhone()))
	got := create(t, models.Task{TaskName: "n", TaskType: "Dummy", Payload: `{"p":"555-123-4567"}`})

	var rows []models.EventEnvelope
	database.DB.Where("job_id = ? AND event_type LIKE ?", got.JobId, "pii.%").Find(&rows)
	if len(rows) < 4 {
		t.Fatalf("expected scan_started, detected, scan_completed and policy_applied, got %d events", len(rows))
	}
	for _, r := range rows {
		blob, _ := jsonOf(r)
		if strings.Contains(blob, "555-123-4567") || strings.Contains(blob, "5551234567") {
			t.Fatalf("an event must never carry a value: %s", blob)
		}
	}
}
