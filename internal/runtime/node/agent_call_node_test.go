package node

import (
	"context"
	"encoding/json"
	"testing"

	"agentcanvas/internal/runtime/engine"
	"agentcanvas/internal/runtime/toolruntime"
)

type fakeNodeAgentCaller struct {
	req toolruntime.AgentCallRequest
}

func (c *fakeNodeAgentCaller) CallAgent(ctx context.Context, req toolruntime.AgentCallRequest) (*toolruntime.AgentCallResult, error) {
	c.req = req
	return &toolruntime.AgentCallResult{
		RunID:         20,
		AgentID:       req.AgentID,
		FlowVersionID: 30,
		Status:        "succeeded",
		Output:        map[string]any{"content": "child output"},
		LatencyMS:     5,
	}, nil
}

func TestAgentCallNodeRunsChildAgent(t *testing.T) {
	caller := &fakeNodeAgentCaller{}
	node := AgentCallNode{Caller: caller}
	rc := &engine.RunContext{
		OwnerID:       1,
		AgentID:       2,
		RunID:         3,
		CurrentNodeID: "call_writer",
		Input:         map[string]any{"query": "hello"},
		CallDepth:     1,
	}
	output, err := node.Run(context.Background(), rc, nil, json.RawMessage(`{
		"agent_id": 10,
		"input": {"query": "{{sys.query}}"},
		"max_depth": 3
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if caller.req.OwnerID != 1 || caller.req.ParentRunID != 3 || caller.req.CallerAgentID != 2 || caller.req.CallerNodeID != "call_writer" {
		t.Fatalf("unexpected caller context: %+v", caller.req)
	}
	if caller.req.AgentID != 10 || caller.req.Input["query"] != "hello" || caller.req.CallDepth != 1 || caller.req.MaxDepth != 3 {
		t.Fatalf("unexpected call request: %+v", caller.req)
	}
	if output["content"] != "child output" || output["run_id"] != int64(20) {
		t.Fatalf("unexpected output: %+v", output)
	}
}
