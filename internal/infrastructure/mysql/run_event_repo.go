package mysql

import (
	"context"
	"time"

	agentdomain "agentcanvas/internal/domain/agent"

	"gorm.io/gorm"
)

type RunEventRepository struct{ db *gorm.DB }

func NewRunEventRepository(db *gorm.DB) *RunEventRepository { return &RunEventRepository{db: db} }

func (r *RunEventRepository) Create(ctx context.Context, item *agentdomain.RunEvent) error {
	if item.CreatedAt.IsZero() {
		item.CreatedAt = time.Now().UTC()
	}
	return r.db.WithContext(ctx).Create(item).Error
}

func (r *RunEventRepository) ListByRun(ctx context.Context, ownerID, runID int64) ([]agentdomain.RunEvent, error) {
	var items []agentdomain.RunEvent
	err := r.db.WithContext(ctx).Where("owner_id = ? AND run_id = ?", ownerID, runID).Order("id ASC").Find(&items).Error
	return items, err
}
