package pii

import "strings"

// RFC-006 §11 IGNORE: "Suppress a detector match intentionally. This supports known-safe fields or expected false
// positives without disabling the detector globally."

// DropIgnored takes findings that have been through overlap resolution and returns those whose rule is NOT IGNORE, in
// their original order.
//
// It runs AFTER ResolveOverlaps on purpose. An IGNORE rule competes for a piece of text like any other rule, so one with a
// higher priority than a REDACT on the same text wins that text and the text is left alone, while a REDACT with the higher
// priority wins it and is applied. Dropping IGNORE findings before the overlap step would let a lower-priority rule take
// over the text the operator meant to protect from it.
//
// An ignored finding leaves no trace at all: no record, no fingerprint, no vault entry, no event and no transformation.
// (An audit row for it would make PII_DETECTED style alerts fire for values the operator deliberately suppressed.)
func DropIgnored(findings []EvaluatedFinding) []EvaluatedFinding {
	kept := make([]EvaluatedFinding, 0, len(findings))
	for _, f := range findings {
		if strings.EqualFold(f.Rule.Action.Type, "IGNORE") {
			continue
		}
		kept = append(kept, f)
	}
	return kept
}
