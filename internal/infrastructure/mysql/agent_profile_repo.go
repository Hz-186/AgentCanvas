package mysql

import (
	"context"
	"time"

	"agentcanvas/internal/domain/agent"

	"gorm.io/gorm"
)

type AgentProfileRepository struct{ db *gorm.DB }

func NewAgentProfileRepository(db *gorm.DB) *AgentProfileRepository {
	return &AgentProfileRepository{db: db}
}

func (r *AgentProfileRepository) Create(ctx context.Context, item *agent.Profile) error {
	now := time.Now().UTC()
	item.CreatedAt = now
	item.UpdatedAt = now
	return r.db.WithContext(ctx).Create(item).Error
}

func (r *AgentProfileRepository) FindByAgent(ctx context.Context, ownerID, agentID int64) (*agent.Profile, error) {
	var item agent.Profile
	err := r.db.WithContext(ctx).
		Where("owner_id = ? AND agent_id = ? AND deleted_at IS NULL", ownerID, agentID).
		First(&item).Error
	if err != nil {
		return nil, err
	}
	return &item, nil
}

func (r *AgentProfileRepository) Update(ctx context.Context, item *agent.Profile) error {
	item.UpdatedAt = time.Now().UTC()
	return r.db.WithContext(ctx).Save(item).Error
}
