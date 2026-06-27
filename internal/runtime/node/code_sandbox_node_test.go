package node

import (
	"context"
	"encoding/json"
	"testing"

	"agentcanvas/internal/runtime/engine"
	"agentcanvas/internal/runtime/sandbox"
)

type fakeCodeSandboxRunner struct {
	req sandbox.ExecuteRequest
}

func (r *fakeCodeSandboxRunner) Execute(ctx context.Context, req sandbox.ExecuteRequest) (*sandbox.ExecuteResult, error) {
	r.req = req
	return &sandbox.ExecuteResult{Language: req.Language, Stdout: "hello\n", ExitCode: 0, LatencyMS: 4}, nil
}

func TestCodeSandboxNodeRunsPython(t *testing.T) {
	runner := &fakeCodeSandboxRunner{}
	node := CodeSandboxNode{Runner: runner}
	rc := &engine.RunContext{OwnerID: 1, RunID: 2, CurrentNodeID: "sandbox", Input: map[string]any{"name": "AgentCanvas"}}
	output, err := node.Run(context.Background(), rc, nil, json.RawMessage(`{
		"language": "python",
		"code": "print('{{sys.name}}')",
		"timeout_ms": 5000,
		"max_output_bytes": 2048,
		"network_enabled": false,
		"memory_limit_mb": 128
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if runner.req.Code != "print('AgentCanvas')" || runner.req.TimeoutMS != 5000 || runner.req.NetworkEnabled {
		t.Fatalf("unexpected sandbox request: %+v", runner.req)
	}
	if output["content"] != "hello\n" || output["exit_code"] != 0 {
		t.Fatalf("unexpected output: %+v", output)
	}
}
