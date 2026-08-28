package strutil

import (
	"encoding/json"
)

func TruncateWithSuffix(value string, maxBytes int, suffix string) string {
	truncated, _ := TruncateWithSuffixFlag(value, maxBytes, suffix)
	return truncated
}

func TruncateWithSuffixFlag(value string, maxBytes int, suffix string) (string, bool) {
	if maxBytes <= 0 || len(value) <= maxBytes {
		return value, false
	}
	return value[:maxBytes] + suffix, true
}

func TruncateRawJSONWithSuffix(raw json.RawMessage, maxBytes int, suffix string) (json.RawMessage, bool) {
	if maxBytes <= 0 || len(raw) <= maxBytes {
		return raw, false
	}
	data, _ := json.Marshal(TruncateWithSuffix(string(raw), maxBytes, suffix))
	return data, true
}
