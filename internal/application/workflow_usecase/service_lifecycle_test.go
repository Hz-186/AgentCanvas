package workflow_usecase

import (
	"encoding/json"
	"errors"
	"testing"

	"agentcanvas/internal/domain/flow"
	agenterrors "agentcanvas/internal/pkg/errors"
)

func TestValidateLifecycleDSLAllowsPureTransformNodes(t *testing.T) {
	dsl := &flow.DSL{Nodes: []flow.Node{{ID: "begin", Type: "begin"}, {ID: "prompt", Type: "prompt"}, {ID: "out", Type: "json_output"}}}
	if err := validateLifecycleDSL(dsl); err != nil {
		t.Fatal(err)
	}
}

func TestValidateLifecycleDSLRejectsCapabilityNodes(t *testing.T) {
	for _, nodeType := range []string{"agent_loop", "http_tool", "mcp_tool", "memory_write", "code_sandbox", "call_workflow", "message"} {
		dsl := &flow.DSL{Nodes: []flow.Node{{ID: "unsafe", Type: nodeType, Config: json.RawMessage(`{}`)}}}
		if err := validateLifecycleDSL(dsl); !errors.Is(err, agenterrors.ErrForbidden) {
			t.Fatalf("node %s must be rejected, got %v", nodeType, err)
		}
	}
}
