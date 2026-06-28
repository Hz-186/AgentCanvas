package mysql

import (
	"context"
	"time"

	"agentcanvas/internal/domain/agent"

	"gorm.io/gorm"
)

type ApprovalRepository struct{ db *gorm.DB }

func NewApprovalRepository(db *gorm.DB) *ApprovalRepository { return &ApprovalRepository{db: db} }

func (r *ApprovalRepository) CreateApprovalRequest(ctx context.Context, item *agent.ApprovalRequest) error {
	now := time.Now().UTC()
	item.CreatedAt = now
	item.UpdatedAt = now
	if item.Status == "" {
		item.Status = agent.ApprovalStatusPending
	}
	return r.db.WithContext(ctx).Create(item).Error
}

func (r *ApprovalRepository) FindApprovalRequestByID(ctx context.Context, ownerID, id int64) (*agent.ApprovalRequest, error) {
	var item agent.ApprovalRequest
	err := r.db.WithContext(ctx).Where("id = ? AND owner_id = ?", id, ownerID).First(&item).Error
	if err != nil {
		return nil, err
	}
	return &item, nil
}

func (r *ApprovalRepository) FindPendingApprovalByRun(ctx context.Context, ownerID, runID int64) (*agent.ApprovalRequest, error) {
	var item agent.ApprovalRequest
	err := r.db.WithContext(ctx).
		Where("owner_id = ? AND run_id = ? AND status = ?", ownerID, runID, agent.ApprovalStatusPending).
		Order("id DESC").
		First(&item).Error
	if err != nil {
		return nil, err
	}
	return &item, nil
}

func (r *ApprovalRepository) ListApprovalRequests(ctx context.Context, ownerID int64, status string) ([]agent.ApprovalRequest, error) {
	query := r.db.WithContext(ctx).Where("owner_id = ?", ownerID)
	if status != "" {
		query = query.Where("status = ?", status)
	}
	var items []agent.ApprovalRequest
	err := query.Order("id DESC").Find(&items).Error
	return items, err
}

func (r *ApprovalRepository) UpdateApprovalRequest(ctx context.Context, item *agent.ApprovalRequest) error {
	item.UpdatedAt = time.Now().UTC()
	return r.db.WithContext(ctx).Save(item).Error
}

func (r *ApprovalRepository) CreateCheckpoint(ctx context.Context, item *agent.AgentCheckpoint) error {
	now := time.Now().UTC()
	item.CreatedAt = now
	item.UpdatedAt = now
	return r.db.WithContext(ctx).Create(item).Error
}

func (r *ApprovalRepository) FindLatestCheckpointByRun(ctx context.Context, ownerID, runID int64) (*agent.AgentCheckpoint, error) {
	var item agent.AgentCheckpoint
	err := r.db.WithContext(ctx).
		Where("owner_id = ? AND run_id = ?", ownerID, runID).
		Order("id DESC").
		First(&item).Error
	if err != nil {
		return nil, err
	}
	return &item, nil
}
