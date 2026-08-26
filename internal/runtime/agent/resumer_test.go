package agent

import (
	"context"
	"encoding/json"
	"testing"

	"agentcanvas/internal/domain/conversation"
	"agentcanvas/internal/domain/reflection"
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
			AgentID:       2,
			RunID:         3,
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

func TestBuildResumeRequestRejectsUnknownInteractionOption(t *testing.T) {
	cp := &Checkpoint{
		Messages:        []llm.ChatMessage{{Role: conversation.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "call_1", Name: "write_memory", Arguments: json.RawMessage(`{}`)}}}},
		PendingToolCall: &llm.ToolCall{ID: "call_1", Name: "write_memory", Arguments: json.RawMessage(`{}`)},
		Interaction:     &Interaction{ID: "interaction-1", ToolCallID: "call_1", Options: []toolruntime.ApprovalOption{{ID: "keep_both", Label: "Keep both"}}},
		Metadata:        map[string]any{},
	}
	_, err := BuildResumeRequest(ResumeRequest{RunRequest: RunRequest{Model: "m", Task: "save"}, Checkpoint: cp, Approved: true, RejectionNote: "choice:replace"})
	if err == nil {
		t.Fatal("expected unknown interaction option to be rejected")
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
			AgentID:       2,
			RunID:         3,
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

func TestBuildResumeRequestValidatesUserInputAnswers(t *testing.T) {
	cp := &Checkpoint{
		Messages:        []llm.ChatMessage{{Role: conversation.RoleAssistant}},
		PendingToolCall: &llm.ToolCall{ID: "ask", Name: "request_user_input"},
		Interaction: &Interaction{Kind: "request_user_input", Questions: []toolruntime.UserInputQuestion{{
			ID: "scope", Options: []toolruntime.UserInputOption{{Label: "all", Description: "everything"}},
		}}},
		Metadata: map[string]any{},
	}
	if _, err := BuildResumeRequest(ResumeRequest{RunRequest: RunRequest{Model: "m", Task: "continue"}, Checkpoint: cp, Approved: true, RejectionNote: `answers:{"scope":"unknown"}`}); err == nil {
		t.Fatal("unknown answer option must be rejected")
	}
	resumed, err := BuildResumeRequest(ResumeRequest{RunRequest: RunRequest{Model: "m", Task: "continue"}, Checkpoint: cp, Approved: true, RejectionNote: `answers:{"scope":"all"}`})
	if err != nil || len(resumed.ResumeMessages) != 2 || resumed.ResumeMessages[1].Content != `{"answers":{"scope":"all"}}` {
		t.Fatalf("valid answers were not appended: resumed=%+v err=%v", resumed, err)
	}
}

func TestBuildResumeRequestUsesCheckpointRuleSnapshot(t *testing.T) {
	checkpoint := &Checkpoint{
		Messages: []llm.ChatMessage{{Role: conversation.RoleSystem, Content: "system"}},
		Metadata: map[string]any{},
		Rules:    []rules.Rule{{ID: "tenant.release.check", Strength: rules.RuleOptional, Content: "check rollback", Activation: rules.Activation{Always: true}}},
	}
	resumed, err := BuildResumeRequest(ResumeRequest{RunRequest: RunRequest{Rules: []rules.Rule{{ID: "new", Strength: rules.RuleMandatory, Content: "new"}}}, Checkpoint: checkpoint})
	if err != nil {
		t.Fatal(err)
	}
	if len(resumed.Rules) != 1 || resumed.Rules[0].ID != "tenant.release.check" {
		t.Fatalf("expected checkpoint rule snapshot, got %+v", resumed)
	}
}

func TestBuildResumeRequestUsesCheckpointReflectionSnapshot(t *testing.T) {
	checkpointPolicy := reflection.DefaultPolicy()
	checkpointPolicy.RuntimeMode = reflection.RuntimeShadow
	checkpoint := &Checkpoint{
		Messages:              []llm.ChatMessage{{Role: conversation.RoleSystem, Content: "system"}},
		Metadata:              map[string]any{},
		ReflectionPolicy:      checkpointPolicy,
		RecalledReflectionIDs: []int64{7, 8},
	}
	currentPolicy := reflection.DefaultPolicy()
	resumed, err := BuildResumeRequest(ResumeRequest{RunRequest: RunRequest{
		ReflectionPolicy: currentPolicy, RecalledReflectionIDs: []int64{99},
	}, Checkpoint: checkpoint})
	if err != nil {
		t.Fatal(err)
	}
	if resumed.ReflectionPolicy.RuntimeMode != reflection.RuntimeShadow || len(resumed.RecalledReflectionIDs) != 2 || resumed.RecalledReflectionIDs[0] != 7 {
		t.Fatalf("reflection snapshot drifted during resume: %+v", resumed)
	}
}

func TestCheckpointCapturesReflectionState(t *testing.T) {
	policy := reflection.DefaultPolicy()
	checkpoint := checkpointFromMessages(RunRequest{ReflectionPolicy: policy, RecalledReflectionIDs: []int64{4, 5}}, nil,
		ContextTrace{}, nil, nil, StopReasonPaused, 1, 0)
	if checkpoint.ReflectionPolicy.RuntimeMode != reflection.RuntimeActive || len(checkpoint.RecalledReflectionIDs) != 2 {
		t.Fatalf("checkpoint did not capture reflection state: %+v", checkpoint)
	}
}

func TestBuildResumeRequestRejectsTamperedRuleSnapshot(t *testing.T) {
	snapshot, err := rules.NewSnapshot([]rules.Rule{{
		ID: "tenant.audit", Content: "audit", Strength: rules.RuleMandatory,
	}})
	if err != nil {
		t.Fatal(err)
	}
	checkpoint := &Checkpoint{
		Messages: []llm.ChatMessage{{Role: conversation.RoleSystem, Content: "system"}},
		RuleHash: snapshot.Hash,
		Rules:    append([]rules.Rule(nil), snapshot.Rules...),
	}
	checkpoint.Rules[0].Content = "tampered"
	if _, err := BuildResumeRequest(ResumeRequest{Checkpoint: checkpoint}); err == nil {
		t.Fatal("expected tampered checkpoint rule snapshot to be rejected")
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

func TestRunnerResumePlanModeUsesNormalToolRegistration(t *testing.T) {
	client := &fakeToolClient{responses: []llm.ToolChatResponse{
		{Message: llm.ChatMessage{Role: conversation.RoleAssistant, Content: "safe plan"}},
	}}
	tool := &fakeRuntimeTool{name: "write_file", metadata: toolruntime.ToolMetadata{SideEffect: toolruntime.SideEffectWrite}}
	messages := []llm.ChatMessage{
		{Role: conversation.RoleSystem, Content: "agent"},
		{Role: conversation.RoleUser, Content: "task"},
		{Role: conversation.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "write", Name: "write_file", Arguments: json.RawMessage(`{}`)}}},
	}
	result, err := NewRunner(client).Run(context.Background(), RunRequest{
		Model: "m", Mode: "plan", Task: "task", MaxIterations: 3, MaxToolCalls: 3,
		Tools: []toolruntime.RuntimeTool{tool}, ResumeMessages: messages, ResumeIteration: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if tool.input == nil {
		t.Fatalf("resumed Plan Run did not use normal tool registration: input=%s result=%+v", tool.input, result)
	}
}
