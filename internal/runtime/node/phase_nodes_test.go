package node

import (
	"context"
	"encoding/json"
	"testing"

	"agentcanvas/internal/domain/tool"
	"agentcanvas/internal/runtime/engine"
)

func TestHTTPToolRejectsPrivateAddress(t *testing.T) {
	def := &tool.Definition{
		ToolType: tool.TypeHTTP,
		Status:   tool.StatusActive,
		ConfigJSON: json.RawMessage(`{
			"url": "http://127.0.0.1:8080/internal",
			"method": "GET"
		}`),
	}
	_, err := ExecuteHTTPToolDefinition(context.Background(), def, []byte(`{"query":"x"}`))
	if err == nil {
		t.Fatal("ExecuteHTTPToolDefinition() expected private address error")
	}
}

func TestGuardrailBlocksSecrets(t *testing.T) {
	node := GuardrailNode{}
	rc := &engine.RunContext{OwnerID: 1, RunID: 2, Input: map[string]any{"answer": "secret sk-test1234567890"}}
	_, err := node.Run(context.Background(), rc, nil, json.RawMessage(`{"source":"{{sys.answer}}"}`))
	if err == nil {
		t.Fatal("GuardrailNode.Run() expected secret leak error")
	}
}

func TestJSONOutputValidatesObject(t *testing.T) {
	node := JSONOutputNode{}
	rc := &engine.RunContext{OwnerID: 1, RunID: 2, Input: map[string]any{"answer": `{"ok":true}`}}
	output, err := node.Run(context.Background(), rc, nil, json.RawMessage(`{"value":"{{sys.answer}}","schema":{"type":"object","required":["ok"],"properties":{"ok":{"type":"boolean"}}}}`))
	if err != nil {
		t.Fatalf("JSONOutputNode.Run() error = %v", err)
	}
	if output["json"] == nil {
		t.Fatalf("json output is nil")
	}
}
