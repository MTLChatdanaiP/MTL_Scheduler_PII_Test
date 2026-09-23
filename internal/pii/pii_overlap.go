package pii

func overlaps(a, b Finding) bool {
	return a.Start < b.End && b.Start < a.End
}

func winsOver(a, b EvaluatedFinding) bool {
	if a.Rule.Priority != b.Rule.Priority {
		return a.Rule.Priority > b.Rule.Priority
	}

	if a.Finding.Confidence != b.Finding.Confidence {
		return a.Finding.Confidence > b.Finding.Confidence
	}

	if len(a.Finding.Match) != len(b.Finding.Match) {
		return len(a.Finding.Match) > len(b.Finding.Match)
	}

	return a.Finding.DetectorID < b.Finding.DetectorID
}

func ResolveOverlaps(evaluated []EvaluatedFinding) []EvaluatedFinding {
	var kept []EvaluatedFinding

	for _, candidate := range evaluated {
		conflictIndex := -1

		for i, existing := range kept {
			if candidate.Finding.FieldPath != "" &&
				existing.Finding.FieldPath != "" &&
				candidate.Finding.FieldPath != existing.Finding.FieldPath {
				continue
			}

			if overlaps(candidate.Finding, existing.Finding) {
				conflictIndex = i
				break
			}
		}

		if conflictIndex == -1 {
			kept = append(kept, candidate)
			continue
		}

		if winsOver(candidate, kept[conflictIndex]) {
			kept[conflictIndex] = candidate
		}
	}

	return kept
}
