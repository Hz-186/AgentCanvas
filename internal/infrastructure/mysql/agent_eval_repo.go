package mysql

import (
	"context"
	"time"

	"agentcanvas/internal/domain/agent"

	"gorm.io/gorm"
)

type AgentEvalRepository struct{ db *gorm.DB }

func NewAgentEvalRepository(db *gorm.DB) *AgentEvalRepository {
	return &AgentEvalRepository{db: db}
}

func (r *AgentEvalRepository) CreateDataset(ctx context.Context, item *agent.EvalDataset) error {
	now := time.Now().UTC()
	item.CreatedAt = now
	item.UpdatedAt = now
	return r.db.WithContext(ctx).Create(item).Error
}

func (r *AgentEvalRepository) ListDatasetsByAgent(ctx context.Context, ownerID, agentID int64) ([]agent.EvalDataset, error) {
	var items []agent.EvalDataset
	err := r.db.WithContext(ctx).
		Where("owner_id = ? AND agent_id = ? AND deleted_at IS NULL", ownerID, agentID).
		Order("id DESC").
		Find(&items).Error
	return items, err
}

func (r *AgentEvalRepository) FindDatasetByID(ctx context.Context, ownerID, id int64) (*agent.EvalDataset, error) {
	var item agent.EvalDataset
	err := r.db.WithContext(ctx).
		Where("id = ? AND owner_id = ? AND deleted_at IS NULL", id, ownerID).
		First(&item).Error
	if err != nil {
		return nil, err
	}
	return &item, nil
}

func (r *AgentEvalRepository) CreateCase(ctx context.Context, item *agent.EvalCase) error {
	now := time.Now().UTC()
	item.CreatedAt = now
	item.UpdatedAt = now
	return r.db.WithContext(ctx).Create(item).Error
}

func (r *AgentEvalRepository) ListCasesByDataset(ctx context.Context, ownerID, datasetID int64) ([]agent.EvalCase, error) {
	var items []agent.EvalCase
	err := r.db.WithContext(ctx).
		Where("owner_id = ? AND dataset_id = ? AND deleted_at IS NULL", ownerID, datasetID).
		Order("id ASC").
		Find(&items).Error
	return items, err
}

func (r *AgentEvalRepository) CreateEvalRun(ctx context.Context, item *agent.EvalRun) error {
	now := time.Now().UTC()
	item.CreatedAt = now
	item.UpdatedAt = now
	if item.StartedAt.IsZero() {
		item.StartedAt = now
	}
	return r.db.WithContext(ctx).Create(item).Error
}

func (r *AgentEvalRepository) UpdateEvalRun(ctx context.Context, item *agent.EvalRun) error {
	item.UpdatedAt = time.Now().UTC()
	return r.db.WithContext(ctx).Save(item).Error
}

func (r *AgentEvalRepository) FindEvalRunByID(ctx context.Context, ownerID, id int64) (*agent.EvalRun, error) {
	var item agent.EvalRun
	err := r.db.WithContext(ctx).
		Where("id = ? AND owner_id = ?", id, ownerID).
		First(&item).Error
	if err != nil {
		return nil, err
	}
	return &item, nil
}

func (r *AgentEvalRepository) ListEvalRunsByDataset(ctx context.Context, ownerID, datasetID int64) ([]agent.EvalRun, error) {
	var items []agent.EvalRun
	err := r.db.WithContext(ctx).
		Where("owner_id = ? AND dataset_id = ?", ownerID, datasetID).
		Order("id DESC").
		Find(&items).Error
	return items, err
}

func (r *AgentEvalRepository) CreateEvalResult(ctx context.Context, item *agent.EvalResult) error {
	if item.CreatedAt.IsZero() {
		item.CreatedAt = time.Now().UTC()
	}
	return r.db.WithContext(ctx).Create(item).Error
}

func (r *AgentEvalRepository) ListEvalResultsByRun(ctx context.Context, ownerID, evalRunID int64) ([]agent.EvalResult, error) {
	var items []agent.EvalResult
	err := r.db.WithContext(ctx).
		Where("owner_id = ? AND eval_run_id = ?", ownerID, evalRunID).
		Order("id ASC").
		Find(&items).Error
	return items, err
}
