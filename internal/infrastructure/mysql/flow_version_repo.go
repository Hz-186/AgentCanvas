package mysql

import (
	"context"
	"time"

	"agentcanvas/internal/domain/workflow"

	"gorm.io/gorm"
)

type WorkflowVersionRepository struct{ db *gorm.DB }

func NewFlowVersionRepository(db *gorm.DB) *WorkflowVersionRepository {
	return &WorkflowVersionRepository{db: db}
}

func (r *WorkflowVersionRepository) Create(ctx context.Context, item *workflow.WorkflowVersion) error {
	now := time.Now().UTC()
	item.CreatedAt = now
	item.UpdatedAt = now
	return r.db.WithContext(ctx).Create(item).Error
}

func (r *WorkflowVersionRepository) ListByWorkflow(ctx context.Context, ownerID, workflowID int64) ([]workflow.WorkflowVersion, error) {
	var items []workflow.WorkflowVersion
	err := r.db.WithContext(ctx).Where("owner_id = ? AND workflow_id = ?", ownerID, workflowID).Order("version_no DESC").Find(&items).Error
	return items, err
}

func (r *WorkflowVersionRepository) FindByID(ctx context.Context, ownerID, id int64) (*workflow.WorkflowVersion, error) {
	var item workflow.WorkflowVersion
	err := r.db.WithContext(ctx).Where("id = ? AND owner_id = ?", id, ownerID).First(&item).Error
	if err != nil {
		return nil, err
	}
	return &item, nil
}

func (r *WorkflowVersionRepository) FindCurrentByWorkflow(ctx context.Context, ownerID, workflowID int64) (*workflow.WorkflowVersion, error) {
	var item workflow.WorkflowVersion
	err := r.db.WithContext(ctx).Where("owner_id = ? AND workflow_id = ? AND is_published = 1", ownerID, workflowID).Order("version_no DESC").First(&item).Error
	if err != nil {
		return nil, err
	}
	return &item, nil
}

func (r *WorkflowVersionRepository) FindLatestByWorkflow(ctx context.Context, ownerID, workflowID int64) (*workflow.WorkflowVersion, error) {
	var item workflow.WorkflowVersion
	err := r.db.WithContext(ctx).Where("owner_id = ? AND workflow_id = ?", ownerID, workflowID).Order("version_no DESC").First(&item).Error
	if err != nil {
		return nil, err
	}
	return &item, nil
}

func (r *WorkflowVersionRepository) NextVersionNo(ctx context.Context, ownerID, workflowID int64) (int, error) {
	var maxVersion int
	err := r.db.WithContext(ctx).Model(&workflow.WorkflowVersion{}).Where("owner_id = ? AND workflow_id = ?", ownerID, workflowID).Select("COALESCE(MAX(version_no), 0)").Scan(&maxVersion).Error
	return maxVersion + 1, err
}

func (r *WorkflowVersionRepository) Publish(ctx context.Context, ownerID, workflowID, versionID int64) error {
	now := time.Now().UTC()
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&workflow.WorkflowVersion{}).Where("owner_id = ? AND workflow_id = ?", ownerID, workflowID).Updates(map[string]any{"is_published": false, "updated_at": now}).Error; err != nil {
			return err
		}
		if err := tx.Model(&workflow.WorkflowVersion{}).Where("id = ? AND owner_id = ? AND workflow_id = ?", versionID, ownerID, workflowID).Updates(map[string]any{"is_published": true, "is_draft": false, "updated_at": now}).Error; err != nil {
			return err
		}
		return tx.Model(&workflow.Workflow{}).Where("id = ? AND owner_id = ?", workflowID, ownerID).Updates(map[string]any{"current_version_id": versionID, "updated_at": now}).Error
	})
}
