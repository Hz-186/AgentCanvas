package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"agentcanvas/internal/domain/conversation"
	"agentcanvas/internal/infrastructure/llm"
	"agentcanvas/internal/runtime/toolruntime"
)

type fakeToolClient struct {
	responses []llm.ToolChatResponse
	requests  []llm.ToolChatRequest
}

func (c *fakeToolClient) ChatWithTools(ctx context.Context, cfg llm.ChatProviderConfig, req llm.ToolChatRequest) (*llm.ToolChatResponse, error) {
	c.requests = append(c.requests, req)
	if len(c.responses) == 0 {
		return &llm.ToolChatResponse{Message: llm.ChatMessage{Role: conversation.RoleAssistant, Content: "done"}}, nil
	}
	resp := c.responses[0]
	c.responses = c.responses[1:]
	return &resp, nil
}

type fakeRuntimeTool struct {
	name        string
	output      string
	outputJSON  json.RawMessage
	input       json.RawMessage
	metadata    toolruntime.ToolMetadata
	sawDeadline bool
}

func (t *fakeRuntimeTool) Name() string { return t.name }

func (t *fakeRuntimeTool) Description() string { return "fake tool" }

func (t *fakeRuntimeTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"}}}`)
}

func (t *fakeRuntimeTool) Execute(ctx context.Context, rc toolruntime.ToolRunContext, input json.RawMessage) (*toolruntime.ToolResult, error) {
	t.input = input
	_, t.sawDeadline = ctx.Deadline()
	outputJSON := t.outputJSON
	if len(outputJSON) == 0 {
		outputJSON = json.RawMessage(`{"ok":true}`)
	}
	return &toolruntime.ToolResult{ContentText: t.output, ContentJSON: outputJSON}, nil
}

func (t *fakeRuntimeTool) Metadata() toolruntime.ToolMetadata {
	return t.metadata
}

func TestRunnerExecutesToolAndReturnsFinalAnswer(t *testing.T) {
	client := &fakeToolClient{responses: []llm.ToolChatResponse{
		{
			Message: llm.ChatMessage{
				Role: conversation.RoleAssistant,
				ToolCalls: []llm.ToolCall{{
					ID:        "call_1",
					Name:      "search_knowledge",
					Arguments: json.RawMessage(`{"query":"agent"}`),
				}},
			},
			Usage: llm.Usage{TotalTokens: 3},
		},
		{
			Message: llm.ChatMessage{Role: conversation.RoleAssistant, Content: "final answer"},
			Usage:   llm.Usage{TotalTokens: 4},
		},
	}}
	tool := &fakeRuntimeTool{name: "search_knowledge", output: "observation"}
	runner := &Runner{LLM: client, ProviderID: 1, ModelName: "gpt-4"}
	result, err := runner.Run(context.Background(), RunRequest{
		OwnerID:       1,
		RunID:         2,
		NodeID:        "agent",
		Model:         "gpt-4",
		Task:          "answer",
		MaxIterations: 4,
		MaxToolCalls:  2,
		Tools:         []toolruntime.RuntimeTool{tool},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.FinalAnswer != "final answer" || result.StopReason != StopReasonFinalAnswer {
		t.Fatalf("unexpected result: %+v", result)
	}
	if result.ToolCalls != 1 || result.Usage.TotalTokens != 7 {
		t.Fatalf("unexpected counters: %+v", result)
	}
	if len(client.requests) != 2 {
		t.Fatalf("expected two LLM calls, got %d", len(client.requests))
	}
	second := client.requests[1]
	if got := second.Messages[len(second.Messages)-1]; got.Role != conversation.RoleTool || got.Content != "observation" {
		t.Fatalf("tool observation not injected: %+v", second.Messages)
	}
	if string(tool.input) != `{"query":"agent"}` {
		t.Fatalf("tool input mismatch: %s", tool.input)
	}
	for _, step := range result.Steps {
		if step.ProviderID != 1 || step.Model != "gpt-4" {
			t.Fatalf("step missing provider/model: %+v", step)
		}
	}
}

func TestRunnerStopsForHumanApprovalBeforeHighRiskTool(t *testing.T) {
	client := &fakeToolClient{responses: []llm.ToolChatResponse{
		{
			Message: llm.ChatMessage{
				Role: conversation.RoleAssistant,
				ToolCalls: []llm.ToolCall{{
					ID:        "call_approval",
					Name:      "dangerous_tool",
					Arguments: json.RawMessage(`{"ok":true}`),
				}},
			},
		},
	}}
	tool := &fakeRuntimeTool{
		name:     "dangerous_tool",
		output:   "should not execute",
		metadata: toolruntime.ToolMetadata{RiskLevel: toolruntime.RiskHigh, SideEffect: toolruntime.SideEffectExternalAction},
	}
	runner := &Runner{LLM: client, ProviderID: 2, ModelName: "test-model"}
	result, err := runner.Run(context.Background(), RunRequest{
		OwnerID:       1,
		RunID:         2,
		NodeID:        "agent",
		Model:         "test-model",
		Task:          "call tool",
		MaxIterations: 4,
		MaxToolCalls:  2,
		ToolPolicy:    ToolPolicy{RequireApprovalForRisk: []string{toolruntime.RiskHigh}},
		Tools:         []toolruntime.RuntimeTool{tool},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.StopReason != StopReasonWaitingHuman || result.Approval == nil {
		t.Fatalf("expected waiting approval result, got %+v", result)
	}
	if result.Checkpoint == nil || len(result.Checkpoint.Messages) == 0 || result.Checkpoint.PendingToolCall == nil {
		t.Fatalf("expected full checkpoint with pending tool call, got %+v", result.Checkpoint)
	}
	if result.Checkpoint.MessagesSummary == "" || result.Checkpoint.PendingToolCall.Name != "dangerous_tool" {
		t.Fatalf("unexpected checkpoint: %+v", result.Checkpoint)
	}
	if result.Checkpoint.Metadata["iteration"] != 1 || result.Checkpoint.Metadata["tool_calls"] != 0 {
		t.Fatalf("checkpoint should preserve counters, got %+v", result.Checkpoint.Metadata)
	}
	if len(tool.input) != 0 {
		t.Fatalf("expected tool not to execute, got input %s", string(tool.input))
	}
}

func TestRunnerCreatesCheckpointWhenContextIsCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	runner := NewRunner(&fakeToolClient{})

	result, err := runner.Run(ctx, RunRequest{
		OwnerID:       1,
		WorkflowID:    2,
		RunID:         3,
		NodeID:        "agent_loop",
		Model:         "test-model",
		Task:          "pause me",
		MaxIterations: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.StopReason != StopReasonCancelled || result.Checkpoint == nil {
		t.Fatalf("expected canceled result with checkpoint, got %+v", result)
	}
	if len(result.Checkpoint.Messages) == 0 || result.Checkpoint.PendingToolCall != nil {
		t.Fatalf("unexpected checkpoint: %+v", result.Checkpoint)
	}
	if result.Checkpoint.Metadata["node_id"] != "agent_loop" {
		t.Fatalf("checkpoint metadata missing node_id: %+v", result.Checkpoint.Metadata)
	}
	if result.Checkpoint.Metadata["iteration"] != 0 || result.Checkpoint.Metadata["tool_calls"] != 0 {
		t.Fatalf("checkpoint should preserve counters, got %+v", result.Checkpoint.Metadata)
	}
}

func TestRunnerStopsAtMaxIterations(t *testing.T) {
	client := &fakeToolClient{responses: []llm.ToolChatResponse{
		{Message: llm.ChatMessage{Role: conversation.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "call_1", Name: "noop", Arguments: json.RawMessage(`{}`)}}}},
		{Message: llm.ChatMessage{Role: conversation.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "call_2", Name: "noop", Arguments: json.RawMessage(`{}`)}}}},
	}}
	tool := &fakeRuntimeTool{name: "noop", output: "ok"}
	runner := NewRunner(client)
	result, err := runner.Run(context.Background(), RunRequest{
		Model:         "test-model",
		Task:          "answer",
		MaxIterations: 2,
		MaxToolCalls:  10,
		Tools:         []toolruntime.RuntimeTool{tool},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.StopReason != StopReasonMaxIterations {
		t.Fatalf("unexpected stop reason: %+v", result)
	}
	if result.Iterations != 2 {
		t.Fatalf("unexpected iterations: %d", result.Iterations)
	}
}

func TestRunnerFeedsUnknownToolErrorBackToModel(t *testing.T) {
	client := &fakeToolClient{responses: []llm.ToolChatResponse{
		{Message: llm.ChatMessage{Role: conversation.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "call_1", Name: "missing", Arguments: json.RawMessage(`{}`)}}}},
		{Message: llm.ChatMessage{Role: conversation.RoleAssistant, Content: "recovered"}},
	}}
	runner := NewRunner(client)
	result, err := runner.Run(context.Background(), RunRequest{
		Model:         "test-model",
		Task:          "answer",
		MaxIterations: 3,
		MaxToolCalls:  3,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.FinalAnswer != "recovered" {
		t.Fatalf("unexpected result: %+v", result)
	}
	second := client.requests[1]
	got := second.Messages[len(second.Messages)-1]
	if got.Role != conversation.RoleTool || got.ToolCallID != "call_1" || got.Content == "" {
		t.Fatalf("missing tool error observation: %+v", second.Messages)
	}
}

func TestRunnerStopsAtMaxToolCalls(t *testing.T) {
	client := &fakeToolClient{responses: []llm.ToolChatResponse{
		{Message: llm.ChatMessage{Role: conversation.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "call_1", Name: "noop", Arguments: json.RawMessage(`{}`)}}}},
		{Message: llm.ChatMessage{Role: conversation.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "call_2", Name: "noop", Arguments: json.RawMessage(`{}`)}}}},
	}}
	tool := &fakeRuntimeTool{name: "noop", output: "ok"}
	runner := NewRunner(client)
	result, err := runner.Run(context.Background(), RunRequest{
		Model:         "test-model",
		Task:          "answer",
		MaxIterations: 8,
		MaxToolCalls:  1,
		Tools:         []toolruntime.RuntimeTool{tool},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.StopReason != StopReasonMaxToolCalls {
		t.Fatalf("unexpected stop reason: %+v", result)
	}
	if result.ToolCalls != 1 {
		t.Fatalf("unexpected tool calls: %d", result.ToolCalls)
	}
}

func TestRunnerRecordsProviderAndModelInSteps(t *testing.T) {
	client := &fakeToolClient{responses: []llm.ToolChatResponse{
		{
			Message: llm.ChatMessage{Role: conversation.RoleAssistant, Content: "simple answer"},
			Usage:   llm.Usage{TotalTokens: 10},
		},
	}}
	runner := &Runner{LLM: client, ProviderID: 42, ModelName: "claude-3"}
	result, err := runner.Run(context.Background(), RunRequest{
		Model:         "claude-3",
		Task:          "hello",
		MaxIterations: 3,
		MaxToolCalls:  3,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.FinalAnswer != "simple answer" {
		t.Fatalf("unexpected answer: %s", result.FinalAnswer)
	}
	for _, step := range result.Steps {
		if step.ProviderID != 42 || step.Model != "claude-3" {
			t.Fatalf("step missing provider/model: %+v", step)
		}
	}
}

func TestRunnerNoToolsDirectAnswer(t *testing.T) {
	client := &fakeToolClient{responses: []llm.ToolChatResponse{
		{Message: llm.ChatMessage{Role: conversation.RoleAssistant, Content: "hello world"}, Usage: llm.Usage{TotalTokens: 5}},
	}}
	runner := NewRunner(client)
	result, err := runner.Run(context.Background(), RunRequest{
		Model:         "test-model",
		Task:          "greet",
		MaxIterations: 3,
		MaxToolCalls:  3,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.FinalAnswer != "hello world" {
		t.Fatalf("unexpected answer: %s", result.FinalAnswer)
	}
	if result.ToolCalls != 0 {
		t.Fatalf("expected zero tool calls, got %d", result.ToolCalls)
	}
}

func TestRunnerMultipleToolsInOneResponse(t *testing.T) {
	client := &fakeToolClient{responses: []llm.ToolChatResponse{
		{
			Message: llm.ChatMessage{
				Role: conversation.RoleAssistant,
				ToolCalls: []llm.ToolCall{
					{ID: "call_1", Name: "tool_a", Arguments: json.RawMessage(`{}`)},
					{ID: "call_2", Name: "tool_b", Arguments: json.RawMessage(`{}`)},
				},
			},
		},
		{Message: llm.ChatMessage{Role: conversation.RoleAssistant, Content: "done"}},
	}}
	toolA := &fakeRuntimeTool{name: "tool_a", output: "result_a"}
	toolB := &fakeRuntimeTool{name: "tool_b", output: "result_b"}
	runner := NewRunner(client)
	result, err := runner.Run(context.Background(), RunRequest{
		Model:         "test-model",
		Task:          "multi",
		MaxIterations: 3,
		MaxToolCalls:  5,
		Tools:         []toolruntime.RuntimeTool{toolA, toolB},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.ToolCalls != 2 {
		t.Fatalf("expected 2 tool calls, got %d", result.ToolCalls)
	}
}

func TestRunnerCompactsToolObservationByMetadataLimit(t *testing.T) {
	client := &fakeToolClient{responses: []llm.ToolChatResponse{
		{Message: llm.ChatMessage{Role: conversation.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "call_1", Name: "large_tool", Arguments: json.RawMessage(`{}`)}}}},
		{Message: llm.ChatMessage{Role: conversation.RoleAssistant, Content: "done"}},
	}}
	tool := &fakeRuntimeTool{
		name:     "large_tool",
		output:   "abcdefghijklmnopqrstuvwxyz",
		metadata: toolruntime.ToolMetadata{MaxOutputBytes: 8},
	}
	runner := NewRunner(client)
	result, err := runner.Run(context.Background(), RunRequest{
		Model:         "test-model",
		Task:          "compact",
		MaxIterations: 3,
		MaxToolCalls:  2,
		Tools:         []toolruntime.RuntimeTool{tool},
	})
	if err != nil {
		t.Fatal(err)
	}
	second := client.requests[1]
	got := second.Messages[len(second.Messages)-1]
	if !strings.Contains(got.Content, "[truncated]") || strings.Contains(got.Content, "ijklmnopqrstuvwxyz") {
		t.Fatalf("expected compacted tool observation, got %q", got.Content)
	}
	foundToolStep := false
	for _, step := range result.Steps {
		if step.Type == StepTypeToolResult {
			foundToolStep = true
			if !strings.Contains(step.Content, "[truncated]") {
				t.Fatalf("expected compacted tool result step, got %+v", step)
			}
			if !step.Compressed {
				t.Fatalf("expected compacted tool result step to be marked compressed: %+v", step)
			}
		}
	}
	if !foundToolStep {
		t.Fatalf("expected tool result step, got %+v", result.Steps)
	}
}

func TestRunnerAppliesPolicyOutputLimit(t *testing.T) {
	client := &fakeToolClient{responses: []llm.ToolChatResponse{
		{Message: llm.ChatMessage{Role: conversation.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "call_1", Name: "large_tool", Arguments: json.RawMessage(`{}`)}}}},
		{Message: llm.ChatMessage{Role: conversation.RoleAssistant, Content: "done"}},
	}}
	tool := &fakeRuntimeTool{
		name:     "large_tool",
		output:   "abcdefghijklmnopqrstuvwxyz",
		metadata: toolruntime.ToolMetadata{MaxOutputBytes: 128},
	}
	runner := NewRunner(client)
	_, err := runner.Run(context.Background(), RunRequest{
		Model:         "test-model",
		Task:          "compact",
		MaxIterations: 3,
		MaxToolCalls:  2,
		ToolPolicy:    ToolPolicy{MaxToolOutputBytes: 6},
		Tools:         []toolruntime.RuntimeTool{tool},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := client.requests[1].Messages[len(client.requests[1].Messages)-1]
	if !strings.HasPrefix(got.Content, "abcdef") || !strings.Contains(got.Content, "[truncated]") {
		t.Fatalf("expected policy output limit to compact observation, got %q", got.Content)
	}
}

func TestRunnerRecordsDefaultToolHookTrace(t *testing.T) {
	client := &fakeToolClient{responses: []llm.ToolChatResponse{
		{Message: llm.ChatMessage{Role: conversation.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "call_1", Name: "trace_tool", Arguments: json.RawMessage(`{}`)}}}},
		{Message: llm.ChatMessage{Role: conversation.RoleAssistant, Content: "done"}},
	}}
	tool := &fakeRuntimeTool{name: "trace_tool", output: "ok"}
	runner := NewRunner(client)
	result, err := runner.Run(context.Background(), RunRequest{
		Model:         "test-model",
		Task:          "trace hooks",
		MaxIterations: 3,
		MaxToolCalls:  2,
		Tools:         []toolruntime.RuntimeTool{tool},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.HookTrace) < 2 {
		t.Fatalf("expected pre and post tool hook trace, got %+v", result.HookTrace)
	}
	if result.HookTrace[0].ToolName != "trace_tool" {
		t.Fatalf("expected hook trace to include tool name, got %+v", result.HookTrace)
	}
}

func TestRunnerRedactsSensitiveToolObservationFields(t *testing.T) {
	client := &fakeToolClient{responses: []llm.ToolChatResponse{
		{Message: llm.ChatMessage{Role: conversation.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "call_1", Name: "secret_tool", Arguments: json.RawMessage(`{}`)}}}},
		{Message: llm.ChatMessage{Role: conversation.RoleAssistant, Content: "done"}},
	}}
	tool := &fakeRuntimeTool{
		name:       "secret_tool",
		outputJSON: json.RawMessage(`{"api_key":"secret-key","password":"hidden","value":"safe"}`),
	}
	runner := NewRunner(client)
	result, err := runner.Run(context.Background(), RunRequest{
		Model:         "test-model",
		Task:          "redact",
		MaxIterations: 3,
		MaxToolCalls:  2,
		Tools:         []toolruntime.RuntimeTool{tool},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := client.requests[1].Messages[len(client.requests[1].Messages)-1].Content
	if strings.Contains(got, "secret-key") || strings.Contains(got, "hidden") || !strings.Contains(got, "[REDACTED]") {
		t.Fatalf("expected redacted tool observation in context, got %q", got)
	}
	for _, step := range result.Steps {
		if step.Type != StepTypeToolResult {
			continue
		}
		if strings.Contains(string(step.OutputJSON), "secret-key") || strings.Contains(string(step.OutputJSON), "hidden") {
			t.Fatalf("expected redacted tool output step, got %s", string(step.OutputJSON))
		}
	}
}

func TestRunnerRejectsToolHostOutsidePolicyAllowlist(t *testing.T) {
	client := &fakeToolClient{responses: []llm.ToolChatResponse{
		{Message: llm.ChatMessage{Role: conversation.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "call_1", Name: "http_tool", Arguments: json.RawMessage(`{}`)}}}},
		{Message: llm.ChatMessage{Role: conversation.RoleAssistant, Content: "recovered"}},
	}}
	tool := &fakeRuntimeTool{
		name:     "http_tool",
		output:   "should not run",
		metadata: toolruntime.ToolMetadata{AllowedHosts: []string{"api.blocked.test"}},
	}
	runner := NewRunner(client)
	result, err := runner.Run(context.Background(), RunRequest{
		Model:         "test-model",
		Task:          "call http",
		MaxIterations: 3,
		MaxToolCalls:  2,
		ToolPolicy:    ToolPolicy{AllowedHosts: []string{"api.allowed.test"}},
		Tools:         []toolruntime.RuntimeTool{tool},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(tool.input) != 0 {
		t.Fatalf("tool should not execute when host is blocked, input=%s", tool.input)
	}
	if result.FinalAnswer != "recovered" {
		t.Fatalf("expected model to recover after policy observation, got %+v", result)
	}
	second := client.requests[1]
	got := second.Messages[len(second.Messages)-1]
	if got.Role != conversation.RoleTool || !strings.Contains(got.Content, "not allowed") {
		t.Fatalf("expected policy violation observation, got %+v", got)
	}
}

func TestRunnerBlocksDangerousSandboxCommandBeforeExecution(t *testing.T) {
	client := &fakeToolClient{responses: []llm.ToolChatResponse{
		{Message: llm.ChatMessage{Role: conversation.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "call_1", Name: "execute_python", Arguments: json.RawMessage(`{"code":"import os\nos.system('rm -rf /')"}`)}}}},
		{Message: llm.ChatMessage{Role: conversation.RoleAssistant, Content: "blocked and recovered"}},
	}}
	tool := &fakeRuntimeTool{
		name:   "execute_python",
		output: "should not run",
	}
	runner := NewRunner(client)
	result, err := runner.Run(context.Background(), RunRequest{
		Model:         "test-model",
		Task:          "run unsafe code",
		MaxIterations: 3,
		MaxToolCalls:  2,
		Tools:         []toolruntime.RuntimeTool{tool},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(tool.input) != 0 {
		t.Fatalf("dangerous sandbox command should not execute, input=%s", tool.input)
	}
	if result.ToolCalls != 0 {
		t.Fatalf("blocked tool should not increment tool calls, got %d", result.ToolCalls)
	}
	if result.FinalAnswer != "blocked and recovered" {
		t.Fatalf("expected model to recover after blocked tool, got %+v", result)
	}
	got := client.requests[1].Messages[len(client.requests[1].Messages)-1]
	if got.Role != conversation.RoleTool || !strings.Contains(got.Content, "dangerous tool invocation blocked") {
		t.Fatalf("expected blocked observation, got %+v", got)
	}
}

func TestRunnerAppliesPolicyTimeout(t *testing.T) {
	client := &fakeToolClient{responses: []llm.ToolChatResponse{
		{Message: llm.ChatMessage{Role: conversation.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "call_1", Name: "timed_tool", Arguments: json.RawMessage(`{}`)}}}},
		{Message: llm.ChatMessage{Role: conversation.RoleAssistant, Content: "done"}},
	}}
	tool := &fakeRuntimeTool{name: "timed_tool", output: "ok"}
	runner := NewRunner(client)
	_, err := runner.Run(context.Background(), RunRequest{
		Model:         "test-model",
		Task:          "timeout",
		MaxIterations: 3,
		MaxToolCalls:  2,
		ToolPolicy:    ToolPolicy{MaxToolTimeoutMS: 10},
		Tools:         []toolruntime.RuntimeTool{tool},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !tool.sawDeadline {
		t.Fatal("expected tool context to carry policy timeout deadline")
	}
}

func TestCompactStepsMarksCompressed(t *testing.T) {
	steps := CompactSteps([]RunStep{{Type: StepTypeToolResult, Content: "abcdefghijklmnopqrstuvwxyz"}}, 8)
	if len(steps) != 1 || !steps[0].Compressed || !strings.Contains(steps[0].Content, "[truncated]") {
		t.Fatalf("expected compacted step to be marked compressed, got %+v", steps)
	}
}

func TestContextAssemblerProducesContextTrace(t *testing.T) {
	client := &fakeToolClient{responses: []llm.ToolChatResponse{
		{Message: llm.ChatMessage{Role: conversation.RoleAssistant, Content: "done"}},
	}}
	runner := NewRunner(client)
	result, err := runner.Run(context.Background(), RunRequest{
		Model:         "test-model",
		SystemPrompt:  "You are a helpful assistant",
		Mode:          "react",
		Task:          "hello",
		MaxInputChars: 2000,
		MaxIterations: 2,
		MaxToolCalls:  2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Context.MaxChars != 2000 {
		t.Fatalf("expected max chars in context trace, got %+v", result.Context)
	}
	if result.Context.Strategy == "" {
		t.Fatal("expected strategy in context trace")
	}
	if len(result.Context.Included) == 0 {
		t.Fatal("expected included blocks in context trace")
	}
	if result.Context.EstimatedTokens == 0 || len(result.Context.Blocks) == 0 {
		t.Fatalf("expected block-level token audit in context trace, got %+v", result.Context)
	}
	if result.Context.TokenAudit.Total != result.Context.EstimatedTokens || result.Context.TokenAudit.System == 0 || result.Context.TokenAudit.Task == 0 {
		t.Fatalf("expected token audit in context trace, got %+v", result.Context.TokenAudit)
	}
}

func TestRunnerRecordsPlanAndMarksPlanCompleted(t *testing.T) {
	client := &fakeToolClient{responses: []llm.ToolChatResponse{
		{Message: llm.ChatMessage{Role: conversation.RoleAssistant, Content: "planned answer"}},
	}}
	plan := &Plan{Steps: []PlanStep{
		{Number: 1, Description: "inspect", Status: "pending"},
		{Number: 2, Description: "answer", Status: "pending"},
	}}
	runner := NewRunner(client)
	result, err := runner.Run(context.Background(), RunRequest{
		Model:         "test-model",
		Mode:          "plan_execute",
		Plan:          plan,
		Task:          "answer with a plan",
		MaxIterations: 2,
		MaxToolCalls:  2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.StopReason != StopReasonPlanCompleted || result.Plan == nil || !result.Plan.Finished {
		t.Fatalf("expected completed plan result, got %+v", result)
	}
	if result.Plan.Steps[0].Status != "completed" || result.Plan.Steps[1].Status != "completed" {
		t.Fatalf("expected plan steps completed, got %+v", result.Plan.Steps)
	}
	if len(result.Steps) == 0 || result.Steps[0].Type != StepTypePlan {
		t.Fatalf("expected first step to be plan, got %+v", result.Steps)
	}
	if len(client.requests[0].Messages) < 2 {
		t.Fatalf("expected plan context in messages, got %+v", client.requests[0].Messages)
	}
	foundPlan := false
	for _, message := range client.requests[0].Messages {
		if message.Role == conversation.RoleSystem && strings.Contains(message.Content, "Execution Plan") {
			foundPlan = true
		}
	}
	if !foundPlan {
		t.Fatalf("plan context was not added to messages: %+v", client.requests[0].Messages)
	}
}

func TestPlanExecuteModeInstruction(t *testing.T) {
	req := RunRequest{Mode: "plan_execute"}
	instr := modeInstruction(req)
	if instr == "" {
		t.Fatal("expected plan_execute instruction")
	}
}

func TestReflectModeInstruction(t *testing.T) {
	req := RunRequest{Mode: "reflect"}
	instr := modeInstruction(req)
	if instr == "" {
		t.Fatal("expected reflect instruction")
	}
}

func TestSupervisorModeInstruction(t *testing.T) {
	req := RunRequest{Mode: "supervisor"}
	instr := modeInstruction(req)
	if instr == "" {
		t.Fatal("expected supervisor instruction")
	}
}

func TestReactModeNoExtraInstruction(t *testing.T) {
	req := RunRequest{Mode: "react"}
	instr := modeInstruction(req)
	if instr != "" {
		t.Fatalf("expected no instruction for plain react mode, got %q", instr)
	}
}

func TestReactWithReflectionInstruction(t *testing.T) {
	req := RunRequest{Mode: "react", ReflectionEnabled: true}
	instr := modeInstruction(req)
	if instr == "" {
		t.Fatal("expected reflection instruction for react+reflection mode")
	}
}

func TestStepTypeConstants(t *testing.T) {
	expected := map[string]string{
		"llm_response":      StepTypeLLMResponse,
		"plan":              StepTypePlan,
		"tool_call":         StepTypeToolCall,
		"tool_result":       StepTypeToolResult,
		"approval_required": StepTypeApproval,
		"reflection":        StepTypeReflection,
		"final_answer":      StepTypeFinalAnswer,
		"error":             StepTypeError,
	}
	for want, got := range expected {
		if got != want {
			t.Fatalf("step type mismatch: want %q got %q", want, got)
		}
	}
}

func TestStopReasonConstants(t *testing.T) {
	reasons := map[string]bool{
		StopReasonFinalAnswer:      true,
		StopReasonMaxIterations:    true,
		StopReasonMaxToolCalls:     true,
		StopReasonTimeout:          true,
		StopReasonCancelled:        true,
		StopReasonWaitingHuman:     true,
		StopReasonLLMError:         true,
		StopReasonToolNameNotFound: true,
		StopReasonPlanCompleted:    true,
		StopReasonReflectionFailed: true,
	}
	if len(reasons) != 10 {
		t.Fatalf("expected 10 stop reasons, got %d", len(reasons))
	}
}
