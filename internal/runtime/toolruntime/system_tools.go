package toolruntime

import (
	"context"
	"encoding/json"
	"fmt"
)

type WorkflowCallHook func(ctx context.Context, rc ToolRunContext, parentRunID int64, workflowID, flowVersionID int64, input map[string]any) error

var callChainChecker WorkflowCallHook

func SetCallChainChecker(hook WorkflowCallHook) {
	callChainChecker = hook
}

type HumanApprovalTool struct{}

func (HumanApprovalTool) Name() string { return "request_human_approval" }

func (HumanApprovalTool) Description() string {
	return "Request human approval before executing a sensitive action. Use this when the action requires human review."
}

func (HumanApprovalTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"action":{"type":"string","description":"the action requiring approval"},"reason":{"type":"string","description":"why approval is needed"}},"required":["action","reason"]}`)
}

func (HumanApprovalTool) Metadata() ToolMetadata {
	return ToolMetadata{RiskLevel: RiskHigh, RequiresApproval: true, SideEffect: SideEffectExternalAction}
}

func (HumanApprovalTool) Execute(ctx context.Context, rc ToolRunContext, input json.RawMessage) (*ToolResult, error) {
	var args struct {
		Action string `json:"action"`
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	return &ToolResult{
		ContentText: "Human approval requested for action: " + args.Action + ". Message: " + args.Reason + ". Waiting for approval before proceeding.",
		IsError:     false,
	}, nil
}
