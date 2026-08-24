package memory

import (
	"context"
	"errors"
	"testing"
	"time"

	"agentcanvas/internal/domain"
)

func TestResolveScopeRejectsLegacyMemoryTypes(t *testing.T) {
	if _, _, err := ResolveScope("profile_memory", 1, 2, 3, 4, ScopeUser, 1); err == nil {
		t.Fatal("expected legacy memory type to be rejected")
	}
}

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
	items        map[int64]*Memory
	marked       []int64
	replacements int
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
func (r *runtimeRepoFake) Replace(ctx context.Context, ownerID, supersededID int64, replacement *Memory) error {
	if err := r.Create(ctx, replacement); err != nil {
		return err
	}
	previous, err := r.FindByID(ctx, ownerID, supersededID)
	if err != nil {
		return err
	}
	previous.Status = StatusSuperseded
	r.replacements++
	return r.Update(ctx, previous)
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
func (r *runtimeRepoFake) IncrementRecallCount(context.Context, int64, int64) error { return nil }
func (r *runtimeRepoFake) IncrementPromotionCount(context.Context, int64, int64) error {
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

func TestRuntimeServiceKeepsArchivalIndexShadowReadOnly(t *testing.T) {
	cid := int64(7)
	repo := &runtimeRepoFake{}
	archival := &runtimeArchivalFake{}
	service := RuntimeService{Memories: repo, Archival: archival}
	written, err := service.Write(context.Background(), WriteRequest{OwnerID: 1, ConversationID: cid, SourceConversationID: &cid, MemoryType: TypeArchival, Content: "durable fact", Importance: .8})
	if err != nil {
		t.Fatal(err)
	}
	if archival.indexed.ID != 0 {
		t.Fatalf("legacy archival index must not receive writes: %+v", archival.indexed)
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
	repo := &runtimeRepoFake{items: map[int64]*Memory{1: {SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 1, OwnerID: 1}}, SourceConversationID: &other, ScopeType: ScopeConversation, ScopeID: other, MemoryType: TypeArchival, Content: "private"}}}
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
		1: {SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 1, OwnerID: 1}}, ScopeType: ScopeUser, ScopeID: 1, MemoryType: TypeProfile, RetentionTier: "unknown", Content: "unknown retention tier"},
		2: {SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 2, OwnerID: 1}}, ScopeType: ScopeUser, ScopeID: 1, MemoryType: TypeProfile, RetentionTier: TierLongTerm, HasConflict: true, Content: "disputed fact"},
		3: {SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 3, OwnerID: 1}}, ScopeType: ScopeUser, ScopeID: 1, MemoryType: TypeProfile, RetentionTier: TierShortTerm, Content: "relevant fact"},
	}}
	service := RuntimeService{Memories: repo, Retriever: &runtimeSemanticFake{ids: []int64{1, 2, 3}}}
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
	service := RuntimeService{Memories: repo, Retriever: &runtimeSemanticFake{ids: []int64{1, 2, 3, 4, 5, 6, 7}}}
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
	service := RuntimeService{Memories: repo, Retriever: &runtimeSemanticFake{ids: []int64{1, 2, 3}}}
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
	service := RuntimeService{Memories: repo, Retriever: &runtimeSemanticFake{ids: []int64{1, 2, 3, 4, 5, 6, 7}}}
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

func TestRuntimeServiceDefaultsMemoryScopesByType(t *testing.T) {
	conversationID, projectID := int64(7), int64(42)
	repo := &runtimeRepoFake{}
	service := RuntimeService{Memories: repo}
	profile, err := service.Write(context.Background(), WriteRequest{OwnerID: 1, ConversationID: conversationID, ProjectID: projectID, SourceConversationID: &conversationID, SourceProjectID: &projectID, MemoryType: TypeProfile, Content: "preference"})
	if err != nil || profile.Memory.ScopeType != ScopeUser || profile.Memory.ScopeID != 1 || profile.Memory.SourceProjectID == nil || *profile.Memory.SourceProjectID != projectID {
		t.Fatalf("profile scope = %+v err=%v", profile.Memory, err)
	}
	task, err := service.Write(context.Background(), WriteRequest{OwnerID: 1, ConversationID: conversationID, ProjectID: projectID, SourceConversationID: &conversationID, SourceProjectID: &projectID, MemoryType: TypeTask, Content: "project fact"})
	if err != nil || task.Memory.ScopeType != ScopeProject || task.Memory.ScopeID != projectID || task.Memory.SourceProjectID == nil || *task.Memory.SourceProjectID != projectID {
		t.Fatalf("task scope = %+v err=%v", task.Memory, err)
	}
	archival, err := service.Write(context.Background(), WriteRequest{OwnerID: 1, ConversationID: conversationID, SourceConversationID: &conversationID, MemoryType: TypeArchival, Content: "conversation archive"})
	if err != nil || archival.Memory.ScopeType != ScopeConversation || archival.Memory.ScopeID != conversationID {
		t.Fatalf("archival fallback scope = %+v err=%v", archival.Memory, err)
	}
	episodic, err := service.Write(context.Background(), WriteRequest{OwnerID: 1, ConversationID: conversationID, ProjectID: projectID, SourceConversationID: &conversationID, SourceProjectID: &projectID, MemoryType: TypeEpisodic, Content: "conversation event"})
	if err != nil || episodic.Memory.ScopeType != ScopeConversation || episodic.Memory.ScopeID != conversationID || episodic.Memory.SourceProjectID == nil || *episodic.Memory.SourceProjectID != projectID {
		t.Fatalf("episodic scope = %+v err=%v", episodic.Memory, err)
	}
	if _, err := service.Write(context.Background(), WriteRequest{OwnerID: 1, MemoryType: TypeTask, Content: "unscoped task"}); err == nil {
		t.Fatal("task memory without project or conversation must not widen to user scope")
	}
}

func TestRuntimeServiceConflictDetectionDoesNotCrossScopes(t *testing.T) {
	projectID := int64(42)
	repo := &runtimeRepoFake{items: map[int64]*Memory{1: {
		SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 1, OwnerID: 1}}, ScopeType: ScopeProject, ScopeID: projectID, SourceProjectID: &projectID, Status: StatusActive,
		MemoryType: TypeProfile, RetentionTier: TierLongTerm, Title: "response style", Content: "User prefers detailed answers",
	}}}
	service := RuntimeService{Memories: repo, Retriever: &runtimeSemanticFake{ids: []int64{1}}}
	result, err := service.Write(context.Background(), WriteRequest{OwnerID: 1, MemoryType: TypeProfile, Title: "response style", Content: "User prefers concise answers"})
	if err != nil || result.Action != WriteActionCreate || result.Conflict != nil {
		t.Fatalf("cross-scope memory caused a conflict: result=%+v err=%v", result, err)
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
	service := RuntimeService{Memories: repo, Retriever: &runtimeSemanticFake{ids: []int64{1, 1, 2}}}
	result, err := service.Read(context.Background(), ReadRequest{OwnerID: 1, Query: "answer style", SemanticOnly: true})
	if err != nil || result.Count != 1 || len(repo.marked) != 1 {
		t.Fatalf("equivalent memories must enter context once: result=%+v marked=%v err=%v", result, repo.marked, err)
	}
}

func TestRuntimeServicePersistsRecallProvenanceAfterInjection(t *testing.T) {
	repo := &runtimeRepoFake{items: map[int64]*Memory{
		1: {SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 1, OwnerID: 1}}, Status: StatusActive, ScopeType: ScopeAgent, ScopeID: 7, MemoryType: TypeProfile, RetentionTier: TierLongTerm, Content: "User prefers concise answers", Source: "approved_memory_proposal"},
	}}
	logs := &runtimeRecallLogFake{}
	service := RuntimeService{Memories: repo, Retriever: &runtimeSemanticFake{ids: []int64{1}}, RecallLogs: logs}
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
	service := RuntimeService{Memories: repo, Retriever: &runtimeSemanticFake{ids: []int64{1}}, RecallLogs: &runtimeRecallLogFake{err: errors.New("database unavailable")}}
	if _, err := service.Read(context.Background(), ReadRequest{OwnerID: 1, Query: "answer style", SemanticOnly: true}); err == nil {
		t.Fatal("recall log persistence failure must not be silently ignored")
	}
}

func TestRuntimeServiceDetectsConflictingMemoryBeforeWrite(t *testing.T) {
	existing := &Memory{SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 9, OwnerID: 1}}, ScopeType: ScopeUser, ScopeID: 1, MemoryType: TypeProfile, Title: "response style", Content: "User prefers concise answers"}
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

func TestRuntimeServiceConflictUsesExplicitScopeInsteadOfSource(t *testing.T) {
	existing := &Memory{SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 9, OwnerID: 1}}, ScopeType: ScopeConversation, ScopeID: 7, MemoryType: TypeArchival, Title: "conversation fact", Content: "The user prefers concise answers"}
	repo := &runtimeRepoFake{items: map[int64]*Memory{9: existing}}
	service := RuntimeService{Memories: repo, Retriever: &runtimeSemanticFake{ids: []int64{9}}}
	result, err := service.Write(context.Background(), WriteRequest{OwnerID: 1, MemoryType: TypeArchival, Title: "conversation fact", Content: "The user prefers detailed answers", ScopeType: ScopeConversation, ScopeID: 7})
	if err != nil || result.Action != WriteActionConflict || result.Conflict == nil {
		t.Fatalf("explicit conversation scope must drive conflict lookup: result=%+v err=%v", result, err)
	}
}

func TestRuntimeServiceRejectsConflictResolutionForDifferentMemory(t *testing.T) {
	existing := &Memory{SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 9, OwnerID: 1}}, ScopeType: ScopeUser, ScopeID: 1, MemoryType: TypeProfile, Title: "response style", Content: "User prefers concise answers"}
	repo := &runtimeRepoFake{items: map[int64]*Memory{9: existing}}
	service := RuntimeService{Memories: repo, Retriever: &runtimeSemanticFake{ids: []int64{9}}}
	if _, err := service.Write(context.Background(), WriteRequest{OwnerID: 1, MemoryType: TypeProfile, Title: "response style", Content: "User prefers detailed answers", ConflictResolution: "replace:99"}); err == nil {
		t.Fatal("expected resolution for a different memory to be rejected")
	}
}

func TestRuntimeServiceKeepBothPreservesConflictParent(t *testing.T) {
	existing := &Memory{SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 9, OwnerID: 1}}, ScopeType: ScopeUser, ScopeID: 1, MemoryType: TypeProfile, Title: "response style", Content: "User prefers concise answers"}
	repo := &runtimeRepoFake{items: map[int64]*Memory{9: existing}}
	service := RuntimeService{Memories: repo, Retriever: &runtimeSemanticFake{ids: []int64{9}}}
	result, err := service.Write(context.Background(), WriteRequest{OwnerID: 1, MemoryType: TypeProfile, Title: "response style", Content: "User prefers detailed answers", ConflictResolution: "keep_both"})
	if err != nil || result.Action != WriteActionCreate || result.Memory.ConflictWithID == nil || *result.Memory.ConflictWithID != 9 {
		t.Fatalf("keep_both must preserve parent lineage: result=%+v err=%v", result, err)
	}
}

func TestRuntimeServiceAppliesApprovedReplacementAtomically(t *testing.T) {
	existing := &Memory{SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 9, OwnerID: 1}}, Status: StatusActive, ScopeType: ScopeUser, ScopeID: 1, MemoryType: TypeProfile, RetentionTier: TierLongTerm, Title: "response style", Content: "User prefers concise answers"}
	repo := &runtimeRepoFake{items: map[int64]*Memory{9: existing}}
	service := RuntimeService{Memories: repo, Retriever: &runtimeSemanticFake{ids: []int64{9}}}
	result, err := service.Write(context.Background(), WriteRequest{OwnerID: 1, MemoryType: TypeProfile, Title: "response style", Content: "User prefers detailed answers", SupersedesID: &existing.ID, ScopeType: ScopeUser, ScopeID: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !result.ReplacementApplied || repo.replacements != 1 || repo.items[9].Status != StatusSuperseded || result.Memory.SupersedesID == nil || *result.Memory.SupersedesID != 9 {
		t.Fatalf("approved replacement must atomically create lineage and supersede the old memory: result=%+v repo=%+v", result, repo)
	}
}
