package pii

import (
	"strings"
	"unicode"
)

func visibleToReplaceCount(s string, visibleCharacters int, preserveFormat bool) int {
	count := 0

	for _, c := range s {
		if preserveFormat && !isAlphanumeric(c) {
			continue
		}

		count++
	}

	replaceCount := count - visibleCharacters

	if replaceCount < 0 {
		return 0
	}

	return replaceCount
}

func isAlphanumeric(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r)
}

func removeNonAlphanumeric(s string) string {
	runes := []rune(s)
	result := make([]rune, 0, len(runes))

	for _, r := range runes {
		if isAlphanumeric(r) {
			result = append(result, r)
		}
	}

	return string(result)
}

func suffixReplacer(s string, visible int, replacement string, preserveFormat bool) string {

	n := visibleToReplaceCount(s, visible, preserveFormat)

	result := ""
	count := 0

	for _, c := range s {
		if count >= n {
			result += string(c)
			continue
		}

		if preserveFormat && !isAlphanumeric(c) {
			result += string(c)
			continue
		}

		result += replacement
		count++
	}

	return result
}

func prefixReplacer(s string, visible int, replacement string, preserveFormat bool) string {

	n := visibleToReplaceCount(s, visible, preserveFormat)

	result := []rune(s)
	count := 0

	for i := len(result) - 1; i >= 0; i-- {
		if count >= n {
			break
		}

		if preserveFormat && !isAlphanumeric(result[i]) {
			continue
		}

		result[i] = []rune(replacement)[0]
		count++
	}

	return string(result)
}

func prefixsuffixReplacer(s string, visible int, replacement string, preserveFormat bool) string {
	runes := []rune(s)

	if visible*2 >= len(runes) {
		return s
	}

	prefix := runes[:visible]
	middle := runes[visible : len(runes)-visible]
	suffix := runes[len(runes)-visible:]

	maskedMiddle := make([]rune, len(middle))
	for i, r := range middle {
		if preserveFormat && !isAlphanumeric(r) {
			maskedMiddle[i] = r
			continue
		}
		maskedMiddle[i] = []rune(replacement)[0]
	}

	result := append([]rune{}, prefix...)
	result = append(result, maskedMiddle...)
	result = append(result, suffix...)

	return string(result)
}

func countBeforeAt(s string, at string, preserveFormat bool) int {
	count := 0

	for _, r := range s {
		if r == '@' {
			break
		}

		if preserveFormat && !isAlphanumeric(r) {
			continue
		}

		count++
	}

	return count
}

// PrefixBeforeAt masks an email-shaped value, keeping the first `visible`
// characters of the part before the "@" and masking the rest of it. The domain is
// preserved unless domainMode is "MASK". Name and signature are unchanged.
//
// It used to keep the LAST `visible` characters of the local part instead of the
// first (so "jane.doe" with 2 visible became "******oe" rather than "ja******"),
// contradicting RFC-006 §13's localVisiblePrefix, and it panicked on an empty
// mask character. A value with no "@" is treated as all local part, so it is still
// masked rather than passed through.
func PrefixBeforeAt(s string, visible int, replacement string, preserveFormat bool, domainMode string) string {
	if replacement == "" {
		replacement = defaultMaskCharacter
	}
	if visible < 0 {
		visible = 0
	}
	fill := []rune(replacement)[0]

	runes := []rune(s)

	at := -1
	for i, r := range runes {
		if r == '@' {
			at = i
			break
		}
	}
	local := len(runes)
	if at != -1 {
		local = at
	}

	kept := 0
	for i := 0; i < local; i++ {
		if preserveFormat && !isAlphanumeric(runes[i]) {
			continue
		}
		if kept < visible {
			kept++
			continue
		}
		runes[i] = fill
	}

	result := string(runes)

	if strings.ToUpper(domainMode) == "MASK" && at != -1 {
		domain := string(runes[at+1:])
		domain = suffixReplacer(domain, 0, replacement, preserveFormat)
		result = string(runes[:at+1]) + domain
	}

	return result
}
