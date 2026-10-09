package handlers

import "MTL_Scheduler_PII_Test/internal/models"

// RunProjectionExtras is the part of the run projection that RunListItem did not expose. The list and detail handlers copy
// projection fields one at a time, so a field added to models.RunProjection never reached the dashboard until it was also
// copied here. Embedded in RunListItem, its JSON keys stay flat.
type RunProjectionExtras struct {
	LatestAttemptID       string `json:"latest_attempt_id"`
	LatestAttemptStatus   string `json:"latest_attempt_status"`
	LatestWorkerID        string `json:"latest_worker_id"`
	ProjectedAttemptCount int    `json:"attempt_count"` // the projection's own count; RunListItem.AttemptCount is computed from the attempts it loads
	ActiveAnnotationCount int    `json:"active_annotation_count"`
	OpenAlertCount        int    `json:"open_alert_count"`
	Contradicted          bool   `json:"contradicted"`
	ContradictionNote     string `json:"contradiction_note"`
}

// projectionExtras takes a run's projection and returns the extra fields the API exposes.
func projectionExtras(p models.RunProjection) RunProjectionExtras {
	return RunProjectionExtras{
		LatestAttemptID:       p.LatestAttemptID,
		LatestAttemptStatus:   p.LatestAttemptStatus,
		LatestWorkerID:        p.LatestWorkerID,
		ProjectedAttemptCount: p.AttemptCount,
		ActiveAnnotationCount: p.ActiveAnnotationCount,
		OpenAlertCount:        p.OpenAlertCount,
		Contradicted:          p.Contradicted,
		ContradictionNote:     p.ContradictionNote,
	}
}
