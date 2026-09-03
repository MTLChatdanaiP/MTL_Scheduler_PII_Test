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

type PolicyRule struct {
	ID          string
	Priority    int
	DetectorIDs []string
	Action      string
	Mask        MaskConfig
}

type MaskConfig struct {
	Strategy          string // "KEEP_SUFFIX" (start with just this one)
	VisibleCharacters int
	MaskCharacter     string
	DomainMode        string
}
