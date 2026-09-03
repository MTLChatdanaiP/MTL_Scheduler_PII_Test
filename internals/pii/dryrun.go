package pii

import "MTL_Scheduler_PII_Test/internals/models"

type DryRunResult struct {
	DetectorID    string
	PIIType       string
	MatchedText   string
	Action        string
	MaskedPreview string
}

func DryRun(payload string, policy models.PIIPolicy) []DryRunResult {

	var dryrun_results []DryRunResult

	findings := Detect(payload, policy.Spec.Detectors)
	evaluated_findings := EvaluatePolicy(findings, policy)

	for _, evaluated := range evaluated_findings {
		finding := evaluated.Finding
		result := DryRunResult{DetectorID: finding.DetectorID, PIIType: string(finding.Type), MatchedText: finding.Match, Action: evaluated.Rule.Action}
		if result.Action == "MASK" {
			result.MaskedPreview = Mask(finding.Match, evaluated.Rule.Mask)
		}
		dryrun_results = append(dryrun_results, result)
	}

	return dryrun_results
}
