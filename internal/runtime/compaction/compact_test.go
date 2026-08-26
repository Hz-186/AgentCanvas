package compaction

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"agentcanvas/internal/domain/conversation"
	"agentcanvas/internal/infrastructure/llm"
)

type fakeChatClient struct {
	mu        sync.Mutex
	calls     []llm.ChatRequest
	responses []fakeResponse
}

type fakeResponse struct {
	content string
	err     error
}

func (f *fakeChatClient) Chat(ctx context.Context, cfg llm.ChatProviderConfig, req llm.ChatRequest) (*llm.ChatResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, req)
	index := len(f.calls) - 1
	if index < len(f.responses) {
		if f.responses[index].err != nil {
			return nil, f.responses[index].err
		}
		return &llm.ChatResponse{Content: f.responses[index].content}, nil
	}
	return &llm.ChatResponse{Content: "summary"}, nil
}

func (f *fakeChatClient) StreamChat(ctx context.Context, cfg llm.ChatProviderConfig, req llm.ChatRequest, onEvent func(llm.StreamEvent) error) error {
	return nil
}

func (f *fakeChatClient) requestCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *fakeChatClient) request(i int) llm.ChatRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[i]
}

func textEntry(role, content string) Entry {
	return Entry{Role: role, ContentType: conversation.ContentTypeText, Content: content}
}

func callEntry(id, name string, args string) Entry {
	return Entry{Role: conversation.RoleAssistant, ContentType: conversation.ContentTypeFunctionCall, ToolCallID: id, ToolName: name, Arguments: json.RawMessage(args)}
}

func outputEntry(id, name, content string) Entry {
	return Entry{Role: conversation.RoleTool, ContentType: conversation.ContentTypeFunctionCallOutput, ToolCallID: id, ToolName: name, Content: content}
}

func userEntry(content string) Entry {
	return textEntry(conversation.RoleUser, content)
}

var defaultReq = Request{Provider: llm.ChatProviderConfig{ProviderType: "openai"}, Model: "gpt-4"}

func TestCompactSummarizesAllEntryTypes(t *testing.T) {
	client := &fakeChatClient{}
	entries := []Entry{
		userEntry("hello"),
		textEntry(conversation.RoleAssistant, "hi"),
		callEntry("c1", "search", `{"q":"x"}`),
		callEntry("c2", "read", `{"path":"y"}`),
		outputEntry("c1", "search", "result-one"),
		outputEntry("c2", "read", "result-two"),
	}
	result, err := Compact(context.Background(), client, defaultReq, entries)
	if err != nil {
		t.Fatalf("Compact returned error: %v", err)
	}
	if result.ModelCalls != 1 {
		t.Fatalf("expected 1 model call, got %d", result.ModelCalls)
	}
	req := client.request(0)
	if len(req.Messages) != len(entries)+2 { // system + entries + prompt
		t.Fatalf("expected %d messages, got %d", len(entries)+2, len(req.Messages))
	}
	body := make([]string, 0, len(req.Messages))
	for _, msg := range req.Messages {
		body = append(body, msg.Content)
	}
	joined := strings.Join(body, "\n")
	for _, want := range []string{"hello", "hi", `[tool call: search] {"q":"x"}`, `[tool result: search] result-one`, `[tool result: read] result-two`} {
		if !strings.Contains(joined, want) {
			t.Errorf("summarizer input missing %q", want)
		}
	}
	if result.Summary != "summary" {
		t.Errorf("expected summary content, got %q", result.Summary)
	}
}

func TestCompactRollingCarriesParentSummary(t *testing.T) {
	client := &fakeChatClient{}
	req := defaultReq
	req.ParentSummary = "earlier context"
	_, err := Compact(context.Background(), client, req, []Entry{userEntry("next")})
	if err != nil {
		t.Fatalf("Compact returned error: %v", err)
	}
	got := client.request(0).Messages
	if len(got) < 2 || got[1].Role != conversation.RoleUser || got[1].Content != SummaryPrefix+"earlier context" {
		t.Fatalf("expected parent summary as first user message, got %+v", got)
	}
}

func TestCompactExcludesIgnorableEntries(t *testing.T) {
	client := &fakeChatClient{}
	entries := []Entry{
		{Role: conversation.RoleUser, ContentType: conversation.ContentTypeSystemEcho, Content: "/compact"},
		userEntry("real question"),
		{Role: conversation.RoleAssistant, ContentType: conversation.ContentTypeReasoning, Content: "thinking"},
	}
	_, err := Compact(context.Background(), client, defaultReq, entries)
	if err != nil {
		t.Fatalf("Compact returned error: %v", err)
	}
	joined := strings.Join(msgContents(client.request(0).Messages), "\n")
	if strings.Contains(joined, "/compact") || strings.Contains(joined, "thinking") {
		t.Errorf("ignorable entries leaked into summarizer input: %s", joined)
	}
	if !strings.Contains(joined, "real question") {
		t.Errorf("real question missing from summarizer input")
	}
}

func TestCompactTruncatesOversizedToolOutput(t *testing.T) {
	client := &fakeChatClient{}
	huge := strings.Repeat("a", 200_000) // conservative counter ≈ 50k tokens > 8_000 limit
	entries := []Entry{userEntry("q"), callEntry("c1", "run", "{}"), outputEntry("c1", "run", huge)}
	_, err := Compact(context.Background(), client, defaultReq, entries)
	if err != nil {
		t.Fatalf("Compact returned error: %v", err)
	}
	for _, msg := range client.request(0).Messages {
		if strings.Contains(msg.Content, huge) {
			t.Fatalf("oversized tool output sent untruncated (%d runes)", len(msg.Content))
		}
	}
	found := false
	for _, msg := range client.request(0).Messages {
		if strings.HasPrefix(msg.Content, "[tool result: run]") && len(msg.Content) < len(huge) {
			found = true
		}
	}
	if !found {
		t.Errorf("truncated tool result missing from input")
	}
}

func TestCompactEmptySummaryFallsBack(t *testing.T) {
	client := &fakeChatClient{responses: []fakeResponse{{content: "   "}}}
	result, err := Compact(context.Background(), client, defaultReq, []Entry{userEntry("q")})
	if err != nil {
		t.Fatalf("Compact returned error: %v", err)
	}
	if result.Summary != FallbackSummary {
		t.Fatalf("expected fallback %q, got %q", FallbackSummary, result.Summary)
	}
}

func TestCompactOverflowDropsOldestAndRetries(t *testing.T) {
	client := &fakeChatClient{responses: []fakeResponse{
		{err: llm.ErrContextWindowExceeded},
		{content: "summary"},
	}}
	entries := []Entry{userEntry("first"), userEntry("second"), userEntry("third")}
	result, err := Compact(context.Background(), client, defaultReq, entries)
	if err != nil {
		t.Fatalf("Compact returned error: %v", err)
	}
	if result.ModelCalls != 2 {
		t.Fatalf("expected 2 model calls, got %d", result.ModelCalls)
	}
	joined := strings.Join(msgContents(client.request(1).Messages), "\n")
	if strings.Contains(joined, "first") {
		t.Errorf("second attempt still contains dropped oldest entry")
	}
	if !strings.Contains(joined, "second") || !strings.Contains(joined, "third") {
		t.Errorf("second attempt missing retained entries")
	}
}

func TestCompactOverflowWithSingleEntryFails(t *testing.T) {
	client := &fakeChatClient{responses: []fakeResponse{{err: llm.ErrContextWindowExceeded}}}
	_, err := Compact(context.Background(), client, defaultReq, []Entry{userEntry("only")})
	if !errors.Is(err, ErrCompactionFailed) {
		t.Fatalf("expected ErrCompactionFailed, got %v", err)
	}
	if client.requestCount() != 1 {
		t.Fatalf("expected exactly 1 call, got %d", client.requestCount())
	}
}

func msgContents(messages []llm.ChatMessage) []string {
	out := make([]string, 0, len(messages))
	for _, msg := range messages {
		out = append(out, msg.Content)
	}
	return out
}
