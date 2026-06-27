package engine

import (
	"fmt"
	"regexp"
	"strings"
)

var variablePattern = regexp.MustCompile(`\{\{\s*([^{}]+?)\s*\}\}`)

func ResolveTemplate(template string, rc *RunContext) string {
	return variablePattern.ReplaceAllStringFunc(template, func(match string) string {
		parts := variablePattern.FindStringSubmatch(match)
		if len(parts) != 2 {
			return match
		}
		value, ok := ResolveValue(strings.TrimSpace(parts[1]), rc)
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
	for i := range segments {
		segments[i] = strings.TrimSpace(segments[i])
	}
	if segments[0] == "" {
		return nil, false
	}
	var current any
	switch segments[0] {
	case "sys":
		if rc.Input == nil {
			return nil, false
		}
		current = rc.Input
	default:
		if rc.NodeOutputs == nil {
			return nil, false
		}
		output, ok := rc.NodeOutputs[segments[0]]
		if !ok {
			output, ok = rc.NodeOutputs[nodeOutputAlias(segments[0])]
		}
		if !ok {
			return nil, false
		}
		current = map[string]any(output)
	}
	for _, segment := range segments[1:] {
		if segment == "" {
			return nil, false
		}
		m, ok := asStringMap(current)
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

func nodeOutputAlias(id string) string {
	switch id {
	case "retrieve":
		return "retrieval"
	default:
		if strings.Contains(id, "_") {
			return strings.ReplaceAll(id, "_", " ")
		}
		return strings.ReplaceAll(id, " ", "_")
	}
}

func asStringMap(value any) (map[string]any, bool) {
	switch typed := value.(type) {
	case map[string]any:
		return typed, true
	case NodeInput:
		return map[string]any(typed), true
	case NodeOutput:
		return map[string]any(typed), true
	default:
		return nil, false
	}
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
