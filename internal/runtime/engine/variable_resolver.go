package engine

import (
	"fmt"
	"regexp"
	"strings"
)

var variablePattern = regexp.MustCompile(`\{\{\s*([a-zA-Z0-9_\.]+)\s*\}\}`)

func ResolveTemplate(template string, rc *RunContext) string {
	return variablePattern.ReplaceAllStringFunc(template, func(match string) string {
		parts := variablePattern.FindStringSubmatch(match)
		if len(parts) != 2 {
			return match
		}
		value, ok := ResolveValue(parts[1], rc)
		if !ok || value == nil {
			return ""
		}
		return fmt.Sprint(value)
	})
}

func ResolveValue(path string, rc *RunContext) (any, bool) {
	segments := strings.Split(path, ".")
	if len(segments) == 0 || rc == nil {
		return nil, false
	}
	var current any
	switch segments[0] {
	case "sys":
		current = rc.Input
	default:
		output, ok := rc.NodeOutputs[segments[0]]
		if !ok {
			return nil, false
		}
		current = map[string]any(output)
	}
	for _, segment := range segments[1:] {
		m, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		current, ok = m[segment]
		if !ok {
			return nil, false
		}
	}
	return current, true
}

func ResolveAny(value any, rc *RunContext) any {
	switch typed := value.(type) {
	case string:
		return ResolveTemplate(typed, rc)
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			out[key] = ResolveAny(item, rc)
		}
		return out
	case []any:
		out := make([]any, 0, len(typed))
		for _, item := range typed {
			out = append(out, ResolveAny(item, rc))
		}
		return out
	default:
		return value
	}
}
