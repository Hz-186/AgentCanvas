package mysql

import (
	"context"
	"time"

	"agentcanvas/internal/domain/workflow"

	"gorm.io/gorm"
)

type WorkflowTeamRepository struct{ db *gorm.DB }

func NewWorkflowTeamRepository(db *gorm.DB) *WorkflowTeamRepository {
	return &WorkflowTeamRepository{db: db}
}

func (r *WorkflowTeamRepository) CreateTeam(ctx context.Context, item *workflow.Team) error {
	now := time.Now().UTC()
	item.CreatedAt = now
	item.UpdatedAt = now
	return r.db.WithContext(ctx).Create(item).Error
}

func (r *WorkflowTeamRepository) FindTeamByID(ctx context.Context, ownerID, id int64) (*workflow.Team, error) {
	var item workflow.Team
	err := r.db.WithContext(ctx).Where("owner_id = ? AND id = ?", ownerID, id).First(&item).Error
	if err != nil {
		return nil, err
	}
	return &item, nil
}

func (r *WorkflowTeamRepository) ListTeams(ctx context.Context, ownerID int64) ([]workflow.Team, error) {
	var items []workflow.Team
	err := r.db.WithContext(ctx).Where("owner_id = ?", ownerID).Order("updated_at DESC, id DESC").Find(&items).Error
	return items, err
}

func (r *WorkflowTeamRepository) UpdateTeam(ctx context.Context, item *workflow.Team) error {
	item.UpdatedAt = time.Now().UTC()
	return r.db.WithContext(ctx).Save(item).Error
}

func (r *WorkflowTeamRepository) DeleteTeam(ctx context.Context, ownerID, id int64) error {
	return r.db.WithContext(ctx).Where("owner_id = ? AND id = ?", ownerID, id).Delete(&workflow.Team{}).Error
}

func (r *WorkflowTeamRepository) AddMember(ctx context.Context, item *workflow.TeamMember) error {
	item.CreatedAt = time.Now().UTC()
	return r.db.WithContext(ctx).Create(item).Error
}

func (r *WorkflowTeamRepository) RemoveMember(ctx context.Context, ownerID, teamID, agentID int64) error {
	return r.db.WithContext(ctx).Where("owner_id = ? AND team_id = ? AND workflow_id = ?", ownerID, teamID, agentID).Delete(&workflow.TeamMember{}).Error
}

func (r *WorkflowTeamRepository) ListMembers(ctx context.Context, ownerID, teamID int64) ([]workflow.TeamMember, error) {
	var items []workflow.TeamMember
	err := r.db.WithContext(ctx).Where("owner_id = ? AND team_id = ?", ownerID, teamID).Order("id ASC").Find(&items).Error
	return items, err
}

func (r *WorkflowTeamRepository) ListMemberWorkflowIDs(ctx context.Context, ownerID, teamID int64) ([]int64, error) {
	items, err := r.ListMembers(ctx, ownerID, teamID)
	if err != nil {
		return nil, err
	}
	ids := make([]int64, len(items))
	for i, item := range items {
		ids[i] = item.WorkflowID
	}
	return ids, nil
}
