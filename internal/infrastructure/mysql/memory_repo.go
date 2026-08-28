package mysql

import (
	"context"
	"time"

	"agentcanvas/internal/domain/contextresource"
	"agentcanvas/internal/domain/memory"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type MemoryRepository struct{ db *gorm.DB }

func NewMemoryRepository(db *gorm.DB) *MemoryRepository { return &MemoryRepository{db: db} }

func (r *MemoryRepository) Create(ctx context.Context, item *memory.Memory) error {
	now := time.Now().UTC()
	item.ApplyV2Defaults()
	if item.RetentionTier == "" {
		item.RetentionTier = memory.TierLongTerm
	}
	if item.CreatedAt.IsZero() {
		item.CreatedAt = now
	}
	if item.UpdatedAt.IsZero() {
		item.UpdatedAt = now
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var create *gorm.DB
		if item.DeduplicationKey != nil {
			create = tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "owner_id"}, {Name: "deduplication_key"}}, DoNothing: true}).Create(item)
		} else {
			create = tx.Create(item)
		}
		if err := create.Error; err != nil {
			return err
		}
		if create.RowsAffected == 0 && item.DeduplicationKey != nil {
			if err := tx.Where("owner_id = ? AND deduplication_key = ?", item.OwnerID, *item.DeduplicationKey).First(item).Error; err != nil {
				return err
			}
			return nil
		}
		return enqueueMemoryContextResource(ctx, tx, item, now)
	})
}

func (r *MemoryRepository) Update(ctx context.Context, item *memory.Memory) error {
	item.ApplyV2Defaults()
	if item.RetentionTier == "" {
		item.RetentionTier = memory.TierLongTerm
	}
	item.UpdatedAt = time.Now().UTC()
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Save(item).Error; err != nil {
			return err
		}
		return enqueueMemoryContextResource(ctx, tx, item, item.UpdatedAt)
	})
}

func (r *MemoryRepository) FindByID(ctx context.Context, ownerID, id int64) (*memory.Memory, error) {
	var item memory.Memory
	err := r.db.WithContext(ctx).Where("owner_id = ? AND id = ? AND deleted_at IS NULL", ownerID, id).First(&item).Error
	if err != nil {
		return nil, err
	}
	return &item, nil
}

func (r *MemoryRepository) FindByIDs(ctx context.Context, ownerID int64, ids []int64) ([]memory.Memory, error) {
	items := make([]memory.Memory, 0, len(ids))
	if len(ids) == 0 {
		return items, nil
	}
	err := r.db.WithContext(ctx).Where("owner_id = ? AND id IN ? AND deleted_at IS NULL", ownerID, ids).Find(&items).Error
	return items, err
}

func (r *MemoryRepository) List(ctx context.Context, ownerID int64, memoryTypes []string, conversationID *int64, limit, offset int) ([]memory.Memory, error) {
	var items []memory.Memory
	query := r.db.WithContext(ctx).Where("owner_id = ? AND deleted_at IS NULL", ownerID)
	query = filterMemories(query, memoryTypes, conversationID, nil)
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	err := query.Order("updated_at DESC, id DESC").Limit(limit).Offset(offset).Find(&items).Error
	return items, err
}

func (r *MemoryRepository) ListFiltered(ctx context.Context, ownerID int64, filter memory.ListFilter) ([]memory.Memory, error) {
	var items []memory.Memory
	query := r.db.WithContext(ctx).Where("owner_id = ? AND deleted_at IS NULL", ownerID)
	query = filterMemories(query, filter.MemoryTypes, filter.SourceConversationID, filter.SourceProjectID)
	if len(filter.Statuses) > 0 {
		query = query.Where("status IN ?", filter.Statuses)
	}
	if len(filter.ScopeTypes) > 0 {
		query = query.Where("scope_type IN ?", filter.ScopeTypes)
	}
	if filter.ScopeID != nil {
		query = query.Where("scope_id = ?", *filter.ScopeID)
	}
	if len(filter.Sources) > 0 {
		query = query.Where("source IN ?", filter.Sources)
	}
	limit, offset := filter.Limit, filter.Offset
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	err := query.Order("updated_at DESC, id DESC").Limit(limit).Offset(offset).Find(&items).Error
	return items, err
}

// ListBySources implements memory.Repository: active, non-conflicted,
// non-expired rows for the given production sources ordered by id ASC.
// Consolidation evidence is sourced from these rows so artifact source refs
// always carry memories.id values, never job ids.
func (r *MemoryRepository) ListBySources(ctx context.Context, ownerID int64, sources []string, limit int) ([]memory.Memory, error) {
	items := make([]memory.Memory, 0)
	if len(sources) == 0 {
		return items, nil
	}
	query := r.db.WithContext(ctx).
		Where("owner_id = ? AND deleted_at IS NULL", ownerID).
		Where("status = ? AND has_conflict = ?", memory.StatusActive, false).
		Where("(expires_at IS NULL OR expires_at > ?)", time.Now().UTC()).
		Where("source IN ?", sources)
	if limit <= 0 || limit > 256 {
		limit = 256
	}
	err := query.Order("id ASC").Limit(limit).Find(&items).Error
	return items, err
}

func (r *MemoryRepository) ListForRead(ctx context.Context, ownerID int64, memoryTypes []string, conversationID *int64, limit int) ([]memory.Memory, error) {
	return r.listForRead(ctx, ownerID, 0, memoryTypes, conversationID, nil, limit)
}

func (r *MemoryRepository) ListForReadScoped(ctx context.Context, ownerID, agentID int64, memoryTypes []string, conversationID, projectID *int64, limit int) ([]memory.Memory, error) {
	return r.listForRead(ctx, ownerID, agentID, memoryTypes, conversationID, projectID, limit)
}

func (r *MemoryRepository) listForRead(ctx context.Context, ownerID, agentID int64, memoryTypes []string, conversationID, projectID *int64, limit int) ([]memory.Memory, error) {
	var items []memory.Memory
	query := r.db.WithContext(ctx).Where("owner_id = ? AND deleted_at IS NULL", ownerID).
		Where("status = ? AND has_conflict = ?", memory.StatusActive, false).
		Where("(expires_at IS NULL OR expires_at > ?)", time.Now().UTC())
	if projectID != nil && *projectID > 0 {
		if len(memoryTypes) > 0 {
			query = query.Where("memory_type IN ?", memoryTypes)
		}
		conversationScope := int64(0)
		if conversationID != nil {
			conversationScope = *conversationID
		}
		scopeSQL := "(scope_type = ? AND (scope_id = ? OR scope_id = 0)) OR (scope_type = ? AND scope_id = ?) OR (scope_type = ? AND scope_id = ?)"
		scopeArgs := []any{memory.ScopeUser, ownerID, memory.ScopeProject, *projectID, memory.ScopeConversation, conversationScope}
		if agentID > 0 {
			scopeSQL += " OR (scope_type = ? AND scope_id = ?)"
			scopeArgs = append(scopeArgs, memory.ScopeAgent, agentID)
		}
		query = query.Where("("+scopeSQL+")", scopeArgs...)
	} else {
		if len(memoryTypes) > 0 {
			query = query.Where("memory_type IN ?", memoryTypes)
		}
		scopeSQL := "scope_type = ? AND (scope_id = ? OR scope_id = 0)"
		scopeArgs := []any{memory.ScopeUser, ownerID}
		if conversationID != nil && *conversationID > 0 {
			scopeSQL += " OR (scope_type = ? AND scope_id = ?)"
			scopeArgs = append(scopeArgs, memory.ScopeConversation, *conversationID)
		}
		if agentID > 0 {
			scopeSQL += " OR (scope_type = ? AND scope_id = ?)"
			scopeArgs = append(scopeArgs, memory.ScopeAgent, agentID)
		}
		query = query.Where("("+scopeSQL+")", scopeArgs...)
	}
	if limit <= 0 || limit > 20 {
		limit = 5
	}
	err := query.Order("importance DESC, updated_at DESC, id DESC").Limit(limit).Find(&items).Error
	return items, err
}

func (r *MemoryRepository) MarkUsed(ctx context.Context, ownerID int64, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	now := time.Now().UTC()
	return r.db.WithContext(ctx).Model(&memory.Memory{}).
		Where("owner_id = ? AND id IN ? AND deleted_at IS NULL AND status = ?", ownerID, ids, memory.StatusActive).
		Updates(map[string]any{"last_used_at": now, "updated_at": now, "usage_count": gorm.Expr("usage_count + 1")}).Error
}

func filterMemories(query *gorm.DB, memoryTypes []string, conversationID, projectID *int64) *gorm.DB {
	if len(memoryTypes) > 0 {
		query = query.Where("memory_type IN ?", memoryTypes)
	}
	if conversationID != nil {
		query = query.Where("(source_conversation_id IS NULL OR source_conversation_id = ?)", *conversationID)
	}
	if projectID != nil {
		query = query.Where("(source_project_id IS NULL OR source_project_id = ?)", *projectID)
	}
	return query
}

func enqueueMemoryContextResource(ctx context.Context, tx *gorm.DB, item *memory.Memory, now time.Time) error {
	if item == nil {
		return nil
	}
	agentID, conversationID, _ := memoryIndexScope(item)
	if item != nil && item.IsRecallable(now) {
		return enqueueContextResource(ctx, tx, item.OwnerID, agentID, conversationID, contextresource.TypeLongTermMemory, item.ID, contextresource.OperationUpsert, memoryContextText(*item))
	}
	return enqueueContextResource(ctx, tx, item.OwnerID, agentID, conversationID, contextresource.TypeLongTermMemory, item.ID, contextresource.OperationDelete, "")
}

func memoryIndexScope(item *memory.Memory) (agentID, conversationID, projectID int64) {
	if item == nil {
		return 0, 0, 0
	}
	switch item.ScopeType {
	case memory.ScopeAgent:
		agentID = item.ScopeID
	case memory.ScopeConversation:
		conversationID = item.ScopeID
	case memory.ScopeProject:
		projectID = item.ScopeID
	}
	return agentID, conversationID, projectID
}

type MemoryRecallLogRepository struct{ db *gorm.DB }

func NewMemoryRecallLogRepository(db *gorm.DB) *MemoryRecallLogRepository {
	return &MemoryRecallLogRepository{db: db}
}

func (r *MemoryRecallLogRepository) Create(ctx context.Context, item *memory.RecallLog) error {
	if item.CreatedAt.IsZero() {
		item.CreatedAt = time.Now().UTC()
	}
	return r.db.WithContext(ctx).Create(item).Error
}

func (r *MemoryRecallLogRepository) List(ctx context.Context, ownerID, memoryID int64, limit int) ([]memory.RecallLog, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	query := r.db.WithContext(ctx).Where("owner_id = ?", ownerID)
	if memoryID > 0 {
		query = query.Where("JSON_CONTAINS(injected_json, JSON_OBJECT('memory_id', ?))", memoryID)
	}
	var items []memory.RecallLog
	return items, query.Order("id DESC").Limit(limit).Find(&items).Error
}

func (r *MemoryRecallLogRepository) SetFeedback(ctx context.Context, ownerID, id int64, feedback string) error {
	return r.db.WithContext(ctx).Model(&memory.RecallLog{}).Where("owner_id = ? AND id = ?", ownerID, id).Update("feedback", feedback).Error
}
