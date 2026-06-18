package mysql

import (
	"context"
	"time"

	"agentcanvas/internal/domain/agent"

	"gorm.io/gorm"
)

type AgentRepository struct{ db *gorm.DB }

func NewAgentRepository(db *gorm.DB) *AgentRepository { return &AgentRepository{db: db} }

func (r *AgentRepository) Create(ctx context.Context, item *agent.Agent) error {
	now := time.Now().UTC()
	item.CreatedAt = now
	item.UpdatedAt = now
	return r.db.WithContext(ctx).Create(item).Error
}

func (r *AgentRepository) ListByOwner(ctx context.Context, ownerID int64) ([]agent.Agent, error) {
	var items []agent.Agent
	err := r.db.WithContext(ctx).Where("owner_id = ? AND deleted_at IS NULL", ownerID).Order("id DESC").Find(&items).Error
	return items, err
}

func (r *AgentRepository) FindByID(ctx context.Context, ownerID, id int64) (*agent.Agent, error) {
	var item agent.Agent
	err := r.db.WithContext(ctx).Where("id = ? AND owner_id = ? AND deleted_at IS NULL", id, ownerID).First(&item).Error
	if err != nil {
		return nil, err
	}
	return &item, nil
}

func (r *AgentRepository) Update(ctx context.Context, item *agent.Agent) error {
	item.UpdatedAt = time.Now().UTC()
	return r.db.WithContext(ctx).Save(item).Error
}

func (r *AgentRepository) SoftDelete(ctx context.Context, ownerID, id int64) error {
	now := time.Now().UTC()
	return r.db.WithContext(ctx).Model(&agent.Agent{}).Where("id = ? AND owner_id = ? AND deleted_at IS NULL", id, ownerID).Updates(map[string]any{"deleted_at": now, "updated_at": now}).Error
}
