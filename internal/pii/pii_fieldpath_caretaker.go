package pii

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

func ApplyFindingsToJSON(
	payload string,
	evaluated []EvaluatedFinding,
) string {
	// Legacy entry point, kept so existing callers and tests keep working. It cannot report failure: on any error it hands
	// the payload back UNCHANGED, which is the fail-open behaviour RFC-006 forbids. New code must use
	// ApplyFindingsToJSONChecked and treat an error as "do not store this".
	out, _, err := applyFindingsToJSON(payload, evaluated, false)
	if err != nil {
		return payload
	}
	return out
}

// ApplyFindingsToJSONChecked takes a JSON payload and its resolved findings and returns the payload with every REDACT, MASK
// and BLOCK finding rewritten, or an error. It fails CLOSED:
//
//   - the payload may have ANY JSON shape at the top level (object, array or string). The old rewrite only accepted an object,
//     so a payload such as [{"email":"a@b.com"}] was detected and vaulted but stored with the raw value still in it.
//   - an error is returned if the payload is not valid JSON, or if ANY finding could not be applied to a string (its path does
//     not exist, or the text at its offsets is not the text that was found). The caller must then withhold the payload.
//   - a string that has a finding is rewritten from its NFC form (see NormalizeForScan); strings with no finding, and a
//     payload with nothing to rewrite, are returned byte-for-byte unchanged.
//   - numbers are kept exactly as written (json.Number); the old rewrite turned 12345678901234567890 into 1.2345678901234567e+19.
func ApplyFindingsToJSONChecked(payload string, evaluated []EvaluatedFinding) (string, error) {
	out, unapplied, err := applyFindingsToJSON(payload, evaluated, true)
	if err != nil {
		return "", err
	}
	if len(unapplied) > 0 {
		return "", fmt.Errorf("pii: %d finding(s) could not be applied to the payload", len(unapplied))
	}
	return out, nil
}

type jsonCandidate struct {
	index   int
	finding EvaluatedFinding
}

// applyFindingsToJSON does the work for both entry points. It walks the decoded document the same way walkJSON does (same
// path text for objects and arrays) so every path walkJSON produced is one this finds. verifyText additionally requires that
// the text at a finding's offsets equals the text that was found; without it (legacy callers) offsets are trusted blindly.
// It returns the rewritten payload, the indexes of findings that matched no string, and an error for undecodable input.
func applyFindingsToJSON(payload string, evaluated []EvaluatedFinding, verifyText bool) (string, []int, error) {
	dec := json.NewDecoder(strings.NewReader(payload))
	dec.UseNumber()

	var root interface{}
	if err := dec.Decode(&root); err != nil {
		return payload, nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return payload, nil, errors.New("pii: payload has data after the JSON value")
	}

	byPath := make(map[string][]jsonCandidate)
	for i, e := range evaluated {
		byPath[e.Finding.FieldPath] = append(byPath[e.Finding.FieldPath], jsonCandidate{index: i, finding: e})
	}
	applied := make([]bool, len(evaluated))
	changed := false

	var rewrite func(path string, node interface{}) interface{}
	rewrite = func(path string, node interface{}) interface{} {
		switch v := node.(type) {
		case map[string]interface{}:
			for key, val := range v {
				v[key] = rewrite(jsonChildPath(path, key), val)
			}
			return v
		case []interface{}:
			for i, val := range v {
				v[i] = rewrite(path+"["+strconv.Itoa(i)+"]", val)
			}
			return v
		case string:
			candidates := byPath[path]
			if len(candidates) == 0 {
				return v
			}
			out, did := rewriteJSONString(v, candidates, applied, verifyText)
			if did {
				changed = true
			}
			return out
		default:
			return node
		}
	}
	newRoot := rewrite("", root)

	var unapplied []int
	for i, ok := range applied {
		if !ok {
			unapplied = append(unapplied, i)
		}
	}
	if !changed {
		return payload, unapplied, nil
	}

	out, err := json.Marshal(newRoot)
	if err != nil {
		return payload, unapplied, err
	}
	return string(out), unapplied, nil
}

// rewriteJSONString applies the findings that belong to ONE string. Two different strings can share a path (an object key that
// itself contains a dot gives the same path text as a nested key), so each finding is first matched to this string by checking
// that its text really sits at its offsets here.
func rewriteJSONString(leaf string, candidates []jsonCandidate, applied []bool, verifyText bool) (string, bool) {
	text := NormalizeForScan(leaf)

	var mine []EvaluatedFinding
	for _, c := range candidates {
		f := c.finding.Finding
		if verifyText && (f.Start < 0 || f.End > len(text) || f.Start >= f.End || text[f.Start:f.End] != f.Match) {
			continue
		}
		applied[c.index] = true
		mine = append(mine, c.finding)
	}

	var transformations []Transformation
	for _, e := range ResolveOverlaps(mine) {
		switch e.Rule.Action.Type {
		case "REDACT", "BLOCK":
			transformations = append(transformations, Transformation{
				Start:       e.Finding.Start,
				End:         e.Finding.End,
				Replacement: fmt.Sprintf("[%s-REDACTED]", e.Finding.Type),
			})
		case "MASK":
			transformations = append(transformations, Transformation{
				Start:       e.Finding.Start,
				End:         e.Finding.End,
				Replacement: Mask(e.Finding.Match, e.Rule.Action.Mask),
			})
		}
	}
	if len(transformations) == 0 {
		return leaf, false // OBSERVE-only (or nothing): leave the string exactly as it was
	}
	return ApplyTransformations(text, transformations), true
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
