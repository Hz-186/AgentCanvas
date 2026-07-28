package node

import (
	"context"
	"sync"
	"testing"

	"agentcanvas/internal/domain/memory"
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

func TestTruncateStringKeepsUTF8Valid(t *testing.T) {
	got := truncateString("你好世界", 3)
	if got != "你好世..." {
		t.Fatalf("unexpected truncate result: %q", got)
	}
}
