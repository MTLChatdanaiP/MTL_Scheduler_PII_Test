package pii

import (
	"strconv"
	"strings"
)

// fieldPathMatches checks whether a CONCRETE path (e.g. "customers.0.phone")
// satisfies a PATTERN (e.g. "customers[*].phone"). Only supports a bounded
// subset: a segment can be a literal name, or "[*]" meaning "any single
// array index at this position" — matching RFC-006 §10's explicit
// requirement to avoid arbitrary user-provided expressions.

func getNumberInBrackets(s string) (int, bool) {
	idx := strings.LastIndex(s, "[")
	if idx != -1 && strings.HasSuffix(s, "]") {
		numberStr := s[idx+1 : len(s)-1]
		number, err := strconv.Atoi(numberStr)
		if err == nil {
			return number, true
		}
	}
	return -1, false
}

func fieldPathMatches(pattern string, concretePath string) bool {

	patternSegments := strings.Split(pattern, ".")
	pathSegments := strings.Split(concretePath, ".")

	if len(patternSegments) != len(pathSegments) {
		return false
	}

	for i, segment := range patternSegments {
		pathSeg := pathSegments[i]
		if strings.HasSuffix(segment, "[*]") {
			patName := strings.TrimSuffix(segment, "[*]")

			realIdx := strings.LastIndex(pathSeg, "[")
			if realIdx == -1 || !strings.HasSuffix(pathSeg, "]") {
				return false
			}
			realName := pathSeg[:realIdx]

			if patName != realName {
				return false
			}
			if _, ok := getNumberInBrackets(pathSeg); !ok {
				return false
			}
			continue
		}
		if segment != pathSegments[i] {
			return false
		}
	}

	return true
}

func walkJSON(prefix string, data interface{}, scan func(path string, value string)) { //Detection only
	switch v := data.(type) {
	case map[string]interface{}:
		for key, val := range v {
			path := key
			if prefix != "" {
				path = prefix + "." + key
			}
			walkJSON(path, val, scan)
		}
	case []interface{}:
		for i, val := range v {
			path := prefix + "[" + strconv.Itoa(i) + "]"
			walkJSON(path, val, scan)
		}
	case string:
		scan(prefix, v)
	}
}
