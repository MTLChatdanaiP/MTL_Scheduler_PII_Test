package pii

import "MTL_Scheduler_PII_Test/internals/models"

type DryRunResponse struct {
	Results         []DryRunResult
	FailedDetectors []string // detector IDs that failed to compile/run during this dry run
}

type DryRunResult struct {
	DetectorID    string
	PIIType       string
	MatchedText   string
	Action        string
	MaskedPreview string
}

func DryRun(payload string, policy models.PIIPolicy, source string, jobType string) DryRunResponse {

	var dryrun_results []DryRunResult

	findings, failed_dets := Detect(payload, policy.Spec.Detectors)
	evaluated_findings := EvaluatePolicy(findings, policy, source, jobType)

	for _, evaluated := range evaluated_findings {
		finding := evaluated.Finding
		result := DryRunResult{DetectorID: finding.DetectorID, PIIType: string(finding.Type), MatchedText: finding.Match, Action: evaluated.Rule.Action.Type}
		if result.Action == "MASK" {
			result.MaskedPreview = Mask(finding.Match, evaluated.Rule.Action.Mask)
		}
		dryrun_results = append(dryrun_results, result)
	}

	response := DryRunResponse{Results: dryrun_results, FailedDetectors: failed_dets}
	return response
}
