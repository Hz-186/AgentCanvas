package mysql

import (
	"context"
	"time"

	"agentcanvas/internal/domain/workflow"

	"gorm.io/gorm"
)

type WorkflowRepository struct{ db *gorm.DB }

func NewWorkflowRepository(db *gorm.DB) *WorkflowRepository { return &WorkflowRepository{db: db} }

func (r *WorkflowRepository) Create(ctx context.Context, item *workflow.Workflow) error {
	now := time.Now().UTC()
	item.CreatedAt = now
	item.UpdatedAt = now
	return r.db.WithContext(ctx).Create(item).Error
}

func (r *WorkflowRepository) ListByOwner(ctx context.Context, ownerID int64) ([]workflow.Workflow, error) {
	var items []workflow.Workflow
	err := r.db.WithContext(ctx).Where("owner_id = ? AND deleted_at IS NULL", ownerID).Order("id DESC").Find(&items).Error
	return items, err
}

func (r *WorkflowRepository) FindByID(ctx context.Context, ownerID, id int64) (*workflow.Workflow, error) {
	var item workflow.Workflow
	err := r.db.WithContext(ctx).Where("id = ? AND owner_id = ? AND deleted_at IS NULL", id, ownerID).First(&item).Error
	if err != nil {
		return nil, err
	}
	return &item, nil
}

func (r *WorkflowRepository) Update(ctx context.Context, item *workflow.Workflow) error {
	item.UpdatedAt = time.Now().UTC()
	return r.db.WithContext(ctx).Save(item).Error
}

func (r *WorkflowRepository) SoftDelete(ctx context.Context, ownerID, id int64) error {
	now := time.Now().UTC()
	return r.db.WithContext(ctx).Model(&workflow.Workflow{}).Where("id = ? AND owner_id = ? AND deleted_at IS NULL", id, ownerID).Updates(map[string]any{"deleted_at": now, "updated_at": now}).Error
}
