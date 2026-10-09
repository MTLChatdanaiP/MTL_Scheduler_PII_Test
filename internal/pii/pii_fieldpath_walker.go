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
			walkJSON(jsonChildPath(prefix, key), val, scan)
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

// pathKeyEscaper escapes the characters that give a path its structure.
var pathKeyEscaper = strings.NewReplacer(`\`, `\\`, ".", `\.`, "[", `\[`, "]", `\]`)

// jsonChildPath returns the path of the member named key inside the object at prefix. walkJSON and the rewrite in
// pii_fieldpath_caretaker.go both build paths with it, so they cannot disagree.
//
// A key that contains ".", "[", "]" or "\" is escaped. Without that, the key "a.b" and the nested {"a":{"b":...}} gave the SAME
// path text, and findings are grouped and de-duplicated BY PATH: ResolveOverlaps saw two findings with the same path and
// offsets, treated them as overlapping and dropped one, leaving that string unredacted and unrecorded. A crafted key could
// therefore make a value escape. Keys without those characters (every ordinary key) produce exactly the path they always did.
func jsonChildPath(prefix, key string) string {
	key = pathKeyEscaper.Replace(key)
	if prefix == "" {
		return key
	}
	return prefix + "." + key
}
