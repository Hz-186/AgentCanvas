package memory

import (
	"context"
	"testing"
)

type runtimeArchivalFake struct {
	indexed Memory
	ids     []int64
}

func (f *runtimeArchivalFake) Index(_ context.Context, item Memory) error {
	f.indexed = item
	return nil
}
func (f *runtimeArchivalFake) Search(_ context.Context, _ int64, _ string, _ int) ([]int64, error) {
	return f.ids, nil
}
func (f *runtimeArchivalFake) Delete(_ context.Context, _ int64) error { return nil }

type runtimeRepoFake struct {
	items  map[int64]*Memory
	marked []int64
}

func (r *runtimeRepoFake) Create(_ context.Context, item *Memory) error {
	if r.items == nil {
		r.items = map[int64]*Memory{}
	}
	if item.ID == 0 {
		item.ID = int64(len(r.items) + 1)
	}
	clone := *item
	r.items[item.ID] = &clone
	return nil
}
func (r *runtimeRepoFake) Update(_ context.Context, item *Memory) error {
	clone := *item
	r.items[item.ID] = &clone
	return nil
}
func (r *runtimeRepoFake) FindByID(_ context.Context, ownerID, id int64) (*Memory, error) {
	item := *r.items[id]
	return &item, nil
}
func (r *runtimeRepoFake) List(context.Context, int64, []string, *int64, int, int) ([]Memory, error) {
	return nil, nil
}
func (r *runtimeRepoFake) ListForRead(context.Context, int64, []string, *int64, int) ([]Memory, error) {
	return nil, nil
}
func (r *runtimeRepoFake) ListByLevel(context.Context, int64, string, []string, int) ([]Memory, error) {
	return nil, nil
}
func (r *runtimeRepoFake) ListActiveOwnerIDs(context.Context, int) ([]int64, error) { return nil, nil }
func (r *runtimeRepoFake) IncrementAccessCount(context.Context, int64, int64) error { return nil }
func (r *runtimeRepoFake) IncrementConsolidationCount(context.Context, int64, int64) error {
	return nil
}
func (r *runtimeRepoFake) SoftDelete(context.Context, int64, int64) error { return nil }
func (r *runtimeRepoFake) MarkUsed(_ context.Context, _ int64, ids []int64) error {
	r.marked = ids
	return nil
}
func (r *runtimeRepoFake) MarkExpired(context.Context, int64, int) (int64, error) { return 0, nil }
func (r *runtimeRepoFake) UpdateDecayedImportance(context.Context, int64, float64) (int64, error) {
	return 0, nil
}
func (r *runtimeRepoFake) SetEmbedding(context.Context, int64, int64, []byte) error { return nil }

func TestRuntimeServiceIndexesAndReadsArchivalMemory(t *testing.T) {
	cid := int64(7)
	repo := &runtimeRepoFake{}
	archival := &runtimeArchivalFake{}
	service := RuntimeService{Memories: repo, Archival: archival}
	written, err := service.Write(context.Background(), WriteRequest{OwnerID: 1, ConversationID: &cid, MemoryType: TypeArchival, Content: "durable fact", Importance: .8})
	if err != nil {
		t.Fatal(err)
	}
	if archival.indexed.ID != written.Memory.ID {
		t.Fatalf("archival index did not receive memory: %+v", archival.indexed)
	}
	archival.ids = []int64{written.Memory.ID}
	read, err := service.Read(context.Background(), ReadRequest{OwnerID: 1, ConversationID: &cid, MemoryTypes: []string{TypeArchival}, Query: "fact", Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if read.Count != 1 || read.MemoryContext != "durable fact" || len(repo.marked) != 1 {
		t.Fatalf("unexpected read result: %+v marked=%v", read, repo.marked)
	}
}

func TestRuntimeServiceFiltersOtherConversation(t *testing.T) {
	wanted, other := int64(7), int64(8)
	repo := &runtimeRepoFake{items: map[int64]*Memory{1: {ID: 1, OwnerID: 1, ConversationID: &other, MemoryType: TypeArchival, Content: "private"}}}
	service := RuntimeService{Memories: repo, Archival: &runtimeArchivalFake{ids: []int64{1}}}
	read, err := service.Read(context.Background(), ReadRequest{OwnerID: 1, ConversationID: &wanted, MemoryTypes: []string{TypeArchival}, Query: "private"})
	if err != nil {
		t.Fatal(err)
	}
	if read.Count != 0 {
		t.Fatalf("expected conversation result to be filtered: %+v", read)
	}
}
