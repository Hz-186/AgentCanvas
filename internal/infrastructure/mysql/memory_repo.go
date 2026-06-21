package mysql

import (
	"context"
	"time"

	"agentcanvas/internal/domain/memory"

	"gorm.io/gorm"
)

type MemoryRepository struct{ db *gorm.DB }

func NewMemoryRepository(db *gorm.DB) *MemoryRepository { return &MemoryRepository{db: db} }

func (r *MemoryRepository) Create(ctx context.Context, item *memory.Memory) error {
	now := time.Now().UTC()
	if item.CreatedAt.IsZero() {
		item.CreatedAt = now
	}
	if item.UpdatedAt.IsZero() {
		item.UpdatedAt = now
	}
	return r.db.WithContext(ctx).Create(item).Error
}

func (r *MemoryRepository) Update(ctx context.Context, item *memory.Memory) error {
	item.UpdatedAt = time.Now().UTC()
	return r.db.WithContext(ctx).Save(item).Error
}

func (r *MemoryRepository) FindByID(ctx context.Context, ownerID, id int64) (*memory.Memory, error) {
	var item memory.Memory
	err := r.db.WithContext(ctx).Where("owner_id = ? AND id = ? AND deleted_at IS NULL", ownerID, id).First(&item).Error
	if err != nil {
		return nil, err
	}
	return &item, nil
}

func (r *MemoryRepository) List(ctx context.Context, ownerID int64, memoryTypes []string, conversationID *int64, limit, offset int) ([]memory.Memory, error) {
	var items []memory.Memory
	query := r.db.WithContext(ctx).Where("owner_id = ? AND deleted_at IS NULL", ownerID)
	query = filterMemories(query, memoryTypes, conversationID)
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	err := query.Order("updated_at DESC, id DESC").Limit(limit).Offset(offset).Find(&items).Error
	return items, err
}

func (r *MemoryRepository) ListForRead(ctx context.Context, ownerID int64, memoryTypes []string, conversationID *int64, limit int) ([]memory.Memory, error) {
	var items []memory.Memory
	query := r.db.WithContext(ctx).Where("owner_id = ? AND deleted_at IS NULL", ownerID).
		Where("(expires_at IS NULL OR expires_at > ?)", time.Now().UTC())
	query = filterMemories(query, memoryTypes, conversationID)
	if limit <= 0 || limit > 20 {
		limit = 5
	}
	err := query.Order("importance DESC, updated_at DESC, id DESC").Limit(limit).Find(&items).Error
	return items, err
}

func (r *MemoryRepository) SoftDelete(ctx context.Context, ownerID, id int64) error {
	now := time.Now().UTC()
	return r.db.WithContext(ctx).Model(&memory.Memory{}).
		Where("owner_id = ? AND id = ? AND deleted_at IS NULL", ownerID, id).
		Updates(map[string]any{"deleted_at": now, "updated_at": now}).Error
}

func (r *MemoryRepository) MarkUsed(ctx context.Context, ownerID int64, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	now := time.Now().UTC()
	return r.db.WithContext(ctx).Model(&memory.Memory{}).
		Where("owner_id = ? AND id IN ?", ownerID, ids).
		Updates(map[string]any{"last_used_at": now, "updated_at": now}).Error
}

func filterMemories(query *gorm.DB, memoryTypes []string, conversationID *int64) *gorm.DB {
	if len(memoryTypes) > 0 {
		query = query.Where("memory_type IN ?", memoryTypes)
	}
	if conversationID != nil {
		query = query.Where("(conversation_id IS NULL OR conversation_id = ?)", *conversationID)
	}
	return query
}

type MemoryWriteLogRepository struct{ db *gorm.DB }

func NewMemoryWriteLogRepository(db *gorm.DB) *MemoryWriteLogRepository {
	return &MemoryWriteLogRepository{db: db}
}

func (r *MemoryWriteLogRepository) Create(ctx context.Context, item *memory.WriteLog) error {
	if item.CreatedAt.IsZero() {
		item.CreatedAt = time.Now().UTC()
	}
	return r.db.WithContext(ctx).Create(item).Error
}

func (r *MemoryWriteLogRepository) ListByRun(ctx context.Context, ownerID, runID int64) ([]memory.WriteLog, error) {
	var items []memory.WriteLog
	err := r.db.WithContext(ctx).Where("owner_id = ? AND run_id = ?", ownerID, runID).Order("id ASC").Find(&items).Error
	return items, err
}
