package node

import (
	"context"
	"sync"
	"testing"

	"agentcanvas/internal/domain/memory"
	runtimeagent "agentcanvas/internal/runtime/agent"
	"agentcanvas/internal/runtime/engine"
)

type fakeWMRepo struct {
	mu   sync.Mutex
	data map[int64]*memory.WorkingMemory
}

func (r *fakeWMRepo) key(convID int64) int64 { return convID }

func (r *fakeWMRepo) Get(ctx context.Context, ownerID, conversationID int64) (*memory.WorkingMemory, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.data == nil {
		return nil, nil
	}
	wm, ok := r.data[conversationID]
	if !ok {
		return nil, nil
	}
	clone := *wm
	return &clone, nil
}

func (r *fakeWMRepo) Save(ctx context.Context, wm *memory.WorkingMemory) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.data == nil {
		r.data = map[int64]*memory.WorkingMemory{}
	}
	clone := *wm
	r.data[wm.ConversationID] = &clone
	return nil
}

func (r *fakeWMRepo) Update(ctx context.Context, ownerID, conversationID int64, mutate func(*memory.WorkingMemory) error) (*memory.WorkingMemory, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.data == nil {
		r.data = map[int64]*memory.WorkingMemory{}
	}
	wm := r.data[conversationID]
	if wm == nil {
		wm = &memory.WorkingMemory{OwnerID: ownerID, ConversationID: conversationID}
	}
	clone := *wm
	if err := mutate(&clone); err != nil {
		return nil, err
	}
	r.data[conversationID] = &clone
	result := clone
	return &result, nil
}

func (r *fakeWMRepo) Delete(ctx context.Context, ownerID, conversationID int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.data != nil {
		delete(r.data, conversationID)
	}
	return nil
}

var _ memory.WorkingMemoryRepository = (*fakeWMRepo)(nil)

func convIDPtr(id int64) *int64 { return &id }

func TestInjectWorkingMemory_NoRepo(t *testing.T) {
	n := AgentNode{WorkingMemory: nil}
	rc := &engine.RunContext{ConversationID: convIDPtr(1)}
	blocks := []runtimeagent.ContextBlock{{Name: "test", Content: "hello"}}
	result := n.injectWorkingMemory(context.Background(), rc, blocks)
	if len(result) != 1 {
		t.Fatalf("expected 1 block, got %d", len(result))
	}
}

func TestInjectWorkingMemory_NilConversation(t *testing.T) {
	repo := &fakeWMRepo{}
	n := AgentNode{WorkingMemory: repo}
	rc := &engine.RunContext{ConversationID: nil}
	blocks := []runtimeagent.ContextBlock{{Name: "test", Content: "hello"}}
	result := n.injectWorkingMemory(context.Background(), rc, blocks)
	if len(result) != 1 {
		t.Fatalf("expected 1 block, got %d", len(result))
	}
}

func TestInjectWorkingMemory_EmptyWM(t *testing.T) {
	repo := &fakeWMRepo{}
	repo.Save(context.Background(), &memory.WorkingMemory{OwnerID: 100, ConversationID: 1})
	n := AgentNode{WorkingMemory: repo}
	rc := &engine.RunContext{OwnerID: 100, ConversationID: convIDPtr(1)}
	blocks := []runtimeagent.ContextBlock{{Name: "test", Content: "hello"}}
	result := n.injectWorkingMemory(context.Background(), rc, blocks)
	if len(result) != 1 {
		t.Fatalf("expected 1 block when WM is empty, got %d", len(result))
	}
}

func TestInjectWorkingMemory_WithContent(t *testing.T) {
	repo := &fakeWMRepo{}
	repo.Save(context.Background(), &memory.WorkingMemory{
		OwnerID:        100,
		ConversationID: 1,
		ActiveTask:     &memory.WorkingTask{Goal: "test task"},
		RoundNumber:    2,
	})
	n := AgentNode{WorkingMemory: repo}
	rc := &engine.RunContext{OwnerID: 100, ConversationID: convIDPtr(1)}
	blocks := []runtimeagent.ContextBlock{{Name: "test", Content: "hello"}}
	result := n.injectWorkingMemory(context.Background(), rc, blocks)
	if len(result) != 2 {
		t.Fatalf("expected 2 blocks (wm + original), got %d", len(result))
	}
	if result[0].Name != "working_memory" {
		t.Fatalf("expected working_memory block first, got name=%s", result[0].Name)
	}
	if result[0].Role != "system" {
		t.Fatalf("expected system role, got %s", result[0].Role)
	}
}

func TestUpdateWorkingMemory_NilRepo(t *testing.T) {
	n := AgentNode{WorkingMemory: nil}
	rc := &engine.RunContext{ConversationID: convIDPtr(1)}
	n.updateWorkingMemory(context.Background(), rc, &runtimeagent.RunResult{FinalAnswer: "ok"})
}

func TestUpdateWorkingMemory_NilConversation(t *testing.T) {
	repo := &fakeWMRepo{}
	n := AgentNode{WorkingMemory: repo}
	rc := &engine.RunContext{ConversationID: nil}
	n.updateWorkingMemory(context.Background(), rc, &runtimeagent.RunResult{FinalAnswer: "ok"})
}

func TestUpdateWorkingMemory_CreatesNewAndIncrements(t *testing.T) {
	repo := &fakeWMRepo{}
	n := AgentNode{WorkingMemory: repo}
	rc := &engine.RunContext{OwnerID: 100, ConversationID: convIDPtr(1)}

	n.updateWorkingMemory(context.Background(), rc, &runtimeagent.RunResult{
		FinalAnswer: "The answer is 42",
		StopReason:  runtimeagent.StopReasonFinalAnswer,
	})
	wm, _ := repo.Get(context.Background(), 100, 1)
	if wm.RoundNumber != 1 {
		t.Fatalf("expected round 1, got %d", wm.RoundNumber)
	}
	if wm.ContextSummary != "The answer is 42" {
		t.Fatalf("unexpected summary: %s", wm.ContextSummary)
	}

	n.updateWorkingMemory(context.Background(), rc, &runtimeagent.RunResult{
		FinalAnswer: "Updated answer",
		StopReason:  runtimeagent.StopReasonFinalAnswer,
	})
	wm, _ = repo.Get(context.Background(), 100, 1)
	if wm.RoundNumber != 2 {
		t.Fatalf("expected round 2, got %d", wm.RoundNumber)
	}
}

func TestUpdateWorkingMemory_NilResult(t *testing.T) {
	repo := &fakeWMRepo{}
	n := AgentNode{WorkingMemory: repo}
	rc := &engine.RunContext{ConversationID: convIDPtr(1)}
	n.updateWorkingMemory(context.Background(), rc, nil)

	wm, _ := repo.Get(context.Background(), 100, 1)
	if wm != nil {
		t.Fatal("expected nil wm when result is nil")
	}
}

func TestUpdateWorkingMemory_NonFinalStopDoesNotWrite(t *testing.T) {
	repo := &fakeWMRepo{}
	n := AgentNode{WorkingMemory: repo}
	rc := &engine.RunContext{OwnerID: 100, ConversationID: convIDPtr(1)}

	n.updateWorkingMemory(context.Background(), rc, &runtimeagent.RunResult{
		StopReason: runtimeagent.StopReasonMaxIterations,
	})
	wm, _ := repo.Get(context.Background(), 100, 1)
	if wm != nil {
		t.Fatalf("non-final state must not update working memory: %+v", wm)
	}
}

func TestTruncateStringKeepsUTF8Valid(t *testing.T) {
	got := truncateString("你好世界", 3)
	if got != "你好世..." {
		t.Fatalf("unexpected truncate result: %q", got)
	}
}
