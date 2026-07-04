package node

import (
	"context"
	"encoding/json"
	"testing"

	"agentcanvas/internal/runtime/engine"
	"agentcanvas/internal/runtime/toolruntime"
)

type fakeNodeAgentCaller struct {
	req toolruntime.WorkflowCallRequest
}

func (c *fakeNodeAgentCaller) CallWorkflow(ctx context.Context, req toolruntime.WorkflowCallRequest) (*toolruntime.WorkflowCallResult, error) {
	c.req = req
	return &toolruntime.WorkflowCallResult{
		RunID:         20,
		WorkflowID:    req.WorkflowID,
		FlowVersionID: 30,
		Status:        "succeeded",
		Output:        map[string]any{"content": "child output"},
		LatencyMS:     5,
	}, nil
}

func TestWorkflowCallNodeRunsChildAgent(t *testing.T) {
	caller := &fakeNodeAgentCaller{}
	node := WorkflowCallNode{Caller: caller}
	rc := &engine.RunContext{
		OwnerID:       1,
		WorkflowID:    2,
		RunID:         3,
		CurrentNodeID: "call_writer",
		Input:         map[string]any{"query": "hello"},
		CallDepth:     1,
	}
	output, err := node.Run(context.Background(), rc, nil, json.RawMessage(`{
		"workflow_id": 10,
		"input": {"query": "{{sys.query}}"},
		"max_depth": 3
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if caller.req.OwnerID != 1 || caller.req.ParentRunID != 3 || caller.req.CallerWorkflowID != 2 || caller.req.CallerNodeID != "call_writer" {
		t.Fatalf("unexpected caller context: %+v", caller.req)
	}
	if caller.req.WorkflowID != 10 || caller.req.Input["query"] != "hello" || caller.req.CallDepth != 1 || caller.req.MaxDepth != 3 {
		t.Fatalf("unexpected call request: %+v", caller.req)
	}
	if output["content"] != "child output" || output["run_id"] != int64(20) {
		t.Fatalf("unexpected output: %+v", output)
	}
}

func TestAgentCallNodeRunsChildAgentWithAgentCallNodeType(t *testing.T) {
	caller := &fakeNodeAgentCaller{}
	node := AgentCallNode{Caller: caller}
	rc := &engine.RunContext{
		OwnerID:           1,
		WorkflowID:        2,
		RunID:             3,
		CurrentNodeID:     "agent_call",
		WorkflowCallChain: []int64{2},
	}
	output, err := node.Run(context.Background(), rc, nil, json.RawMessage(`{
		"workflow_id": 10,
		"input": {"query": "delegate"},
		"max_depth": 3
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if caller.req.CallerNodeID != "agent_call" || caller.req.WorkflowID != 10 || caller.req.Input["query"] != "delegate" {
		t.Fatalf("unexpected call request: %+v", caller.req)
	}
	if output["content"] != "child output" || output["workflow_id"] != int64(10) {
		t.Fatalf("unexpected output: %+v", output)
	}
}

func TestAgentCallNodeValidate(t *testing.T) {
	node := AgentCallNode{}
	if err := node.Validate(json.RawMessage(`{"workflow_id":0}`)); err == nil {
		t.Fatal("expected missing workflow_id to fail")
	}
	if err := node.Validate(json.RawMessage(`{"workflow_id":1,"max_depth":5}`)); err != nil {
		t.Fatalf("expected valid agent_call config: %v", err)
	}
}
