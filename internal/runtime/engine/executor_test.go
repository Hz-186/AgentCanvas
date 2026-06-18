package engine_test

import (
	"context"
	"encoding/json"
	"testing"

	"agentcanvas/internal/domain/flow"
	"agentcanvas/internal/runtime/engine"
	runtimenode "agentcanvas/internal/runtime/node"
)

func TestExecutorRunsLinearFlow(t *testing.T) {
	executor := engine.NewExecutor([]engine.Node{runtimenode.BeginNode{}, runtimenode.PromptNode{}, runtimenode.MessageNode{}})
	dsl := &flow.DSL{
		SchemaVersion: flow.SchemaVersionV1,
		FlowID:        "flow_test",
		Nodes: []flow.Node{
			{ID: "begin_1", Type: "begin", Config: json.RawMessage(`{"input_schema":{"query":"string"}}`)},
			{ID: "prompt_1", Type: "prompt", Config: json.RawMessage(`{"template":"问题：{{sys.query}}"}`)},
			{ID: "message_1", Type: "message", Config: json.RawMessage(`{"content":"{{prompt_1.prompt}}"}`)},
		},
		Edges: []flow.Edge{{From: "begin_1", To: "prompt_1"}, {From: "prompt_1", To: "message_1"}},
	}
	rc := &engine.RunContext{OwnerID: 1, AgentID: 2, FlowVersionID: 3, RunID: 4, Input: map[string]any{"query": "Agent Flow 如何执行？"}}
	output, err := executor.Execute(context.Background(), rc, dsl)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if output["content"] != "问题：Agent Flow 如何执行？" {
		t.Fatalf("content = %v", output["content"])
	}
	if rc.NodeOutputs["prompt_1"]["prompt"] != "问题：Agent Flow 如何执行？" {
		t.Fatalf("prompt output = %v", rc.NodeOutputs["prompt_1"])
	}
}
