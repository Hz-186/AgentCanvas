package memory_usecase

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"agentcanvas/internal/domain/memory"
	"agentcanvas/internal/pkg/config"

	"gorm.io/gorm"
)

// Lifecycle selection defaults, exact per the sql-memory-es-hybrid spec.
// Production configuration carries them through config.DurableMemoryConfig.
const (
	LifecycleDefaultColdWindowDays = 30
	LifecycleDefaultSelectionCap   = 256
)

// LifecycleConfig carries the usage-driven lifecycle window and selection cap.
type LifecycleConfig struct {
	ColdWindowDays int
	SelectionCap   int
}

// NewLifecycleConfig maps the production configuration section onto the
// lifecycle contract, applying the agreed defaults (30 days, 256 entries) for
// zero/negative values so production configuration always carries them.
func NewLifecycleConfig(cfg config.DurableMemoryConfig) LifecycleConfig {
	days, cap := cfg.LifecycleColdWindowDays, cfg.LifecycleSelectionCap
	if days <= 0 {
		days = LifecycleDefaultColdWindowDays
	}
	if cap <= 0 {
		cap = LifecycleDefaultSelectionCap
	}
	return LifecycleConfig{ColdWindowDays: days, SelectionCap: cap}
}

// PrunedSourceSink is the seam through which pruned memory IDs reach
// source-aware consolidation cleanup so handbook/summary source refs are
// removed surgically instead of by string deletion. It exposes no scoring.
type PrunedSourceSink interface {
	NotifyPrunedSources(ctx context.Context, ownerID int64, deletedIDs []int64) error
}

// LifecycleResult reports one lifecycle pass: the top-N selected rows and the
// IDs actually pruned.
type LifecycleResult struct {
	Selected  []memory.Memory
	PrunedIDs []int64
}

// LifecycleService owns usage-driven selection and source-aware pruning.
// Selection is fully deterministic: eligible active rows ordered by
// usage_count DESC, COALESCE(last_used_at, updated_at) DESC, id ASC, with the
// cold-window and consolidated-protection predicates applied, capped at the
// configured selection cap. No LLM quality scoring exists anywhere in the
// dependency set: the repository is the only data surface and the artifact
// repository only supplies the protection set.
type LifecycleService struct {
	memories   memory.LifecycleRepository
	artifacts  memory.MemoryArtifactRepository
	sink       PrunedSourceSink
	coldWindow time.Duration
	cap        int
	Now        func() time.Time
}

// NewLifecycleService builds the lifecycle service with the agreed defaults
// for any zero configuration value.
func NewLifecycleService(memories memory.LifecycleRepository, artifacts memory.MemoryArtifactRepository, cfg LifecycleConfig) *LifecycleService {
	if cfg.ColdWindowDays <= 0 {
		cfg.ColdWindowDays = LifecycleDefaultColdWindowDays
	}
	if cfg.SelectionCap <= 0 {
		cfg.SelectionCap = LifecycleDefaultSelectionCap
	}
	return &LifecycleService{
		memories:   memories,
		artifacts:  artifacts,
		coldWindow: time.Duration(cfg.ColdWindowDays) * 24 * time.Hour,
		cap:        cfg.SelectionCap,
		Now:        time.Now,
	}
}

// ConfigurePrunedSink attaches the source-aware cleanup seam. The sink is
// optional: without it the pruned IDs still surface in LifecycleResult.
func (s *LifecycleService) ConfigurePrunedSink(sink PrunedSourceSink) {
	s.sink = sink
}

func (s *LifecycleService) now() time.Time {
	if s.Now == nil {
		return time.Now()
	}
	return s.Now()
}

func (s *LifecycleService) coldCutoff() time.Time {
	return s.now().Add(-s.coldWindow)
}

// protectedIDs derives the consolidated-protection set from the current
// handbook/summary artifact source refs. Protection is membership in the
// current artifact versions; an artifact read failure surfaces as an error so
// protection is never silently skipped.
func (s *LifecycleService) protectedIDs(ctx context.Context, ownerID int64) ([]int64, error) {
	if s.artifacts == nil {
		return nil, nil
	}
	var ids []int64
	for _, kind := range []string{memory.ArtifactKindHandbook, memory.ArtifactKindSummary} {
		artifact, err := s.artifacts.Latest(ctx, ownerID, kind)
		if errors.Is(err, gorm.ErrRecordNotFound) || artifact == nil {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("load %s artifact for lifecycle protection: %w", kind, err)
		}
		ids = append(ids, protectedRefIDs(artifact)...)
	}
	return ids, nil
}

func protectedRefIDs(artifact *memory.MemoryArtifact) []int64 {
	refs := decodeRefs(artifact)
	if len(refs) == 0 {
		return nil
	}
	ids := make([]int64, 0, len(refs))
	for _, ref := range refs {
		if ref.SourceID > 0 {
			ids = append(ids, ref.SourceID)
		}
	}
	return ids
}

// lifecycleRecency is the recency fallback used by selection and pruning:
// last_used_at, then the row's updated_at. The spec's source_updated_at has
// no current column; updated_at is the source-updated fallback.
func lifecycleRecency(item memory.Memory) time.Time {
	if item.LastUsedAt != nil && !item.LastUsedAt.IsZero() {
		return *item.LastUsedAt
	}
	return item.UpdatedAt
}

// sortLifecycleRows applies the deterministic SQL-key ordering in Go:
// usage_count DESC, COALESCE(last_used_at, updated_at) DESC, id ASC.
func sortLifecycleRows(rows []memory.Memory) {
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].UsageCount != rows[j].UsageCount {
			return rows[i].UsageCount > rows[j].UsageCount
		}
		ri, rj := lifecycleRecency(rows[i]), lifecycleRecency(rows[j])
		if !ri.Equal(rj) {
			return ri.After(rj)
		}
		return rows[i].ID < rows[j].ID
	})
}

// Select returns the top-N lifecycle candidates: active rows ordered by usage
// then recency, excluding cold unprotected rows, capped at the configured cap.
// The repository query implements the same deterministic predicate in SQL;
// re-applying it here keeps the contract exact for any repository.
func (s *LifecycleService) Select(ctx context.Context, ownerID int64) ([]memory.Memory, error) {
	if s == nil || s.memories == nil {
		return nil, fmt.Errorf("memory lifecycle is not configured")
	}
	if ownerID <= 0 {
		return nil, fmt.Errorf("memory lifecycle owner is required")
	}
	protectedIDs, err := s.protectedIDs(ctx, ownerID)
	if err != nil {
		return nil, err
	}
	rows, err := s.memories.SelectLifecycleCandidates(ctx, ownerID, s.coldCutoff(), protectedIDs, s.cap)
	if err != nil {
		return nil, err
	}
	protected := make(map[int64]struct{}, len(protectedIDs))
	for _, id := range protectedIDs {
		protected[id] = struct{}{}
	}
	cutoff := s.coldCutoff()
	selected := make([]memory.Memory, 0, len(rows))
	for _, row := range rows {
		if lifecycleRecency(row).Before(cutoff) {
			if _, ok := protected[row.ID]; !ok {
				continue
			}
		}
		selected = append(selected, row)
	}
	sortLifecycleRows(selected)
	if len(selected) > s.cap {
		selected = selected[:s.cap]
	}
	return selected, nil
}

// Prune soft-deletes cold unprotected rows and returns the IDs actually
// deleted. Protected cold rows are never deleted; their protection marker
// (membership in the current handbook/summary source refs) is untouched.
func (s *LifecycleService) Prune(ctx context.Context, ownerID int64) ([]int64, error) {
	if s == nil || s.memories == nil {
		return nil, fmt.Errorf("memory lifecycle is not configured")
	}
	if ownerID <= 0 {
		return nil, fmt.Errorf("memory lifecycle owner is required")
	}
	protectedIDs, err := s.protectedIDs(ctx, ownerID)
	if err != nil {
		return nil, err
	}
	coldRows, err := s.memories.ListColdRows(ctx, ownerID, s.coldCutoff(), protectedIDs)
	if err != nil {
		return nil, err
	}
	protected := make(map[int64]struct{}, len(protectedIDs))
	for _, id := range protectedIDs {
		protected[id] = struct{}{}
	}
	toDelete := make([]int64, 0, len(coldRows))
	for _, row := range coldRows {
		if _, ok := protected[row.ID]; ok {
			continue
		}
		toDelete = append(toDelete, row.ID)
	}
	if len(toDelete) == 0 {
		return nil, nil
	}
	deleted, err := s.memories.PruneMemories(ctx, ownerID, toDelete)
	if err != nil {
		return nil, err
	}
	return deleted, nil
}

// Run executes one full lifecycle pass: selection, then pruning, then
// delivery of the pruned IDs to the source-aware cleanup sink. A sink failure
// is returned alongside the completed result so the pruning is never rolled
// back by a cleanup problem.
func (s *LifecycleService) Run(ctx context.Context, ownerID int64) (LifecycleResult, error) {
	result := LifecycleResult{}
	selected, err := s.Select(ctx, ownerID)
	if err != nil {
		return result, err
	}
	result.Selected = selected
	pruned, err := s.Prune(ctx, ownerID)
	if err != nil {
		return result, err
	}
	result.PrunedIDs = pruned
	if s.sink != nil && len(pruned) > 0 {
		if err := s.sink.NotifyPrunedSources(ctx, ownerID, pruned); err != nil {
			return result, fmt.Errorf("deliver pruned sources for consolidation cleanup: %w", err)
		}
	}
	return result, nil
}
