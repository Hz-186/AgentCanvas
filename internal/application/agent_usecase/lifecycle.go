package agent_usecase

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	agentdomain "agentcanvas/internal/domain/agent"
	"agentcanvas/internal/domain/workflow"
	"agentcanvas/internal/runtime/engine"
	runtimeevent "agentcanvas/internal/runtime/event"
	"agentcanvas/internal/runtime/toolruntime"
)

type lifecycleBinding struct {
	event      string
	workflowID int64
	versionID  int64
}

func lifecycleBindings(definition agentdomain.Definition) []lifecycleBinding {
	items := make([]lifecycleBinding, 0, 2)
	if definition.PreTurnWorkflowID != nil && definition.PreTurnWorkflowVersionID != nil {
		items = append(items, lifecycleBinding{event: "pre_turn", workflowID: *definition.PreTurnWorkflowID, versionID: *definition.PreTurnWorkflowVersionID})
	}
	if definition.PostTurnWorkflowID != nil && definition.PostTurnWorkflowVersionID != nil {
		items = append(items, lifecycleBinding{event: "post_turn", workflowID: *definition.PostTurnWorkflowID, versionID: *definition.PostTurnWorkflowVersionID})
	}
	return items
}

type lifecycleOutcome struct {
	Action       string         `json:"action"`
	Reason       string         `json:"reason"`
	ContextPatch map[string]any `json:"context_patch"`
	OutputPatch  map[string]any `json:"output_patch"`
	Metadata     map[string]any `json:"metadata"`
}

func (s *Service) runLifecycle(
	ctx context.Context,
	event string,
	definition agentdomain.Definition,
	turn *agentdomain.Turn,
	run *workflow.Run,
	userInput string,
	agentOutput map[string]any,
	emit engine.EventEmitter,
) (*lifecycleOutcome, error) {
	var workflowID, versionID int64
	switch event {
	case "pre_turn":
		if definition.PreTurnWorkflowID == nil || definition.PreTurnWorkflowVersionID == nil {
			return nil, nil
		}
		workflowID, versionID = *definition.PreTurnWorkflowID, *definition.PreTurnWorkflowVersionID
	case "post_turn":
		if definition.PostTurnWorkflowID == nil || definition.PostTurnWorkflowVersionID == nil {
			return nil, nil
		}
		workflowID, versionID = *definition.PostTurnWorkflowID, *definition.PostTurnWorkflowVersionID
	default:
		return nil, fmt.Errorf("unsupported lifecycle event %q", event)
	}
	if s.lifecycle == nil {
		return nil, fmt.Errorf("lifecycle workflow runtime is not configured")
	}
	_ = emit.Emit(ctx, runtimeevent.Event{Type: runtimeevent.LifecycleStarted, RunID: run.ID, NodeID: event,
		NodeType: "lifecycle_workflow", Payload: map[string]any{"event": event, "workflow_id": workflowID, "flow_version_id": versionID}})
	payload := map[string]any{
		"event": event, "agent_id": turn.AgentID, "agent_release_id": turn.AgentReleaseID,
		"conversation_id": turn.ConversationID, "turn_id": turn.ID, "run_id": run.ID,
		"user_input": map[string]any{"query": userInput}, "agent_output": agentOutput, "context": map[string]any{},
	}
	result, err := s.lifecycle.CallWorkflow(ctx, toolruntime.WorkflowCallRequest{
		OwnerID: turn.OwnerID, ParentRunID: run.ID, CallerAgentID: turn.AgentID, AgentReleaseID: turn.AgentReleaseID,
		CallerNodeID: event, WorkflowID: workflowID, FlowVersionID: versionID, Input: payload,
		CallDepth: run.CallDepth, MaxDepth: 2, RunKind: workflow.RunKindLifecycleWorkflow, Lifecycle: true,
	})
	if err != nil {
		_ = emit.Emit(ctx, runtimeevent.Event{Type: runtimeevent.LifecycleFailed, RunID: run.ID, NodeID: event,
			NodeType: "lifecycle_workflow", Payload: map[string]any{"event": event, "error": err.Error()}})
		return nil, err
	}
	outcome := decodeLifecycleOutcome(result.Output)
	if outcome.Action == "" {
		outcome.Action = "continue"
	}
	if outcome.Action != "continue" && outcome.Action != "block" && outcome.Action != "replace" {
		return nil, fmt.Errorf("lifecycle workflow returned unsupported action %q", outcome.Action)
	}
	_ = emit.Emit(ctx, runtimeevent.Event{Type: runtimeevent.LifecycleFinished, RunID: run.ID, NodeID: event,
		NodeType: "lifecycle_workflow", Payload: map[string]any{"event": event, "workflow_run_id": result.RunID, "action": outcome.Action, "reason": outcome.Reason}})
	return &outcome, nil
}

func decodeLifecycleOutcome(output map[string]any) lifecycleOutcome {
	value := findLifecycleContract(output, 0)
	raw, _ := json.Marshal(value)
	var outcome lifecycleOutcome
	_ = json.Unmarshal(raw, &outcome)
	if outcome.ContextPatch == nil {
		outcome.ContextPatch = map[string]any{}
	}
	if outcome.OutputPatch == nil {
		outcome.OutputPatch = map[string]any{}
	}
	if outcome.Metadata == nil {
		outcome.Metadata = map[string]any{}
	}
	outcome.Action, outcome.Reason = strings.TrimSpace(outcome.Action), strings.TrimSpace(outcome.Reason)
	return outcome
}

func findLifecycleContract(value any, depth int) map[string]any {
	if depth > 6 {
		return map[string]any{}
	}
	switch current := value.(type) {
	case map[string]any:
		if _, ok := current["action"]; ok {
			return current
		}
		for _, key := range []string{"output", "result", "content", "structured_output", "data"} {
			if child, ok := current[key]; ok {
				if found := findLifecycleContract(child, depth+1); len(found) > 0 {
					return found
				}
			}
		}
	case string:
		var decoded any
		if json.Unmarshal([]byte(current), &decoded) == nil {
			return findLifecycleContract(decoded, depth+1)
		}
	}
	return map[string]any{}
}
