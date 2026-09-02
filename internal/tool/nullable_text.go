package tool

import "strings"

// NormalizeNullableText converts historical textual null sentinels to the
// canonical application-level empty value. Callers decide whether that empty
// value is persisted as SQL NULL (optional fields) or rejected (required
// fields).
func NormalizeNullableText(value string) string {
	normalized := strings.TrimSpace(value)
	if strings.EqualFold(normalized, "null") {
		return ""
	}
	return normalized
}

func IsEmptyLikeText(value string) bool {
	return NormalizeNullableText(value) == ""
}
