package mysql

import (
	"context"
	"time"

	"agentcanvas/internal/domain/tool"

	"gorm.io/gorm"
)

type ToolDefinitionRepository struct{ db *gorm.DB }

func NewToolDefinitionRepository(db *gorm.DB) *ToolDefinitionRepository {
	return &ToolDefinitionRepository{db: db}
}

func (r *ToolDefinitionRepository) Create(ctx context.Context, item *tool.Definition) error {
	now := time.Now().UTC()
	if item.CreatedAt.IsZero() {
		item.CreatedAt = now
	}
	if item.UpdatedAt.IsZero() {
		item.UpdatedAt = now
	}
	return r.db.WithContext(ctx).Create(item).Error
}

func (r *ToolDefinitionRepository) Update(ctx context.Context, item *tool.Definition) error {
	item.UpdatedAt = time.Now().UTC()
	return r.db.WithContext(ctx).Save(item).Error
}

func (r *ToolDefinitionRepository) FindByID(ctx context.Context, ownerID, id int64) (*tool.Definition, error) {
	var item tool.Definition
	err := r.db.WithContext(ctx).Where("owner_id = ? AND id = ? AND deleted_at IS NULL", ownerID, id).First(&item).Error
	if err != nil {
		return nil, err
	}
	return &item, nil
}

func (r *ToolDefinitionRepository) List(ctx context.Context, ownerID int64, limit, offset int) ([]tool.Definition, error) {
	var items []tool.Definition
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	err := r.db.WithContext(ctx).Where("owner_id = ? AND deleted_at IS NULL", ownerID).
		Order("updated_at DESC, id DESC").Limit(limit).Offset(offset).Find(&items).Error
	return items, err
}

func (r *ToolDefinitionRepository) SoftDelete(ctx context.Context, ownerID, id int64) error {
	now := time.Now().UTC()
	return r.db.WithContext(ctx).Model(&tool.Definition{}).
		Where("owner_id = ? AND id = ? AND deleted_at IS NULL", ownerID, id).
		Updates(map[string]any{"deleted_at": now, "updated_at": now}).Error
}

type ToolInvocationRepository struct{ db *gorm.DB }

func NewToolInvocationRepository(db *gorm.DB) *ToolInvocationRepository {
	return &ToolInvocationRepository{db: db}
}

func (r *ToolInvocationRepository) Create(ctx context.Context, item *tool.Invocation) error {
	if item.CreatedAt.IsZero() {
		item.CreatedAt = time.Now().UTC()
	}
	return r.db.WithContext(ctx).Create(item).Error
}

func (r *ToolInvocationRepository) ListByRun(ctx context.Context, ownerID, runID int64) ([]tool.Invocation, error) {
	var items []tool.Invocation
	err := r.db.WithContext(ctx).Where("owner_id = ? AND run_id = ?", ownerID, runID).Order("id ASC").Find(&items).Error
	return items, err
}
