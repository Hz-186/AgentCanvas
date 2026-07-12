package agent

import (
	"context"
	"encoding/json"
	"testing"

	"agentcanvas/internal/domain/conversation"
	domainagent "agentcanvas/internal/domain/workflow"
	"agentcanvas/internal/infrastructure/llm"
	"agentcanvas/internal/runtime/harness/rules"
	"agentcanvas/internal/runtime/toolruntime"
)

func TestCheckpointFromJSON(t *testing.T) {
	data := json.RawMessage(`{"messages":[{"role":"assistant","content":"ok"}]}`)
	cp, err := CheckpointFromJSON(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(cp.Messages) != 1 || cp.Messages[0].Role != "assistant" {
		t.Fatalf("unexpected checkpoint: %+v", cp)
	}
}

func TestBuildResumeRequestApproved(t *testing.T) {
	cp := &Checkpoint{
		Messages: []llm.ChatMessage{
			{Role: conversation.RoleSystem, Content: "You are an agent"},
			{Role: conversation.RoleUser, Content: "do task"},
			{Role: conversation.RoleAssistant, Content: "", ToolCalls: []llm.ToolCall{{ID: "call_1", Name: "search", Arguments: json.RawMessage(`{"q":"x"}`)}}},
		},
		PendingToolCall: &llm.ToolCall{ID: "call_1", Name: "search", Arguments: json.RawMessage(`{"q":"x"}`)},
		ToolPolicy:      ToolPolicy{},
		ToolNames:       []string{"search"},
		Metadata:        map[string]any{"iteration": 1.0, "tool_calls": 0.0},
	}
	req := ResumeRequest{
		RunRequest: RunRequest{
			OwnerID:       1,
			WorkflowID:    2,
			RunID:         3,
			NodeID:        "agent",
			Model:         "gpt-4",
			Mode:          "react",
			MaxIterations: 5,
			MaxToolCalls:  10,
		},
		Approved:   true,
		Checkpoint: cp,
	}
	runReq, err := BuildResumeRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	if len(runReq.ResumeMessages) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(runReq.ResumeMessages))
	}
	if runReq.ResumeIteration != 1 {
		t.Fatalf("expected iteration 1, got %d", runReq.ResumeIteration)
	}
	if len(runReq.ResumeApprovedToolCallIDs) != 1 || runReq.ResumeApprovedToolCallIDs[0] != "call_1" {
		t.Fatalf("approved resume must identify the approved call: %+v", runReq.ResumeApprovedToolCallIDs)
	}
}

func TestBuildResumeRequestRejected(t *testing.T) {
	cp := &Checkpoint{
		Messages: []llm.ChatMessage{
			{Role: conversation.RoleUser, Content: "do task"},
			{Role: conversation.RoleAssistant, Content: "", ToolCalls: []llm.ToolCall{{ID: "call_1", Name: "danger", Arguments: json.RawMessage(`{}`)}}},
		},
		PendingToolCall: &llm.ToolCall{ID: "call_1", Name: "danger", Arguments: json.RawMessage(`{}`)},
	}
	req := ResumeRequest{
		RunRequest: RunRequest{
			OwnerID:       1,
			WorkflowID:    2,
			RunID:         3,
			NodeID:        "agent",
			Model:         "gpt-4",
			MaxIterations: 5,
			MaxToolCalls:  10,
		},
		Approved:      false,
		RejectionNote: "not allowed",
		Checkpoint:    cp,
	}
	runReq, err := BuildResumeRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	lastMsg := runReq.ResumeMessages[len(runReq.ResumeMessages)-1]
	if lastMsg.Role != conversation.RoleTool {
		t.Fatalf("expected tool role, got %s", lastMsg.Role)
	}
	if lastMsg.Content != "Human rejected: not allowed" {
		t.Fatalf("expected rejection message, got %s", lastMsg.Content)
	}
}

func TestBuildResumeRequestUsesCheckpointRuleSnapshot(t *testing.T) {
	checkpoint := &Checkpoint{
		Messages:       []llm.ChatMessage{{Role: conversation.RoleSystem, Content: "system"}},
		Metadata:       map[string]any{},
		RuleSetVersion: "release-2026-07",
		CustomRules:    []rules.Rule{{ID: "tenant.release.check", Level: rules.LevelL2Scenario, Content: "check rollback"}},
	}
	resumed, err := BuildResumeRequest(ResumeRequest{RunRequest: RunRequest{RuleSetVersion: "current", CustomRules: []rules.Rule{{ID: "new", Content: "new"}}}, Checkpoint: checkpoint})
	if err != nil {
		t.Fatal(err)
	}
	if resumed.RuleSetVersion != "release-2026-07" || len(resumed.CustomRules) != 1 || resumed.CustomRules[0].ID != "tenant.release.check" {
		t.Fatalf("expected checkpoint rule snapshot, got %+v", resumed)
	}
}

func TestFindUnresolvedToolCalls(t *testing.T) {
	toolByName := map[string]toolruntime.RuntimeTool{
		"search": &fakeResumeTool{name: "search"},
	}
	messages := []llm.ChatMessage{
		{Role: conversation.RoleUser, Content: "find info"},
		{Role: conversation.RoleAssistant, Content: "", ToolCalls: []llm.ToolCall{
			{ID: "call_1", Name: "search", Arguments: json.RawMessage(`{}`)},
			{ID: "call_2", Name: "search", Arguments: json.RawMessage(`{}`)},
		}},
	}
	unresolved := findUnresolvedToolCalls(messages, toolByName)
	if len(unresolved) != 2 {
		t.Fatalf("expected 2 unresolved, got %d", len(unresolved))
	}
	messages = append(messages, llm.ChatMessage{Role: conversation.RoleTool, ToolCallID: "call_1", Content: "result"})
	unresolved = findUnresolvedToolCalls(messages, toolByName)
	if len(unresolved) != 1 {
		t.Fatalf("expected 1 unresolved after result, got %d", len(unresolved))
	}
}

type fakeResumeTool struct {
	name string
}

func (t *fakeResumeTool) Name() string                { return t.name }
func (t *fakeResumeTool) Description() string         { return "resume tool" }
func (t *fakeResumeTool) Parameters() json.RawMessage { return json.RawMessage(`{}`) }
func (t *fakeResumeTool) Execute(ctx context.Context, rc toolruntime.ToolRunContext, input json.RawMessage) (*toolruntime.ToolResult, error) {
	return &toolruntime.ToolResult{ContentText: "ok"}, nil
}

func TestRunnerResumeExecutesPendingTool(t *testing.T) {
	client := &fakeToolClient{responses: []llm.ToolChatResponse{
		{Message: llm.ChatMessage{Role: conversation.RoleAssistant, Content: "final after resume"}, Usage: llm.Usage{TotalTokens: 5}},
	}}
	tool := &fakeRuntimeTool{name: "search", output: "resume result"}
	tools := []toolruntime.RuntimeTool{tool}
	toolByName := map[string]toolruntime.RuntimeTool{"search": tool}

	runner := &Runner{LLM: client, ProviderID: 1, ModelName: "gpt-4"}
	msgs := []llm.ChatMessage{
		{Role: conversation.RoleSystem, Content: "agent"},
		{Role: conversation.RoleUser, Content: "task"},
		{Role: conversation.RoleAssistant, Content: "", ToolCalls: []llm.ToolCall{{ID: "call_1", Name: "search", Arguments: json.RawMessage(`{}`)}}},
	}
	_ = toolByName

	result, err := runner.Run(context.Background(), RunRequest{
		OwnerID:         1,
		RunID:           5,
		NodeID:          "agent",
		Model:           "gpt-4",
		Task:            "task",
		MaxIterations:   5,
		MaxToolCalls:    10,
		Tools:           tools,
		ResumeMessages:  msgs,
		ResumeIteration: 1,
		ResumeToolCalls: 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.FinalAnswer != "final after resume" {
		t.Fatalf("expected 'final after resume', got %s", result.FinalAnswer)
	}
	if result.ToolCalls < 1 {
		t.Fatalf("expected at least 1 tool call after resume, got %d", result.ToolCalls)
	}
}

func TestAggregateEvalMetrics(t *testing.T) {
	results := []domainagent.EvalResult{
		{Score: 0.8, LatencyMS: 100, MetricsJSON: json.RawMessage(`{"tool_calls":2,"total_tokens":50,"tool_success":true}`)},
		{Score: 0.3, LatencyMS: 200, MetricsJSON: json.RawMessage(`{"tool_calls":1,"total_tokens":30,"tool_success":false,"max_iter_exceeded":true}`)},
		{Score: 0.9, LatencyMS: 150, MetricsJSON: json.RawMessage(`{"tool_calls":3,"total_tokens":70,"tool_success":true,"json_compliant":true}`)},
	}
	m := AggregateEvalMetrics(results)
	if m.TotalCases != 3 {
		t.Fatalf("expected 3 cases, got %d", m.TotalCases)
	}
	if m.Passed != 2 {
		t.Fatalf("expected 2 passed, got %d", m.Passed)
	}
	if m.AvgLatencyMS != 150 {
		t.Fatalf("expected avg latency 150, got %d", m.AvgLatencyMS)
	}
	if m.ToolSuccessRate != 2.0/3.0 {
		t.Fatalf("expected tool success rate 0.66, got %.2f", m.ToolSuccessRate)
	}
}

func TestAggregateEvalMetricsEmpty(t *testing.T) {
	m := AggregateEvalMetrics(nil)
	if m.TotalCases != 0 {
		t.Fatalf("expected 0 cases, got %d", m.TotalCases)
	}
}
