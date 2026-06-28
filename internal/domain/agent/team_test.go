package agent

import "testing"

func TestTeamTableName(t *testing.T) {
	tm := Team{}
	if tm.TableName() != "agent_teams" {
		t.Fatalf("expected agent_teams, got %s", tm.TableName())
	}
}

func TestTeamMemberTableName(t *testing.T) {
	m := TeamMember{}
	if m.TableName() != "agent_team_members" {
		t.Fatalf("expected agent_team_members, got %s", m.TableName())
	}
}

func TestTeamDefaults(t *testing.T) {
	tm := Team{
		Name:              "Research Team",
		SupervisorAgentID: 1,
		HandoffStrategy:   "supervisor",
		MaxDepth:          3,
	}
	if tm.HandoffStrategy != "supervisor" {
		t.Fatalf("expected supervisor, got %s", tm.HandoffStrategy)
	}
	if tm.MaxDepth != 3 {
		t.Fatalf("expected 3, got %d", tm.MaxDepth)
	}
}
