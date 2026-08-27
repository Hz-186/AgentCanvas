package memory_usecase

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"sync"
	"testing"
	"time"

	"agentcanvas/internal/domain"
	"agentcanvas/internal/domain/memory"

	"gorm.io/gorm"
)

// fakeArtifactRepo is an in-memory MemoryArtifactRepository with a steerable
// failure switch so projection write failures are deterministic without MySQL.
type fakeArtifactRepo struct {
	mu          sync.Mutex
	artifacts   map[int64]*memory.MemoryArtifact
	nextID      int64
	createCalls int
	failCreate  error
	now         time.Time
}

func newFakeArtifactRepo() *fakeArtifactRepo {
	return &fakeArtifactRepo{artifacts: map[int64]*memory.MemoryArtifact{}, nextID: 1, now: time.Now().UTC()}
}

func (r *fakeArtifactRepo) Create(_ context.Context, artifact *memory.MemoryArtifact) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.createCalls++
	if r.failCreate != nil {
		return r.failCreate
	}
	clone := *artifact
	if clone.ID == 0 {
		clone.ID = r.nextID
		r.nextID++
	}
	if clone.CreatedAt.IsZero() {
		clone.CreatedAt = r.now
	}
	clone.UpdatedAt = clone.CreatedAt
	r.artifacts[clone.ID] = &clone
	return nil
}

func (r *fakeArtifactRepo) Latest(_ context.Context, ownerID int64, kind string) (*memory.MemoryArtifact, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var latest *memory.MemoryArtifact
	for _, artifact := range r.artifacts {
		if artifact.OwnerID != ownerID || artifact.Kind != kind {
			continue
		}
		if latest == nil || artifact.Version > latest.Version || (artifact.Version == latest.Version && artifact.ID > latest.ID) {
			latest = artifact
		}
	}
	if latest == nil {
		return nil, gorm.ErrRecordNotFound
	}
	clone := *latest
	return &clone, nil
}

func (r *fakeArtifactRepo) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.artifacts)
}

// fakeConsolidationAgent records every consolidation invocation and returns
// canned handbook/summary output.
type fakeConsolidationAgent struct {
	mu       sync.Mutex
	calls    []ConsolidationCall
	handbook string
	summary  string
	err      error
}

type ConsolidationCall struct {
	OwnerID       int64
	Diff          ConsolidationDiff
	PriorHandbook string
	PriorSummary  string
	RawInput      string
}

func (a *fakeConsolidationAgent) Consolidate(_ context.Context, ownerID int64, diff ConsolidationDiff, priorHandbook, priorSummary, raw string) (string, string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.calls = append(a.calls, ConsolidationCall{OwnerID: ownerID, Diff: diff, PriorHandbook: priorHandbook, PriorSummary: priorSummary, RawInput: raw})
	if a.err != nil {
		return "", "", a.err
	}
	return a.handbook, a.summary, nil
}

func (a *fakeConsolidationAgent) callCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.calls)
}

func (a *fakeConsolidationAgent) lastCall() ConsolidationCall {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.calls[len(a.calls)-1]
}

func projectionTestNow() time.Time {
	return time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
}

func projectionService(repo *fakeArtifactRepo) *ConsolidationProjection {
	service := NewConsolidationProjection(repo)
	service.Now = projectionTestNow
	return service
}

func newRolloutInput(sourceID, conversationID int64, raw, summary string, sourceAt time.Time) ProjectionInput {
	return ProjectionInput{
		SourceRef:      ProjectionSourceRef{SourceID: sourceID, Kind: ConsolidationSourceRollout, ConversationID: conversationID},
		RawMemory:      raw,
		RolloutSummary: summary,
		SourceAt:       sourceAt,
	}
}

func decodeProjectionSourceRefs(raw json.RawMessage, refs *[]ProjectionSourceRef) error {
	if len(raw) == 0 {
		*refs = nil
		return nil
	}
	return json.Unmarshal(raw, refs)
}

func (a ConsolidationCall) addedSourceIDs() []int64 {
	ids := make([]int64, 0, len(a.Diff.Added))
	for _, ref := range a.Diff.Added {
		ids = append(ids, ref.SourceID)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func (a ConsolidationCall) removedSourceIDs() []int64 {
	ids := make([]int64, 0, len(a.Diff.Removed))
	for _, ref := range a.Diff.Removed {
		ids = append(ids, ref.SourceID)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

// TestConsolidationProjectionShouldWriteHandbookAndSummaryRows verifies one
// consolidation call produces two versioned SQL artifacts with source refs.
func TestConsolidationProjectionShouldWriteHandbookAndSummaryRows(t *testing.T) {
	repo := newFakeArtifactRepo()
	agent := &fakeConsolidationAgent{handbook: "# Memory\n\nuser prefers concise answers", summary: "concise answers"}
	service := projectionService(repo)
	inputs := []ProjectionInput{newRolloutInput(1, 3, "the user prefers concise answers", "rollout summary", projectionTestNow())}

	handbook, summary, err := service.Project(context.Background(), 7, inputs, agent)
	if err != nil {
		t.Fatalf("projection failed: %v", err)
	}
	if agent.callCount() != 1 {
		t.Fatalf("consolidation agent called %d time(s), want exactly one", agent.callCount())
	}
	if handbook == nil || summary == nil {
		t.Fatal("projection returned nil artifacts")
	}
	if handbook.Kind != memory.ArtifactKindHandbook || handbook.Version != 1 {
		t.Fatalf("handbook kind=%q version=%d, want %q version 1", handbook.Kind, handbook.Version, memory.ArtifactKindHandbook)
	}
	if summary.Kind != memory.ArtifactKindSummary || summary.Version != 1 {
		t.Fatalf("summary kind=%q version=%d, want %q version 1", summary.Kind, summary.Version, memory.ArtifactKindSummary)
	}
	if handbook.Content != agent.handbook || summary.Content != agent.summary {
		t.Fatalf("artifact content mismatch: handbook=%q summary=%q", handbook.Content, summary.Content)
	}
	if handbook.OwnerID != 7 || summary.OwnerID != 7 {
		t.Fatalf("artifact owner mismatch: handbook=%d summary=%d, want 7", handbook.OwnerID, summary.OwnerID)
	}
	if handbook.ConsolidatedAt == nil || !handbook.ConsolidatedAt.Equal(projectionTestNow()) {
		t.Fatalf("handbook consolidated_at=%v, want %v", handbook.ConsolidatedAt, projectionTestNow())
	}
	if repo.count() != 2 {
		t.Fatalf("artifact repository holds %d rows, want two versioned artifacts", repo.count())
	}
	var refs []ProjectionSourceRef
	if err := decodeProjectionSourceRefs(handbook.SourceRefsJSON, &refs); err != nil {
		t.Fatalf("decode handbook source refs: %v", err)
	}
	if len(refs) != 1 || refs[0].SourceID != 1 || refs[0].Kind != ConsolidationSourceRollout || refs[0].ConversationID != 3 {
		t.Fatalf("handbook source refs=%+v, want rollout source 1 conversation 3", refs)
	}
	latestHandbook, err := repo.Latest(context.Background(), 7, memory.ArtifactKindHandbook)
	if err != nil || latestHandbook == nil {
		t.Fatalf("latest handbook lookup failed: %v", err)
	}
	if latestHandbook.Checksum == "" {
		t.Fatal("artifact checksum is required")
	}
}

// TestConsolidationProjectionShouldShortCircuitEmptyBatch verifies an empty
// batch calls neither the LLM nor the artifact repository write path.
func TestConsolidationProjectionShouldShortCircuitEmptyBatch(t *testing.T) {
	repo := newFakeArtifactRepo()
	agent := &fakeConsolidationAgent{handbook: "unexpected", summary: "unexpected"}
	service := projectionService(repo)

	handbook, summary, err := service.Project(context.Background(), 7, nil, agent)
	if err != nil {
		t.Fatalf("empty batch projection failed: %v", err)
	}
	if handbook != nil || summary != nil {
		t.Fatalf("empty batch wrote artifacts: handbook=%+v summary=%+v", handbook, summary)
	}
	if agent.callCount() != 0 {
		t.Fatalf("empty batch called the consolidation agent %d time(s), want zero", agent.callCount())
	}
	if repo.createCalls != 0 {
		t.Fatalf("empty batch attempted %d artifact write(s), want zero", repo.createCalls)
	}
}

// TestConsolidationProjectionShouldReturnRetryableErrorOnArtifactFailure
// verifies an artifact write failure surfaces as an error with no fallback.
func TestConsolidationProjectionShouldReturnRetryableErrorOnArtifactFailure(t *testing.T) {
	repo := newFakeArtifactRepo()
	repo.failCreate = errors.New("sql transaction failed")
	agent := &fakeConsolidationAgent{handbook: "# Memory", summary: "summary"}
	service := projectionService(repo)
	inputs := []ProjectionInput{newRolloutInput(1, 3, "fact", "summary", projectionTestNow())}

	_, _, err := service.Project(context.Background(), 7, inputs, agent)
	if err == nil {
		t.Fatal("expected artifact write failure to surface as an error")
	}
	if agent.callCount() != 1 {
		t.Fatalf("consolidation agent called %d time(s), want exactly one", agent.callCount())
	}
	if repo.count() != 0 {
		t.Fatalf("failed projection persisted %d artifact row(s), want zero", repo.count())
	}
}

// TestConsolidationProjectionShouldRetainProtectedArtifact verifies protected
// sources stay referenced while cold unprotected inputs are excluded.
func TestConsolidationProjectionShouldRetainProtectedArtifact(t *testing.T) {
	repo := newFakeArtifactRepo()
	protectedRefs := marshalProjectionRefs([]ProjectionSourceRef{{SourceID: 1, Kind: ConsolidationSourceRollout, ConversationID: 3}})
	if err := repo.Create(context.Background(), &memory.MemoryArtifact{
		BaseModel: domain.BaseModel{OwnerID: 7},
		Kind:      memory.ArtifactKindHandbook, Version: 1, Content: "# Memory\n\nprotected fact",
		Source: "consolidation", SourceRefsJSON: protectedRefs, Checksum: "v1checksum",
	}); err != nil {
		t.Fatalf("seed protected artifact: %v", err)
	}

	agent := &fakeConsolidationAgent{handbook: "# Memory\n\nprotected fact plus warm fact", summary: "warm fact"}
	service := projectionService(repo)
	inputs := []ProjectionInput{
		newRolloutInput(1, 3, "protected fact", "summary", projectionTestNow().Add(-time.Hour)),
		newRolloutInput(2, 4, "warm new fact", "summary", projectionTestNow().Add(-time.Hour)),
		newRolloutInput(3, 5, "cold unprotected fact", "summary", projectionTestNow().Add(-45*24*time.Hour)),
	}

	handbook, _, err := service.Project(context.Background(), 7, inputs, agent)
	if err != nil {
		t.Fatalf("projection failed: %v", err)
	}
	var refs []ProjectionSourceRef
	if err := decodeProjectionSourceRefs(handbook.SourceRefsJSON, &refs); err != nil {
		t.Fatalf("decode new source refs: %v", err)
	}
	if len(refs) != 2 || refs[0].SourceID != 1 || refs[1].SourceID != 2 {
		t.Fatalf("new source refs=%+v, want protected source 1 and warm source 2, cold source 3 excluded", refs)
	}
	call := agent.lastCall()
	if got := call.addedSourceIDs(); len(got) != 1 || got[0] != 2 {
		t.Fatalf("diff added=%v, want exactly source 2", got)
	}
	if got := call.removedSourceIDs(); len(got) != 0 {
		t.Fatalf("diff removed=%v, want none", got)
	}
	if call.RawInput == "" {
		t.Fatal("agent received no raw input")
	}
}

// TestConsolidationProjectionShouldUseDiffBeforeConsolidation verifies the
// agent receives the exact version diff and is called exactly once.
func TestConsolidationProjectionShouldUseDiffBeforeConsolidation(t *testing.T) {
	repo := newFakeArtifactRepo()
	priorRefs := marshalProjectionRefs([]ProjectionSourceRef{{SourceID: 1, Kind: ConsolidationSourceRollout, ConversationID: 3}})
	if err := repo.Create(context.Background(), &memory.MemoryArtifact{
		BaseModel: domain.BaseModel{OwnerID: 7},
		Kind:      memory.ArtifactKindHandbook, Version: 3, Content: "# Memory\n\nold content",
		Source: "consolidation", SourceRefsJSON: priorRefs, Checksum: "v3checksum",
	}); err != nil {
		t.Fatalf("seed prior artifact: %v", err)
	}

	agent := &fakeConsolidationAgent{handbook: "# Memory\n\nnew content", summary: "new summary"}
	service := projectionService(repo)
	inputs := []ProjectionInput{newRolloutInput(2, 4, "new fact", "summary", projectionTestNow())}

	handbook, summary, err := service.Project(context.Background(), 7, inputs, agent)
	if err != nil {
		t.Fatalf("projection failed: %v", err)
	}
	if agent.callCount() != 1 {
		t.Fatalf("consolidation agent called %d time(s), want exactly one", agent.callCount())
	}
	if handbook.Version != 4 || summary.Version != 1 {
		t.Fatalf("artifact versions: handbook=%d (want 4), summary=%d (want 1)", handbook.Version, summary.Version)
	}
	call := agent.lastCall()
	if got := call.addedSourceIDs(); len(got) != 1 || got[0] != 2 {
		t.Fatalf("diff added=%v, want exactly source 2", got)
	}
	if got := call.removedSourceIDs(); len(got) != 1 || got[0] != 1 {
		t.Fatalf("diff removed=%v, want exactly source 1", got)
	}
	if call.PriorHandbook != "# Memory\n\nold content" {
		t.Fatalf("agent prior handbook=%q, want seeded version", call.PriorHandbook)
	}
	var refs []ProjectionSourceRef
	if err := decodeProjectionSourceRefs(handbook.SourceRefsJSON, &refs); err != nil {
		t.Fatalf("decode new source refs: %v", err)
	}
	if len(refs) != 1 || refs[0].SourceID != 2 {
		t.Fatalf("new source refs=%+v, want source 2", refs)
	}
}

// TestConsolidationProjectionShouldRemovePrunedSourcesSurgically verifies the
// source-aware cleanup path: the pruned memory IDs are removed from the
// artifact source refs (reference removal, never string deletion), the agent
// receives a removal-only diff exactly once, and both artifacts are rewritten
// with the surviving refs.
func TestConsolidationProjectionShouldRemovePrunedSourcesSurgically(t *testing.T) {
	repo := newFakeArtifactRepo()
	priorRefs := marshalProjectionRefs([]ProjectionSourceRef{
		{SourceID: 1, Kind: ConsolidationSourceRollout, ConversationID: 3},
		{SourceID: 2, Kind: ConsolidationSourceAdHoc, ConversationID: 4},
		{SourceID: 3, Kind: ConsolidationSourceRollout, ConversationID: 5},
	})
	if err := repo.Create(context.Background(), &memory.MemoryArtifact{
		BaseModel: domain.BaseModel{OwnerID: 7},
		Kind:      memory.ArtifactKindHandbook, Version: 2, Content: "# Memory\n\none two three",
		Source: "consolidation", SourceRefsJSON: priorRefs, Checksum: "v2checksum",
	}); err != nil {
		t.Fatalf("seed handbook artifact: %v", err)
	}
	if err := repo.Create(context.Background(), &memory.MemoryArtifact{
		BaseModel: domain.BaseModel{OwnerID: 7},
		Kind:      memory.ArtifactKindSummary, Version: 1, Content: "one two three",
		Source: "consolidation", SourceRefsJSON: priorRefs, Checksum: "s1checksum",
	}); err != nil {
		t.Fatalf("seed summary artifact: %v", err)
	}

	agent := &fakeConsolidationAgent{handbook: "# Memory\n\none three", summary: "one three"}
	service := projectionService(repo)

	removed, err := service.RemovePrunedSources(context.Background(), 7, []int64{2}, agent)
	if err != nil {
		t.Fatalf("pruned-source cleanup failed: %v", err)
	}
	if removed != 1 {
		t.Fatalf("removed %d source ref(s), want exactly one", removed)
	}
	if agent.callCount() != 1 {
		t.Fatalf("consolidation agent called %d time(s), want exactly one", agent.callCount())
	}
	call := agent.lastCall()
	if got := call.addedSourceIDs(); len(got) != 0 {
		t.Fatalf("removal diff added=%v, want none", got)
	}
	if got := call.removedSourceIDs(); len(got) != 1 || got[0] != 2 {
		t.Fatalf("removal diff removed=%v, want exactly source 2", got)
	}
	if call.PriorHandbook != "# Memory\n\none two three" || call.PriorSummary != "one two three" {
		t.Fatalf("agent priors=%q/%q, want the seeded versions", call.PriorHandbook, call.PriorSummary)
	}
	latestHandbook, err := repo.Latest(context.Background(), 7, memory.ArtifactKindHandbook)
	if err != nil || latestHandbook == nil {
		t.Fatalf("reload latest handbook: %v", err)
	}
	if latestHandbook.Version != 3 {
		t.Fatalf("handbook version=%d, want 3", latestHandbook.Version)
	}
	var surviving []ProjectionSourceRef
	if err := decodeProjectionSourceRefs(latestHandbook.SourceRefsJSON, &surviving); err != nil {
		t.Fatalf("decode surviving refs: %v", err)
	}
	if len(surviving) != 2 || surviving[0].SourceID != 1 || surviving[1].SourceID != 3 {
		t.Fatalf("surviving refs=%+v, want sources 1 and 3 with source 2 removed surgically", surviving)
	}
	if latestHandbook.Content != agent.handbook {
		t.Fatalf("handbook content=%q, want the rewritten content %q", latestHandbook.Content, agent.handbook)
	}
	latestSummary, err := repo.Latest(context.Background(), 7, memory.ArtifactKindSummary)
	if err != nil || latestSummary == nil {
		t.Fatalf("reload latest summary: %v", err)
	}
	if latestSummary.Version != 2 || latestSummary.Content != agent.summary {
		t.Fatalf("summary version=%d content=%q, want version 2 content %q", latestSummary.Version, latestSummary.Content, agent.summary)
	}
}

// TestConsolidationProjectionShouldNoopWhenNoPrunedRefMatches verifies that
// deleted IDs with no matching current source ref short-circuit with zero
// agent calls and zero artifact writes.
func TestConsolidationProjectionShouldNoopWhenNoPrunedRefMatches(t *testing.T) {
	repo := newFakeArtifactRepo()
	priorRefs := marshalProjectionRefs([]ProjectionSourceRef{{SourceID: 1, Kind: ConsolidationSourceRollout, ConversationID: 3}})
	if err := repo.Create(context.Background(), &memory.MemoryArtifact{
		BaseModel: domain.BaseModel{OwnerID: 7},
		Kind:      memory.ArtifactKindHandbook, Version: 1, Content: "# Memory\n\nfact",
		Source: "consolidation", SourceRefsJSON: priorRefs, Checksum: "v1checksum",
	}); err != nil {
		t.Fatalf("seed handbook artifact: %v", err)
	}

	agent := &fakeConsolidationAgent{handbook: "unexpected", summary: "unexpected"}
	service := projectionService(repo)

	removed, err := service.RemovePrunedSources(context.Background(), 7, []int64{99}, agent)
	if err != nil {
		t.Fatalf("no-match cleanup returned an error: %v", err)
	}
	if removed != 0 {
		t.Fatalf("removed %d source ref(s), want zero", removed)
	}
	if agent.callCount() != 0 {
		t.Fatalf("no-match cleanup called the agent %d time(s), want zero", agent.callCount())
	}
	if repo.count() != 1 {
		t.Fatalf("no-match cleanup wrote %d artifact(s), want only the seeded row", repo.count())
	}
}

// TestConsolidationProjectionShouldEmptyRefsWhenAllSourcesPruned verifies the
// all-removed edge: the surviving refs marshal as an empty JSON array, never
// null, so later runs and readers decode a stable empty set.
func TestConsolidationProjectionShouldEmptyRefsWhenAllSourcesPruned(t *testing.T) {
	repo := newFakeArtifactRepo()
	refs := marshalProjectionRefs([]ProjectionSourceRef{{SourceID: 1, Kind: ConsolidationSourceRollout, ConversationID: 3}})
	if err := repo.Create(context.Background(), &memory.MemoryArtifact{
		BaseModel: domain.BaseModel{OwnerID: 7},
		Kind:      memory.ArtifactKindHandbook, Version: 1, Content: "# Memory\n\nonly fact",
		Source: "consolidation", SourceRefsJSON: refs, Checksum: "v1checksum",
	}); err != nil {
		t.Fatalf("seed handbook artifact: %v", err)
	}

	agent := &fakeConsolidationAgent{handbook: "# Memory", summary: "none"}
	service := projectionService(repo)

	removed, err := service.RemovePrunedSources(context.Background(), 7, []int64{1}, agent)
	if err != nil {
		t.Fatalf("all-pruned cleanup failed: %v", err)
	}
	if removed != 1 {
		t.Fatalf("removed %d source ref(s), want exactly one", removed)
	}
	latest, err := repo.Latest(context.Background(), 7, memory.ArtifactKindHandbook)
	if err != nil || latest == nil {
		t.Fatalf("reload latest handbook: %v", err)
	}
	if string(latest.SourceRefsJSON) != "[]" {
		t.Fatalf("surviving refs JSON=%s, want [] (empty array, not null)", string(latest.SourceRefsJSON))
	}
	if latest.Version != 2 {
		t.Fatalf("handbook version=%d, want 2", latest.Version)
	}
}
