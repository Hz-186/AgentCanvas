package mysql

import (
	"context"
	"time"

	"agentcanvas/internal/domain/agent"

	"gorm.io/gorm"
)

type AgentTeamRepository struct{ db *gorm.DB }

func NewAgentTeamRepository(db *gorm.DB) *AgentTeamRepository {
	return &AgentTeamRepository{db: db}
}

func (r *AgentTeamRepository) CreateTeam(ctx context.Context, item *agent.Team) error {
	now := time.Now().UTC()
	item.CreatedAt = now
	item.UpdatedAt = now
	return r.db.WithContext(ctx).Create(item).Error
}

func (r *AgentTeamRepository) FindTeamByID(ctx context.Context, ownerID, id int64) (*agent.Team, error) {
	var item agent.Team
	err := r.db.WithContext(ctx).Where("owner_id = ? AND id = ?", ownerID, id).First(&item).Error
	if err != nil {
		return nil, err
	}
	return &item, nil
}

func (r *AgentTeamRepository) ListTeams(ctx context.Context, ownerID int64) ([]agent.Team, error) {
	var items []agent.Team
	err := r.db.WithContext(ctx).Where("owner_id = ?", ownerID).Order("updated_at DESC, id DESC").Find(&items).Error
	return items, err
}

func (r *AgentTeamRepository) UpdateTeam(ctx context.Context, item *agent.Team) error {
	item.UpdatedAt = time.Now().UTC()
	return r.db.WithContext(ctx).Save(item).Error
}

func (r *AgentTeamRepository) DeleteTeam(ctx context.Context, ownerID, id int64) error {
	return r.db.WithContext(ctx).Where("owner_id = ? AND id = ?", ownerID, id).Delete(&agent.Team{}).Error
}

func (r *AgentTeamRepository) AddMember(ctx context.Context, item *agent.TeamMember) error {
	item.CreatedAt = time.Now().UTC()
	return r.db.WithContext(ctx).Create(item).Error
}

func (r *AgentTeamRepository) RemoveMember(ctx context.Context, ownerID, teamID, agentID int64) error {
	return r.db.WithContext(ctx).Where("owner_id = ? AND team_id = ? AND agent_id = ?", ownerID, teamID, agentID).Delete(&agent.TeamMember{}).Error
}

func (r *AgentTeamRepository) ListMembers(ctx context.Context, ownerID, teamID int64) ([]agent.TeamMember, error) {
	var items []agent.TeamMember
	err := r.db.WithContext(ctx).Where("owner_id = ? AND team_id = ?", ownerID, teamID).Order("id ASC").Find(&items).Error
	return items, err
}

func (r *AgentTeamRepository) ListMemberAgentIDs(ctx context.Context, ownerID, teamID int64) ([]int64, error) {
	items, err := r.ListMembers(ctx, ownerID, teamID)
	if err != nil {
		return nil, err
	}
	ids := make([]int64, len(items))
	for i, item := range items {
		ids[i] = item.AgentID
	}
	return ids, nil
}
