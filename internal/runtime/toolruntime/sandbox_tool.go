package toolruntime

import (
	"context"
	"encoding/json"
	"fmt"

	"agentcanvas/internal/runtime/sandbox"
)

type PythonSandboxTool struct {
	Runner sandbox.Runner
}

type pythonSandboxInput struct {
	Code      string `json:"code"`
	TimeoutMS int    `json:"timeout_ms"`
}

func (PythonSandboxTool) Name() string { return "execute_python" }

func (PythonSandboxTool) Description() string {
	return "Execute Python code in an isolated sandbox. Use this for calculation, data parsing, or verifying assumptions. Network is disabled."
}

func (PythonSandboxTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"code":{"type":"string","description":"Python code to execute. Print relevant results to stdout."},"timeout_ms":{"type":"number","description":"Execution timeout in milliseconds. Maximum is 30000."}},"required":["code"],"additionalProperties":false}`)
}

func (t PythonSandboxTool) Execute(ctx context.Context, rc ToolRunContext, input json.RawMessage) (*ToolResult, error) {
	if t.Runner == nil {
		return nil, fmt.Errorf("sandbox runner is not configured")
	}
	var parsed pythonSandboxInput
	if err := json.Unmarshal(input, &parsed); err != nil {
		return &ToolResult{ContentText: err.Error(), IsError: true}, err
	}
	result, err := t.Runner.Execute(ctx, sandbox.ExecuteRequest{
		Language:       "python",
		Code:           parsed.Code,
		TimeoutMS:      parsed.TimeoutMS,
		MaxOutputBytes: 64 * 1024,
		NetworkEnabled: false,
	})
	if err != nil {
		content := err.Error()
		if result != nil {
			data, _ := json.Marshal(result)
			content = string(data)
		}
		return &ToolResult{ContentText: content, IsError: true}, err
	}
	return ResultFromValue(result)
}
