package memory

import (
	"context"
	"testing"
)

type runtimeArchivalFake struct {
	indexed Memory
	ids     []int64
}

type runtimeSemanticFake struct {
	ids   []int64
	query string
}

func (f *runtimeSemanticFake) Index(context.Context, Memory) error { return nil }
func (f *runtimeSemanticFake) Search(_ context.Context, _ int64, query string, _ []string, _ int) ([]int64, error) {
	f.query = query
	return f.ids, nil
}
func (f *runtimeSemanticFake) Delete(context.Context, int64) error { return nil }

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
func (r *runtimeRepoFake) FindByIDs(ctx context.Context, ownerID int64, ids []int64) ([]Memory, error) {
	items := make([]Memory, 0, len(ids))
	for _, id := range ids {
		if item, err := r.FindByID(ctx, ownerID, id); err == nil {
			items = append(items, *item)
		}
	}
	return items, nil
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

func TestRuntimeServiceSemanticOnlyNeverListsRecentMemories(t *testing.T) {
	repo := &runtimeRepoFake{}
	service := RuntimeService{Memories: repo, Retriever: &runtimeSemanticFake{}}
	if _, err := service.Read(context.Background(), ReadRequest{OwnerID: 1, SemanticOnly: true}); err == nil {
		t.Fatal("expected semantic query requirement")
	}
}

func TestRuntimeServiceExcludesWorkingAndConflictingMemoriesFromSemanticRecall(t *testing.T) {
	repo := &runtimeRepoFake{items: map[int64]*Memory{
		1: {ID: 1, OwnerID: 1, MemoryType: TypeProfile, MemoryLevel: LevelWorking, Content: "working state"},
		2: {ID: 2, OwnerID: 1, MemoryType: TypeProfile, MemoryLevel: LevelLongTerm, ConflictFlag: true, Content: "disputed fact"},
		3: {ID: 3, OwnerID: 1, MemoryType: TypeProfile, MemoryLevel: LevelShortTerm, Content: "relevant fact"},
	}}
	service := RuntimeService{Memories: repo, Retriever: &runtimeSemanticFake{ids: []int64{1, 2, 3}}}
	result, err := service.Read(context.Background(), ReadRequest{OwnerID: 1, MemoryTypes: []string{TypeProfile}, Query: "fact", SemanticOnly: true})
	if err != nil || result.Count != 1 || result.Memories[0].ID != 3 {
		t.Fatalf("semantic recall must exclude working/conflicting memories: %+v err=%v", result, err)
	}
}

func TestRuntimeServiceDetectsConflictingMemoryBeforeWrite(t *testing.T) {
	existing := &Memory{ID: 9, OwnerID: 1, MemoryType: TypeProfile, Title: "response style", Content: "User prefers concise answers"}
	repo := &runtimeRepoFake{items: map[int64]*Memory{9: existing}}
	retriever := &runtimeSemanticFake{ids: []int64{9}}
	service := RuntimeService{Memories: repo, Retriever: retriever}
	result, err := service.Write(context.Background(), WriteRequest{OwnerID: 1, MemoryType: TypeProfile, Title: "response style", Content: "User prefers detailed answers"})
	if err != nil || result.Action != WriteActionConflict || result.Conflict == nil || len(result.Conflict.Options) != 3 {
		t.Fatalf("expected conflict options, result=%+v err=%v", result, err)
	}
	if len(repo.items) != 1 {
		t.Fatalf("conflicting write must not persist before approval: %+v", repo.items)
	}
}

func TestRuntimeServiceRejectsConflictResolutionForDifferentMemory(t *testing.T) {
	existing := &Memory{ID: 9, OwnerID: 1, MemoryType: TypeProfile, Title: "response style", Content: "User prefers concise answers"}
	repo := &runtimeRepoFake{items: map[int64]*Memory{9: existing}}
	service := RuntimeService{Memories: repo, Retriever: &runtimeSemanticFake{ids: []int64{9}}}
	if _, err := service.Write(context.Background(), WriteRequest{OwnerID: 1, MemoryType: TypeProfile, Title: "response style", Content: "User prefers detailed answers", ConflictResolution: "replace:99"}); err == nil {
		t.Fatal("expected resolution for a different memory to be rejected")
	}
}

func TestRuntimeServiceKeepBothPreservesConflictParent(t *testing.T) {
	existing := &Memory{ID: 9, OwnerID: 1, MemoryType: TypeProfile, Title: "response style", Content: "User prefers concise answers"}
	repo := &runtimeRepoFake{items: map[int64]*Memory{9: existing}}
	service := RuntimeService{Memories: repo, Retriever: &runtimeSemanticFake{ids: []int64{9}}}
	result, err := service.Write(context.Background(), WriteRequest{OwnerID: 1, MemoryType: TypeProfile, Title: "response style", Content: "User prefers detailed answers", ConflictResolution: "keep_both"})
	if err != nil || result.Action != WriteActionCreate || result.Memory.ParentID == nil || *result.Memory.ParentID != 9 {
		t.Fatalf("keep_both must preserve parent lineage: result=%+v err=%v", result, err)
	}
}
