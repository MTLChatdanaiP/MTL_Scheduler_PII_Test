package pii

import "sort"

type Transformation struct {
	Start       int
	End         int
	Replacement string
}

func ApplyTransformations(payload string, transformations []Transformation) string {
	sort.Slice(transformations, func(i, j int) bool {
		return transformations[i].Start > transformations[j].Start
	})

	for _, t := range transformations {
		payload = payload[:t.Start] + t.Replacement + payload[t.End:]
	}

	return payload
}
