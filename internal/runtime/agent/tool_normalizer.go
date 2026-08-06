package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"agentcanvas/internal/infrastructure/llm"
	"agentcanvas/internal/runtime/toolruntime"
)

// ToolCallIssueCode classifies deterministic failures found before execution.
// Keeping this classification at the runtime boundary lets callers create a
// ToolResult without ever invoking an invalid tool implementation.
type ToolCallIssueCode string

const (
	ToolCallIssueMissingName      ToolCallIssueCode = "missing_name"
	ToolCallIssueUnknownName      ToolCallIssueCode = "unknown_name"
	ToolCallIssueAmbiguousName    ToolCallIssueCode = "ambiguous_name"
	ToolCallIssueInvalidJSON      ToolCallIssueCode = "invalid_json"
	ToolCallIssueInvalidArguments ToolCallIssueCode = "invalid_arguments"
	ToolCallIssueInvalidAlias     ToolCallIssueCode = "invalid_alias"
)

type ToolCallIssue struct {
	Code    ToolCallIssueCode `json:"code"`
	Message string            `json:"message"`
}

// NormalizedToolCall is the only representation that should cross the tool
// safety boundary. CallID is always non-empty and unique within one batch.
type NormalizedToolCall struct {
	Call     llm.ToolCall
	Tool     toolruntime.RuntimeTool
	Metadata toolruntime.ToolMetadata
	Issue    *ToolCallIssue
}

// ToolCallNormalizer resolves names without fuzzy matching. Exact names win;
// aliases are explicit, and case-insensitive matching is only accepted when
// it yields one canonical name.
type ToolCallNormalizer struct {
	Tools           map[string]toolruntime.RuntimeTool
	Aliases         map[string]string
	CaseInsensitive bool
}

func NewToolCallNormalizer(tools []toolruntime.RuntimeTool, aliases map[string]string) (*ToolCallNormalizer, error) {
	n := &ToolCallNormalizer{Tools: make(map[string]toolruntime.RuntimeTool), Aliases: make(map[string]string), CaseInsensitive: true}
	for _, tool := range tools {
		if tool == nil {
			continue
		}
		name := strings.TrimSpace(tool.Name())
		if name == "" {
			continue
		}
		if _, exists := n.Tools[name]; exists {
			return nil, fmt.Errorf("duplicate tool name %q", name)
		}
		n.Tools[name] = tool
	}
	for alias, target := range aliases {
		alias = strings.TrimSpace(alias)
		target = strings.TrimSpace(target)
		if alias == "" || target == "" {
			return nil, fmt.Errorf("tool alias and target must not be empty")
		}
		if _, exists := n.Aliases[alias]; exists {
			return nil, fmt.Errorf("duplicate tool alias %q", alias)
		}
		if _, exists := n.Tools[target]; !exists {
			return nil, fmt.Errorf("tool alias %q points to unknown tool %q", alias, target)
		}
		n.Aliases[alias] = target
	}
	return n, nil
}

func (n *ToolCallNormalizer) NormalizeBatch(calls []llm.ToolCall) []NormalizedToolCall {
	result := make([]NormalizedToolCall, 0, len(calls))
	usedIDs := make(map[string]int, len(calls))
	for index, call := range calls {
		item := n.Normalize(call, index)
		baseID := item.Call.ID
		if baseID == "" {
			baseID = generatedToolCallID(index, item.Call)
		}
		item.Call.ID = uniqueToolCallID(baseID, usedIDs)
		result = append(result, item)
	}
	return result
}

func (n *ToolCallNormalizer) Normalize(call llm.ToolCall, index int) NormalizedToolCall {
	item := NormalizedToolCall{Call: call}
	item.Call.ID = strings.TrimSpace(call.ID)
	if item.Call.ID == "" {
		item.Call.ID = generatedToolCallID(index, call)
	}
	item.Call.Name = strings.TrimSpace(call.Name)
	if item.Call.Name == "" {
		item.Issue = &ToolCallIssue{Code: ToolCallIssueMissingName, Message: "tool name is required"}
		return item
	}
	canonical, issue := n.resolveName(item.Call.Name)
	if issue != nil {
		item.Issue = issue
		return item
	}
	item.Call.Name = canonical
	item.Tool = n.Tools[canonical]
	item.Metadata = toolruntime.MetadataOf(item.Tool)
	args := strings.TrimSpace(string(item.Call.Arguments))
	if args == "" || args == "null" {
		item.Call.Arguments = json.RawMessage(`{}`)
	} else if !json.Valid(item.Call.Arguments) {
		item.Issue = &ToolCallIssue{Code: ToolCallIssueInvalidJSON, Message: "tool arguments must be valid JSON"}
		return item
	}
	if err := ValidateToolArguments(item.Tool.Parameters(), item.Call.Arguments); err != nil {
		item.Issue = &ToolCallIssue{Code: ToolCallIssueInvalidArguments, Message: err.Error()}
	}
	return item
}

func (n *ToolCallNormalizer) resolveName(name string) (string, *ToolCallIssue) {
	if _, ok := n.Tools[name]; ok {
		return name, nil
	}
	if target, ok := n.Aliases[name]; ok {
		return target, nil
	}
	if !n.CaseInsensitive {
		return "", &ToolCallIssue{Code: ToolCallIssueUnknownName, Message: fmt.Sprintf("tool %q is not available", name)}
	}
	needle := strings.ToLower(name)
	candidates := make([]string, 0, 1)
	for canonical := range n.Tools {
		if strings.ToLower(canonical) == needle {
			candidates = append(candidates, canonical)
		}
	}
	for alias, target := range n.Aliases {
		if strings.ToLower(alias) == needle {
			candidates = append(candidates, target)
		}
	}
	candidates = uniqueStrings(candidates)
	if len(candidates) == 1 {
		return candidates[0], nil
	}
	if len(candidates) > 1 {
		return "", &ToolCallIssue{Code: ToolCallIssueAmbiguousName, Message: fmt.Sprintf("tool name %q is ambiguous", name)}
	}
	return "", &ToolCallIssue{Code: ToolCallIssueUnknownName, Message: fmt.Sprintf("tool %q is not available", name)}
}

func generatedToolCallID(index int, call llm.ToolCall) string {
	hash := sha256.New()
	_, _ = hash.Write([]byte(fmt.Sprintf("%d\x00%s\x00%s", index, strings.TrimSpace(call.Name), string(call.Arguments))))
	return "call_" + hex.EncodeToString(hash.Sum(nil))[:16]
}

func uniqueToolCallID(base string, used map[string]int) string {
	base = strings.TrimSpace(base)
	if base == "" {
		base = "call_unknown"
	}
	count := used[base]
	used[base] = count + 1
	if count == 0 {
		return base
	}
	return fmt.Sprintf("%s_%d", base, count+1)
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}
