package models

type PIIPolicy struct {
	APIVersion string
	Kind       string
	Metadata   struct {
		Name     string
		Version  int
		Checksum string
	}
	Spec struct {
		EvaluationMode string // "FIRST_MATCH"
		Defaults       struct {
			Action      string // "OBSERVE"
			OnScanError string // "FAIL_OPEN"
		}
		Detectors []DetectorDefinition
		Rules     []PolicyRule
	}
}

type DetectorDefinition struct {
	ID                string
	PIIType           string
	Type              string // "REGEX", "BUILTIN", "FIELD_NAME", "COMPOSITE"
	Enabled           bool
	Pattern           string // only used if Type == "REGEX"
	MinimumConfidence float64
}

type MatchConditions struct {
	Sources           []string
	JobTypes          []string
	Queues            []string
	FieldPaths        []string
	PIITypes          []string
	DetectorIDs       []string `json:"detectorIds"`
	MinimumConfidence float64
	Labels            []string
}

type PolicyRule struct {
	ID       string          `json:"id"`
	Priority int             `json:"priority"`
	Match    MatchConditions `json:"match"`
	Action   PolicyAction    `json:"action"`
}

type PolicyAction struct {
	Type        string     `json:"type"`
	Replacement string     `json:"replacement"`
	ReasonCode  string     `json:"reasonCode"`
	Mask        MaskConfig `json:"mask"`
}

type MaskConfig struct {
	Strategy          string `json:"strategy"`
	VisibleCharacters int    `json:"visibleCharacters"`
	MaskCharacter     string `json:"maskCharacter"`
	DomainMode        string `json:"domainMode"`

	// RFC-006 §13: how many characters of the part before the "@" stay visible
	// for the EMAIL strategy. Falls back to VisibleCharacters when unset.
	//
	// omitempty is REQUIRED here, not cosmetic. LoadPolicy computes the policy
	// checksum as sha256(json.Marshal(policy.Spec)), so adding a field WITHOUT
	// omitempty would make every existing policy marshal differently, fail its
	// checksum, and refuse to load. With omitempty a zero value is omitted and
	// every existing policy keeps byte-identical output. It must also stay the
	// LAST field so the order of the existing fields is untouched.
	LocalVisiblePrefix int `json:"localVisiblePrefix,omitempty"`
}
