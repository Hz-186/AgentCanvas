package memory_usecase

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"agentcanvas/internal/domain"
	"agentcanvas/internal/domain/memory"
	"agentcanvas/internal/pkg/config"

	"gorm.io/gorm"
)

// fakeLifecycleRepo is an in-memory LifecycleRepository double. SelectLifecycleCandidates
// deliberately returns the FULL seeded set (ignoring cutoff/protection/limit) so the
// lifecycle service's deterministic predicate is exercised by every test; the production
// SQL pushes the same predicate down. ListColdRows applies only the cold-window predicate
// (the service re-checks protection before deleting), and PruneMemories records and
// applies explicit-ID soft deletes. No scoring exists on this surface.
type fakeLifecycleRepo struct {
	mu            sync.Mutex
	rows          []memory.Memory
	deleted       map[int64]bool
	selectCalls   int
	coldCalls     int
	pruneCalls    int
	prunedBatches [][]int64
	fail          error
}

func newFakeLifecycleRepo(rows ...memory.Memory) *fakeLifecycleRepo {
	return &fakeLifecycleRepo{rows: rows, deleted: map[int64]bool{}}
}

func (r *fakeLifecycleRepo) SelectLifecycleCandidates(_ context.Context, _ int64, _ time.Time, _ []int64, _ int) ([]memory.Memory, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.selectCalls++
	if r.fail != nil {
		return nil, r.fail
	}
	out := make([]memory.Memory, len(r.rows))
	copy(out, r.rows)
	return out, nil
}

func (r *fakeLifecycleRepo) ListColdRows(_ context.Context, _ int64, cutoff time.Time, _ []int64) ([]memory.Memory, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.coldCalls++
	if r.fail != nil {
		return nil, r.fail
	}
	out := make([]memory.Memory, 0, len(r.rows))
	for _, row := range r.rows {
		if r.deleted[row.ID] {
			continue
		}
		recency := row.UpdatedAt
		if row.LastUsedAt != nil {
			recency = *row.LastUsedAt
		}
		if recency.Before(cutoff) {
			out = append(out, row)
		}
	}
	return out, nil
}

func (r *fakeLifecycleRepo) PruneMemories(_ context.Context, _ int64, ids []int64) ([]int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pruneCalls++
	if r.fail != nil {
		return nil, r.fail
	}
	deleted := make([]int64, 0, len(ids))
	for _, id := range ids {
		if r.deleted[id] {
			continue
		}
		r.deleted[id] = true
		deleted = append(deleted, id)
	}
	r.prunedBatches = append(r.prunedBatches, deleted)
	return deleted, nil
}

func (r *fakeLifecycleRepo) isDeleted(id int64) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.deleted[id]
}

// fakeLifecycleArtifacts is an in-memory MemoryArtifactRepository storing the
// latest artifact per kind.
type fakeLifecycleArtifacts struct {
	mu          sync.Mutex
	byKind      map[string]*memory.MemoryArtifact
	createCalls int
}

func newFakeLifecycleArtifacts() *fakeLifecycleArtifacts {
	return &fakeLifecycleArtifacts{byKind: map[string]*memory.MemoryArtifact{}}
}

func (a *fakeLifecycleArtifacts) Create(_ context.Context, artifact *memory.MemoryArtifact) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.createCalls++
	clone := *artifact
	a.byKind[artifact.Kind] = &clone
	return nil
}

func (a *fakeLifecycleArtifacts) Latest(_ context.Context, ownerID int64, kind string) (*memory.MemoryArtifact, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	artifact, ok := a.byKind[kind]
	if !ok || artifact == nil || artifact.OwnerID != ownerID {
		return nil, gorm.ErrRecordNotFound
	}
	clone := *artifact
	return &clone, nil
}

func (a *fakeLifecycleArtifacts) seed(artifact *memory.MemoryArtifact) {
	a.mu.Lock()
	defer a.mu.Unlock()
	clone := *artifact
	a.byKind[artifact.Kind] = &clone
}

// fakePrunedSink records every pruned-ID delivery so tests can prove the
// source-aware consolidation cleanup seam receives exactly the deleted IDs.
type fakePrunedSink struct {
	mu    sync.Mutex
	calls [][]int64
	err   error
}

func (s *fakePrunedSink) NotifyPrunedSources(_ context.Context, _ int64, deletedIDs []int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	clone := append([]int64(nil), deletedIDs...)
	s.calls = append(s.calls, clone)
	return s.err
}

func (s *fakePrunedSink) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.calls)
}

func lifecycleServiceForTest(repo *fakeLifecycleRepo, artifacts *fakeLifecycleArtifacts) *LifecycleService {
	svc := NewLifecycleService(repo, artifacts, LifecycleConfig{ColdWindowDays: LifecycleDefaultColdWindowDays, SelectionCap: LifecycleDefaultSelectionCap})
	svc.Now = projectionTestNow
	return svc
}

func lifecycleTestMemory(id int64, usage int, lastUsed *time.Time, updated time.Time) memory.Memory {
	return memory.Memory{
		SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: id, OwnerID: 7, UpdatedAt: updated}},
		UsageCount:      usage,
		LastUsedAt:      lastUsed,
		Status:          memory.StatusActive,
	}
}

func lifecycleTestPtr(value time.Time) *time.Time { return &value }

func selectedIDs(rows []memory.Memory) []int64 {
	ids := make([]int64, len(rows))
	for i, row := range rows {
		ids[i] = row.ID
	}
	return ids
}

func equalInt64Slices(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestMemoryLifecycleShouldOrderByUsageThenRecency is
// MemoryLifecycleTest#shouldOrderByUsageThenRecency: usage desc first, then
// recency (last_used_at, updated_at fallback) desc, then id asc.
func TestMemoryLifecycleShouldOrderByUsageThenRecency(t *testing.T) {
	now := projectionTestNow()
	oldFallback := now.Add(-20 * 24 * time.Hour)
	olderUse := now.Add(-2 * time.Hour)
	midUse := now.Add(-90 * time.Minute)
	recentUse := now.Add(-time.Hour)

	repo := newFakeLifecycleRepo(
		// Seeded in scrambled order; the service must order them.
		lifecycleTestMemory(3, 2, &recentUse, recentUse),
		lifecycleTestMemory(1, 8, &olderUse, olderUse),
		lifecycleTestMemory(2, 2, nil, oldFallback),
		lifecycleTestMemory(4, 2, &midUse, midUse),
	)
	svc := lifecycleServiceForTest(repo, newFakeLifecycleArtifacts())

	selected, err := svc.Select(context.Background(), 7)
	if err != nil {
		t.Fatalf("lifecycle selection failed: %v", err)
	}
	got := selectedIDs(selected)
	// usage desc: id 1 (8) first; usage-2 ties broken by recency desc:
	// id 3 (recent), id 4 (mid), id 2 (nil last_used_at falls back to updated_at, oldest).
	want := []int64{1, 3, 4, 2}
	if !equalInt64Slices(got, want) {
		t.Fatalf("selection order=%v, want usage-then-recency order %v", got, want)
	}
}

// TestMemoryLifecycleShouldExcludeColdUnprotectedInputs is
// MemoryLifecycleTest#shouldExcludeColdUnprotectedInputs: rows outside the
// 30-day window without consolidated protection are excluded from top-256
// selection even when their usage is high.
func TestMemoryLifecycleShouldExcludeColdUnprotectedInputs(t *testing.T) {
	now := projectionTestNow()
	warmUse := now.Add(-time.Hour)
	coldUpdated := now.Add(-45 * 24 * time.Hour)
	coldUse := now.Add(-45 * 24 * time.Hour)

	repo := newFakeLifecycleRepo(
		lifecycleTestMemory(2, 8, nil, coldUpdated), // cold, high usage, unprotected
		lifecycleTestMemory(3, 5, &coldUse, coldUpdated),
		lifecycleTestMemory(1, 1, &warmUse, warmUse), // warm, low usage
	)
	svc := lifecycleServiceForTest(repo, newFakeLifecycleArtifacts())

	selected, err := svc.Select(context.Background(), 7)
	if err != nil {
		t.Fatalf("lifecycle selection failed: %v", err)
	}
	got := selectedIDs(selected)
	want := []int64{1}
	if !equalInt64Slices(got, want) {
		t.Fatalf("selection=%v, want only the warm row %v (cold unprotected rows excluded from top-256)", got, want)
	}
}

// TestMemoryLifecycleShouldProtectConsolidatedRows is
// MemoryLifecycleTest#shouldProtectConsolidatedRows: a cold row referenced by
// the current handbook/summary source refs is never directly deleted and its
// protection is retained.
func TestMemoryLifecycleShouldProtectConsolidatedRows(t *testing.T) {
	now := projectionTestNow()
	coldUpdated := now.Add(-45 * 24 * time.Hour)
	warmUse := now.Add(-time.Hour)

	repo := newFakeLifecycleRepo(
		lifecycleTestMemory(5, 0, nil, coldUpdated), // cold, consolidated-protected
		lifecycleTestMemory(6, 0, nil, coldUpdated), // cold, unprotected
		lifecycleTestMemory(7, 3, &warmUse, warmUse),
	)
	artifacts := newFakeLifecycleArtifacts()
	refs := marshalProjectionRefs([]ProjectionSourceRef{{SourceID: 5, Kind: ConsolidationSourceRollout, ConversationID: 3}})
	artifacts.seed(&memory.MemoryArtifact{
		BaseModel: domain.BaseModel{OwnerID: 7},
		Kind:      memory.ArtifactKindHandbook, Version: 1, Content: "# Memory\n\nprotected fact",
		Source: "consolidation", SourceRefsJSON: refs, Checksum: "v1checksum",
	})
	sink := &fakePrunedSink{}
	svc := lifecycleServiceForTest(repo, artifacts)
	svc.ConfigurePrunedSink(sink)

	result, err := svc.Run(context.Background(), 7)
	if err != nil {
		t.Fatalf("lifecycle run failed: %v", err)
	}
	if got, want := result.PrunedIDs, []int64{6}; !equalInt64Slices(got, want) {
		t.Fatalf("pruned ids=%v, want only the cold unprotected row %v", got, want)
	}
	if repo.isDeleted(5) {
		t.Fatal("protected consolidated row 5 was directly deleted")
	}
	if !repo.isDeleted(6) {
		t.Fatal("cold unprotected row 6 was not pruned")
	}
	// Protection is retained: the handbook source ref still names row 5.
	latest, err := artifacts.Latest(context.Background(), 7, memory.ArtifactKindHandbook)
	if err != nil {
		t.Fatalf("reload handbook artifact: %v", err)
	}
	var kept []ProjectionSourceRef
	if err := json.Unmarshal(latest.SourceRefsJSON, &kept); err != nil {
		t.Fatalf("decode retained source refs: %v", err)
	}
	if len(kept) != 1 || kept[0].SourceID != 5 {
		t.Fatalf("protection marker lost: handbook refs=%+v, want source 5", kept)
	}
	// The pruned IDs are delivered to the source-aware cleanup seam.
	if sink.callCount() != 1 || !equalInt64Slices(sink.calls[0], []int64{6}) {
		t.Fatalf("pruned-source delivery=%v, want one delivery of [6]", sink.calls)
	}
}

// TestMemoryLifecycleShouldCapSelectionAt256 is
// MemoryLifecycleTest#shouldCapSelectionAt256: 300 eligible rows select exactly
// 256 and the remaining rows are untouched (not pruned).
func TestMemoryLifecycleShouldCapSelectionAt256(t *testing.T) {
	now := projectionTestNow()
	rows := make([]memory.Memory, 0, 300)
	for i := 1; i <= 300; i++ {
		used := now.Add(-time.Duration(i) * time.Minute)
		rows = append(rows, lifecycleTestMemory(int64(i), 1000-i, &used, used))
	}
	repo := newFakeLifecycleRepo(rows...)
	svc := lifecycleServiceForTest(repo, newFakeLifecycleArtifacts())

	result, err := svc.Run(context.Background(), 7)
	if err != nil {
		t.Fatalf("lifecycle run failed: %v", err)
	}
	if len(result.Selected) != 256 {
		t.Fatalf("selected %d rows, want exactly 256", len(result.Selected))
	}
	if result.Selected[0].ID != 1 || result.Selected[255].ID != 256 {
		t.Fatalf("selected boundary rows are ids %d..%d, want 1..256 (usage desc)", result.Selected[0].ID, result.Selected[255].ID)
	}
	if len(result.PrunedIDs) != 0 {
		t.Fatalf("pruned %d warm rows, want zero (rows beyond the cap stay untouched)", len(result.PrunedIDs))
	}
	if repo.pruneCalls != 0 {
		t.Fatalf("prune called %d time(s) for warm beyond-cap rows, want zero", repo.pruneCalls)
	}
}

// qualityScorer is the LLM quality-scoring shape the lifecycle must never
// expose. The production dependencies are type-asserted against it.
type qualityScorer interface {
	Score(context.Context, int64, []memory.Memory) ([]memory.Memory, error)
}

// TestMemoryLifecycleShouldAvoidQualityScoring is
// MemoryLifecycleTest#shouldAvoidQualityScoring: no dependency exposes an LLM
// scorer, usage data flows through, and selection is deterministic SQL-key
// ordering with zero prune/cleanup side effects.
func TestMemoryLifecycleShouldAvoidQualityScoring(t *testing.T) {
	now := projectionTestNow()
	fallbackOld := now.Add(-25 * 24 * time.Hour)
	useOld := now.Add(-3 * time.Hour)
	useNew := now.Add(-time.Hour)

	repo := newFakeLifecycleRepo(
		lifecycleTestMemory(2, 9, nil, fallbackOld),
		lifecycleTestMemory(1, 4, &useNew, useNew),
		lifecycleTestMemory(3, 9, &useOld, useOld),
	)
	artifacts := newFakeLifecycleArtifacts()
	svc := lifecycleServiceForTest(repo, artifacts)

	if _, ok := any(svc).(qualityScorer); ok {
		t.Fatal("lifecycle service exposes an LLM quality scorer")
	}
	if _, ok := any(repo).(qualityScorer); ok {
		t.Fatal("lifecycle repository exposes an LLM quality scorer")
	}
	if _, ok := any(artifacts).(qualityScorer); ok {
		t.Fatal("lifecycle artifact dependency exposes an LLM quality scorer")
	}

	first, err := svc.Select(context.Background(), 7)
	if err != nil {
		t.Fatalf("first selection failed: %v", err)
	}
	second, err := svc.Select(context.Background(), 7)
	if err != nil {
		t.Fatalf("second selection failed: %v", err)
	}
	// usage 9 group: id 3 (last_used 1h ago) beats id 2 (never used, updated_at
	// 25d ago) on recency; then usage 4.
	want := []int64{3, 2, 1}
	if got := selectedIDs(first); !equalInt64Slices(got, want) {
		t.Fatalf("deterministic SQL-key order=%v, want %v", got, want)
	}
	if got := selectedIDs(second); !equalInt64Slices(got, want) {
		t.Fatalf("second selection order=%v diverges from first %v", got, want)
	}
	// Usage data flows through untouched.
	if first[0].UsageCount != 9 || first[0].LastUsedAt == nil || first[1].UsageCount != 9 || first[2].UsageCount != 4 {
		t.Fatalf("selection dropped usage data: %+v", first)
	}
	if repo.coldCalls != 0 || repo.pruneCalls != 0 {
		t.Fatalf("selection path performed lifecycle mutations (cold=%d prune=%d), want zero", repo.coldCalls, repo.pruneCalls)
	}
}

// TestMemoryLifecycleConfigDefaults verifies the production configuration
// mapping carries the exact spec defaults: 30-day cold window and 256 cap.
func TestMemoryLifecycleConfigDefaults(t *testing.T) {
	mapped := NewLifecycleConfig(config.DurableMemoryConfig{})
	if mapped.ColdWindowDays != 30 || mapped.SelectionCap != 256 {
		t.Fatalf("default lifecycle config=%+v, want 30 days / 256 cap", mapped)
	}
	overridden := NewLifecycleConfig(config.DurableMemoryConfig{LifecycleColdWindowDays: 14, LifecycleSelectionCap: 64})
	if overridden.ColdWindowDays != 14 || overridden.SelectionCap != 64 {
		t.Fatalf("explicit lifecycle config=%+v, want 14 days / 64 cap", overridden)
	}
	svc := NewLifecycleService(newFakeLifecycleRepo(), newFakeLifecycleArtifacts(), LifecycleConfig{})
	if svc.coldWindow != 30*24*time.Hour || svc.cap != 256 {
		t.Fatalf("zero-value service config: window=%v cap=%d, want 30 days / 256", svc.coldWindow, svc.cap)
	}
}
