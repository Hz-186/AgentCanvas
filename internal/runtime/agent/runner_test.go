package agent

import (
	"context"
	"encoding/json"
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
	name   string
	output string
	input  json.RawMessage
}

func (t *fakeRuntimeTool) Name() string { return t.name }

func (t *fakeRuntimeTool) Description() string { return "fake tool" }

func (t *fakeRuntimeTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"}}}`)
}

func (t *fakeRuntimeTool) Execute(ctx context.Context, rc toolruntime.ToolRunContext, input json.RawMessage) (*toolruntime.ToolResult, error) {
	t.input = input
	return &toolruntime.ToolResult{ContentText: t.output, ContentJSON: json.RawMessage(`{"ok":true}`)}, nil
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
	runner := NewRunner(client)
	result, err := runner.Run(context.Background(), RunRequest{
		OwnerID:       1,
		RunID:         2,
		NodeID:        "agent",
		Model:         "test-model",
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
