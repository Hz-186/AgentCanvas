package toolruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	agenterrors "agentcanvas/internal/pkg/errors"
)

type AgentCallTool struct {
	Caller          AgentCaller
	AllowedAgentIDs []int64
	MaxDepth        int
}

func (AgentCallTool) Name() string { return "call_agent" }
func (AgentCallTool) Description() string {
	return "Call a published independent Agent as a specialist. The child receives its own context and pinned release, and its final result is returned to the caller."
}
func (t AgentCallTool) Parameters() json.RawMessage {
	idSchema := `"type":"number","description":"ID of the independent Agent to call."`
	if len(t.AllowedAgentIDs) > 0 {
		values, _ := json.Marshal(t.AllowedAgentIDs)
		idSchema = fmt.Sprintf(`"type":"number","enum":%s`, values)
	}
	return json.RawMessage(fmt.Sprintf(`{"type":"object","properties":{"agent_id":{%s},"task":{"type":"string"}},"required":["agent_id","task"],"additionalProperties":false}`, idSchema))
}
func (AgentCallTool) Metadata() ToolMetadata {
	return ToolMetadata{RiskLevel: RiskMedium, SideEffect: SideEffectExternalAction, ExecutionClass: ExecutionDelegation}
}
func (t AgentCallTool) Execute(ctx context.Context, rc ToolRunContext, raw json.RawMessage) (*ToolResult, error) {
	var input struct {
		AgentID int64  `json:"agent_id"`
		Task    string `json:"task"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return &ToolResult{ContentText: err.Error(), IsError: true}, err
	}
	input.Task = strings.TrimSpace(input.Task)
	if input.AgentID <= 0 || input.Task == "" {
		return &ToolResult{ContentText: "agent_id and task are required", IsError: true}, fmt.Errorf("%w: agent_id and task are required", agenterrors.ErrInvalidInput)
	}
	if len(t.AllowedAgentIDs) > 0 && !slices.Contains(t.AllowedAgentIDs, input.AgentID) {
		return &ToolResult{ContentText: "agent is outside the release allowlist", IsError: true}, fmt.Errorf("%w: agent is outside the release allowlist", agenterrors.ErrForbidden)
	}
	if t.Caller == nil {
		return nil, fmt.Errorf("agent caller is not configured")
	}
	result, err := t.Caller.CallAgent(ctx, AgentCallRequest{OwnerID: rc.OwnerID, ParentRunID: rc.RunID, CallerAgentID: rc.AgentID,
		AgentID: input.AgentID, Task: input.Task, CallDepth: rc.CallDepth, MaxDepth: t.MaxDepth})
	if err != nil {
		return &ToolResult{ContentText: err.Error(), IsError: true}, err
	}
	return ResultFromValue(result)
}
