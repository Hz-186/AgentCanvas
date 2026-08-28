package memory

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"agentcanvas/internal/domain"
	"agentcanvas/internal/domain/contextresource"
)

func TestResolveScopeRejectsLegacyMemoryTypes(t *testing.T) {
	if _, _, err := ResolveScope("profile_memory", 1, 2, 3, 4, ScopeUser, 1); err == nil {
		t.Fatal("expected legacy memory type to be rejected")
	}
}

type runtimeRepoFake struct {
	items  map[int64]*Memory
	marked []int64
}

type runtimeRecallLogFake struct {
	item *RecallLog
	err  error
}

func (f *runtimeRecallLogFake) Create(_ context.Context, item *RecallLog) error {
	if f.err != nil {
		return f.err
	}
	clone := *item
	f.item = &clone
	return nil
}
func (f *runtimeRecallLogFake) List(context.Context, int64, int64, int) ([]RecallLog, error) {
	return nil, nil
}
func (f *runtimeRecallLogFake) SetFeedback(context.Context, int64, int64, string) error {
	return nil
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
func (r *runtimeRepoFake) ListBySources(context.Context, int64, []string, int) ([]Memory, error) {
	return nil, nil
}
func (r *runtimeRepoFake) MarkUsed(_ context.Context, _ int64, ids []int64) error {
	r.marked = ids
	return nil
}

// runtimeKeywordIndexFake splits keyword and vector routing exactly like the
// production context backend: keyword mode is the only allowed search mode for
// memory detail reads, and any vector/hybrid request is recorded and fails.
type runtimeKeywordIndexFake struct {
	hits         []contextresource.SearchResult
	err          error
	mode         string
	topK         int
	keywordCalls int
	vectorCalls  int
}

func (f *runtimeKeywordIndexFake) Upsert(context.Context, contextresource.Document, contextresource.EmbeddingProfile) (contextresource.EmbeddingProfile, error) {
	return contextresource.EmbeddingProfile{}, nil
}
func (f *runtimeKeywordIndexFake) Delete(context.Context, contextresource.OutboxItem) error {
	return nil
}
func (f *runtimeKeywordIndexFake) Search(_ context.Context, request contextresource.SearchRequest) ([]contextresource.SearchResult, error) {
	switch strings.ToLower(strings.TrimSpace(request.Mode)) {
	case "", "keyword":
		f.keywordCalls++
	default:
		f.vectorCalls++
		return nil, errors.New("vector branch must not run")
	}
	f.mode = request.Mode
	f.topK = request.TopK
	return f.hits, f.err
}

// runtimeHydrationFake returns SQL rows in reverse request order to prove the
// runtime re-ranks by ES score instead of trusting the hydration order.
type runtimeHydrationFake struct {
	*runtimeRepoFake
	hydrated  []int64
	listCalls int
}

func (r *runtimeHydrationFake) FindByIDs(_ context.Context, _ int64, ids []int64) ([]Memory, error) {
	r.hydrated = append([]int64(nil), ids...)
	items := make([]Memory, 0, len(ids))
	for i := len(ids) - 1; i >= 0; i-- {
		if item, err := r.runtimeRepoFake.FindByID(context.Background(), 0, ids[i]); err == nil {
			items = append(items, *item)
		}
	}
	return items, nil
}

func (r *runtimeHydrationFake) ListForRead(context.Context, int64, []string, *int64, int) ([]Memory, error) {
	r.listCalls++
	return nil, nil
}

func TestRuntimeMemoryReadTest(t *testing.T) {
	readable := func(id, ownerID int64, content string) *Memory {
		return &Memory{SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: id, OwnerID: ownerID}}, Status: StatusActive,
			ScopeType: ScopeUser, ScopeID: ownerID, MemoryType: TypeProfile, RetentionTier: TierLongTerm, Content: content}
	}
	t.Run("shouldReturnKeywordScoreOrder", func(t *testing.T) {
		repo := &runtimeHydrationFake{runtimeRepoFake: &runtimeRepoFake{items: map[int64]*Memory{
			1: readable(1, 1, "first fact"),
			2: readable(2, 1, "second fact"),
			3: readable(3, 1, "third fact"),
		}}}
		index := &runtimeKeywordIndexFake{hits: []contextresource.SearchResult{
			{ResourceID: "1", Score: 4.2},
			{ResourceID: "2", Score: 2.8},
			{ResourceID: "3", Score: 1.1},
		}}
		service := RuntimeService{Memories: repo, ContextIndex: index}
		result, err := service.Read(context.Background(), ReadRequest{OwnerID: 1, Query: "fact", Limit: 5})
		if err != nil {
			t.Fatal(err)
		}
		if result.Count != 3 || result.Memories[0].ID != 1 || result.Memories[1].ID != 2 || result.Memories[2].ID != 3 {
			t.Fatalf("response must follow ES score order after SQL hydration: %+v", result.Memories)
		}
	})
	t.Run("shouldSkipVectorBranch", func(t *testing.T) {
		repo := &runtimeHydrationFake{runtimeRepoFake: &runtimeRepoFake{items: map[int64]*Memory{
			1: readable(1, 1, "only fact"),
		}}}
		index := &runtimeKeywordIndexFake{hits: []contextresource.SearchResult{{ResourceID: "1", Score: 3}}}
		service := RuntimeService{Memories: repo, ContextIndex: index}
		result, err := service.Read(context.Background(), ReadRequest{OwnerID: 1, Query: "fact"})
		if err != nil {
			t.Fatal(err)
		}
		if result.Count != 1 || index.keywordCalls != 1 || index.vectorCalls != 0 {
			t.Fatalf("keyword hits must never trigger the vector branch: keywordCalls=%d vectorCalls=%d mode=%q result=%+v",
				index.keywordCalls, index.vectorCalls, index.mode, result)
		}
	})
	t.Run("shouldReturnEmptyWhenIndexUnavailable", func(t *testing.T) {
		repo := &runtimeHydrationFake{runtimeRepoFake: &runtimeRepoFake{items: map[int64]*Memory{}}}
		index := &runtimeKeywordIndexFake{err: errors.New("es unavailable")}
		service := RuntimeService{Memories: repo, ContextIndex: index}
		_, err := service.Read(context.Background(), ReadRequest{OwnerID: 1, Query: "fact", AllowLegacyListFallback: true})
		if err == nil {
			t.Fatal("expected an observable error when the keyword index is unavailable")
		}
		if repo.listCalls != 0 {
			t.Fatalf("full-table list fallback must not run when the index throws: %d calls", repo.listCalls)
		}
	})
	t.Run("shouldEnforceScopeAndTruncation", func(t *testing.T) {
		repo := &runtimeHydrationFake{runtimeRepoFake: &runtimeRepoFake{items: map[int64]*Memory{
			1:  readable(1, 1, strings.Repeat("x", 8000)),
			99: readable(99, 999, "foreign owner fact"),
		}}}
		index := &runtimeKeywordIndexFake{hits: []contextresource.SearchResult{
			{ResourceID: "1", Score: 3},
			{ResourceID: "99", Score: 2},
		}}
		service := RuntimeService{Memories: repo, ContextIndex: index}
		result, err := service.Read(context.Background(), ReadRequest{OwnerID: 1, Query: "fact", Limit: 25})
		if err != nil {
			t.Fatal(err)
		}
		if index.topK != 40 {
			t.Fatalf("requested limit 25 must be capped at 20 before search (TopK 40), got %d", index.topK)
		}
		if result.Count != 1 || result.Memories[0].ID != 1 {
			t.Fatalf("foreign-owner hit must be omitted during hydration: %+v", result.Memories)
		}
		if got := len([]rune(result.Memories[0].Content)); got > 6000 {
			t.Fatalf("per-entry content must be truncated to 6000 chars, got %d", got)
		}
	})
	t.Run("shouldUseStableIDTieBreak", func(t *testing.T) {
		repo := &runtimeHydrationFake{runtimeRepoFake: &runtimeRepoFake{items: map[int64]*Memory{
			7:  readable(7, 1, "seven"),
			12: readable(12, 1, "twelve"),
		}}}
		index := &runtimeKeywordIndexFake{hits: []contextresource.SearchResult{
			{ResourceID: "12", Score: 2},
			{ResourceID: "7", Score: 2},
		}}
		service := RuntimeService{Memories: repo, ContextIndex: index}
		result, err := service.Read(context.Background(), ReadRequest{OwnerID: 1, Query: "fact"})
		if err != nil {
			t.Fatal(err)
		}
		if result.Count != 2 || result.Memories[0].ID != 7 || result.Memories[1].ID != 12 {
			t.Fatalf("equal ES scores must tie-break by ascending memory ID: %+v", result.Memories)
		}
		if len(repo.hydrated) != 2 || repo.hydrated[0] != 7 || repo.hydrated[1] != 12 {
			t.Fatalf("SQL hydration must receive the ranked IDs: %v", repo.hydrated)
		}
	})
}

// runtimeKeywordHits turns memory IDs into keyword hits with descending
// scores so the ES score order matches the given ID order.
func runtimeKeywordHits(ids ...int64) []contextresource.SearchResult {
	hits := make([]contextresource.SearchResult, 0, len(ids))
	for i, id := range ids {
		hits = append(hits, contextresource.SearchResult{ResourceID: strconv.FormatInt(id, 10), Score: float64(len(ids) - i)})
	}
	return hits
}

func TestRuntimeServiceFiltersOtherConversation(t *testing.T) {
	wanted, other := int64(7), int64(8)
	repo := &runtimeRepoFake{items: map[int64]*Memory{1: {SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 1, OwnerID: 1}}, SourceConversationID: &other, ScopeType: ScopeConversation, ScopeID: other, MemoryType: TypeArchival, Content: "private"}}}
	service := RuntimeService{Memories: repo, ContextIndex: &runtimeKeywordIndexFake{hits: runtimeKeywordHits(1)}}
	read, err := service.Read(context.Background(), ReadRequest{OwnerID: 1, ConversationID: &wanted, MemoryTypes: []string{TypeArchival}, Query: "private"})
	if err != nil {
		t.Fatal(err)
	}
	if read.Count != 0 {
		t.Fatalf("expected conversation result to be filtered: %+v", read)
	}
}

func TestRuntimeServiceExcludesWorkingAndConflictingMemoriesFromSemanticRecall(t *testing.T) {
	repo := &runtimeRepoFake{items: map[int64]*Memory{
		1: {SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 1, OwnerID: 1}}, ScopeType: ScopeUser, ScopeID: 1, MemoryType: TypeProfile, RetentionTier: "unknown", Content: "unknown retention tier"},
		2: {SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 2, OwnerID: 1}}, ScopeType: ScopeUser, ScopeID: 1, MemoryType: TypeProfile, RetentionTier: TierLongTerm, HasConflict: true, Content: "disputed fact"},
		3: {SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 3, OwnerID: 1}}, ScopeType: ScopeUser, ScopeID: 1, MemoryType: TypeProfile, RetentionTier: TierShortTerm, Content: "relevant fact"},
	}}
	service := RuntimeService{Memories: repo, ContextIndex: &runtimeKeywordIndexFake{hits: runtimeKeywordHits(1, 2, 3)}}
	result, err := service.Read(context.Background(), ReadRequest{OwnerID: 1, MemoryTypes: []string{TypeProfile}, Query: "fact", SemanticOnly: true})
	if err != nil || result.Count != 1 || result.Memories[0].ID != 3 {
		t.Fatalf("semantic recall must exclude unknown/conflicting memories: %+v err=%v", result, err)
	}
}

func TestRuntimeServiceExcludesInactiveExpiredAndCrossScopeMemories(t *testing.T) {
	conversationID := int64(7)
	expired, deleted := time.Now().Add(-time.Minute), time.Now().Add(-time.Minute)
	repo := &runtimeRepoFake{items: map[int64]*Memory{
		1: {SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 1, OwnerID: 1}}, ScopeType: ScopeUser, ScopeID: 1, Status: StatusRevoked, MemoryType: TypeProfile, RetentionTier: TierLongTerm, Content: "revoked"},
		2: {SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 2, OwnerID: 1}}, ScopeType: ScopeConversation, ScopeID: 8, Status: StatusActive, MemoryType: TypeProfile, RetentionTier: TierLongTerm, Content: "other conversation"},
		3: {SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 3, OwnerID: 1}}, ScopeType: ScopeUser, ScopeID: 1, Status: StatusActive, MemoryType: TypeProfile, RetentionTier: TierLongTerm, Content: "expired", ExpiresAt: &expired},
		4: {SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 4, OwnerID: 1}}, ScopeType: ScopeConversation, ScopeID: 7, Status: StatusActive, MemoryType: TypeProfile, RetentionTier: TierLongTerm, Content: "current"},
		5: {SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 5, OwnerID: 1}}, ScopeType: ScopeUser, ScopeID: 1, Status: StatusSuperseded, MemoryType: TypeProfile, RetentionTier: TierLongTerm, Content: "superseded"},
		6: {SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 6, OwnerID: 1}}, ScopeType: ScopeUser, ScopeID: 1, Status: StatusActive, HasConflict: true, MemoryType: TypeProfile, RetentionTier: TierLongTerm, Content: "conflicting"},
		7: {SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 7, OwnerID: 1}, DeletedAt: &deleted}, ScopeType: ScopeUser, ScopeID: 1, Status: StatusActive, MemoryType: TypeProfile, RetentionTier: TierLongTerm, Content: "deleted"},
	}}
	service := RuntimeService{Memories: repo, ContextIndex: &runtimeKeywordIndexFake{hits: runtimeKeywordHits(1, 2, 3, 4, 5, 6, 7)}}
	result, err := service.Read(context.Background(), ReadRequest{OwnerID: 1, ConversationID: &conversationID, Query: "preference", SemanticOnly: true})
	if err != nil || result.Count != 1 || result.Memories[0].ID != 4 {
		t.Fatalf("recall must enforce lifecycle and scope: result=%+v err=%v", result, err)
	}
}

func TestRuntimeServiceIsolatesProjectMemoryAcrossProjects(t *testing.T) {
	projectID, firstConversation, secondConversation := int64(42), int64(7), int64(8)
	repo := &runtimeRepoFake{items: map[int64]*Memory{
		1: {SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 1, OwnerID: 1}}, ScopeType: ScopeProject, ScopeID: 42, SourceProjectID: &projectID, Status: StatusActive, MemoryType: TypeTask, RetentionTier: TierLongTerm, Content: "project 42 fact"},
		2: {SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 2, OwnerID: 1}}, ScopeType: ScopeProject, ScopeID: 99, Status: StatusActive, MemoryType: TypeTask, RetentionTier: TierLongTerm, Content: "project 99 fact"},
		3: {SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 3, OwnerID: 1}}, ScopeType: ScopeUser, ScopeID: 1, Status: StatusActive, MemoryType: TypeProfile, RetentionTier: TierLongTerm, Content: "shared user preference"},
	}}
	service := RuntimeService{Memories: repo, ContextIndex: &runtimeKeywordIndexFake{hits: runtimeKeywordHits(1, 2, 3)}}
	for _, conversationID := range []*int64{&firstConversation, &secondConversation} {
		result, err := service.Read(context.Background(), ReadRequest{OwnerID: 1, ProjectID: 42, ConversationID: conversationID, Query: "fact", SemanticOnly: true})
		if err != nil || result.Count != 2 {
			t.Fatalf("project recall should cross conversations but include only the current project and user scope: result=%+v err=%v", result, err)
		}
		for _, item := range result.Memories {
			if item.ID == 2 {
				t.Fatalf("cross-project memory leaked into recall: %+v", result.Memories)
			}
		}
	}
}

func TestRuntimeServiceIsolatesAllMemoryScopes(t *testing.T) {
	conversationID := int64(10)
	repo := &runtimeRepoFake{items: map[int64]*Memory{
		1: {SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 1, OwnerID: 1}}, ScopeType: ScopeUser, ScopeID: 1, Status: StatusActive, MemoryType: TypeProfile, RetentionTier: TierLongTerm, Content: "user"},
		2: {SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 2, OwnerID: 1}}, ScopeType: ScopeAgent, ScopeID: 7, Status: StatusActive, MemoryType: TypeProfile, RetentionTier: TierLongTerm, Content: "current agent"},
		3: {SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 3, OwnerID: 1}}, ScopeType: ScopeAgent, ScopeID: 8, Status: StatusActive, MemoryType: TypeProfile, RetentionTier: TierLongTerm, Content: "other agent"},
		4: {SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 4, OwnerID: 1}}, ScopeType: ScopeProject, ScopeID: 42, Status: StatusActive, MemoryType: TypeTask, RetentionTier: TierLongTerm, Content: "current project"},
		5: {SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 5, OwnerID: 1}}, ScopeType: ScopeProject, ScopeID: 99, Status: StatusActive, MemoryType: TypeTask, RetentionTier: TierLongTerm, Content: "other project"},
		6: {SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 6, OwnerID: 1}}, ScopeType: ScopeConversation, ScopeID: 10, Status: StatusActive, MemoryType: TypeEpisodic, RetentionTier: TierLongTerm, Content: "current conversation"},
		7: {SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 7, OwnerID: 1}}, ScopeType: ScopeConversation, ScopeID: 11, Status: StatusActive, MemoryType: TypeEpisodic, RetentionTier: TierLongTerm, Content: "other conversation"},
	}}
	service := RuntimeService{Memories: repo, ContextIndex: &runtimeKeywordIndexFake{hits: runtimeKeywordHits(1, 2, 3, 4, 5, 6, 7)}}
	result, err := service.Read(context.Background(), ReadRequest{OwnerID: 1, AgentID: 7, ProjectID: 42, ConversationID: &conversationID, Query: "scope", SemanticOnly: true})
	if err != nil || result.Count != 4 {
		t.Fatalf("unexpected scoped recall: result=%+v err=%v", result, err)
	}
	for _, item := range result.Memories {
		if item.ID == 3 || item.ID == 5 || item.ID == 7 {
			t.Fatalf("cross-scope memory leaked into recall: %+v", result.Memories)
		}
	}
}

func TestResolveScopeDefaultsByMemoryType(t *testing.T) {
	conversationID, projectID := int64(7), int64(42)
	profileType, profileID, err := ResolveScope(TypeProfile, 1, 0, projectID, conversationID, "", 0)
	if err != nil || profileType != ScopeUser || profileID != 1 {
		t.Fatalf("profile scope = %s/%d err=%v", profileType, profileID, err)
	}
	taskType, taskID, err := ResolveScope(TypeTask, 1, 0, projectID, conversationID, "", 0)
	if err != nil || taskType != ScopeProject || taskID != projectID {
		t.Fatalf("task scope = %s/%d err=%v", taskType, taskID, err)
	}
	archivalType, archivalID, err := ResolveScope(TypeArchival, 1, 0, 0, conversationID, "", 0)
	if err != nil || archivalType != ScopeConversation || archivalID != conversationID {
		t.Fatalf("archival fallback scope = %s/%d err=%v", archivalType, archivalID, err)
	}
	episodicType, episodicID, err := ResolveScope(TypeEpisodic, 1, 0, projectID, conversationID, "", 0)
	if err != nil || episodicType != ScopeConversation || episodicID != conversationID {
		t.Fatalf("episodic scope = %s/%d err=%v", episodicType, episodicID, err)
	}
	if _, _, err := ResolveScope(TypeTask, 1, 0, 0, 0, "", 0); err == nil {
		t.Fatal("task memory without project or conversation must not widen to user scope")
	}
}

func TestMemoryV2DefaultsAndRecallability(t *testing.T) {
	conversationID := int64(9)
	item := Memory{SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{OwnerID: 3}}, SourceConversationID: &conversationID, RetentionTier: TierLongTerm}
	item.ApplyV2Defaults()
	if item.Status != StatusActive || item.ScopeType != ScopeUser || item.ScopeID != 3 || !item.IsRecallable(time.Now()) {
		t.Fatalf("unexpected V2 defaults: %+v", item)
	}
	item.Status = StatusSuperseded
	if item.IsRecallable(time.Now()) {
		t.Fatal("superseded memory must not be recallable")
	}
}

func TestMemoryPolicyDefaultsAndValidation(t *testing.T) {
	policy, err := ParsePolicy(nil)
	if err != nil || !policy.RecallActive(true) || policy.WriteMode != WriteModeSuggest || policy.TopK != 8 || policy.TokenBudget != 1200 {
		t.Fatalf("unexpected default memory policy: %+v err=%v", policy, err)
	}
	if _, err := ParsePolicy([]byte(`{"top_k":21}`)); err == nil {
		t.Fatal("expected invalid top_k to be rejected")
	}
}

func TestRuntimeServiceDeduplicatesEquivalentRecallContent(t *testing.T) {
	repo := &runtimeRepoFake{items: map[int64]*Memory{
		1: {SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 1, OwnerID: 1}}, Status: StatusActive, ScopeType: ScopeUser, ScopeID: 1, MemoryType: TypeProfile, RetentionTier: TierLongTerm, Content: "User prefers concise answers"},
		2: {SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 2, OwnerID: 1}}, Status: StatusActive, ScopeType: ScopeUser, ScopeID: 1, MemoryType: TypeProfile, RetentionTier: TierLongTerm, Content: " user   prefers concise answers "},
	}}
	service := RuntimeService{Memories: repo, ContextIndex: &runtimeKeywordIndexFake{hits: runtimeKeywordHits(1, 1, 2)}}
	result, err := service.Read(context.Background(), ReadRequest{OwnerID: 1, Query: "answer style", SemanticOnly: true})
	if err != nil || result.Count != 1 || len(repo.marked) != 1 {
		t.Fatalf("equivalent memories must enter context once: result=%+v marked=%v err=%v", result, repo.marked, err)
	}
}

func TestRuntimeServicePersistsRecallProvenanceAfterInjection(t *testing.T) {
	repo := &runtimeRepoFake{items: map[int64]*Memory{
		1: {SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 1, OwnerID: 1}}, Status: StatusActive, ScopeType: ScopeAgent, ScopeID: 7, MemoryType: TypeProfile, RetentionTier: TierLongTerm, Content: "User prefers concise answers", Source: "proposal"},
	}}
	logs := &runtimeRecallLogFake{}
	service := RuntimeService{Memories: repo, ContextIndex: &runtimeKeywordIndexFake{hits: runtimeKeywordHits(1)}, RecallLogs: logs}
	result, err := service.Read(context.Background(), ReadRequest{OwnerID: 1, AgentID: 7, RunID: 11, Query: "answer style", SemanticOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.Count != 1 || len(result.RecallDetails) != 1 || result.RecallDetails[0].MemoryID != 1 || result.RecallDetails[0].Reason == "" || result.RecallDetails[0].TokenCost <= 0 {
		t.Fatalf("missing structured recall provenance: %+v", result)
	}
	if logs.item == nil || logs.item.OwnerID != 1 || logs.item.AgentID != 7 || logs.item.RunID != 11 || logs.item.TokenCost <= 0 || len(logs.item.InjectedJSON) == 0 {
		t.Fatalf("missing persisted recall log: %+v", logs.item)
	}
}

func TestRuntimeServicePropagatesRecallLogFailure(t *testing.T) {
	repo := &runtimeRepoFake{items: map[int64]*Memory{
		1: {SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 1, OwnerID: 1}}, Status: StatusActive, ScopeType: ScopeUser, ScopeID: 1, MemoryType: TypeProfile, RetentionTier: TierLongTerm, Content: "User prefers concise answers"},
	}}
	service := RuntimeService{Memories: repo, ContextIndex: &runtimeKeywordIndexFake{hits: runtimeKeywordHits(1)}, RecallLogs: &runtimeRecallLogFake{err: errors.New("database unavailable")}}
	if _, err := service.Read(context.Background(), ReadRequest{OwnerID: 1, Query: "answer style", SemanticOnly: true}); err == nil {
		t.Fatal("recall log persistence failure must not be silently ignored")
	}
}
