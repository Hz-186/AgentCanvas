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
	return f.current, nil
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
	defer f.mu.Unlock()
	f.claimed = false
	return nil
}

type delayedChat struct {
	mu    sync.Mutex
	calls int
}

func (f *delayedChat) Chat(context.Context, llm.ChatProviderConfig, llm.ChatRequest) (*llm.ChatResponse, error) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	time.Sleep(10 * time.Millisecond)
	return &llm.ChatResponse{Content: "Goal: retain\nConstraints and preferences: none\nConfirmed decisions: none\nCompleted work: none\nCurrent progress: ongoing\nOpen issues and next actions: continue\nEvidence and artifacts: test"}, nil
}
func (*delayedChat) StreamChat(context.Context, llm.ChatProviderConfig, llm.ChatRequest, func(llm.StreamEvent) error) error {
	return errors.New("unused")
}

type fakeChat struct {
	calls     int
	err       error
	responses []*llm.ChatResponse
	errors    []error
	providers []llm.ChatProviderConfig
	requests  []llm.ChatRequest
}

func (f *fakeChat) Chat(_ context.Context, provider llm.ChatProviderConfig, req llm.ChatRequest) (*llm.ChatResponse, error) {
	f.calls++
	f.providers = append(f.providers, provider)
	f.requests = append(f.requests, req)
	if len(f.errors) > 0 {
		err := f.errors[0]
		f.errors = f.errors[1:]
		return nil, err
	}
	if f.err != nil {
		return nil, f.err
	}
	if len(f.responses) > 0 {
		response := f.responses[0]
		f.responses = f.responses[1:]
		return response, nil
	}
	return &llm.ChatResponse{Content: "Goal: retain constraints\nConstraints and preferences: preserve user requirements\nConfirmed decisions: none\nCompleted work: none\nCurrent progress: ongoing\nOpen issues and next actions: continue\nEvidence and artifacts: test", Usage: llm.Usage{TotalTokens: 3}}, nil
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

func testMessage(id int64, role, content string) conversation.Message {
	return conversation.Message{ImmutableModel: domain.ImmutableModel{ID: id}, Role: role, Content: content}
}

func TestPrepareDoesNotCallModelBelowThreshold(t *testing.T) {
	client := &fakeChat{}
	coordinator := Coordinator{History: fakeHistory{messages: []conversation.Message{testMessage(1, "user", "small")}}, Snapshots: &fakeSnapshots{}, Client: client}
	result, err := coordinator.Prepare(context.Background(), Request{OwnerID: 1, ConversationID: 1, Provider: llm.ChatProviderConfig{ProviderType: "openai_compatible"}, Model: "gpt-4o", WindowTokens: 1000, AutoLimit: 900, Render: render})
	if err != nil || client.calls != 0 || result.Trace.Created {
		t.Fatalf("unexpected result=%+v calls=%d err=%v", result, client.calls, err)
	}
}

func TestValidateSnapshotSummaryRequiresStableSections(t *testing.T) {
	if err := validateSnapshotSummary("openai_compatible", "gpt-4o", "Goal: only this section", 100); err == nil {
		t.Fatal("malformed snapshot summary must be rejected")
	}
	if err := validateSnapshotSummary("openai_compatible", "gpt-4o", "Goal: g\nConstraints and preferences: \nConfirmed decisions: d\nCompleted work: w\nCurrent progress: p\nOpen issues and next actions: n\nEvidence and artifacts: e", 100); err == nil {
		t.Fatal("empty snapshot section must be rejected")
	}
	if err := validateSnapshotSummary("openai_compatible", "gpt-4o", "Goal appears in prose; Constraints and preferences also appears", 100); err == nil {
		t.Fatal("section names in prose must not satisfy the structure")
	}
	valid := "Goal: g\nConstraints and preferences: c\nConfirmed decisions: d\nCompleted work: w\nCurrent progress: p\nOpen issues and next actions: n\nEvidence and artifacts: e"
	if err := validateSnapshotSummary("openai_compatible", "gpt-4o", valid, 100); err != nil {
		t.Fatalf("valid snapshot summary rejected: %v", err)
	}
}

func TestClipToolResultMessagesPreservesHeadTail(t *testing.T) {
	messages := []conversation.Message{testMessage(9, conversation.RoleTool, strings.Repeat("a", 4096)+strings.Repeat("b", 4096)+strings.Repeat("c", 2048))}
	clipped := clipToolResultMessages(messages)
	if clipped[0].ID != 9 || !strings.HasPrefix(clipped[0].Content, strings.Repeat("a", 4096)) || !strings.HasSuffix(clipped[0].Content, strings.Repeat("c", 1024)) {
		t.Fatalf("tool result evidence was not preserved: %+v", clipped[0])
	}
}

func TestPrepareCreatesRollingSnapshot(t *testing.T) {
	messages := make([]conversation.Message, 10)
	for i := range messages {
		messages[i] = testMessage(int64(i+1), "user", "important context detail")
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
		messages[i] = testMessage(int64(i+1), "user", "important context detail")
	}
	snapshots := &fakeSnapshots{}
	coordinator := Coordinator{History: fakeHistory{messages: messages}, Snapshots: snapshots, Client: &fakeChat{err: errors.New("timeout")}}
	result, err := coordinator.Prepare(context.Background(), Request{OwnerID: 1, ConversationID: 1, Provider: llm.ChatProviderConfig{ProviderType: "openai_compatible"}, Model: "gpt-4o", WindowTokens: 1000, AutoLimit: 1, Render: render})
	if err != nil || result.Trace.Failure == "" || snapshots.current != nil {
		t.Fatalf("soft-limit failure must keep original history without advancing snapshot: result=%+v snapshot=%+v err=%v", result, snapshots.current, err)
	}
}

func TestPrepareReturnsRetryableErrorWhenCompactionFailsBeyondHardLimit(t *testing.T) {
	messages := make([]conversation.Message, 20)
	for i := range messages {
		content := "recent"
		if i < 12 {
			content = strings.Repeat("large context ", 100)
		}
		messages[i] = testMessage(int64(i+1), conversation.RoleUser, content)
	}
	snapshots := &fakeSnapshots{}
	coordinator := Coordinator{History: fakeHistory{messages: messages}, Snapshots: snapshots, Client: &fakeChat{err: errors.New("timeout")}}
	_, err := coordinator.Prepare(context.Background(), Request{OwnerID: 1, ConversationID: 1, Provider: llm.ChatProviderConfig{ProviderType: "openai_compatible"}, Model: "gpt-4o", WindowTokens: 128, AutoLimit: 1, Render: render})
	if !errors.Is(err, ErrCompactionFailed) || snapshots.current != nil {
		t.Fatalf("hard-limit failure must be retryable without snapshot: snapshot=%+v err=%v", snapshots.current, err)
	}
}

func TestForcePrepareWithShortHistoryReturnsErrorInsteadOfPanicking(t *testing.T) {
	client := &fakeChat{}
	snapshots := &fakeSnapshots{}
	coordinator := Coordinator{History: fakeHistory{messages: []conversation.Message{testMessage(1, "user", "short")}}, Snapshots: snapshots, Client: client}
	_, err := coordinator.Prepare(context.Background(), Request{OwnerID: 1, ConversationID: 1, Provider: llm.ChatProviderConfig{ProviderType: "openai_compatible"}, Model: "gpt-4o", Force: true, Render: render})
	if !errors.Is(err, ErrCompactionFailed) || client.calls != 0 || snapshots.current != nil {
		t.Fatalf("short forced compaction must fail safely: calls=%d snapshot=%+v err=%v", client.calls, snapshots.current, err)
	}
}

func TestPrepareRollsOnlyMessagesAfterSnapshotBoundary(t *testing.T) {
	messages := make([]conversation.Message, 10)
	for i := range messages {
		messages[i] = testMessage(int64(i+1), "user", "important context detail")
	}
	history := &fakeHistory{messages: messages}
	client := &fakeChat{}
	snapshots := &fakeSnapshots{}
	coordinator := Coordinator{History: history, Snapshots: snapshots, Client: client}
	req := Request{OwnerID: 1, ConversationID: 1, Provider: llm.ChatProviderConfig{ProviderType: "openai_compatible"}, Model: "gpt-4o", WindowTokens: 1000, AutoLimit: 1, Render: render}
	first, err := coordinator.Prepare(context.Background(), req)
	if err != nil || first.Window.Snapshot == nil || first.Window.Snapshot.LastMessageID != 2 || client.calls != 1 {
		t.Fatalf("first snapshot failed: result=%+v calls=%d err=%v", first, client.calls, err)
	}
	unchanged, err := coordinator.Prepare(context.Background(), req)
	if err != nil || unchanged.Trace.Created || client.calls != 1 {
		t.Fatalf("snapshot must be reused without new compactable history: result=%+v calls=%d err=%v", unchanged, client.calls, err)
	}
	history.messages = append(history.messages,
		testMessage(11, "assistant", "new result"),
		testMessage(12, "user", "new request"))
	rolled, err := coordinator.Prepare(context.Background(), req)
	if err != nil || rolled.Window.Snapshot == nil || rolled.Window.Snapshot.SnapshotVersion != 2 || rolled.Window.Snapshot.LastMessageID != 4 || client.calls != 2 {
		t.Fatalf("rolling snapshot failed: result=%+v calls=%d err=%v", rolled, client.calls, err)
	}
}

func TestPrepareFallsBackInsideCoordinatorAndRecordsActualModel(t *testing.T) {
	valid := "Goal: retained\nConstraints and preferences: none\nConfirmed decisions: none\nCompleted work: none\nCurrent progress: ongoing\nOpen issues and next actions: continue\nEvidence and artifacts: test"
	client := &fakeChat{responses: []*llm.ChatResponse{
		{Content: "malformed", Usage: llm.Usage{PromptTokens: 1, CompletionTokens: 1, TotalTokens: 2}},
		{Content: valid, Usage: llm.Usage{PromptTokens: 2, CompletionTokens: 2, TotalTokens: 4}},
	}, errors: []error{errors.New("auxiliary context too small")}}
	snapshots := &fakeSnapshots{}
	coordinator := Coordinator{History: fakeHistory{messages: repeatedUserMessages(10)}, Snapshots: snapshots, Client: client}
	result, err := coordinator.Prepare(context.Background(), Request{
		OwnerID: 1, ConversationID: 1, ProviderID: 1, Provider: llm.ChatProviderConfig{ProviderType: "main", BaseURL: "main"}, Model: "main-model",
		CompactionProviderID: 2, CompactionProvider: llm.ChatProviderConfig{ProviderType: "aux", BaseURL: "aux"}, CompactionModel: "aux-model",
		WindowTokens: 1000, AutoLimit: 1, Render: render,
	})
	if err != nil || !result.Trace.Created || result.Trace.ModelCalls != 3 || result.Trace.ProviderID != 1 || result.Trace.Model != "main-model" || result.Trace.Usage.TotalTokens != 6 || len(client.providers) != 3 {
		t.Fatalf("coordinator fallback was not recorded precisely: result=%+v providers=%+v err=%v", result, client.providers, err)
	}
	if snapshots.current == nil || snapshots.current.ProviderID != 1 || snapshots.current.Model != "main-model" || result.Trace.FallbackReason == "" {
		t.Fatalf("snapshot must record successful fallback model: snapshot=%+v trace=%+v", snapshots.current, result.Trace)
	}
}

func TestPrepareStopsAfterAuxiliaryValidationRetry(t *testing.T) {
	valid := "Goal: retained\nConstraints and preferences: none\nConfirmed decisions: none\nCompleted work: none\nCurrent progress: ongoing\nOpen issues and next actions: continue\nEvidence and artifacts: test"
	client := &fakeChat{responses: []*llm.ChatResponse{{Content: "malformed"}, {Content: "still malformed"}, {Content: valid}}}
	snapshots := &fakeSnapshots{}
	coordinator := Coordinator{History: fakeHistory{messages: repeatedUserMessages(10)}, Snapshots: snapshots, Client: client}
	result, err := coordinator.Prepare(context.Background(), Request{
		OwnerID: 1, ConversationID: 1, ProviderID: 1, Provider: llm.ChatProviderConfig{ProviderType: "main", BaseURL: "main"}, Model: "main-model",
		CompactionProviderID: 2, CompactionProvider: llm.ChatProviderConfig{ProviderType: "aux", BaseURL: "aux"}, CompactionModel: "aux-model",
		WindowTokens: 1000, AutoLimit: 1, Render: render,
	})
	if err != nil || result.Trace.Failure == "" || result.Trace.ModelCalls != 2 || len(client.providers) != 2 || client.providers[0].ProviderType != "aux" || client.providers[1].ProviderType != "aux" || snapshots.current != nil {
		t.Fatalf("invalid auxiliary summary must stop after one repair: result=%+v providers=%+v snapshot=%+v err=%v", result, client.providers, snapshots.current, err)
	}
}

func TestPrepareRejectsOversizedCurrentUserInputWithoutTruncation(t *testing.T) {
	coordinator := Coordinator{History: fakeHistory{messages: []conversation.Message{testMessage(1, conversation.RoleUser, strings.Repeat("user input ", 200))}}, Snapshots: &fakeSnapshots{}, Client: &fakeChat{}}
	result, err := coordinator.Prepare(context.Background(), Request{OwnerID: 1, ConversationID: 1, Provider: llm.ChatProviderConfig{ProviderType: "main"}, Model: "main-model", WindowTokens: 128, AutoLimit: 1, Render: render})
	if !errors.Is(err, ErrOverflow) || result.Trace.BeforeTokens == 0 {
		t.Fatalf("oversized current input must fail explicitly: result=%+v err=%v", result, err)
	}
}

func TestRecentMessageCutStartsAtUserTurn(t *testing.T) {
	messages := []conversation.Message{
		testMessage(1, conversation.RoleUser, strings.Repeat("old user ", 80)),
		testMessage(2, conversation.RoleAssistant, strings.Repeat("assistant result ", 80)),
		testMessage(3, conversation.RoleTool, strings.Repeat("tool result ", 80)),
		testMessage(4, conversation.RoleUser, "new user"),
		testMessage(5, conversation.RoleAssistant, "new assistant"),
	}
	cut := recentMessageCut(Request{Provider: llm.ChatProviderConfig{ProviderType: "main"}, Model: "main-model", WindowTokens: 1000}, messages)
	if cut > 0 && messages[cut].Role != conversation.RoleUser {
		t.Fatalf("recent history must start at a complete user turn: cut=%d messages=%+v", cut, messages)
	}
}

func TestPrepareRejectsThroughBoundaryInsideToolExchange(t *testing.T) {
	messages := []conversation.Message{
		testMessage(1, conversation.RoleUser, "run tool"),
		testMessage(2, conversation.RoleAssistant, "tool call"),
		testMessage(3, conversation.RoleTool, "tool result"),
		testMessage(4, conversation.RoleUser, "continue"),
	}
	client := &fakeChat{}
	snapshots := &fakeSnapshots{}
	coordinator := Coordinator{History: fakeHistory{messages: messages}, Snapshots: snapshots, Client: client}
	_, err := coordinator.Prepare(context.Background(), Request{OwnerID: 1, ConversationID: 1, Provider: llm.ChatProviderConfig{ProviderType: "main"}, Model: "main-model", WindowTokens: 1000, ThroughMessageID: 2, Force: true, Render: render})
	if !errors.Is(err, ErrCompactionFailed) || client.calls != 0 || snapshots.current != nil {
		t.Fatalf("split tool exchange must not become a snapshot: calls=%d snapshot=%+v err=%v", client.calls, snapshots.current, err)
	}
}

func TestConcurrentPrepareCreatesOnlyOneSnapshotVersion(t *testing.T) {
	client := &delayedChat{}
	snapshots := &fakeSnapshots{}
	coordinator := Coordinator{History: fakeHistory{messages: repeatedUserMessages(10)}, Snapshots: snapshots, Client: client}
	req := Request{OwnerID: 1, ConversationID: 1, Provider: llm.ChatProviderConfig{ProviderType: "main"}, Model: "main-model", WindowTokens: 1000, AutoLimit: 1, Render: render}
	results := make(chan error, 2)
	for range 2 {
		go func() {
			result, err := coordinator.Prepare(context.Background(), req)
			if err == nil && result.Window.Snapshot == nil {
				err = errors.New("prepared context has no snapshot")
			}
			results <- err
		}()
	}
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	client.mu.Lock()
	calls := client.calls
	client.mu.Unlock()
	snapshots.mu.Lock()
	current, nextID := snapshots.current, snapshots.nextID
	snapshots.mu.Unlock()
	if calls != 1 || nextID != 1 || current == nil || current.SnapshotVersion != 1 || current.ParentSnapshotID != nil {
		t.Fatalf("concurrent prepare forked snapshots: calls=%d next_id=%d current=%+v", calls, nextID, current)
	}
}

func TestInvalidSummariesNeverAdvanceSnapshot(t *testing.T) {
	for _, test := range []struct {
		name    string
		content string
	}{
		{name: "empty", content: ""},
		{name: "missing sections", content: "Goal: only one section"},
		{name: "over budget", content: strings.Repeat("too long ", 1000)},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := &fakeChat{responses: []*llm.ChatResponse{{Content: test.content}, {Content: test.content}}}
			snapshots := &fakeSnapshots{}
			coordinator := Coordinator{History: fakeHistory{messages: repeatedUserMessages(10)}, Snapshots: snapshots, Client: client}
			result, err := coordinator.Prepare(context.Background(), Request{OwnerID: 1, ConversationID: 1, Provider: llm.ChatProviderConfig{ProviderType: "main"}, Model: "main-model", WindowTokens: 1000, AutoLimit: 1, Render: render})
			snapshots.mu.Lock()
			current := snapshots.current
			snapshots.mu.Unlock()
			if err != nil || result.Trace.Failure == "" || result.Trace.ModelCalls != 2 || current != nil {
				t.Fatalf("invalid summary became durable: result=%+v snapshot=%+v err=%v", result, current, err)
			}
		})
	}
}

func TestRecentTokenBudgetUsesBoundedQuarterWindow(t *testing.T) {
	if got := recentTokenBudget(Request{WindowTokens: 10000, ReservedOutput: 1000, SafetyMargin: 100}); got != 2225 {
		t.Fatalf("unexpected quarter-window budget: %d", got)
	}
	if got := recentTokenBudget(Request{WindowTokens: 1000000, ReservedOutput: 1, SafetyMargin: 1}); got != maxRecentTokens {
		t.Fatalf("recent budget must be capped: %d", got)
	}
}

func TestLongHistoryCompactsBelowThirtyFivePercent(t *testing.T) {
	messages := repeatedUserMessages(100)
	for index := range messages {
		messages[index].Content = strings.Repeat("important context ", 100)
	}
	coordinator := Coordinator{History: fakeHistory{messages: messages}, Snapshots: &fakeSnapshots{}, Client: &fakeChat{}}
	result, err := coordinator.Prepare(context.Background(), Request{OwnerID: 1, ConversationID: 1, Provider: llm.ChatProviderConfig{ProviderType: "main"}, Model: "main-model", WindowTokens: 10000, ReservedOutput: 1000, SafetyMargin: 100, AutoLimit: 1, Render: render})
	if err != nil || !result.Trace.Created || result.Trace.BeforeTokens == 0 || result.Trace.AfterTokens*100 > result.Trace.BeforeTokens*35 {
		t.Fatalf("history compaction ratio exceeded target: before=%d after=%d result=%+v err=%v", result.Trace.BeforeTokens, result.Trace.AfterTokens, result, err)
	}
}

func repeatedUserMessages(count int) []conversation.Message {
	messages := make([]conversation.Message, count)
	for i := range messages {
		messages[i] = testMessage(int64(i+1), conversation.RoleUser, "important context detail")
	}
	return messages
}
