package agent

import (
	"encoding/json"
	"fmt"

	"agentcanvas/internal/pkg/strutil"
	"agentcanvas/internal/runtime/harness/hooks"
)

// CheckCallChain protects the legacy workflow caller and the dynamic
// run_subagent bridge from cycles and excessive nesting. It is a guardrail,
// not a Supervisor or a role registry.
func CheckCallChain(callChain []int64, targetID int64, maxDepth int, currentDepth int) error {
	for _, id := range callChain {
		if id == targetID {
			return fmt.Errorf("circular delegation detected: agent %d is already in the call chain", targetID)
		}
	}
	if maxDepth > 0 && currentDepth >= maxDepth {
		return fmt.Errorf("max workflow call depth exceeded: current=%d max=%d", currentDepth, maxDepth)
	}
	return nil
}

// CompactToolOutput remains a small compatibility helper for historical
// workflow traces; tool execution itself is owned by toolruntime.
func CompactToolOutput(content string, maxBytes int) string {
	return strutil.TruncateWithSuffix(content, maxBytes, "...[compressed]")
}

// RedactSensitiveFields is shared by legacy trace paths and the current hook
// chain. It does not imply any Supervisor behavior.
func RedactSensitiveFields(raw json.RawMessage, fields []string) json.RawMessage {
	return hooks.RedactSensitiveFields(raw, fields)
}
