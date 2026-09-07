package pii

import (
	"MTL_Scheduler_PII_Test/internals/models"
	"encoding/json"
	"log/slog"
	"regexp"
)

// RFC-006 §5 PII Categories: EMAIL_ADDRESS, PHONE_NUMBER, NATIONAL_ID, PASSPORT_NUMBER, CREDIT_CARD_NUMBER, BANK_ACCOUNT, IP_ADDRESS, PERSON_NAME, ADDRESS, DATE_OF_BIRTH — this project implements a subset: Email, Phone, SSN, CreditCard
type PIIType string

type Finding struct {
	Type       PIIType
	Match      string
	DetectorID string
	FieldPath  string
}

// RFC-006 §7 Scan Pipeline: Content -> Normalizer -> Detector Set -> Candidate Findings -> Policy Evaluation. This function implements the Detector Set stage; there is no Normalizer stage yet
// RFC-006 §5 Detector Definitions: patterns are no longer hardcoded in Go — they are compiled at call-time from the loaded PIIPolicy's detector list, matching the RFC's requirement that detection be policy-driven rather than baked into application code
// RFC-006 §8 Scan Sources: this only scans JOB_PAYLOAD/TASK_NAME-equivalent text passed in as a string, not metadata/results/logs
func Detect(text string, detectors []models.DetectorDefinition) ([]Finding, []string) {

	var findings []Finding
	var failed_det []string

	for _, det := range detectors {
		if !det.Enabled {
			continue
		}

		compiled_pattern, err := regexp.Compile(det.Pattern)
		if err != nil {
			failed_det = append(failed_det, det.ID)
			slog.Error("invalid detector pattern, skipping", "detector_id", det.ID, "error", err)
			continue
		}

		for _, m := range compiled_pattern.FindAllString(text, -1) {
			findings = append(findings, Finding{Type: PIIType(det.PIIType), Match: m, DetectorID: det.ID})
		}
	}

	return findings, failed_det
}

// TODO: internals/pii/json_scan.go (or add to the existing pii_controller.go)

func DetectJSON(jsonText string, detectors []models.DetectorDefinition, policy models.PIIPolicy, source string, jobType string) ([]EvaluatedFinding, []string, bool) {
	var parsed interface{}
	if err := json.Unmarshal([]byte(jsonText), &parsed); err != nil {
		return nil, nil, false
	}

	var evaluated []EvaluatedFinding
	var failedDetectors []string

	walkJSON("", parsed, func(path string, value string) {
		findings, failed := Detect(value, detectors)
		failedDetectors = append(failedDetectors, failed...)

		for _, f := range findings {
			f.FieldPath = path
			ctx := MatchContext{
				Source:     source,
				JobType:    jobType,
				PIIType:    string(f.Type),
				DetectorID: f.DetectorID,
				FieldPath:  path,
				Confidence: 1.0,
			}
			rule := ResolveRule(ctx, policy)
			evaluated = append(evaluated, EvaluatedFinding{Finding: f, Rule: rule})
		}
	})

	return evaluated, failedDetectors, true
}
