package agent

import (
	"encoding/json"
	"fmt"

	"agentcanvas/internal/runtime/harness/hooks"
)

// CheckCallChain protects dynamic sub-Agent delegation from cycles and
// excessive nesting. It is a guardrail, not a Supervisor or role registry.
func CheckCallChain(callChain []int64, targetID int64, maxDepth int, currentDepth int) error {
	for _, id := range callChain {
		if id == targetID {
			return fmt.Errorf("circular delegation detected: agent %d is already in the call chain", targetID)
		}
	}
	if maxDepth > 0 && currentDepth >= maxDepth {
		return fmt.Errorf("max subagent depth exceeded: current=%d max=%d", currentDepth, maxDepth)
	}
	return nil
}

// RedactSensitiveFields is shared by trace paths and the current hook chain.
func RedactSensitiveFields(raw json.RawMessage, fields []string) json.RawMessage {
	return hooks.RedactSensitiveFields(raw, fields)
}
