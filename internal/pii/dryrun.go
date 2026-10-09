package pii

import "MTL_Scheduler_PII_Test/internal/models"

type DryRunResponse struct {
	Results         []DryRunResult
	FailedDetectors []string // detector IDs that failed to compile/run during this dry run

	// RFC-006 §13/§32: problems ValidatePolicy finds in the candidate policy, so a
	// bad mask setting shows up in the dry run instead of at activation or at runtime.
	PolicyProblems []string
}

type DryRunResult struct {
	DetectorID    string
	PIIType       string
	MatchedText   string
	Action        string
	MaskedPreview string
}

func DryRun(payload string, policy models.PIIPolicy, source string, jobType string, queue string) DryRunResponse {

	var dryrun_results []DryRunResult

	findings, failed_dets := Detect(payload, policy.Spec.Detectors)
	evaluated_findings := EvaluatePolicy(findings, policy, source, jobType, queue)

	for _, evaluated := range evaluated_findings {
		finding := evaluated.Finding
		result := DryRunResult{DetectorID: finding.DetectorID, PIIType: string(finding.Type), MatchedText: finding.Match, Action: evaluated.Rule.Action.Type}
		if result.Action == "MASK" {
			result.MaskedPreview = Mask(finding.Match, evaluated.Rule.Action.Mask)
		}
		dryrun_results = append(dryrun_results, result)
	}

	response := DryRunResponse{Results: dryrun_results, FailedDetectors: failed_dets, PolicyProblems: ValidatePolicy(policy)}
	return response
}
