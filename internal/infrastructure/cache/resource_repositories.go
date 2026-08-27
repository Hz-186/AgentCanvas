package cache

import (
	"context"
	"fmt"
	"time"

	"agentcanvas/internal/domain/knowledge"
	"agentcanvas/internal/domain/memory"
	"agentcanvas/internal/domain/resource"
	"agentcanvas/internal/domain/skill"
	"agentcanvas/internal/domain/tool"
)

func invalidate(ctx context.Context, invalidator resource.Invalidator, ownerID int64, kind resource.Kind) {
	if invalidator != nil && ownerID > 0 {
		_ = invalidator.Invalidate(ctx, ownerID, kind)
	}
}

type SkillRepository struct {
	skill.Repository
	invalidator resource.Invalidator
}

func NewSkillRepository(next skill.Repository, invalidator resource.Invalidator) *SkillRepository {
	return &SkillRepository{Repository: next, invalidator: invalidator}
}

func (r *SkillRepository) Create(ctx context.Context, item *skill.Skill) error {
	if err := r.Repository.Create(ctx, item); err != nil {
		return err
	}
	invalidate(ctx, r.invalidator, item.OwnerID, resource.KindSkills)
	return nil
}
func (r *SkillRepository) Update(ctx context.Context, item *skill.Skill) error {
	if err := r.Repository.Update(ctx, item); err != nil {
		return err
	}
	invalidate(ctx, r.invalidator, item.OwnerID, resource.KindSkills)
	return nil
}
func (r *SkillRepository) SoftDelete(ctx context.Context, ownerID, id int64) error {
	if err := r.Repository.SoftDelete(ctx, ownerID, id); err != nil {
		return err
	}
	invalidate(ctx, r.invalidator, ownerID, resource.KindSkills)
	return nil
}

type ToolDefinitionRepository struct {
	tool.DefinitionRepository
	invalidator resource.Invalidator
}

func NewToolDefinitionRepository(next tool.DefinitionRepository, invalidator resource.Invalidator) *ToolDefinitionRepository {
	return &ToolDefinitionRepository{DefinitionRepository: next, invalidator: invalidator}
}
func (r *ToolDefinitionRepository) Create(ctx context.Context, item *tool.Definition) error {
	if err := r.DefinitionRepository.Create(ctx, item); err != nil {
		return err
	}
	invalidate(ctx, r.invalidator, item.OwnerID, resource.KindHTTPTools)
	return nil
}
func (r *ToolDefinitionRepository) Update(ctx context.Context, item *tool.Definition) error {
	if err := r.DefinitionRepository.Update(ctx, item); err != nil {
		return err
	}
	invalidate(ctx, r.invalidator, item.OwnerID, resource.KindHTTPTools)
	return nil
}
func (r *ToolDefinitionRepository) SoftDelete(ctx context.Context, ownerID, id int64) error {
	if err := r.DefinitionRepository.SoftDelete(ctx, ownerID, id); err != nil {
		return err
	}
	invalidate(ctx, r.invalidator, ownerID, resource.KindHTTPTools)
	return nil
}

type KnowledgeRepository struct {
	knowledge.BaseRepository
	invalidator resource.Invalidator
}

func NewKnowledgeRepository(next knowledge.BaseRepository, invalidator resource.Invalidator) *KnowledgeRepository {
	return &KnowledgeRepository{BaseRepository: next, invalidator: invalidator}
}
func (r *KnowledgeRepository) Create(ctx context.Context, item *knowledge.KnowledgeBase) error {
	if err := r.BaseRepository.Create(ctx, item); err != nil {
		return err
	}
	invalidate(ctx, r.invalidator, item.OwnerID, resource.KindKnowledgeBases)
	return nil
}
func (r *KnowledgeRepository) Update(ctx context.Context, item *knowledge.KnowledgeBase) error {
	if err := r.BaseRepository.Update(ctx, item); err != nil {
		return err
	}
	invalidate(ctx, r.invalidator, item.OwnerID, resource.KindKnowledgeBases)
	return nil
}
func (r *KnowledgeRepository) SoftDelete(ctx context.Context, ownerID, id int64) error {
	if err := r.BaseRepository.SoftDelete(ctx, ownerID, id); err != nil {
		return err
	}
	invalidate(ctx, r.invalidator, ownerID, resource.KindKnowledgeBases)
	return nil
}
func (r *KnowledgeRepository) AdjustCounts(ctx context.Context, ownerID, id int64, documentDelta, chunkDelta int) error {
	if err := r.BaseRepository.AdjustCounts(ctx, ownerID, id, documentDelta, chunkDelta); err != nil {
		return err
	}
	invalidate(ctx, r.invalidator, ownerID, resource.KindKnowledgeBases)
	return nil
}

type MemoryRepository struct {
	memory.Repository
	resourceInvalidator resource.Invalidator
	memoryCache         memory.Cache
}

func NewMemoryRepository(next memory.Repository, resourceInvalidator resource.Invalidator, memoryCache memory.Cache) *MemoryRepository {
	return &MemoryRepository{Repository: next, resourceInvalidator: resourceInvalidator, memoryCache: memoryCache}
}
func (r *MemoryRepository) changed(ctx context.Context, ownerID int64) {
	invalidate(ctx, r.resourceInvalidator, ownerID, resource.KindMemories)
	if r.memoryCache != nil {
		_ = r.memoryCache.InvalidateOwner(ctx, ownerID)
	}
}
func (r *MemoryRepository) Create(ctx context.Context, item *memory.Memory) error {
	if err := r.Repository.Create(ctx, item); err != nil {
		return err
	}
	r.changed(ctx, item.OwnerID)
	return nil
}
func (r *MemoryRepository) Replace(ctx context.Context, ownerID, supersededID int64, replacement *memory.Memory) error {
	repository, ok := r.Repository.(memory.AtomicReplacementRepository)
	if !ok {
		return fmt.Errorf("memory repository does not support atomic replacement")
	}
	if err := repository.Replace(ctx, ownerID, supersededID, replacement); err != nil {
		return err
	}
	r.changed(ctx, ownerID)
	return nil
}
func (r *MemoryRepository) ListFiltered(ctx context.Context, ownerID int64, filter memory.ListFilter) ([]memory.Memory, error) {
	repository, ok := r.Repository.(memory.FilteredRepository)
	if !ok {
		return nil, fmt.Errorf("memory repository does not support filtered listing")
	}
	return repository.ListFiltered(ctx, ownerID, filter)
}
func (r *MemoryRepository) ListForReadScoped(ctx context.Context, ownerID, agentID int64, memoryTypes []string, conversationID, projectID *int64, limit int) ([]memory.Memory, error) {
	repository, ok := r.Repository.(memory.ScopedReader)
	if !ok {
		return nil, fmt.Errorf("memory repository does not support scoped reading")
	}
	return repository.ListForReadScoped(ctx, ownerID, agentID, memoryTypes, conversationID, projectID, limit)
}
func (r *MemoryRepository) FindByIDs(ctx context.Context, ownerID int64, ids []int64) ([]memory.Memory, error) {
	byID := make(map[int64]memory.Memory, len(ids))
	misses := make([]int64, 0, len(ids))
	for _, id := range ids {
		if r.memoryCache != nil {
			if items, hit, err := r.memoryCache.Get(ctx, ownerID, fmt.Sprintf("id:%d", id)); err == nil && hit && len(items) > 0 {
				byID[id] = items[0]
				continue
			}
		}
		misses = append(misses, id)
	}
	if len(misses) > 0 {
		items, err := r.Repository.FindByIDs(ctx, ownerID, misses)
		if err != nil {
			return nil, err
		}
		for _, item := range items {
			byID[item.ID] = item
			if r.memoryCache != nil {
				_ = r.memoryCache.Set(ctx, ownerID, fmt.Sprintf("id:%d", item.ID), []memory.Memory{item}, 5*time.Minute)
			}
		}
	}
	ordered := make([]memory.Memory, 0, len(ids))
	for _, id := range ids {
		if item, ok := byID[id]; ok {
			ordered = append(ordered, item)
		}
	}
	return ordered, nil
}
func (r *MemoryRepository) Update(ctx context.Context, item *memory.Memory) error {
	if err := r.Repository.Update(ctx, item); err != nil {
		return err
	}
	r.changed(ctx, item.OwnerID)
	return nil
}
func (r *MemoryRepository) SoftDelete(ctx context.Context, ownerID, id int64) error {
	if err := r.Repository.SoftDelete(ctx, ownerID, id); err != nil {
		return err
	}
	r.changed(ctx, ownerID)
	return nil
}
func (r *MemoryRepository) MarkUsed(ctx context.Context, ownerID int64, ids []int64) error {
	if err := r.Repository.MarkUsed(ctx, ownerID, ids); err != nil {
		return err
	}
	if len(ids) > 0 {
		r.changed(ctx, ownerID)
	}
	return nil
}
func (r *MemoryRepository) IncrementUsageCount(ctx context.Context, ownerID, id int64) error {
	if err := r.Repository.IncrementUsageCount(ctx, ownerID, id); err != nil {
		return err
	}
	r.changed(ctx, ownerID)
	return nil
}
func (r *MemoryRepository) IncrementPromotionCount(ctx context.Context, ownerID, id int64) error {
	if err := r.Repository.IncrementPromotionCount(ctx, ownerID, id); err != nil {
		return err
	}
	r.changed(ctx, ownerID)
	return nil
}
func (r *MemoryRepository) MarkExpired(ctx context.Context, ownerID int64, maxAgeDays int) (int64, error) {
	count, err := r.Repository.MarkExpired(ctx, ownerID, maxAgeDays)
	if err == nil && count > 0 {
		r.changed(ctx, ownerID)
	}
	return count, err
}
func (r *MemoryRepository) UpdateDecayedImportance(ctx context.Context, ownerID int64, decayRate float64) (int64, error) {
	count, err := r.Repository.UpdateDecayedImportance(ctx, ownerID, decayRate)
	if err == nil && count > 0 {
		r.changed(ctx, ownerID)
	}
	return count, err
}
