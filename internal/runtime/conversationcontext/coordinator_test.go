package conversationcontext

import (
	"context"
	"errors"
	"testing"
	"time"

	"agentcanvas/internal/domain/conversation"
	"agentcanvas/internal/infrastructure/llm"

	"gorm.io/gorm"
)

type fakeHistory struct{ messages []conversation.Message }

func (f fakeHistory) ListActiveByConversation(context.Context, int64, int64) ([]conversation.Message, error) {
	return append([]conversation.Message(nil), f.messages...), nil
}

type fakeSnapshots struct {
	current *conversation.Compaction
	claimed bool
	nextID  int64
}

func (f *fakeSnapshots) FindCurrentSnapshot(context.Context, int64, int64) (*conversation.Compaction, error) {
	if f.current == nil {
		return nil, gorm.ErrRecordNotFound
	}
	return f.current, nil
}
func (f *fakeSnapshots) ClaimSnapshot(context.Context, int64, int64, *int64, int, string, time.Time) (bool, error) {
	if f.claimed {
		return false, nil
	}
	f.claimed = true
	return true, nil
}
func (f *fakeSnapshots) CompleteSnapshot(_ context.Context, item *conversation.Compaction, _ *int64, _ int, _ string) error {
	f.nextID++
	item.ID = f.nextID
	f.current = item
	f.claimed = false
	return nil
}
func (f *fakeSnapshots) ReleaseSnapshotClaim(context.Context, int64, int64, string, string) error {
	f.claimed = false
	return nil
}

type fakeChat struct {
	calls int
	err   error
}

func (f *fakeChat) Chat(context.Context, llm.ChatProviderConfig, llm.ChatRequest) (*llm.ChatResponse, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return &llm.ChatResponse{Content: "goal: retain constraints", Usage: llm.Usage{TotalTokens: 3}}, nil
}
func (*fakeChat) StreamChat(context.Context, llm.ChatProviderConfig, llm.ChatRequest, func(llm.StreamEvent) error) error {
	return errors.New("unused")
}

func render(window Window) ([]llm.ChatMessage, int, error) {
	messages := []llm.ChatMessage{{Role: "system", Content: "system"}}
	if window.Snapshot != nil {
		messages = append(messages, llm.ChatMessage{Role: "system", Content: window.Snapshot.Summary})
	}
	for _, message := range window.Messages {
		messages = append(messages, llm.ChatMessage{Role: message.Role, Content: message.Content})
	}
	return messages, 0, nil
}

func TestPrepareDoesNotCallModelBelowThreshold(t *testing.T) {
	client := &fakeChat{}
	coordinator := Coordinator{History: fakeHistory{messages: []conversation.Message{{ID: 1, Role: "user", Content: "small"}}}, Snapshots: &fakeSnapshots{}, Client: client}
	result, err := coordinator.Prepare(context.Background(), Request{OwnerID: 1, ConversationID: 1, Provider: llm.ChatProviderConfig{ProviderType: "openai_compatible"}, Model: "gpt-4o", WindowTokens: 1000, AutoLimit: 900, Render: render})
	if err != nil || client.calls != 0 || result.Trace.Created {
		t.Fatalf("unexpected result=%+v calls=%d err=%v", result, client.calls, err)
	}
}

func TestPrepareCreatesRollingSnapshot(t *testing.T) {
	messages := make([]conversation.Message, 10)
	for i := range messages {
		messages[i] = conversation.Message{ID: int64(i + 1), Role: "user", Content: "important context detail"}
	}
	client := &fakeChat{}
	snapshots := &fakeSnapshots{}
	coordinator := Coordinator{History: fakeHistory{messages: messages}, Snapshots: snapshots, Client: client}
	result, err := coordinator.Prepare(context.Background(), Request{OwnerID: 1, ConversationID: 1, Provider: llm.ChatProviderConfig{ProviderType: "openai_compatible"}, Model: "gpt-4o", WindowTokens: 1000, AutoLimit: 1, Render: render})
	if err != nil || client.calls != 1 || !result.Trace.Created || snapshots.current == nil || snapshots.current.LastMessageID != 2 {
		t.Fatalf("snapshot not created: result=%+v snapshot=%+v calls=%d err=%v", result, snapshots.current, client.calls, err)
	}
	if len(result.Window.Messages) != keepRecentMessages {
		t.Fatalf("recent messages were not preserved: %+v", result.Window.Messages)
	}
}

func TestPrepareDoesNotAdvanceSnapshotWhenModelFails(t *testing.T) {
	messages := make([]conversation.Message, 10)
	for i := range messages {
		messages[i] = conversation.Message{ID: int64(i + 1), Role: "user", Content: "important context detail"}
	}
	snapshots := &fakeSnapshots{}
	coordinator := Coordinator{History: fakeHistory{messages: messages}, Snapshots: snapshots, Client: &fakeChat{err: errors.New("timeout")}}
	_, err := coordinator.Prepare(context.Background(), Request{OwnerID: 1, ConversationID: 1, Provider: llm.ChatProviderConfig{ProviderType: "openai_compatible"}, Model: "gpt-4o", WindowTokens: 1000, AutoLimit: 1, Render: render})
	if !errors.Is(err, ErrCompactionFailed) || snapshots.current != nil {
		t.Fatalf("failure advanced snapshot: snapshot=%+v err=%v", snapshots.current, err)
	}
}
