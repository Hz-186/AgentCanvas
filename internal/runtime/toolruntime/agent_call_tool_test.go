package toolruntime

import (
	"context"
	"encoding/json"
	"testing"
)

type fakeAgentCaller struct {
	req AgentCallRequest
}

func (c *fakeAgentCaller) CallAgent(ctx context.Context, req AgentCallRequest) (*AgentCallResult, error) {
	c.req = req
	return &AgentCallResult{
		RunID:         99,
		AgentID:       req.AgentID,
		FlowVersionID: 100,
		Status:        "succeeded",
		Output:        map[string]any{"content": "worker answer"},
		LatencyMS:     12,
	}, nil
}

func TestAgentCallToolCallsAllowedAgent(t *testing.T) {
	caller := &fakeAgentCaller{}
	tool := AgentCallTool{Caller: caller, AllowedAgentIDs: []int64{10}, MaxDepth: 3}
	result, err := tool.Execute(context.Background(), ToolRunContext{
		OwnerID:   1,
		AgentID:   2,
		RunID:     3,
		NodeID:    "agent_loop",
		CallDepth: 1,
	}, json.RawMessage(`{"agent_id":10,"task":"research this"}`))
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error result: %+v", result)
	}
	if caller.req.OwnerID != 1 || caller.req.ParentRunID != 3 || caller.req.CallerAgentID != 2 || caller.req.CallerNodeID != "agent_loop" {
		t.Fatalf("unexpected caller context: %+v", caller.req)
	}
	if caller.req.AgentID != 10 || caller.req.Input["query"] != "research this" || caller.req.CallDepth != 1 || caller.req.MaxDepth != 3 {
		t.Fatalf("unexpected call request: %+v", caller.req)
	}
	var output AgentCallResult
	if err := json.Unmarshal(result.ContentJSON, &output); err != nil {
		t.Fatal(err)
	}
	if output.RunID != 99 || output.Output["content"] != "worker answer" {
		t.Fatalf("unexpected output: %+v", output)
	}
}

func TestAgentCallToolRejectsDisallowedAgent(t *testing.T) {
	tool := AgentCallTool{Caller: &fakeAgentCaller{}, AllowedAgentIDs: []int64{10}}
	result, err := tool.Execute(context.Background(), ToolRunContext{}, json.RawMessage(`{"agent_id":11,"task":"x"}`))
	if err == nil {
		t.Fatal("expected disallowed agent error")
	}
	if result == nil || !result.IsError {
		t.Fatalf("expected error result: %+v", result)
	}
}
