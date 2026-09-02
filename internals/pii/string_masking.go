package pii

import (
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

func PrefixBeforeAt(s string, visible int, replacement string, preserveFormat bool) string {

	n := countBeforeAt(s, "@", preserveFormat)

	runes := []rune(s)
	count := 0

	for i, r := range runes {
		if r == '@' {
			break
		}

		if count >= n {
			continue
		}

		if preserveFormat && !isAlphanumeric(r) {
			continue
		}

		runes[i] = []rune(replacement)[0]
		count++
	}

	return string(runes)
}
