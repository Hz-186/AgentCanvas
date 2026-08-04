package mysql

import (
	"context"
	"time"

	agentdomain "agentcanvas/internal/domain/agent"

	"gorm.io/gorm"
)

type RunRepository struct{ db *gorm.DB }

func NewRunRepository(db *gorm.DB) *RunRepository { return &RunRepository{db: db} }

func (r *RunRepository) Create(ctx context.Context, item *agentdomain.Run) error {
	now := time.Now().UTC()
	item.CreatedAt = now
	item.UpdatedAt = now
	if item.StartedAt.IsZero() {
		item.StartedAt = now
	}
	return r.db.WithContext(ctx).Create(item).Error
}

func (r *RunRepository) FindByID(ctx context.Context, ownerID, id int64) (*agentdomain.Run, error) {
	var item agentdomain.Run
	err := r.db.WithContext(ctx).Where("id = ? AND owner_id = ?", id, ownerID).First(&item).Error
	if err != nil {
		return nil, err
	}
	return &item, nil
}

func (r *RunRepository) ListByParent(ctx context.Context, ownerID, parentRunID int64) ([]agentdomain.Run, error) {
	var items []agentdomain.Run
	err := r.db.WithContext(ctx).
		Where("owner_id = ? AND parent_run_id = ?", ownerID, parentRunID).
		Order("id ASC").
		Find(&items).Error
	return items, err
}

func (r *RunRepository) Update(ctx context.Context, item *agentdomain.Run) error {
	item.UpdatedAt = time.Now().UTC()
	return r.db.WithContext(ctx).Save(item).Error
}
