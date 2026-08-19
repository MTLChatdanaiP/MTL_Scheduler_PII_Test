package pii

import (
	"fmt"
	"regexp"
	"strings"
)

// RFC-006 §5 PII Categories: EMAIL_ADDRESS, PHONE_NUMBER, NATIONAL_ID, PASSPORT_NUMBER, CREDIT_CARD_NUMBER, BANK_ACCOUNT, IP_ADDRESS, PERSON_NAME, ADDRESS, DATE_OF_BIRTH — this project implements a subset: Email, Phone, SSN, CreditCard
type PIIType string

const (
	Email      PIIType = "Email"
	Phone      PIIType = "Phone"
	SSN        PIIType = "SSN"
	CreditCard PIIType = "CreditCard"
)

var AllTypes = []PIIType{Email, Phone, SSN, CreditCard}

type Finding struct {
	Type  PIIType
	Match string
}

// RFC-006 §6 Detector Strategies: "A detector may combine regex/pattern, checksum/validation, field-name heuristic, context heuristic, dictionary, statistical/model-based detector..." — this project implements only the regex/pattern strategy
var patterns = map[PIIType]*regexp.Regexp{
	Email:      regexp.MustCompile(`[^\s@]+@[^\s@]+\.[^\s@]+`), //gmail only for now cause idk how to detect other options
	Phone:      regexp.MustCompile(`\d{3}-\d{3}-\d{4}`),        // thai numbers only
	SSN:        regexp.MustCompile(`\d{3}-\d{2}-\d{4}`),        // XXX-XX-XXXX
	CreditCard: regexp.MustCompile(`\d{4}-\d{4}-\d{4}-\d{4}`),  //
}

// Detect scans the given text and returns every PII match found.
// RFC-006 §7 Scan Model: Content -> Normalizer -> Detector Set -> Candidate Findings -> Policy Evaluation. This function implements the Detector Set stage; there is no Normalizer stage yet
// RFC-006 §30 (PRD) PII Scanning Locations: this only scans JOB_PAYLOAD-equivalent text passed in as a string, not metadata/results/logs
func Detect(text string) []Finding {

	var Finding_Slice []Finding

	for piiType, pattern := range patterns {
		for index, PII_string := range pattern.FindAllString(text, -1) {
			fmt.Println(index, PII_string, piiType)
			Finding_Slice = append(Finding_Slice, Finding{Type: piiType, Match: PII_string})
		}
	}

	return Finding_Slice
}

// RFC-006 §11 Policy Actions — REDACT: "Replace sensitive content with a marker." This project always applies REDACT; OBSERVE/MASK/BLOCK are not implemented
// PRD §38 PII-Safe Logging: example pattern "[NATIONAL_ID_REDACTED]" — this project uses "[Type-Index]" placeholders, e.g. "[SSN-2]", to keep occurrences distinguishable for future rehydration
func Replacer(payload string, match string, PII_Type PIIType, index string) string {
	for _, piiType := range AllTypes {
		if PII_Type == piiType {
			replacement := "[" + string(piiType) + "-" + index + "]"
			return strings.Replace(payload, match, replacement, 1)
		}
	}

	return strings.Replace(payload, match, "[Unknown]", 1)
}
