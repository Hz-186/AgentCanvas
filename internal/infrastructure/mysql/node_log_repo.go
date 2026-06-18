package mysql

import (
	"context"
	"time"

	"agentcanvas/internal/domain/agent"

	"gorm.io/gorm"
)

type NodeLogRepository struct{ db *gorm.DB }

func NewNodeLogRepository(db *gorm.DB) *NodeLogRepository { return &NodeLogRepository{db: db} }

func (r *NodeLogRepository) Create(ctx context.Context, item *agent.NodeLog) error {
	if item.CreatedAt.IsZero() {
		item.CreatedAt = time.Now().UTC()
	}
	if item.StartedAt.IsZero() {
		item.StartedAt = item.CreatedAt
	}
	return r.db.WithContext(ctx).Create(item).Error
}

func (r *NodeLogRepository) Update(ctx context.Context, item *agent.NodeLog) error {
	return r.db.WithContext(ctx).Save(item).Error
}

func (r *NodeLogRepository) ListByRun(ctx context.Context, ownerID, runID int64) ([]agent.NodeLog, error) {
	var items []agent.NodeLog
	err := r.db.WithContext(ctx).Where("owner_id = ? AND run_id = ?", ownerID, runID).Order("id ASC").Find(&items).Error
	return items, err
}
