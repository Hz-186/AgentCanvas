package mysql

import (
	"context"
	"time"

	"agentcanvas/internal/domain/workflow"

	"gorm.io/gorm"
)

type WorkflowProfileRepository struct{ db *gorm.DB }

func NewWorkflowProfileRepository(db *gorm.DB) *WorkflowProfileRepository {
	return &WorkflowProfileRepository{db: db}
}

func (r *WorkflowProfileRepository) Create(ctx context.Context, item *workflow.Profile) error {
	now := time.Now().UTC()
	item.CreatedAt = now
	item.UpdatedAt = now
	return r.db.WithContext(ctx).Create(item).Error
}

func (r *WorkflowProfileRepository) FindByWorkflow(ctx context.Context, ownerID, agentID int64) (*workflow.Profile, error) {
	var item workflow.Profile
	err := r.db.WithContext(ctx).
		Where("owner_id = ? AND workflow_id = ? AND deleted_at IS NULL", ownerID, agentID).
		First(&item).Error
	if err != nil {
		return nil, err
	}
	return &item, nil
}

func (r *WorkflowProfileRepository) Update(ctx context.Context, item *workflow.Profile) error {
	item.UpdatedAt = time.Now().UTC()
	return r.db.WithContext(ctx).Save(item).Error
}
