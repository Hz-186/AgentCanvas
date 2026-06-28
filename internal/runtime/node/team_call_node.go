package node

import (
	"context"
	"encoding/json"
	"fmt"

	"agentcanvas/internal/domain/workflow"
	"agentcanvas/internal/runtime/engine"
	runtimeevent "agentcanvas/internal/runtime/event"
	"agentcanvas/internal/runtime/toolruntime"

	agenterrors "agentcanvas/internal/pkg/errors"
)

type TeamCallNode struct {
	Teams  workflow.TeamRepository
	Caller toolruntime.WorkflowCaller
}

type teamCallConfig struct {
	TeamID   int64          `json:"team_id"`
	Input    map[string]any `json:"input"`
	MaxDepth int            `json:"max_depth"`
}

func (TeamCallNode) Type() string { return "team_call" }

func (TeamCallNode) Validate(config json.RawMessage) error {
	var cfg teamCallConfig
	if err := json.Unmarshal(config, &cfg); err != nil {
		return fmt.Errorf("%w: invalid team_call config", agenterrors.ErrInvalidInput)
	}
	if cfg.TeamID <= 0 {
		return fmt.Errorf("%w: team_call team_id is required", agenterrors.ErrInvalidInput)
	}
	if cfg.MaxDepth < 0 || cfg.MaxDepth > 5 {
		return fmt.Errorf("%w: team_call max_depth must be <= 5", agenterrors.ErrInvalidInput)
	}
	return nil
}

func (n TeamCallNode) Run(ctx context.Context, rc *engine.RunContext, input engine.NodeInput, config json.RawMessage) (engine.NodeOutput, error) {
	if n.Teams == nil || n.Caller == nil {
		return nil, fmt.Errorf("team_call dependencies are not configured")
	}
	var cfg teamCallConfig
	if err := json.Unmarshal(config, &cfg); err != nil {
		return nil, err
	}
	team, err := n.Teams.FindTeamByID(ctx, rc.OwnerID, cfg.TeamID)
	if err != nil {
		return nil, err
	}
	members, err := n.Teams.ListMembers(ctx, rc.OwnerID, cfg.TeamID)
	if err != nil {
		return nil, err
	}
	callInput := resolveWorkflowCallInput(cfg.Input, rc, input)
	handoff := team.HandoffStrategy == "handoff"
	callInput["_team"] = map[string]any{
		"id":                     team.ID,
		"name":                   team.Name,
		"handoff_strategy":       team.HandoffStrategy,
		"supervisor_workflow_id": team.SupervisorWorkflowID,
		"members":                teamMembersForInput(members),
	}
	maxDepth := cfg.MaxDepth
	if maxDepth <= 0 {
		maxDepth = team.MaxDepth
	}
	emitRuntimeEvent(ctx, rc, runtimeevent.Event{
		Type:     runtimeevent.WorkflowCallStarted,
		RunID:    rc.RunID,
		NodeID:   rc.CurrentNodeID,
		NodeType: n.Type(),
		Payload:  map[string]any{"team_id": team.ID, "supervisor_workflow_id": team.SupervisorWorkflowID, "handoff_strategy": team.HandoffStrategy, "handoff": handoff},
	})
	result, err := n.Caller.CallWorkflow(ctx, toolruntime.WorkflowCallRequest{
		OwnerID:           rc.OwnerID,
		ParentRunID:       rc.RunID,
		CallerWorkflowID:  rc.WorkflowID,
		CallerNodeID:      rc.CurrentNodeID,
		WorkflowID:        team.SupervisorWorkflowID,
		Input:             callInput,
		CallDepth:         rc.CallDepth,
		WorkflowCallChain: append([]int64(nil), rc.WorkflowCallChain...),
		MaxDepth:          maxDepth,
	})
	if err != nil {
		emitRuntimeEvent(ctx, rc, runtimeevent.Event{
			Type:     runtimeevent.WorkflowCallFailed,
			RunID:    rc.RunID,
			NodeID:   rc.CurrentNodeID,
			NodeType: n.Type(),
			Payload:  map[string]any{"team_id": team.ID, "handoff_strategy": team.HandoffStrategy, "handoff": handoff, "error": err.Error()},
		})
		return nil, err
	}
	emitRuntimeEvent(ctx, rc, runtimeevent.Event{
		Type:     runtimeevent.WorkflowCallFinished,
		RunID:    rc.RunID,
		NodeID:   rc.CurrentNodeID,
		NodeType: n.Type(),
		Payload:  map[string]any{"team_id": team.ID, "run_id": result.RunID, "status": result.Status, "handoff_strategy": team.HandoffStrategy, "handoff": handoff},
	})
	return engine.NodeOutput{
		"team_id":                team.ID,
		"team_name":              team.Name,
		"handoff_strategy":       team.HandoffStrategy,
		"handoff":                handoff,
		"supervisor_workflow_id": team.SupervisorWorkflowID,
		"run_id":                 result.RunID,
		"workflow_id":            result.WorkflowID,
		"status":                 result.Status,
		"output":                 result.Output,
		"content":                result.Output["content"],
		"latency_ms":             result.LatencyMS,
	}, nil
}

func teamMembersForInput(members []workflow.TeamMember) []map[string]any {
	out := make([]map[string]any, 0, len(members))
	for _, member := range members {
		out = append(out, map[string]any{"workflow_id": member.WorkflowID, "role": member.Role})
	}
	return out
}
