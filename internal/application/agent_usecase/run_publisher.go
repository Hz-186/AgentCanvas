package agent_usecase

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"agentcanvas/internal/domain"
	agentdomain "agentcanvas/internal/domain/agent"
	"agentcanvas/internal/domain/conversation"
	workspacedomain "agentcanvas/internal/domain/workspace"
	"agentcanvas/internal/infrastructure/llm"
	runtimeagent "agentcanvas/internal/runtime/agent"
	agentruntime "agentcanvas/internal/runtime/agentruntime"
	runtimeevent "agentcanvas/internal/runtime/event"
	"agentcanvas/internal/runtime/eventhub"
)

type runStreamHub = eventhub.Hub

type runEventEmitter struct {
	repo           agentdomain.RunEventRepository
	hub            runStreamHub
	ownerID, runID int64
	conversationID *int64
	onUsage        func(context.Context, int64, llm.Usage)

	// mu is the per-run publication lane. It keeps sequence reservation, the
	// durable audit append, and live publication in the same order while also
	// preventing model deltas from overtaking durable lifecycle events.
	mu               sync.Mutex
	assistantSegment string
	reasoningSegment string
	planSegment      string
}

func (s *Service) ConfigureEventHub(hub runStreamHub) {
	s.streamHub = hub
}

func (s *Service) newRunEventEmitter(ownerID, runID int64, conversationID *int64) *runEventEmitter {
	return &runEventEmitter{repo: s.events, hub: s.streamHub, ownerID: ownerID, runID: runID, conversationID: conversationID,
		onUsage: func(ctx context.Context, runID int64, usage llm.Usage) {
			s.accountGoalUsageEvent(ctx, ownerID, runID, usage)
		}}
}

var ErrRunStreamUnavailable = errors.New("run event stream is not configured")

func (s *Service) SubscribeRunStream(ctx context.Context, ownerID, runID int64, afterSeq uint64) ([]eventhub.StreamEvent, <-chan eventhub.StreamEvent, func(), error) {
	if _, err := s.GetRun(ctx, ownerID, runID); err != nil {
		return nil, nil, nil, err
	}
	if s.streamHub == nil {
		return nil, nil, nil, ErrRunStreamUnavailable
	}
	replay, live, cancel := s.streamHub.Subscribe(runID, afterSeq)
	return replay, live, cancel, nil
}

// Emit preserves the existing audit contract and only publishes its v1
// projection after the database append succeeds. A failed append may leave a
// reserved sequence hole; reconnecting clients recover through a snapshot.
func (e *runEventEmitter) Emit(ctx context.Context, event runtimeevent.Event) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if event.RunID == 0 {
		event.RunID = e.runID
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}
	projected := e.prepare(projectRuntimeEvent(event, e.conversationID))
	payload, _ := json.Marshal(event.Payload)
	if err := e.repo.Create(ctx, &agentdomain.RunEvent{ImmutableModel: domain.ImmutableModel{OwnerID: e.ownerID, CreatedAt: event.CreatedAt}, RunID: event.RunID, EventType: event.Type,
		PayloadJSON: payload}); err != nil {
		return err
	}
	e.publish(projected)
	return nil
}

// EmitModelEvent is live-only by design. In particular, reasoning never
// enters the RunEvent repository and therefore cannot leak through history or
// terminal snapshots.
func (e *runEventEmitter) EmitModelEvent(ctx context.Context, modelEvent llm.ModelStreamEvent) error {
	if modelEvent.Kind == llm.ModelUsage && e.onUsage != nil {
		e.onUsage(ctx, e.runID, modelEvent.Usage)
	}
	if e.hub == nil {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	switch modelEvent.Kind {
	case llm.ModelTextStart:
		e.assistantSegment = e.publishSegmentStart("assistant.start", "assistant")
	case llm.ModelTextDelta:
		if e.assistantSegment == "" {
			e.assistantSegment = e.publishSegmentStart("assistant.start", "assistant")
		}
		e.publishData("assistant.delta", map[string]any{"segment_id": e.assistantSegment, "text": modelEvent.Text})
	case llm.ModelTextEnd:
		if e.assistantSegment != "" {
			e.publishData("assistant.end", map[string]any{"segment_id": e.assistantSegment})
			e.assistantSegment = ""
		}
	case llm.ModelReasoningStart:
		e.reasoningSegment = e.publishSegmentStart("reasoning.start", "reasoning")
	case llm.ModelReasoningDelta:
		if e.reasoningSegment == "" {
			e.reasoningSegment = e.publishSegmentStart("reasoning.start", "reasoning")
		}
		e.publishData("reasoning.delta", map[string]any{"segment_id": e.reasoningSegment, "text": modelEvent.Text})
	case llm.ModelReasoningEnd:
		if e.reasoningSegment != "" {
			e.publishData("reasoning.end", map[string]any{"segment_id": e.reasoningSegment})
			e.reasoningSegment = ""
		}
	case llm.ModelProposedPlanStart:
		if e.planSegment == "" {
			e.planSegment = e.publishSegmentStart(runtimeevent.PlanStart, "plan")
		}
	case llm.ModelProposedPlanDelta:
		if e.planSegment == "" {
			e.planSegment = e.publishSegmentStart(runtimeevent.PlanStart, "plan")
		}
		e.publishData(runtimeevent.PlanDelta, map[string]any{"segment_id": e.planSegment, "text": modelEvent.Text})
	case llm.ModelProposedPlanEnd:
		if e.planSegment != "" {
			e.publishData(runtimeevent.PlanEnd, map[string]any{"segment_id": e.planSegment})
		}
	case llm.ModelUsage:
		e.publishData("usage.update", modelEvent.Usage)
	case llm.ModelError:
		message := "model stream failed"
		if modelEvent.Err != nil {
			message = modelEvent.Err.Error()
		}
		e.publishData("status.update", map[string]any{"message": message, "level": "error"})
	}
	return nil
}

func (e *runEventEmitter) publishSegmentStart(kind, prefix string) string {
	prepared := e.hub.Prepare(e.runID, eventhub.StreamEvent{RunID: e.runID, ConversationID: e.conversationID, Kind: kind})
	segmentID := fmt.Sprintf("%s:%d:%d", prefix, e.runID, prepared.Seq)
	prepared.Data, _ = json.Marshal(map[string]any{"segment_id": segmentID})
	e.hub.PublishPrepared(prepared)
	return segmentID
}

func (e *runEventEmitter) publishData(kind string, data any) {
	raw, _ := json.Marshal(data)
	prepared := e.hub.Prepare(e.runID, eventhub.StreamEvent{RunID: e.runID, ConversationID: e.conversationID, Kind: kind, Data: raw})
	e.hub.PublishPrepared(prepared)
}

func (e *runEventEmitter) prepare(events []eventhub.StreamEvent) []eventhub.StreamEvent {
	if e.hub == nil || len(events) == 0 {
		return nil
	}
	prepared := make([]eventhub.StreamEvent, 0, len(events))
	for _, event := range events {
		prepared = append(prepared, e.hub.Prepare(e.runID, event))
	}
	return prepared
}

func (e *runEventEmitter) publish(events []eventhub.StreamEvent) {
	if e.hub == nil {
		return
	}
	for _, event := range events {
		e.hub.PublishPrepared(event)
	}
}

func projectRuntimeEvent(event runtimeevent.Event, conversationID *int64) []eventhub.StreamEvent {
	stream := func(kind string, data any) []eventhub.StreamEvent {
		raw, _ := json.Marshal(data)
		return []eventhub.StreamEvent{{RunID: event.RunID, ConversationID: conversationID, Kind: kind, CreatedAt: event.CreatedAt, Data: raw}}
	}
	switch event.Type {
	case runtimeevent.AgentStarted:
		return stream("status.update", map[string]any{"message": "Agent started", "level": "info"})
	case runtimeevent.AgentFailed:
		return stream("status.update", map[string]any{"message": "Agent runtime failed", "level": "error"})
	case runtimeevent.AgentFinished:
		return stream("status.update", map[string]any{"message": "Agent runtime finished; finalizing", "level": "info"})
	case runtimeevent.TodoUpdated:
		return stream("todo.updated", event.Payload)
	case runtimeevent.RequestUserInput:
		return stream("request_user_input", event.Payload)
	case runtimeevent.GoalUpdated:
		return stream(runtimeevent.GoalUpdated, event.Payload)
	case runtimeevent.AgentStep:
		stepType, _ := event.Payload["type"].(string)
		callID, _ := event.Payload["tool_call_id"].(string)
		toolName, _ := event.Payload["tool_name"].(string)
		segmentID := "tool:" + callID
		switch stepType {
		case runtimeagent.StepTypeToolCall:
			return stream("tool.start", map[string]any{"call_id": callID, "segment_id": segmentID, "name": toolName, "status": "running"})
		case runtimeagent.StepTypeToolResult:
			kind, status := "tool.complete", "succeeded"
			if isError, _ := event.Payload["is_error"].(bool); isError {
				kind, status = "tool.error", "failed"
			}
			return stream(kind, map[string]any{"call_id": callID, "segment_id": segmentID, "name": toolName, "status": status,
				"output": event.Payload["content"], "truncated": event.Payload["compressed"]})
		case runtimeagent.StepTypeError:
			message, _ := event.Payload["error"].(string)
			return stream("status.update", map[string]any{"message": message, "level": "error"})
		}
	case runtimeevent.WorkspaceCreated, runtimeevent.WorkspaceReady, runtimeevent.WorkspaceFailed,
		runtimeevent.WorkspaceStatusChanged, runtimeevent.WorkspacePreserved, runtimeevent.WorkspaceCleaned,
		runtimeevent.GitStatusChanged, runtimeevent.GitCommitCreated:
		return stream("workspace.update", event.Payload)
	}
	return nil
}

type streamRunSnapshot struct {
	ID              int64                      `json:"id"`
	OwnerID         int64                      `json:"owner_id"`
	AgentID         int64                      `json:"agent_id"`
	ConversationID  *int64                     `json:"conversation_id,omitempty"`
	WorkspaceID     *int64                     `json:"workspace_id,omitempty"`
	Workspace       *workspacedomain.Workspace `json:"workspace,omitempty"`
	ParentRunID     *int64                     `json:"parent_run_id,omitempty"`
	RunType         string                     `json:"run_type"`
	DelegationDepth int                        `json:"delegation_depth"`
	DefinitionHash  string                     `json:"definition_hash,omitempty"`
	RuleHash        string                     `json:"rule_hash,omitempty"`
	Status          string                     `json:"status"`
	InputJSON       json.RawMessage            `json:"input_json"`
	OutputJSON      json.RawMessage            `json:"output_json"`
	ErrorMessage    string                     `json:"error_message"`
	TotalTokens     int                        `json:"total_tokens"`
	LatencyMS       int                        `json:"latency_ms"`
	StartedAt       time.Time                  `json:"started_at"`
	FinishedAt      *time.Time                 `json:"finished_at,omitempty"`
	CreatedAt       time.Time                  `json:"created_at"`
	UpdatedAt       time.Time                  `json:"updated_at"`
}

type terminalSnapshotPayload struct {
	Run     streamRunSnapshot     `json:"run"`
	Turn    *agentdomain.Turn     `json:"turn,omitempty"`
	Message *conversation.Message `json:"message,omitempty"`
	Usage   llm.Usage             `json:"usage"`
}

func publicStreamRun(run *agentdomain.Run, workspace *workspacedomain.Workspace) streamRunSnapshot {
	if run != nil && workspace != nil && workspace.Kind == workspacedomain.KindShared && workspace.RunID != run.ID {
		view := *workspace
		view.RunID = run.ID
		workspace = &view
	}
	return streamRunSnapshot{ID: run.ID, OwnerID: run.OwnerID, AgentID: run.AgentID,
		ConversationID: run.ConversationID, WorkspaceID: run.WorkspaceID, Workspace: workspace, ParentRunID: run.ParentRunID, RunType: run.RunType, DelegationDepth: run.DelegationDepth,
		DefinitionHash: run.DefinitionHash, RuleHash: run.RuleHash, Status: run.Status, InputJSON: run.InputJSON, OutputJSON: run.OutputJSON,
		ErrorMessage: run.ErrorMessage, TotalTokens: run.TotalTokens, LatencyMS: run.LatencyMS, StartedAt: run.StartedAt,
		FinishedAt: run.FinishedAt, CreatedAt: run.CreatedAt, UpdatedAt: run.UpdatedAt}
}

func (s *Service) streamWorkspace(run *agentdomain.Run) *workspacedomain.Workspace {
	if s.workspace == nil || run == nil || run.WorkspaceID == nil {
		return nil
	}
	item, err := s.workspace.GetWorkspace(context.Background(), run.OwnerID, *run.WorkspaceID)
	if err != nil {
		return nil
	}
	if item.Kind == workspacedomain.KindShared && item.RunID != run.ID {
		view := *item
		view.RunID = run.ID
		return &view
	}
	return item
}

func usageFromOutput(output agentruntime.RunOutput) llm.Usage {
	if usage, ok := output["usage"].(llm.Usage); ok {
		return usage
	}
	raw, ok := output["usage"]
	if !ok {
		return llm.Usage{}
	}
	data, _ := json.Marshal(raw)
	var usage llm.Usage
	_ = json.Unmarshal(data, &usage)
	return usage
}

func (s *Service) publishApprovalRequired(run *agentdomain.Run, approval *agentdomain.ApprovalRequest) {
	if s.streamHub == nil || run == nil || approval == nil {
		return
	}
	var runtimeApproval runtimeagent.Approval
	_ = json.Unmarshal(approval.RequestJSON, &runtimeApproval)
	data, _ := json.Marshal(map[string]any{"request_id": approval.ID, "call_id": approval.ToolCallID, "tool_name": approval.ToolName,
		"reason": approval.Reason, "is_blocking": runtimeApproval.IsBlocking, "options": runtimeApproval.Options, "questions": runtimeApproval.Questions})
	event := s.streamHub.Prepare(run.ID, eventhub.StreamEvent{RunID: run.ID, ConversationID: run.ConversationID, Kind: "approval.required", Data: data})
	s.streamHub.PublishPrepared(event)
}

func (s *Service) publishRunSnapshot(run *agentdomain.Run, turn *agentdomain.Turn, message *conversation.Message, usage llm.Usage) {
	if s.streamHub == nil || run == nil {
		return
	}
	kind := ""
	switch run.Status {
	case agentdomain.RunStatusSucceeded:
		kind = eventhub.RunComplete
	case agentdomain.RunStatusFailed, agentdomain.RunStatusTimeout:
		kind = eventhub.RunFailed
	case agentdomain.RunStatusCancelled:
		kind = eventhub.RunCancelled
	case agentdomain.RunStatusWaitingHuman:
		kind = eventhub.RunWaiting
	case agentdomain.RunStatusPaused:
		kind = eventhub.RunPaused
	default:
		return
	}
	data, _ := json.Marshal(terminalSnapshotPayload{Run: publicStreamRun(run, s.streamWorkspace(run)), Turn: turn, Message: message, Usage: usage})
	event := s.streamHub.Prepare(run.ID, eventhub.StreamEvent{RunID: run.ID, ConversationID: run.ConversationID, Kind: kind, Data: data})
	if run.Status == agentdomain.RunStatusSucceeded || run.Status == agentdomain.RunStatusFailed || run.Status == agentdomain.RunStatusCancelled || run.Status == agentdomain.RunStatusTimeout {
		s.streamHub.CloseRun(run.ID, event)
		return
	}
	s.streamHub.PublishPrepared(event)
}
