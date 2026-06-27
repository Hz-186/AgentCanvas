package toolruntime

import (
	"context"
	"encoding/json"
	"testing"

	"agentcanvas/internal/runtime/sandbox"
)

type fakeSandboxRunner struct {
	req sandbox.ExecuteRequest
}

func (r *fakeSandboxRunner) Execute(ctx context.Context, req sandbox.ExecuteRequest) (*sandbox.ExecuteResult, error) {
	r.req = req
	return &sandbox.ExecuteResult{
		Language:  req.Language,
		Stdout:    "42\n",
		ExitCode:  0,
		LatencyMS: 3,
	}, nil
}

func TestPythonSandboxToolExecutesCode(t *testing.T) {
	runner := &fakeSandboxRunner{}
	tool := PythonSandboxTool{Runner: runner}
	result, err := tool.Execute(context.Background(), ToolRunContext{}, json.RawMessage(`{"code":"print(6*7)","timeout_ms":2000}`))
	if err != nil {
		t.Fatal(err)
	}
	if runner.req.Language != "python" || runner.req.Code != "print(6*7)" || runner.req.TimeoutMS != 2000 || runner.req.NetworkEnabled {
		t.Fatalf("unexpected sandbox request: %+v", runner.req)
	}
	var output sandbox.ExecuteResult
	if err := json.Unmarshal(result.ContentJSON, &output); err != nil {
		t.Fatal(err)
	}
	if output.Stdout != "42\n" || output.ExitCode != 0 {
		t.Fatalf("unexpected output: %+v", output)
	}
}
