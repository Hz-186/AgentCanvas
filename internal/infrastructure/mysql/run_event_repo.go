package mysql

import (
	"context"
	"time"

	"agentcanvas/internal/domain/workflow"

	"gorm.io/gorm"
)

type RunEventRepository struct{ db *gorm.DB }

func NewRunEventRepository(db *gorm.DB) *RunEventRepository { return &RunEventRepository{db: db} }

func (r *RunEventRepository) Create(ctx context.Context, item *workflow.RunEvent) error {
	if item.CreatedAt.IsZero() {
		item.CreatedAt = time.Now().UTC()
	}
	return r.db.WithContext(ctx).Create(item).Error
}

func (r *RunEventRepository) ListByRun(ctx context.Context, ownerID, runID int64) ([]workflow.RunEvent, error) {
	var items []workflow.RunEvent
	err := r.db.WithContext(ctx).Where("owner_id = ? AND run_id = ?", ownerID, runID).Order("id ASC").Find(&items).Error
	return items, err
}
