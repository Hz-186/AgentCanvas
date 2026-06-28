package workflow

import "testing"

func TestTeamTableName(t *testing.T) {
	tm := Team{}
	if tm.TableName() != "workflow_teams" {
		t.Fatalf("expected workflow_teams, got %s", tm.TableName())
	}
}

func TestTeamMemberTableName(t *testing.T) {
	m := TeamMember{}
	if m.TableName() != "workflow_team_members" {
		t.Fatalf("expected workflow_team_members, got %s", m.TableName())
	}
}

func TestTeamDefaults(t *testing.T) {
	tm := Team{
		Name:                 "Research Team",
		SupervisorWorkflowID: 1,
		HandoffStrategy:      "supervisor",
		MaxDepth:             3,
	}
	if tm.HandoffStrategy != "supervisor" {
		t.Fatalf("expected supervisor, got %s", tm.HandoffStrategy)
	}
	if tm.MaxDepth != 3 {
		t.Fatalf("expected 3, got %d", tm.MaxDepth)
	}
}
