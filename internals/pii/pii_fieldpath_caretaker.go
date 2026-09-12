package pii

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

func ApplyFindingsToJSON(
	payload string,
	evaluated []EvaluatedFinding,
) string {

	var root map[string]interface{}

	if err := json.Unmarshal([]byte(payload), &root); err != nil {
		return payload
	}

	byPath := make(map[string][]EvaluatedFinding)

	for _, e := range evaluated {
		byPath[e.Finding.FieldPath] =
			append(byPath[e.Finding.FieldPath], e)
	}

	for path, findings := range byPath {

		resolved := ResolveOverlaps(findings)

		currentValue, ok := getStringAtPath(root, path)
		if !ok {
			continue
		}

		var transformations []Transformation

		for _, e := range resolved {

			switch e.Rule.Action.Type {

			case "REDACT":
				transformations = append(
					transformations,
					Transformation{
						Start: e.Finding.Start,
						End:   e.Finding.End,
						Replacement: fmt.Sprintf(
							"[%s-REDACTED]",
							e.Finding.Type,
						),
					},
				)

			case "MASK":
				transformations = append(
					transformations,
					Transformation{
						Start: e.Finding.Start,
						End:   e.Finding.End,
						Replacement: Mask(
							e.Finding.Match,
							e.Rule.Action.Mask,
						),
					},
				)
			}
		}

		newValue := ApplyTransformations(
			currentValue,
			transformations,
		)

		setStringAtPath(root, path, newValue)
	}

	result, err := json.Marshal(root)
	if err != nil {
		return payload
	}

	return string(result)
}

func getStringAtPath(root map[string]interface{}, path string) (string, bool) {
	current := interface{}(root)

	for _, part := range strings.Split(path, ".") {

		if strings.Contains(part, "[") {

			idx := strings.Index(part, "[")
			key := part[:idx]

			endIdx := strings.Index(part, "]")
			arrayIndex, err := strconv.Atoi(part[idx+1 : endIdx])
			if err != nil {
				return "", false
			}

			obj, ok := current.(map[string]interface{})
			if !ok {
				return "", false
			}

			arr, ok := obj[key].([]interface{})
			if !ok || arrayIndex >= len(arr) {
				return "", false
			}

			current = arr[arrayIndex]

		} else {

			obj, ok := current.(map[string]interface{})
			if !ok {
				return "", false
			}

			current = obj[part]
		}
	}

	s, ok := current.(string)
	return s, ok
}

func setStringAtPath(root map[string]interface{}, path string, value string) bool {
	current := interface{}(root)

	parts := strings.Split(path, ".")

	for i, part := range parts {

		last := i == len(parts)-1

		if strings.Contains(part, "[") {

			idx := strings.Index(part, "[")
			key := part[:idx]

			endIdx := strings.Index(part, "]")
			arrayIndex, err := strconv.Atoi(part[idx+1 : endIdx])
			if err != nil {
				return false
			}

			obj, ok := current.(map[string]interface{})
			if !ok {
				return false
			}

			arr, ok := obj[key].([]interface{})
			if !ok || arrayIndex >= len(arr) {
				return false
			}

			if last {
				arr[arrayIndex] = value
				return true
			}

			current = arr[arrayIndex]

		} else {

			obj, ok := current.(map[string]interface{})
			if !ok {
				return false
			}

			if last {
				if _, exists := obj[part]; !exists {
					return false
				}
				obj[part] = value
				return true
			}

			current = obj[part]
		}
	}

	return false
}
