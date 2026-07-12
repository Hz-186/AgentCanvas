package toolruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	agenterrors "agentcanvas/internal/pkg/errors"
)

type WorkflowCallTool struct {
	Caller             WorkflowCaller
	AllowedWorkflowIDs []int64
	MaxDepth           int
	ToolName           string
}

type workflowCallToolInput struct {
	WorkflowID    int64          `json:"workflow_id"`
	FlowVersionID int64          `json:"flow_version_id"`
	Input         map[string]any `json:"input"`
	Task          string         `json:"task"`
}

func (t WorkflowCallTool) Name() string {
	if strings.TrimSpace(t.ToolName) != "" {
		return strings.TrimSpace(t.ToolName)
	}
	return "call_workflow"
}

func (WorkflowCallTool) Description() string {
	return "Call another published AgentCanvas agent as a worker. Use this when a task should be delegated to a specialist agent, then use the returned output to continue."
}

func (t WorkflowCallTool) Parameters() json.RawMessage {
	workflowIDSchema := `"type":"number","description":"ID of the worker agent to call."`
	if len(t.AllowedWorkflowIDs) > 0 {
		values, _ := json.Marshal(t.AllowedWorkflowIDs)
		workflowIDSchema = fmt.Sprintf(`"type":"number","enum":%s,"description":"ID of the allowed worker agent to call."`, values)
	}
	return json.RawMessage(fmt.Sprintf(`{"type":"object","properties":{"workflow_id":{%s},"flow_version_id":{"type":"number","description":"Optional flow version ID. Leave empty to use the worker agent's current published version."},"task":{"type":"string","description":"Natural language task for the worker. Used as input.query when input is omitted."},"input":{"type":"object","description":"Structured input object for the worker workflow."}},"required":["workflow_id"],"additionalProperties":false}`, workflowIDSchema))
}

func (WorkflowCallTool) Metadata() ToolMetadata {
	return ToolMetadata{
		RiskLevel: RiskMedium, SideEffect: SideEffectExternalAction,
	}
}

func (t WorkflowCallTool) Execute(ctx context.Context, rc ToolRunContext, input json.RawMessage) (*ToolResult, error) {
	if t.Caller == nil {
		return nil, fmt.Errorf("workflow caller is not configured")
	}
	var parsed workflowCallToolInput
	if err := json.Unmarshal(input, &parsed); err != nil {
		return &ToolResult{
			ContentText: err.Error(),
			IsError:     true,
		}, err
	}
	if parsed.WorkflowID <= 0 {
		return &ToolResult{
			ContentText: "workflow_id is required", IsError: true,
		}, fmt.Errorf("%w: workflow_id is required", agenterrors.ErrInvalidInput)
	}
	if len(t.AllowedWorkflowIDs) > 0 && !slices.Contains(t.AllowedWorkflowIDs, parsed.WorkflowID) {
		msg := fmt.Sprintf("agent %d is not in allowed call_workflow_ids", parsed.WorkflowID)
		return &ToolResult{ContentText: msg, IsError: true}, fmt.Errorf("%w: %s", agenterrors.ErrForbidden, msg)
	}
	if callChainChecker != nil {
		chainErr := callChainChecker(ctx, rc, rc.RunID, parsed.WorkflowID, parsed.FlowVersionID, nil)
		if chainErr != nil {
			msg := fmt.Sprintf("call_workflow blocked: %s", chainErr.Error())
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
	result, err := t.Caller.CallWorkflow(ctx, WorkflowCallRequest{
		OwnerID:           rc.OwnerID,
		ParentRunID:       rc.RunID,
		CallerWorkflowID:  rc.WorkflowID,
		CallerNodeID:      rc.NodeID,
		WorkflowID:        parsed.WorkflowID,
		FlowVersionID:     parsed.FlowVersionID,
		Input:             callInput,
		CallDepth:         rc.CallDepth,
		WorkflowCallChain: append([]int64(nil), rc.WorkflowCallChain...),
		MaxDepth:          t.MaxDepth,
	})
	if err != nil {
		return &ToolResult{ContentText: err.Error(), IsError: true}, err
	}
	return ResultFromValue(result)
}
