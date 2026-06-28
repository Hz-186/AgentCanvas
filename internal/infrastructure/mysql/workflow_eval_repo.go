package mysql

import (
	"context"
	"time"

	"agentcanvas/internal/domain/workflow"

	"gorm.io/gorm"
)

type WorkflowEvalRepository struct{ db *gorm.DB }

func NewWorkflowEvalRepository(db *gorm.DB) *WorkflowEvalRepository {
	return &WorkflowEvalRepository{db: db}
}

func (r *WorkflowEvalRepository) CreateDataset(ctx context.Context, item *workflow.EvalDataset) error {
	now := time.Now().UTC()
	item.CreatedAt = now
	item.UpdatedAt = now
	return r.db.WithContext(ctx).Create(item).Error
}

func (r *WorkflowEvalRepository) ListDatasetsByWorkflow(ctx context.Context, ownerID, agentID int64) ([]workflow.EvalDataset, error) {
	var items []workflow.EvalDataset
	err := r.db.WithContext(ctx).
		Where("owner_id = ? AND workflow_id = ? AND deleted_at IS NULL", ownerID, agentID).
		Order("id DESC").
		Find(&items).Error
	return items, err
}

func (r *WorkflowEvalRepository) FindDatasetByID(ctx context.Context, ownerID, id int64) (*workflow.EvalDataset, error) {
	var item workflow.EvalDataset
	err := r.db.WithContext(ctx).
		Where("id = ? AND owner_id = ? AND deleted_at IS NULL", id, ownerID).
		First(&item).Error
	if err != nil {
		return nil, err
	}
	return &item, nil
}

func (r *WorkflowEvalRepository) CreateCase(ctx context.Context, item *workflow.EvalCase) error {
	now := time.Now().UTC()
	item.CreatedAt = now
	item.UpdatedAt = now
	return r.db.WithContext(ctx).Create(item).Error
}

func (r *WorkflowEvalRepository) ListCasesByDataset(ctx context.Context, ownerID, datasetID int64) ([]workflow.EvalCase, error) {
	var items []workflow.EvalCase
	err := r.db.WithContext(ctx).
		Where("owner_id = ? AND dataset_id = ? AND deleted_at IS NULL", ownerID, datasetID).
		Order("id ASC").
		Find(&items).Error
	return items, err
}

func (r *WorkflowEvalRepository) CreateEvalRun(ctx context.Context, item *workflow.EvalRun) error {
	now := time.Now().UTC()
	item.CreatedAt = now
	item.UpdatedAt = now
	if item.StartedAt.IsZero() {
		item.StartedAt = now
	}
	return r.db.WithContext(ctx).Create(item).Error
}

func (r *WorkflowEvalRepository) UpdateEvalRun(ctx context.Context, item *workflow.EvalRun) error {
	item.UpdatedAt = time.Now().UTC()
	return r.db.WithContext(ctx).Save(item).Error
}

func (r *WorkflowEvalRepository) FindEvalRunByID(ctx context.Context, ownerID, id int64) (*workflow.EvalRun, error) {
	var item workflow.EvalRun
	err := r.db.WithContext(ctx).
		Where("id = ? AND owner_id = ?", id, ownerID).
		First(&item).Error
	if err != nil {
		return nil, err
	}
	return &item, nil
}

func (r *WorkflowEvalRepository) ListEvalRunsByDataset(ctx context.Context, ownerID, datasetID int64) ([]workflow.EvalRun, error) {
	var items []workflow.EvalRun
	err := r.db.WithContext(ctx).
		Where("owner_id = ? AND dataset_id = ?", ownerID, datasetID).
		Order("id DESC").
		Find(&items).Error
	return items, err
}

func (r *WorkflowEvalRepository) CreateEvalResult(ctx context.Context, item *workflow.EvalResult) error {
	if item.CreatedAt.IsZero() {
		item.CreatedAt = time.Now().UTC()
	}
	return r.db.WithContext(ctx).Create(item).Error
}

func (r *WorkflowEvalRepository) ListEvalResultsByRun(ctx context.Context, ownerID, evalRunID int64) ([]workflow.EvalResult, error) {
	var items []workflow.EvalResult
	err := r.db.WithContext(ctx).
		Where("owner_id = ? AND eval_run_id = ?", ownerID, evalRunID).
		Order("id ASC").
		Find(&items).Error
	return items, err
}
