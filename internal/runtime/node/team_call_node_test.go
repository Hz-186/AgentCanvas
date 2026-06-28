package node

import (
	"context"
	"encoding/json"
	"testing"

	"agentcanvas/internal/domain/agent"
	"agentcanvas/internal/runtime/engine"
)

type fakeTeamRepository struct {
	team    agent.Team
	members []agent.TeamMember
}

func (r *fakeTeamRepository) CreateTeam(ctx context.Context, item *agent.Team) error { return nil }

func (r *fakeTeamRepository) FindTeamByID(ctx context.Context, ownerID, id int64) (*agent.Team, error) {
	return &r.team, nil
}

func (r *fakeTeamRepository) ListTeams(ctx context.Context, ownerID int64) ([]agent.Team, error) {
	return []agent.Team{r.team}, nil
}

func (r *fakeTeamRepository) UpdateTeam(ctx context.Context, item *agent.Team) error { return nil }

func (r *fakeTeamRepository) DeleteTeam(ctx context.Context, ownerID, id int64) error { return nil }

func (r *fakeTeamRepository) AddMember(ctx context.Context, item *agent.TeamMember) error { return nil }

func (r *fakeTeamRepository) RemoveMember(ctx context.Context, ownerID, teamID, agentID int64) error {
	return nil
}

func (r *fakeTeamRepository) ListMembers(ctx context.Context, ownerID, teamID int64) ([]agent.TeamMember, error) {
	return r.members, nil
}

func (r *fakeTeamRepository) ListMemberAgentIDs(ctx context.Context, ownerID, teamID int64) ([]int64, error) {
	ids := make([]int64, 0, len(r.members))
	for _, member := range r.members {
		ids = append(ids, member.AgentID)
	}
	return ids, nil
}

func TestTeamCallNodeRunsSupervisorAgentWithTeamContext(t *testing.T) {
	caller := &fakeNodeAgentCaller{}
	teams := &fakeTeamRepository{
		team: agent.Team{
			ID:                9,
			OwnerID:           1,
			Name:              "Research Team",
			SupervisorAgentID: 12,
			HandoffStrategy:   "supervisor",
			MaxDepth:          4,
		},
		members: []agent.TeamMember{
			{TeamID: 9, AgentID: 21, Role: "researcher"},
			{TeamID: 9, AgentID: 22, Role: "writer"},
		},
	}
	node := TeamCallNode{Teams: teams, Caller: caller}
	rc := &engine.RunContext{
		OwnerID:       1,
		AgentID:       2,
		RunID:         3,
		CurrentNodeID: "team_call",
		Input:         map[string]any{"query": "hello"},
		CallDepth:     1,
		CallChain:     []int64{2},
	}

	output, err := node.Run(context.Background(), rc, nil, json.RawMessage(`{
		"team_id": 9,
		"input": {"query": "{{sys.query}}"}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if caller.req.AgentID != 12 || caller.req.MaxDepth != 4 || caller.req.Input["query"] != "hello" {
		t.Fatalf("unexpected call request: %+v", caller.req)
	}
	teamPayload, ok := caller.req.Input["_team"].(map[string]any)
	if !ok {
		t.Fatalf("team context was not injected: %+v", caller.req.Input)
	}
	if teamPayload["name"] != "Research Team" || teamPayload["supervisor_agent_id"] != int64(12) {
		t.Fatalf("unexpected team payload: %+v", teamPayload)
	}
	members, ok := teamPayload["members"].([]map[string]any)
	if !ok || len(members) != 2 || members[0]["agent_id"] != int64(21) {
		t.Fatalf("unexpected team members payload: %+v", teamPayload["members"])
	}
	if output["content"] != "child output" || output["team_id"] != int64(9) || output["supervisor_agent_id"] != int64(12) {
		t.Fatalf("unexpected output: %+v", output)
	}
	if output["handoff_strategy"] != "supervisor" || output["handoff"] != false {
		t.Fatalf("unexpected handoff output: %+v", output)
	}
}

func TestTeamCallNodeMarksHandoffStrategy(t *testing.T) {
	caller := &fakeNodeAgentCaller{}
	teams := &fakeTeamRepository{
		team: agent.Team{
			ID:                9,
			OwnerID:           1,
			Name:              "Handoff Team",
			SupervisorAgentID: 12,
			HandoffStrategy:   "handoff",
			MaxDepth:          4,
		},
	}
	node := TeamCallNode{Teams: teams, Caller: caller}
	output, err := node.Run(context.Background(), &engine.RunContext{
		OwnerID:       1,
		AgentID:       2,
		RunID:         3,
		CurrentNodeID: "team_call",
		CallChain:     []int64{2},
	}, nil, json.RawMessage(`{"team_id":9}`))
	if err != nil {
		t.Fatal(err)
	}
	if output["handoff_strategy"] != "handoff" || output["handoff"] != true || output["content"] != "child output" {
		t.Fatalf("unexpected handoff output: %+v", output)
	}
}

func TestTeamCallNodeValidate(t *testing.T) {
	node := TeamCallNode{}
	if err := node.Validate(json.RawMessage(`{"team_id":0}`)); err == nil {
		t.Fatal("expected missing team_id to fail")
	}
	if err := node.Validate(json.RawMessage(`{"team_id":1,"max_depth":6}`)); err == nil {
		t.Fatal("expected oversized max_depth to fail")
	}
	if err := node.Validate(json.RawMessage(`{"team_id":1,"max_depth":5}`)); err != nil {
		t.Fatalf("expected valid config: %v", err)
	}
}
