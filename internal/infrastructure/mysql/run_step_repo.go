package mysql

import (
	"context"
	"time"

	agentdomain "agentcanvas/internal/domain/agent"

	"gorm.io/gorm"
)

type RunStepRepository struct{ db *gorm.DB }

func NewRunStepRepository(db *gorm.DB) *RunStepRepository { return &RunStepRepository{db: db} }

func (r *RunStepRepository) Create(ctx context.Context, item *agentdomain.RunStep) error {
	if item.CreatedAt.IsZero() {
		item.CreatedAt = time.Now().UTC()
	}
	return r.db.WithContext(ctx).Create(item).Error
}

func (r *RunStepRepository) ListByRun(ctx context.Context, ownerID, runID int64) ([]agentdomain.RunStep, error) {
	var items []agentdomain.RunStep
	err := r.db.WithContext(ctx).
		Where("owner_id = ? AND run_id = ?", ownerID, runID).
		Order("step_index ASC, id ASC").
		Find(&items).Error
	return items, err
}
