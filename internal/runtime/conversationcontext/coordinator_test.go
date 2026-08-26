package conversationcontext

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"agentcanvas/internal/domain"
	"agentcanvas/internal/domain/conversation"
	"agentcanvas/internal/infrastructure/llm"

	"gorm.io/gorm"
)

type fakeHistory struct{ messages []conversation.Message }

func (f fakeHistory) ListActiveByConversation(context.Context, int64, int64) ([]conversation.Message, error) {
	return append([]conversation.Message(nil), f.messages...), nil
}

type fakeSnapshots struct {
	mu      sync.Mutex
	current *conversation.Compaction
	claimed bool
	nextID  int64
}

func (f *fakeSnapshots) FindCurrentSnapshot(context.Context, int64, int64) (*conversation.Compaction, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.current == nil {
		return nil, gorm.ErrRecordNotFound
	}
	copy := *f.current
	return &copy, nil
}
func (f *fakeSnapshots) ClaimSnapshot(context.Context, int64, int64, *int64, int, string, time.Time) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.claimed {
		return false, nil
	}
	f.claimed = true
	return true, nil
}
func (f *fakeSnapshots) CompleteSnapshot(_ context.Context, item *conversation.Compaction, _ *int64, _ int, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	item.ID = f.nextID
	f.current = item
	f.claimed = false
	return nil
}
func (f *fakeSnapshots) ReleaseSnapshotClaim(context.Context, int64, int64, string, string) error {
	f.mu.Lock()
	f.claimed = false
	f.mu.Unlock()
	return nil
}

type fakeChat struct {
	mu       sync.Mutex
	calls    int
	errors   []error
	requests []llm.ChatRequest
	content  string
}

func (f *fakeChat) Chat(_ context.Context, _ llm.ChatProviderConfig, req llm.ChatRequest) (*llm.ChatResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.requests = append(f.requests, req)
	if len(f.errors) > 0 {
		err := f.errors[0]
		f.errors = f.errors[1:]
		return nil, err
	}
	content := f.content
	if content == "" {
		content = "progress and decisions"
	}
	return &llm.ChatResponse{Content: content, Usage: llm.Usage{TotalTokens: 3}}, nil
}
func (*fakeChat) StreamChat(context.Context, llm.ChatProviderConfig, llm.ChatRequest, func(llm.StreamEvent) error) error {
	return errors.New("unused")
}

func testMessage(id int64, role, content string) conversation.Message {
	return conversation.Message{ImmutableModel: domain.ImmutableModel{ID: id}, Role: role, Content: content}
}

func render(window Window) ([]llm.ChatMessage, int, error) {
	messages := []llm.ChatMessage{{Role: conversation.RoleSystem, Content: "system"}}
	if window.Snapshot != nil && window.Snapshot.Summary != "" {
		messages = append(messages, llm.ChatMessage{Role: conversation.RoleUser, Content: conversation.CompactionSummaryPrefix + window.Snapshot.Summary})
	}
	for _, message := range window.Messages {
		messages = append(messages, llm.ChatMessage{Role: message.Role, Content: message.Content})
	}
	return messages, 0, nil
}

func TestRenderUsesNinetyPercentAndOnlyLowersConfiguredLimit(t *testing.T) {
	c := Coordinator{History: fakeHistory{messages: []conversation.Message{testMessage(1, conversation.RoleUser, "small")}}, Snapshots: &fakeSnapshots{}}
	result, err := c.Prepare(context.Background(), Request{OwnerID: 1, ConversationID: 1, Provider: llm.ChatProviderConfig{ProviderType: "main"}, Model: "m", WindowTokens: 1000, AutoLimit: 9999, Render: render})
	if err != nil || result.Trace.Threshold != 865 {
		t.Fatalf("90%% threshold must be clamped to hard limit: %+v err=%v", result.Trace, err)
	}
	result, err = c.Prepare(context.Background(), Request{OwnerID: 1, ConversationID: 1, Provider: llm.ChatProviderConfig{ProviderType: "main"}, Model: "m", WindowTokens: 1000, AutoLimit: 100, Render: render})
	if err != nil || result.Trace.Threshold != 100 {
		t.Fatalf("configured lower threshold must be respected: %+v err=%v", result.Trace, err)
	}
}

func TestPrepareCompactsFullHistoryAndRetainsUsers(t *testing.T) {
	messages := []conversation.Message{
		testMessage(1, conversation.RoleUser, strings.Repeat("old ", 50000)),
		testMessage(2, conversation.RoleAssistant, "tool call"),
		testMessage(3, conversation.RoleTool, "tool result"),
		testMessage(4, conversation.RoleUser, "latest request"),
	}
	snapshots := &fakeSnapshots{}
	c := Coordinator{History: fakeHistory{messages: messages}, Snapshots: snapshots, Client: &fakeChat{}}
	result, err := c.Prepare(context.Background(), Request{OwnerID: 1, ConversationID: 1, Provider: llm.ChatProviderConfig{ProviderType: "main"}, Model: "m", WindowTokens: 30000, AutoLimit: 1, Render: render})
	if err != nil || !result.Trace.Created || snapshots.current == nil {
		t.Fatalf("compaction failed: result=%+v err=%v", result, err)
	}
	if snapshots.current.FirstMessageID != 1 || snapshots.current.LastMessageID != 4 || len(snapshots.current.FirstMessageContent) >= len(messages[0].Content) {
		t.Fatalf("frozen window must contain the recent user message: %+v", snapshots.current)
	}
	if len(result.Window.Messages) != 2 || result.Window.Messages[0].Role != conversation.RoleUser || result.Window.Messages[1].Role != conversation.RoleUser {
		t.Fatalf("assistant/tool messages must not be retained: %+v", result.Window.Messages)
	}
}

func TestPrepareCompactionFailureIsReturned(t *testing.T) {
	snapshots := &fakeSnapshots{}
	c := Coordinator{History: fakeHistory{messages: []conversation.Message{testMessage(1, conversation.RoleUser, "x")}}, Snapshots: snapshots, Client: &fakeChat{errors: []error{errors.New("timeout"), errors.New("timeout"), errors.New("timeout")}}}
	_, err := c.Prepare(context.Background(), Request{OwnerID: 1, ConversationID: 1, Provider: llm.ChatProviderConfig{ProviderType: "main"}, Model: "m", WindowTokens: 1000, AutoLimit: 1, Render: render})
	if !errors.Is(err, ErrCompactionFailed) || snapshots.current != nil {
		t.Fatalf("failed compaction must stop the run and not advance snapshot: snapshot=%+v err=%v", snapshots.current, err)
	}
}

func TestLoadRebuildsFrozenUsersAndTail(t *testing.T) {
	history := fakeHistory{messages: []conversation.Message{
		testMessage(1, conversation.RoleUser, "frozen"), testMessage(2, conversation.RoleAssistant, "discarded"),
		testMessage(3, conversation.RoleUser, "tail"), testMessage(4, conversation.RoleAssistant, "answer"),
	}}
	snapshots := &fakeSnapshots{current: &conversation.Compaction{ImmutableModel: domain.ImmutableModel{ID: 9}, FirstMessageID: 1, LastMessageID: 1, SnapshotVersion: 1, Summary: "s"}}
	c := Coordinator{History: history, Snapshots: snapshots}
	window, err := c.Load(context.Background(), 1, 1)
	if err != nil || len(window.Messages) != 4 || window.Messages[0].ID != 1 || window.Messages[1].ID != 2 || window.Messages[2].ID != 3 || window.Messages[3].ID != 4 {
		t.Fatalf("unexpected reconstructed window: %+v err=%v", window, err)
	}
	snapshots.current.FirstMessageID, snapshots.current.LastMessageID = 0, 0
	window, err = c.Load(context.Background(), 1, 1)
	if err != nil || len(window.Messages) != 0 {
		t.Fatalf("zero-boundary runtime row must be summary-only: %+v err=%v", window, err)
	}
}

func TestCompactionContextErrorDropsOldestHistory(t *testing.T) {
	client := &fakeChat{errors: []error{llm.ErrContextWindowExceeded}, content: "ok"}
	c := Coordinator{History: fakeHistory{messages: []conversation.Message{testMessage(1, conversation.RoleUser, "a"), testMessage(2, conversation.RoleUser, "b")}}, Snapshots: &fakeSnapshots{}, Client: client}
	_, err := c.Prepare(context.Background(), Request{OwnerID: 1, ConversationID: 1, Provider: llm.ChatProviderConfig{ProviderType: "main"}, Model: "m", WindowTokens: 1000, AutoLimit: 1, Render: render})
	if err != nil || client.calls != 2 {
		t.Fatalf("context error should retry after dropping oldest history: calls=%d err=%v", client.calls, err)
	}
}

func TestCompactionContextErrorStopsWithSingleHistoryItem(t *testing.T) {
	client := &fakeChat{errors: []error{llm.ErrContextWindowExceeded}}
	c := Coordinator{History: fakeHistory{messages: []conversation.Message{testMessage(1, conversation.RoleUser, "only")}}, Snapshots: &fakeSnapshots{}, Client: client}
	_, err := c.Prepare(context.Background(), Request{OwnerID: 1, ConversationID: 1, Provider: llm.ChatProviderConfig{ProviderType: "main"}, Model: "m", WindowTokens: 1000, AutoLimit: 1, Render: render})
	if !errors.Is(err, ErrCompactionFailed) || client.calls != 1 {
		t.Fatalf("single oversized history item must fail once: calls=%d err=%v", client.calls, err)
	}
}

func TestBodyAfterPrefixScopeIgnoresFixedPrefixBelowHardLimit(t *testing.T) {
	client := &fakeChat{}
	c := Coordinator{History: fakeHistory{messages: []conversation.Message{testMessage(1, conversation.RoleUser, "small")}}, Snapshots: &fakeSnapshots{}, Client: client}
	_, err := c.Prepare(context.Background(), Request{OwnerID: 1, ConversationID: 1, Provider: llm.ChatProviderConfig{ProviderType: "main"}, Model: "m", WindowTokens: 1000, ReservedOutput: 1, SafetyMargin: 1, AutoLimit: 100, AutoLimitScope: "body_after_prefix", Render: func(window Window) ([]llm.ChatMessage, int, error) {
		messages, _, err := render(window)
		return messages, 700, err
	}})
	if err != nil || client.calls != 0 {
		t.Fatalf("body scope should ignore the fixed prefix: calls=%d err=%v", client.calls, err)
	}
}

func TestManualCompactionForcesSnapshot(t *testing.T) {
	client, snapshots := &fakeChat{}, &fakeSnapshots{}
	c := Coordinator{History: fakeHistory{messages: []conversation.Message{testMessage(1, conversation.RoleUser, "small")}}, Snapshots: snapshots, Client: client}
	result, err := c.Prepare(context.Background(), Request{OwnerID: 1, ConversationID: 1, Provider: llm.ChatProviderConfig{ProviderType: "main"}, Model: "m", WindowTokens: 1000, Force: true, Trigger: conversation.CompactionTriggerManual, Render: render})
	if err != nil || !result.Trace.Created || snapshots.current == nil || snapshots.current.TriggerType != conversation.CompactionTriggerManual {
		t.Fatalf("manual compaction did not create a manual snapshot: result=%+v snapshot=%+v err=%v", result, snapshots.current, err)
	}
}

func TestPrepareCompactsAfterModelDowngrade(t *testing.T) {
	messages := []conversation.Message{
		testMessage(1, conversation.RoleUser, "old request"),
		testMessage(2, conversation.RoleAssistant, strings.Repeat("old response ", 1000)),
		testMessage(3, conversation.RoleUser, "latest request"),
	}
	snapshots := &fakeSnapshots{current: &conversation.Compaction{ImmutableModel: domain.ImmutableModel{ID: 9}, FirstMessageID: 1, LastMessageID: 1, SnapshotVersion: 1, Summary: "old", Model: "large", ContextWindowTokens: 4000}}
	client := &fakeChat{}
	c := Coordinator{History: fakeHistory{messages: messages}, Snapshots: snapshots, Client: client}
	result, err := c.Prepare(context.Background(), Request{OwnerID: 1, ConversationID: 1, Provider: llm.ChatProviderConfig{ProviderType: "main"}, Model: "small", WindowTokens: 1000, Render: render})
	if err != nil || !result.Trace.Created || client.calls != 1 || snapshots.current.Model != "small" || snapshots.current.ContextWindowTokens != 1000 {
		t.Fatalf("model downgrade did not create a snapshot for the smaller window: result=%+v snapshot=%+v calls=%d err=%v", result, snapshots.current, client.calls, err)
	}
}

func TestTokenBudgetCompactionSkipsSummaryModel(t *testing.T) {
	snapshots := &fakeSnapshots{}
	c := Coordinator{History: fakeHistory{messages: []conversation.Message{
		testMessage(1, conversation.RoleDeveloper, "retain"),
		testMessage(2, conversation.RoleUser, "discard"),
		testMessage(3, conversation.RoleAssistant, "discard too"),
	}}, Snapshots: snapshots}
	result, err := c.Prepare(context.Background(), Request{OwnerID: 1, ConversationID: 1, Provider: llm.ChatProviderConfig{ProviderType: "main"}, Model: "m", WindowTokens: 1000, Force: true, TokenBudgetCompaction: true, RetainClientDeveloperMessages: true, Render: render})
	if err != nil || !result.Trace.Created || result.Trace.ModelCalls != 0 || snapshots.current == nil || snapshots.current.Summary != "" || snapshots.current.LastMessageID != 3 || len(result.Window.Messages) != 1 || result.Window.Messages[0].Role != conversation.RoleDeveloper {
		t.Fatalf("token-budget window mismatch: result=%+v snapshot=%+v err=%v", result, snapshots.current, err)
	}
}
