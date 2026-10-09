package worker

// RFC-006 §11 IGNORE in what a run PRODUCED: the output is stored as it is and nothing is recorded.

import (
	"context"
	"strings"
	"testing"

	"MTL_Scheduler_PII_Test/internal/database"
	"MTL_Scheduler_PII_Test/internal/models"
)

func TestIgnoreInOutput_IsStoredUnchangedAndRecordsNothing(t *testing.T) {
	p := artifactPolicy()
	p.Spec.Rules = []models.PolicyRule{
		{ID: "ignore-support-line", Priority: 200, Match: models.MatchConditions{Sources: []string{"ERROR_MESSAGE"}, PIITypes: []string{"Phone"}}, Action: models.PolicyAction{Type: "IGNORE"}},
		{ID: "redact-phone", Priority: 100, Match: models.MatchConditions{Sources: []string{"ERROR_MESSAGE"}, PIITypes: []string{"Phone"}}, Action: models.PolicyAction{Type: "REDACT"}},
	}
	useTestPolicy(t, p)

	task := newRunnableTask(t, "fail_invalid_input", "n", `support line 555-123-4567`)
	ProcessTask(context.Background(), task.JobId, "ignore-output-worker")

	a := bySource(artifactsFor(t, task.JobId), "ERROR_MESSAGE")
	if a == nil {
		t.Fatal("no ERROR_MESSAGE artifact")
	}
	if !strings.Contains(a.SanitizedBody, "555-123-4567") || a.ScanStatus != "CLEAN" || a.FindingCount != 0 {
		t.Fatalf("an ignored match is stored as it is and counts as no finding: status=%s findings=%d body=%q", a.ScanStatus, a.FindingCount, a.SanitizedBody)
	}
	var n int64
	database.DB.Model(&models.PIIRecord{}).Where("job_id = ? AND source = ?", task.JobId, "ERROR_MESSAGE").Count(&n)
	if n != 0 {
		t.Fatalf("an ignored match must record nothing, found %d findings", n)
	}
}
