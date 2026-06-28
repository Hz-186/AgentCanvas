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

type agentCallToolInput struct {
	AgentID       int64          `json:"agent_id"`
	FlowVersionID int64          `json:"flow_version_id"`
	Input         map[string]any `json:"input"`
	Task          string         `json:"task"`
}

func (AgentCallTool) Name() string { return "call_agent" }

func (AgentCallTool) Description() string {
	return "Call another published AgentCanvas agent as a worker. Use this when a task should be delegated to a specialist agent, then use the returned output to continue."
}

func (t AgentCallTool) Parameters() json.RawMessage {
	agentIDSchema := `"type":"number","description":"ID of the worker agent to call."`
	if len(t.AllowedAgentIDs) > 0 {
		values, _ := json.Marshal(t.AllowedAgentIDs)
		agentIDSchema = fmt.Sprintf(`"type":"number","enum":%s,"description":"ID of the allowed worker agent to call."`, values)
	}
	return json.RawMessage(fmt.Sprintf(`{"type":"object","properties":{"agent_id":{%s},"flow_version_id":{"type":"number","description":"Optional flow version ID. Leave empty to use the worker agent's current published version."},"task":{"type":"string","description":"Natural language task for the worker. Used as input.query when input is omitted."},"input":{"type":"object","description":"Structured input object for the worker agent."}},"required":["agent_id"],"additionalProperties":false}`, agentIDSchema))
}

func (AgentCallTool) Metadata() ToolMetadata {
	return ToolMetadata{RiskLevel: RiskMedium, SideEffect: SideEffectExternalAction}
}

func (t AgentCallTool) Execute(ctx context.Context, rc ToolRunContext, input json.RawMessage) (*ToolResult, error) {
	if t.Caller == nil {
		return nil, fmt.Errorf("agent caller is not configured")
	}
	var parsed agentCallToolInput
	if err := json.Unmarshal(input, &parsed); err != nil {
		return &ToolResult{ContentText: err.Error(), IsError: true}, err
	}
	if parsed.AgentID <= 0 {
		return &ToolResult{ContentText: "agent_id is required", IsError: true}, fmt.Errorf("%w: agent_id is required", agenterrors.ErrInvalidInput)
	}
	if len(t.AllowedAgentIDs) > 0 && !slices.Contains(t.AllowedAgentIDs, parsed.AgentID) {
		msg := fmt.Sprintf("agent %d is not in allowed call_agent_ids", parsed.AgentID)
		return &ToolResult{ContentText: msg, IsError: true}, fmt.Errorf("%w: %s", agenterrors.ErrForbidden, msg)
	}
	if callChainChecker != nil {
		chainErr := callChainChecker(ctx, rc, rc.RunID, parsed.AgentID, parsed.FlowVersionID, nil)
		if chainErr != nil {
			msg := fmt.Sprintf("call_agent blocked: %s", chainErr.Error())
			return &ToolResult{ContentText: msg, IsError: true}, fmt.Errorf("%w: %s", agenterrors.ErrForbidden, msg)
		}
	}
	callInput := parsed.Input
	if callInput == nil {
		callInput = map[string]any{}
	}
	if len(callInput) == 0 && strings.TrimSpace(parsed.Task) != "" {
		callInput["query"] = strings.TrimSpace(parsed.Task)
	}
	if len(callInput) == 0 {
		return &ToolResult{ContentText: "input or task is required", IsError: true}, fmt.Errorf("%w: input or task is required", agenterrors.ErrInvalidInput)
	}
	result, err := t.Caller.CallAgent(ctx, AgentCallRequest{
		OwnerID:       rc.OwnerID,
		ParentRunID:   rc.RunID,
		CallerAgentID: rc.AgentID,
		CallerNodeID:  rc.NodeID,
		AgentID:       parsed.AgentID,
		FlowVersionID: parsed.FlowVersionID,
		Input:         callInput,
		CallDepth:     rc.CallDepth,
		CallChain:     append([]int64(nil), rc.CallChain...),
		MaxDepth:      t.MaxDepth,
	})
	if err != nil {
		return &ToolResult{ContentText: err.Error(), IsError: true}, err
	}
	return ResultFromValue(result)
}
