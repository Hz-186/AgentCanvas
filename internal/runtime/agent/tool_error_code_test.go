package agent

import (
	"context"
	"encoding/json"
	"testing"

	"agentcanvas/internal/domain/conversation"
	"agentcanvas/internal/infrastructure/llm"
	"agentcanvas/internal/runtime/harness/hooks"
	"agentcanvas/internal/runtime/toolruntime"
)

// errorMetadataTool returns a canned tool result so tests can drive the
// runner's tool_result step construction for failed tools without a backend.
type errorMetadataTool struct {
	name   string
	result *toolruntime.ToolResult
	err    error
}

func (t *errorMetadataTool) Name() string        { return t.name }
func (t *errorMetadataTool) Description() string { return "error metadata tool" }
func (t *errorMetadataTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"}}}`)
}
func (t *errorMetadataTool) Metadata() toolruntime.ToolMetadata { return toolruntime.ToolMetadata{} }
func (t *errorMetadataTool) Execute(context.Context, toolruntime.ToolRunContext, json.RawMessage) (*toolruntime.ToolResult, error) {
	return t.result, t.err
}

func toolResultStepByCallID(t *testing.T, steps []RunStep, toolCallID string) RunStep {
	t.Helper()
	for _, step := range steps {
		if step.Type == StepTypeToolResult && step.ToolCallID == toolCallID {
			return step
		}
	}
	t.Fatalf("no tool_result step for %s in %+v", toolCallID, steps)
	return RunStep{}
}

func TestToolResultStep(t *testing.T) {
	t.Run("shouldCarryErrorCodeFromResultMetadata", func(t *testing.T) {
		client := &fakeToolClient{responses: []llm.ToolChatResponse{
			{Message: llm.ChatMessage{Role: conversation.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "call_args", Name: "lookup", Arguments: json.RawMessage(`{"query":"x"}`)}}}},
			{Message: llm.ChatMessage{Role: conversation.RoleAssistant, Content: "done"}},
		}}
		tool := &errorMetadataTool{name: "lookup", result: &toolruntime.ToolResult{
			ContentText: "invalid arguments supplied",
			IsError:     true,
			Metadata:    map[string]any{"error_code": "invalid_arguments"},
		}}
		runner := &Runner{LLM: client, ProviderID: 1, ModelName: "gpt-4"}
		result, err := runner.Run(context.Background(), RunRequest{
			Model: "gpt-4", Mode: "react", Task: "lookup", MaxIterations: 3, MaxToolCalls: 2,
			Tools: []toolruntime.RuntimeTool{tool},
		})
		if err != nil {
			t.Fatal(err)
		}
		step := toolResultStepByCallID(t, result.Steps, "call_args")
		if !step.IsError {
			t.Fatalf("tool_result step must stay marked as error: %+v", step)
		}
		if step.ErrorCode != "invalid_arguments" {
			t.Fatalf("tool_result step must carry the metadata error code, got %q", step.ErrorCode)
		}
	})

	t.Run("shouldCarryIssueErrorCode", func(t *testing.T) {
		item := ToolBatchItem{Index: 0, Call: NormalizedToolCall{
			Call:  llm.ToolCall{ID: "call_issue", Name: "lookup", Arguments: json.RawMessage(`{"query":"x"}`)},
			Issue: &ToolCallIssue{Code: ToolCallIssueInvalidArguments, Message: "query must be a string"},
		}}
		executions := ExecuteToolBatch(context.Background(), []ToolBatchSegment{{Parallel: false, Items: []ToolBatchItem{item}}}, 1,
			func(context.Context, ToolBatchItem) (*toolruntime.ToolResult, error) {
				t.Fatal("issue calls must not reach tool execution")
				return nil, nil
			})
		if len(executions) != 1 {
			t.Fatalf("expected one execution, got %d", len(executions))
		}
		prepared := &preparedToolCall{call: item.Call.Call, result: executions[0].Result, err: executions[0].Err, latencyMS: 5}
		step := (&Runner{ProviderID: 1, ModelName: "gpt-4"}).newToolResultStep(prepared, hooks.PostToolUseResult{Content: executions[0].Result.ContentText})
		if !step.IsError {
			t.Fatalf("issue results must stay marked as error: %+v", step)
		}
		if step.ErrorCode != string(ToolCallIssueInvalidArguments) {
			t.Fatalf("issue results must surface their issue code on the step, got %q", step.ErrorCode)
		}
		if step.Error == "" {
			t.Fatalf("issue steps must keep the execution error text: %+v", step)
		}
	})

	t.Run("shouldLeaveErrorCodeEmptyOnSuccess", func(t *testing.T) {
		client := &fakeToolClient{responses: []llm.ToolChatResponse{
			{Message: llm.ChatMessage{Role: conversation.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "call_ok", Name: "lookup", Arguments: json.RawMessage(`{"query":"x"}`)}}}},
			{Message: llm.ChatMessage{Role: conversation.RoleAssistant, Content: "done"}},
		}}
		runner := &Runner{LLM: client, ProviderID: 1, ModelName: "gpt-4"}
		result, err := runner.Run(context.Background(), RunRequest{
			Model: "gpt-4", Mode: "react", Task: "lookup", MaxIterations: 3, MaxToolCalls: 2,
			Tools: []toolruntime.RuntimeTool{&fakeRuntimeTool{name: "lookup", output: "observation"}},
		})
		if err != nil {
			t.Fatal(err)
		}
		step := toolResultStepByCallID(t, result.Steps, "call_ok")
		if step.IsError || step.ErrorCode != "" {
			t.Fatalf("successful tool results must not carry error state: %+v", step)
		}
	})
}
