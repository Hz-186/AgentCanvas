package toolruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode"

	"agentcanvas/internal/domain/tool"

	agenterrors "agentcanvas/internal/pkg/errors"
)

type BasicRegistry struct {
	Tools       tool.DefinitionRepository
	Invocations tool.InvocationRepository
}

func (r BasicRegistry) LoadForAgent(ctx context.Context, ownerID int64, toolIDs []int64) ([]RuntimeTool, error) {
	loaded := make([]RuntimeTool, 0, len(toolIDs))
	seenNames := make(map[string]bool, len(toolIDs))
	for _, item := range loaded {
		seenNames[item.Name()] = true
	}
	if len(toolIDs) == 0 {
		return loaded, nil
	}
	if r.Tools == nil {
		return nil, fmt.Errorf("tool definition repository is not configured")
	}
	for _, id := range toolIDs {
		def, err := r.Tools.FindByID(ctx, ownerID, id)
		if err != nil {
			return nil, err
		}
		if def.Status != tool.StatusActive {
			return nil, fmt.Errorf("%w: tool %d is not active", agenterrors.ErrInvalidInput, id)
		}
		switch def.ToolType {
		case tool.TypeHTTP:
			runtimeTool := NewHTTPRuntimeTool(def, r.Invocations)
			name := runtimeTool.Name()
			if seenNames[name] {
				return nil, fmt.Errorf("%w: duplicate agent tool name %s", agenterrors.ErrInvalidInput, name)
			}
			seenNames[name] = true
			loaded = append(loaded, runtimeTool)
		default:
			return nil, fmt.Errorf("%w: unsupported agent tool type %s", agenterrors.ErrInvalidInput, def.ToolType)
		}
	}
	return loaded, nil
}

func normalizeToolName(name string, id int64) string {
	name = strings.TrimSpace(strings.ToLower(name))
	var b strings.Builder
	previousUnderscore := false
	for _, r := range name {
		allowed := unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_'
		if allowed {
			b.WriteRune(r)
			previousUnderscore = false
			continue
		}
		if !previousUnderscore {
			b.WriteByte('_')
			previousUnderscore = true
		}
	}
	out := strings.Trim(b.String(), "_")
	if out == "" {
		out = fmt.Sprintf("tool_%d", id)
	}
	first := rune(out[0])
	if !(unicode.IsLetter(first) || first == '_') {
		out = "tool_" + out
	}
	if len(out) > 64 {
		out = out[:64]
		out = strings.TrimRight(out, "_")
	}
	return out
}

func defaultObjectSchema(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 || strings.TrimSpace(string(raw)) == "" || string(raw) == "null" {
		return json.RawMessage(`{"type":"object","properties":{},"additionalProperties":true}`)
	}
	return raw
}
