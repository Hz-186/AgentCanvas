package toolruntime

import (
	"context"
	"encoding/json"
	"testing"

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
	return []memory.Memory{{ID: 7, OwnerID: ownerID, MemoryType: memory.TypeSummary, Content: "remember this"}}, nil
}

func (r *fakeMemoryRepo) SoftDelete(ctx context.Context, ownerID, id int64) error { return nil }

func (r *fakeMemoryRepo) MarkUsed(ctx context.Context, ownerID int64, ids []int64) error {
	r.marked = ids
	return nil
}

func (r *fakeMemoryRepo) ListByLevel(ctx context.Context, ownerID int64, level string, memoryTypes []string, limit int) ([]memory.Memory, error) {
	return nil, nil
}
func (r *fakeMemoryRepo) ListActiveOwnerIDs(ctx context.Context, limit int) ([]int64, error) {
	return nil, nil
}
func (r *fakeMemoryRepo) IncrementAccessCount(ctx context.Context, ownerID int64, id int64) error {
	return nil
}
func (r *fakeMemoryRepo) IncrementConsolidationCount(ctx context.Context, ownerID int64, id int64) error {
	return nil
}
func (r *fakeMemoryRepo) MarkExpired(ctx context.Context, ownerID int64, maxAgeDays int) (int64, error) {
	return 0, nil
}
func (r *fakeMemoryRepo) UpdateDecayedImportance(ctx context.Context, ownerID int64, decayRate float64) (int64, error) {
	return 0, nil
}
func (r *fakeMemoryRepo) SetEmbedding(ctx context.Context, ownerID, id int64, embedding []byte) error {
	return nil
}

type fakeMemoryLogRepo struct {
	items []memory.WriteLog
}

type fakeMemoryCandidateWriter struct {
	request memory.CandidateRequest
	id      int64
}

func (f *fakeMemoryCandidateWriter) Suggest(_ context.Context, request memory.CandidateRequest) (int64, error) {
	f.request = request
	if f.id == 0 {
		f.id = 11
	}
	return f.id, nil
}

func (r *fakeMemoryLogRepo) Create(ctx context.Context, item *memory.WriteLog) error {
	r.items = append(r.items, *item)
	return nil
}

func (r *fakeMemoryLogRepo) ListByRun(ctx context.Context, ownerID, runID int64) ([]memory.WriteLog, error) {
	return r.items, nil
}

func TestMemoryReadToolReadsAndMarksMemory(t *testing.T) {
	repo := &fakeMemoryRepo{}
	tool := MemoryReadTool{Memories: repo, AllowLegacyListFallback: true}
	result, err := tool.Execute(context.Background(), ToolRunContext{OwnerID: 1}, json.RawMessage(`{"memory_types":["summary_memory"],"limit":3}`))
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

func TestMemoryReadToolRequiresUnifiedVectorIndexByDefault(t *testing.T) {
	repo := &fakeMemoryRepo{}
	tool := MemoryReadTool{Memories: repo}

	result, err := tool.Execute(context.Background(), ToolRunContext{OwnerID: 1, RunID: 2, Task: "relevant fact"}, json.RawMessage(`{"limit":3}`))
	if err == nil || result != nil {
		t.Fatalf("expected missing vector index configuration error, result=%+v err=%v", result, err)
	}
	if repo.readReq.limit != 0 {
		t.Fatalf("memory list fallback must not be used: %+v", repo.readReq)
	}
}

func TestMemoryWriteToolCreatesReviewCandidate(t *testing.T) {
	candidates := &fakeMemoryCandidateWriter{}
	tool := MemoryWriteTool{Candidates: candidates}
	result, err := tool.Execute(context.Background(), ToolRunContext{OwnerID: 1, RunID: 2}, json.RawMessage(`{
		"memory_type":"task_memory",
		"title":"Preference",
		"content":"User prefers concise answers",
		"importance":0.8,
		"reason":"User stated preference"
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if candidates.request.Content != "User prefers concise answers" || candidates.request.RunID != 2 {
		t.Fatalf("unexpected candidate: %+v", candidates.request)
	}
	var output map[string]any
	if err := json.Unmarshal(result.ContentJSON, &output); err != nil {
		t.Fatal(err)
	}
	if output["action"] != "suggest" || output["status"] != "pending" {
		t.Fatalf("unexpected output: %+v", output)
	}
}

type fakeSemanticRetriever struct {
	ids []int64
}

func (r *fakeSemanticRetriever) Index(ctx context.Context, item memory.Memory) error {
	return nil
}
func (r *fakeSemanticRetriever) Search(ctx context.Context, ownerID int64, query string, memoryTypes []string, limit int) ([]int64, error) {
	return r.ids, nil
}
func (r *fakeSemanticRetriever) Delete(ctx context.Context, memoryID int64) error { return nil }

var _ memory.SemanticRetriever = (*fakeSemanticRetriever)(nil)

func TestMemoryReadToolWithSemanticQuery(t *testing.T) {
	repo := &fakeMemoryRepo{}
	repo.Create(context.Background(), &memory.Memory{ID: 42, OwnerID: 1, Content: "user prefers dark mode"})
	retriever := &fakeSemanticRetriever{ids: []int64{42}}
	tool := MemoryReadTool{Memories: repo, Retriever: retriever}

	result, err := tool.Execute(context.Background(), ToolRunContext{OwnerID: 1}, json.RawMessage(`{
		"query": "dark mode",
		"limit": 5
	}`))
	if err != nil {
		t.Fatal(err)
	}
	var output map[string]any
	if err := json.Unmarshal(result.ContentJSON, &output); err != nil {
		t.Fatal(err)
	}
	if output["count"].(float64) != 1 {
		t.Fatalf("expected 1 result from semantic search, got %v", output["count"])
	}
	if output["query"] != "dark mode" {
		t.Fatalf("expected query in output, got %v", output["query"])
	}
}

func TestMemoryReadToolFallsBackWithoutQuery(t *testing.T) {
	repo := &fakeMemoryRepo{}
	retriever := &fakeSemanticRetriever{ids: []int64{99}}
	tool := MemoryReadTool{Memories: repo, Retriever: retriever, AllowLegacyListFallback: true}

	_, err := tool.Execute(context.Background(), ToolRunContext{OwnerID: 1}, json.RawMessage(`{
		"memory_types": ["summary_memory"],
		"limit": 3
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if repo.readReq.limit != 3 {
		t.Fatalf("expected fallback to ListForRead with limit 3, got %+v", repo.readReq)
	}
}

func TestMemoryReadToolFallsBackWhenSearchFails(t *testing.T) {
	repo := &fakeMemoryRepo{}
	retriever := &fakeSemanticRetriever{ids: nil}
	tool := MemoryReadTool{Memories: repo, Retriever: retriever, AllowLegacyListFallback: true}

	_, err := tool.Execute(context.Background(), ToolRunContext{OwnerID: 1}, json.RawMessage(`{
		"query": "something",
		"memory_types": ["profile_memory"],
		"limit": 3
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if repo.readReq.limit != 3 {
		t.Fatalf("expected fallback to ListForRead, got %+v", repo.readReq)
	}
}

func TestMemoryWriteToolNeverSelfApprovesConflictingFact(t *testing.T) {
	candidates := &fakeMemoryCandidateWriter{}
	tool := MemoryWriteTool{Candidates: candidates}
	result, err := tool.Execute(context.Background(), ToolRunContext{OwnerID: 1, RunID: 2}, json.RawMessage(`{"memory_type":"profile_memory","title":"response style","content":"User prefers detailed answers"}`))
	if err != nil || result == nil || result.Approval != nil || candidates.request.Content == "" {
		t.Fatalf("expected pending proposal without direct memory approval, result=%+v candidate=%+v err=%v", result, candidates.request, err)
	}
}
