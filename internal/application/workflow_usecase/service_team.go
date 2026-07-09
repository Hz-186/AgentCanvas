package workflow_usecase

import (
	"context"
	"fmt"
	"strings"

	"agentcanvas/internal/domain/workflow"
	agenterrors "agentcanvas/internal/pkg/errors"
)

func (s *Service) CreateTeam(ctx context.Context, ownerID int64, req CreateTeamRequest) (*workflow.Team, error) {
	if s.teams == nil {
		return nil, fmt.Errorf("%w: team repository is not configured", agenterrors.ErrInvalidInput)
	}
	name := strings.TrimSpace(req.Name)
	if ownerID <= 0 || name == "" || req.SupervisorWorkflowID <= 0 {
		return nil, agenterrors.ErrInvalidInput
	}
	if _, err := s.GetWorkflow(ctx, ownerID, req.SupervisorWorkflowID); err != nil {
		return nil, err
	}
	strategy := strings.TrimSpace(req.HandoffStrategy)
	if strategy == "" {
		strategy = "supervisor"
	}
	if strategy != "supervisor" && strategy != "handoff" {
		return nil, fmt.Errorf("%w: handoff_strategy must be supervisor or handoff", agenterrors.ErrInvalidInput)
	}
	maxDepth := req.MaxDepth
	if maxDepth <= 0 {
		maxDepth = 3
	}
	if maxDepth > 5 {
		return nil, fmt.Errorf("%w: max_depth must be <= 5", agenterrors.ErrInvalidInput)
	}
	item := &workflow.Team{OwnerID: ownerID, Name: name, SupervisorWorkflowID: req.SupervisorWorkflowID, HandoffStrategy: strategy, MaxDepth: maxDepth}
	if err := s.teams.CreateTeam(ctx, item); err != nil {
		return nil, err
	}
	return item, nil
}

func (s *Service) ListTeams(ctx context.Context, ownerID int64) ([]workflow.Team, error) {
	if s.teams == nil {
		return nil, fmt.Errorf("%w: team repository is not configured", agenterrors.ErrInvalidInput)
	}
	return s.teams.ListTeams(ctx, ownerID)
}

func (s *Service) AddTeamMember(ctx context.Context, ownerID, teamID int64, req AddTeamMemberRequest) (*workflow.TeamMember, error) {
	if s.teams == nil {
		return nil, fmt.Errorf("%w: team repository is not configured", agenterrors.ErrInvalidInput)
	}
	if _, err := s.teams.FindTeamByID(ctx, ownerID, teamID); err != nil {
		return nil, mapNotFound(err)
	}
	if _, err := s.GetWorkflow(ctx, ownerID, req.WorkflowID); err != nil {
		return nil, err
	}
	item := &workflow.TeamMember{OwnerID: ownerID, TeamID: teamID, WorkflowID: req.WorkflowID, Role: strings.TrimSpace(req.Role)}
	if err := s.teams.AddMember(ctx, item); err != nil {
		return nil, err
	}
	return item, nil
}

func (s *Service) ListTeamMembers(ctx context.Context, ownerID, teamID int64) ([]workflow.TeamMember, error) {
	if s.teams == nil {
		return nil, fmt.Errorf("%w: team repository is not configured", agenterrors.ErrInvalidInput)
	}
	if _, err := s.teams.FindTeamByID(ctx, ownerID, teamID); err != nil {
		return nil, mapNotFound(err)
	}
	return s.teams.ListMembers(ctx, ownerID, teamID)
}

func (s *Service) RemoveTeamMember(ctx context.Context, ownerID, teamID, workflowID int64) error {
	if s.teams == nil {
		return fmt.Errorf("%w: team repository is not configured", agenterrors.ErrInvalidInput)
	}
	return s.teams.RemoveMember(ctx, ownerID, teamID, workflowID)
}

func (s *Service) DeleteTeam(ctx context.Context, ownerID, teamID int64) error {
	if s.teams == nil {
		return fmt.Errorf("%w: team repository is not configured", agenterrors.ErrInvalidInput)
	}
	return s.teams.DeleteTeam(ctx, ownerID, teamID)
}
