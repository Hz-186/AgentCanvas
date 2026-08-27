package toolruntime

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"agentcanvas/internal/domain"
	"agentcanvas/internal/domain/contextresource"
	"agentcanvas/internal/domain/conversation"
	"agentcanvas/internal/domain/memory"
)

type fakeSessionSearchIndex struct {
	request conversation.MessageSearchRequest
}

func (*fakeSessionSearchIndex) EnsureIndex(context.Context) error { return nil }
func (*fakeSessionSearchIndex) IndexMessage(context.Context, int64, int64, *conversation.Message) error {
	return nil
}
func (f *fakeSessionSearchIndex) SearchMessages(_ context.Context, request conversation.MessageSearchRequest) ([]conversation.MessageSearchResult, error) {
	f.request = request
	return []conversation.MessageSearchResult{{MessageID: 9, AgentID: request.AgentID, Content: "prior decision"}}, nil
}
func (*fakeSessionSearchIndex) DeleteConversation(context.Context, int64, int64, int64) error {
	return nil
}

func TestSessionSearchToolForcesTenantAndAgentScope(t *testing.T) {
	index := &fakeSessionSearchIndex{}
	tool := SessionSearchTool{Index: index}
	result, err := tool.Execute(context.Background(), ToolRunContext{OwnerID: 3, AgentID: 7}, json.RawMessage(`{"query":"decision","limit":5}`))
	if err != nil {
		t.Fatal(err)
	}
	if index.request.OwnerID != 3 || index.request.AgentID != 7 || index.request.Query != "decision" {
		t.Fatalf("unexpected search scope: %+v", index.request)
	}
	if result == nil || result.IsError {
		t.Fatalf("unexpected result: %+v", result)
	}
}

type fakeMemoryRepo struct {
	items   map[int64]*memory.Memory
	readReq struct {
		ownerID int64
		types   []string
		limit   int
		project *int64
	}
	marked []int64
}

func (r *fakeMemoryRepo) Create(ctx context.Context, item *memory.Memory) error {
	if r.items == nil {
		r.items = map[int64]*memory.Memory{}
	}
	if item.ID == 0 {
		item.ID = int64(len(r.items) + 1)
	}
	clone := *item
	r.items[item.ID] = &clone
	*item = clone
	return nil
}

func (r *fakeMemoryRepo) Update(ctx context.Context, item *memory.Memory) error {
	if r.items == nil {
		r.items = map[int64]*memory.Memory{}
	}
	clone := *item
	r.items[item.ID] = &clone
	return nil
}

func (r *fakeMemoryRepo) FindByID(ctx context.Context, ownerID, id int64) (*memory.Memory, error) {
	item := r.items[id]
	clone := *item
	return &clone, nil
}
func (r *fakeMemoryRepo) FindByIDs(ctx context.Context, ownerID int64, ids []int64) ([]memory.Memory, error) {
	items := make([]memory.Memory, 0, len(ids))
	for _, id := range ids {
		if item, err := r.FindByID(ctx, ownerID, id); err == nil {
			items = append(items, *item)
		}
	}
	return items, nil
}

func (r *fakeMemoryRepo) List(ctx context.Context, ownerID int64, memoryTypes []string, conversationID *int64, limit, offset int) ([]memory.Memory, error) {
	items := make([]memory.Memory, 0, len(r.items))
	for _, item := range r.items {
		if item.OwnerID != ownerID {
			continue
		}
		if len(memoryTypes) > 0 {
			matched := false
			for _, memoryType := range memoryTypes {
				if item.MemoryType == memoryType {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
		}
		items = append(items, *item)
	}
	return items, nil
}

func (r *fakeMemoryRepo) ListForRead(ctx context.Context, ownerID int64, memoryTypes []string, conversationID *int64, limit int) ([]memory.Memory, error) {
	r.readReq.ownerID = ownerID
	r.readReq.types = memoryTypes
	r.readReq.limit = limit
	return []memory.Memory{{SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 7, OwnerID: ownerID}}, Status: memory.StatusActive, ScopeType: memory.ScopeUser, ScopeID: ownerID, MemoryType: memory.TypeProfile, RetentionTier: memory.TierLongTerm, Content: "remember this"}}, nil
}

func (r *fakeMemoryRepo) ListForReadScoped(ctx context.Context, ownerID, _ int64, memoryTypes []string, conversationID, projectID *int64, limit int) ([]memory.Memory, error) {
	if projectID != nil {
		value := *projectID
		r.readReq.project = &value
	}
	return r.ListForRead(ctx, ownerID, memoryTypes, conversationID, limit)
}

func (r *fakeMemoryRepo) SoftDelete(ctx context.Context, ownerID, id int64) error { return nil }

func (r *fakeMemoryRepo) MarkUsed(ctx context.Context, ownerID int64, ids []int64) error {
	r.marked = ids
	return nil
}

func (r *fakeMemoryRepo) ListByLevel(ctx context.Context, ownerID int64, level string, memoryTypes []string, limit int) ([]memory.Memory, error) {
	return nil, nil
}

func (r *fakeMemoryRepo) ListBySources(ctx context.Context, ownerID int64, sources []string, limit int) ([]memory.Memory, error) {
	return nil, nil
}
func (r *fakeMemoryRepo) ListActiveOwnerIDs(ctx context.Context, limit int) ([]int64, error) {
	return nil, nil
}
func (r *fakeMemoryRepo) IncrementUsageCount(ctx context.Context, ownerID int64, id int64) error {
	return nil
}
func (r *fakeMemoryRepo) IncrementPromotionCount(ctx context.Context, ownerID int64, id int64) error {
	return nil
}
func (r *fakeMemoryRepo) MarkExpired(ctx context.Context, ownerID int64, maxAgeDays int) (int64, error) {
	return 0, nil
}
func (r *fakeMemoryRepo) UpdateDecayedImportance(ctx context.Context, ownerID int64, decayRate float64) (int64, error) {
	return 0, nil
}

func TestMemoryReadToolReadsAndMarksMemory(t *testing.T) {
	repo := &fakeMemoryRepo{}
	tool := MemoryReadTool{Memories: repo, AllowLegacyListFallback: true}
	result, err := tool.Execute(context.Background(), ToolRunContext{OwnerID: 1}, json.RawMessage(`{"memory_types":["profile"],"limit":3}`))
	if err != nil {
		t.Fatal(err)
	}
	if repo.readReq.ownerID != 1 || repo.readReq.limit != 3 || len(repo.marked) != 1 || repo.marked[0] != 7 {
		t.Fatalf("unexpected repo calls: %+v marked=%v", repo.readReq, repo.marked)
	}
	var output map[string]any
	if err := json.Unmarshal(result.ContentJSON, &output); err != nil {
		t.Fatal(err)
	}
	if output["memory_context"] != "remember this" || output["count"].(float64) != 1 {
		t.Fatalf("unexpected output: %+v", output)
	}
}

func TestMemoryReadToolUsesProjectWithoutWorkspace(t *testing.T) {
	repo := &fakeMemoryRepo{}
	tool := MemoryReadTool{Memories: repo, AllowLegacyListFallback: true}
	if _, err := tool.Execute(context.Background(), ToolRunContext{OwnerID: 1, ProjectID: 42}, json.RawMessage(`{"limit":3}`)); err != nil {
		t.Fatal(err)
	}
	if repo.readReq.project == nil || *repo.readReq.project != 42 {
		t.Fatalf("project scope was not used without a workspace: %+v", repo.readReq)
	}
}

func TestMemoryReadToolRequiresUnifiedKeywordIndexByDefault(t *testing.T) {
	repo := &fakeMemoryRepo{}
	tool := MemoryReadTool{Memories: repo}

	result, err := tool.Execute(context.Background(), ToolRunContext{OwnerID: 1, RunID: 2, Task: "relevant fact"}, json.RawMessage(`{"limit":3}`))
	if err == nil || result != nil {
		t.Fatalf("expected missing keyword index configuration error, result=%+v err=%v", result, err)
	}
	if repo.readReq.limit != 0 {
		t.Fatalf("memory list fallback must not be used: %+v", repo.readReq)
	}
}

type fakeContextKeywordIndex struct {
	hits []contextresource.SearchResult
	err  error
	mode string
	topK int
}

func (f *fakeContextKeywordIndex) Upsert(context.Context, contextresource.Document, contextresource.EmbeddingProfile) (contextresource.EmbeddingProfile, error) {
	return contextresource.EmbeddingProfile{}, nil
}
func (f *fakeContextKeywordIndex) Delete(context.Context, contextresource.OutboxItem) error {
	return nil
}
func (f *fakeContextKeywordIndex) Search(_ context.Context, request contextresource.SearchRequest) ([]contextresource.SearchResult, error) {
	f.mode = request.Mode
	f.topK = request.TopK
	return f.hits, f.err
}

func TestMemoryReadToolWithKeywordQuery(t *testing.T) {
	repo := &fakeMemoryRepo{}
	repo.Create(context.Background(), &memory.Memory{SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 42, OwnerID: 1}}, Status: memory.StatusActive, ScopeType: memory.ScopeUser, ScopeID: 1, MemoryType: memory.TypeProfile, RetentionTier: memory.TierLongTerm, Content: "user prefers dark mode"})
	index := &fakeContextKeywordIndex{hits: []contextresource.SearchResult{{ResourceType: contextresource.TypeLongTermMemory, ResourceID: "42", Score: 0.9}}}
	tool := MemoryReadTool{Memories: repo, ContextIndex: index}

	result, err := tool.Execute(context.Background(), ToolRunContext{OwnerID: 1}, json.RawMessage(`{
		"query": "dark mode",
		"limit": 5
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if index.mode != "keyword" {
		t.Fatalf("read_memory must search the keyword index, got mode %q", index.mode)
	}
	var output map[string]any
	if err := json.Unmarshal(result.ContentJSON, &output); err != nil {
		t.Fatal(err)
	}
	if output["count"].(float64) != 1 {
		t.Fatalf("expected 1 result from keyword search, got %v", output["count"])
	}
	if output["query"] != "dark mode" {
		t.Fatalf("expected query in output, got %v", output["query"])
	}
}

func TestMemoryReadToolFallsBackWithoutQuery(t *testing.T) {
	repo := &fakeMemoryRepo{}
	tool := MemoryReadTool{Memories: repo, AllowLegacyListFallback: true}

	_, err := tool.Execute(context.Background(), ToolRunContext{OwnerID: 1}, json.RawMessage(`{
		"memory_types": ["profile"],
		"limit": 3
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if repo.readReq.limit != 3 {
		t.Fatalf("expected fallback to ListForRead with limit 3, got %+v", repo.readReq)
	}
}

func TestMemoryReadToolReturnsErrorWhenKeywordIndexUnavailable(t *testing.T) {
	repo := &fakeMemoryRepo{}
	index := &fakeContextKeywordIndex{err: errors.New("es unavailable")}
	tool := MemoryReadTool{Memories: repo, ContextIndex: index, AllowLegacyListFallback: true}

	result, err := tool.Execute(context.Background(), ToolRunContext{OwnerID: 1}, json.RawMessage(`{
		"query": "something",
		"memory_types": ["profile"],
		"limit": 3
	}`))
	if err == nil || result == nil || !result.IsError {
		t.Fatalf("expected an observable keyword index error, result=%+v err=%v", result, err)
	}
	if repo.readReq.limit != 0 {
		t.Fatalf("full-table ListForRead fallback must not be used: %+v", repo.readReq)
	}
}
