package toolruntime

import (
	"context"
	"encoding/json"
	"testing"
)

type fakeAgentCaller struct {
	req WorkflowCallRequest
}

func (c *fakeAgentCaller) CallWorkflow(ctx context.Context, req WorkflowCallRequest) (*WorkflowCallResult, error) {
	c.req = req
	return &WorkflowCallResult{
		RunID:         99,
		WorkflowID:    req.WorkflowID,
		FlowVersionID: 100,
		Status:        "succeeded",
		Output:        map[string]any{"content": "worker answer"},
		LatencyMS:     12,
	}, nil
}

func TestWorkflowCallToolCallsAllowedAgent(t *testing.T) {
	caller := &fakeAgentCaller{}
	tool := WorkflowCallTool{Caller: caller, AllowedWorkflowIDs: []int64{10}, MaxDepth: 3}
	result, err := tool.Execute(context.Background(), ToolRunContext{
		OwnerID:    1,
		WorkflowID: 2,
		RunID:      3,
		NodeID:     "agent_loop",
		CallDepth:  1,
	}, json.RawMessage(`{"workflow_id":10,"task":"research this"}`))
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error result: %+v", result)
	}
	if caller.req.OwnerID != 1 || caller.req.ParentRunID != 3 || caller.req.CallerWorkflowID != 2 || caller.req.CallerNodeID != "agent_loop" {
		t.Fatalf("unexpected caller context: %+v", caller.req)
	}
	if caller.req.WorkflowID != 10 || caller.req.Input["query"] != "research this" || caller.req.CallDepth != 1 || caller.req.MaxDepth != 3 {
		t.Fatalf("unexpected call request: %+v", caller.req)
	}
	var output WorkflowCallResult
	if err := json.Unmarshal(result.ContentJSON, &output); err != nil {
		t.Fatal(err)
	}
	if output.RunID != 99 || output.Output["content"] != "worker answer" {
		t.Fatalf("unexpected output: %+v", output)
	}
}

func TestWorkflowCallToolRejectsDisallowedAgent(t *testing.T) {
	tool := WorkflowCallTool{Caller: &fakeAgentCaller{}, AllowedWorkflowIDs: []int64{10}}
	result, err := tool.Execute(context.Background(), ToolRunContext{}, json.RawMessage(`{"workflow_id":11,"task":"x"}`))
	if err == nil {
		t.Fatal("expected disallowed agent error")
	}
	if result == nil || !result.IsError {
		t.Fatalf("expected error result: %+v", result)
	}
}
