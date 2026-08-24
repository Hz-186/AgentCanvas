package agent_usecase

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	agentdomain "agentcanvas/internal/domain/agent"
	workspacedomain "agentcanvas/internal/domain/workspace"
	"agentcanvas/internal/infrastructure/llm"
	agenterrors "agentcanvas/internal/pkg/errors"
	runtimeagent "agentcanvas/internal/runtime/agent"
	agentruntime "agentcanvas/internal/runtime/agentruntime"
	runtimeevent "agentcanvas/internal/runtime/event"
	"agentcanvas/internal/runtime/toolruntime"
)

func (s *Service) ResumeRun(ctx context.Context, run *agentdomain.Run, stored *agentdomain.RunCheckpoint, decision *agentdomain.ApprovalRequest) (*agentdomain.Run, error) {
	return s.resumeRun(ctx, run, stored, decision, nil)
}

func (s *Service) resumeRun(ctx context.Context, run *agentdomain.Run, stored *agentdomain.RunCheckpoint, decision *agentdomain.ApprovalRequest, claimedTurn *agentdomain.Turn) (*agentdomain.Run, error) {
	if run == nil || stored == nil || run.AgentID <= 0 {
		return nil, agenterrors.ErrInvalidInput
	}
	turn := claimedTurn
	if len(run.DefinitionJSON) == 0 || strings.TrimSpace(run.DefinitionHash) == "" {
		return nil, agenterrors.ErrInvalidInput
	}
	if turn == nil && run.RunType != agentdomain.RunTypeSubagent {
		loadedTurn, err := s.turns.FindByRunID(ctx, run.OwnerID, run.ID)
		if err != nil {
			return nil, mapNotFound(err)
		}
		turn = loadedTurn
	}
	if turn != nil && (turn.RunID == nil || *turn.RunID != run.ID || turn.OwnerID != run.OwnerID) {
		return nil, agenterrors.ErrInvalidInput
	}
	definition, err := agentruntime.DecodeDefinition(run.DefinitionJSON)
	if err != nil {
		return nil, err
	}
	checkpoint, err := decodeCheckpoint(stored)
	if err != nil {
		return nil, err
	}
	approved := decision != nil && decision.Status == agentdomain.ApprovalStatusApproved
	note := ""
	if decision != nil {
		note = decision.DecisionNote
	}
	input, err := decodeInputJSON(run.InputJSON)
	if err != nil {
		return nil, fmt.Errorf("decode run input: %w", err)
	}
	task, _ := input["query"].(string)
	if mode, modeErr := normalizeAgentMode(fmt.Sprint(input["mode"])); modeErr == nil {
		definition.Mode = mode
	}
	projectID := int64(0)
	if s.conversations != nil && run.ConversationID != nil {
		conv, findErr := s.conversations.FindByID(ctx, run.OwnerID, *run.ConversationID)
		if findErr != nil {
			return nil, mapNotFound(findErr)
		}
		if conv.ProjectID != nil {
			projectID = *conv.ProjectID
		}
	}
	if err := run.TransitionStatus(agentdomain.RunStatusResuming); err != nil {
		return nil, err
	}
	if turn != nil {
		turn.Status = agentdomain.TurnStatusRunning
		if err := s.turns.UpdateRunOwned(ctx, turn, run, false); err != nil {
			return nil, err
		}
	} else if err := s.runs.Update(ctx, run); err != nil {
		return nil, err
	}
	failResumePreparation := func(cause error) (*agentdomain.Run, error) {
		if turn != nil {
			s.failTurn(ctx, turn, run, cause)
			return run, cause
		}
		now := time.Now().UTC()
		if transitionErr := run.TransitionStatus(agentdomain.RunStatusFailed); transitionErr != nil {
			run.ErrorMessage = transitionErr.Error()
		} else {
			run.ErrorMessage, run.FinishedAt = cause.Error(), &now
		}
		run.LatencyMS = int(now.Sub(run.StartedAt).Milliseconds())
		if updateErr := s.runs.Update(ctx, run); updateErr != nil {
			return run, errors.Join(cause, fmt.Errorf("persist failed resume: %w", updateErr))
		}
		s.publishRunSnapshot(run, nil, nil, llm.Usage{})
		return run, cause
	}
	releaseID := int64(0)
	if run.AgentReleaseID != nil {
		releaseID = *run.AgentReleaseID
	}
	var runtimeWorkspace *toolruntime.WorkspaceContext
	var resolvedWorkspace *workspacedomain.Workspace
	if s.workspace != nil && run.WorkspaceID != nil {
		var item *workspacedomain.Workspace
		var workspaceErr error
		if run.WorkspaceID != nil {
			item, workspaceErr = s.workspace.GetWorkspace(ctx, run.OwnerID, *run.WorkspaceID)
		} else {
			item, workspaceErr = s.workspace.GetRunWorkspace(ctx, run.OwnerID, run.ID)
		}
		if workspaceErr != nil {
			return failResumePreparation(workspaceErr)
		}
		resolved, resolveErr := s.workspace.ResolveExistingWorkspace(ctx, item)
		if resolveErr != nil {
			return failResumePreparation(resolveErr)
		}
		if lockErr := s.workspace.AcquireRunWorkspaceLock(ctx, resolved, run.ID); lockErr != nil {
			return failResumePreparation(lockErr)
		}
		defer func() { _ = s.workspace.ReleaseRunWorkspaceLock(context.Background(), run.OwnerID, run.ID) }()
		runtimeWorkspace = runtimeWorkspaceContext(s.workspace.WorkspaceContext(resolved))
		if runtimeWorkspace == nil {
			return failResumePreparation(fmt.Errorf("run workspace is not ready"))
		}
		runtimeWorkspace.RunID = run.ID
		if projectID == 0 {
			projectID = runtimeWorkspace.ProjectID
		}
		resolvedWorkspace = resolved
	}
	execCtx, cancel := context.WithCancel(ctx)
	s.registerCancel(run.ID, cancel)
	defer func() { cancel(); s.unregisterCancel(run.ID) }()
	emitter := s.newRunEventEmitter(run.OwnerID, run.ID, run.ConversationID)
	if runtimeWorkspace != nil {
		_ = emitter.Emit(ctx, runtimeevent.Event{Type: runtimeevent.WorkspaceReady, RunID: run.ID, Payload: workspaceEventPayload(resolvedWorkspace, nil)})
	}
	result, execErr := s.runtime.Resume(execCtx,
		agentruntime.ResumeRequest{
			RunRequest: agentruntime.RunRequest{
				RunIdentity:         agentruntime.RunIdentity{OwnerID: run.OwnerID, AgentID: run.AgentID, AgentReleaseID: releaseID, RunID: run.ID, ParentRunID: run.ParentRunID},
				ConversationContext: agentruntime.ConversationContext{ConversationID: run.ConversationID, ProjectID: projectID},
				ExecutionTask:       agentruntime.ExecutionTask{Task: task},
				RuntimeResources:    agentruntime.RuntimeResources{Definition: definition},
				RuntimePolicy:       agentruntime.RuntimePolicy{DelegationDepth: run.DelegationDepth, RuleHash: run.RuleHash},
				WorkspaceContext:    agentruntime.WorkspaceContext{Workspace: runtimeWorkspace},
				PersistenceHooks:    agentruntime.PersistenceHooks{StepRecorder: &runStepRecorder{repo: s.steps}},
			},
			Checkpoint:    checkpoint,
			Approved:      approved,
			RejectionNote: note,
		}, emitter)
	if resolvedWorkspace != nil {
		if refreshed := s.refreshWorkspaceSnapshot(context.WithoutCancel(ctx), run.OwnerID, resolvedWorkspace.ID); refreshed != nil {
			if refreshed.Kind == workspacedomain.KindShared && refreshed.RunID != run.ID {
				view := *refreshed
				view.RunID = run.ID
				refreshed = &view
			}
			_ = emitter.Emit(context.WithoutCancel(ctx), runtimeevent.Event{Type: runtimeevent.WorkspaceStatusChanged, RunID: run.ID, Payload: workspaceEventPayload(refreshed, nil)})
		}
	}
	if execErr != nil {
		if turn != nil {
			s.retryTurn(ctx, turn, run, execErr)
		} else {
			now := time.Now().UTC()
			if transitionErr := run.TransitionStatus(agentdomain.RunStatusFailed); transitionErr != nil {
				run.ErrorMessage = transitionErr.Error()
			} else {
				run.ErrorMessage, run.FinishedAt = execErr.Error(), &now
			}
			run.LatencyMS = int(now.Sub(run.StartedAt).Milliseconds())
			if updateErr := s.runs.Update(ctx, run); updateErr != nil {
				return run, errors.Join(execErr, fmt.Errorf("persist failed subagent resume: %w", updateErr))
			}
		}
		return run, execErr
	}
	if turn != nil {
		s.completeTurn(ctx, turn, run, result)
	} else {
		s.completeSubagentRun(ctx, run, result)
	}
	return s.runs.FindByID(ctx, run.OwnerID, run.ID)
}

func (s *Service) completeSubagentRun(ctx context.Context, run *agentdomain.Run, result *agentruntime.RunResult) {
	if run == nil || result == nil {
		return
	}
	output, _ := json.Marshal(result.Output)
	run.OutputJSON = output
	stopReason, _ := result.Output["stop_reason"].(string)
	switch stopReason {
	case runtimeagent.StopReasonWaitingHuman:
		if err := run.TransitionStatus(agentdomain.RunStatusWaitingHuman); err != nil {
			run.ErrorMessage = err.Error()
		}
	case runtimeagent.StopReasonPaused:
		if err := run.TransitionStatus(agentdomain.RunStatusPaused); err != nil {
			run.ErrorMessage = err.Error()
		}
	default:
		if err := run.TransitionStatus(agentdomain.RunStatusSucceeded); err != nil {
			run.ErrorMessage = err.Error()
		}
	}
	if run.Status == agentdomain.RunStatusWaitingHuman || run.Status == agentdomain.RunStatusPaused {
		run.FinishedAt = nil
		if err := s.persistCheckpoint(ctx, run, result.Output, run.Status); err != nil {
			if transitionErr := run.TransitionStatus(agentdomain.RunStatusFailed); transitionErr != nil {
				run.ErrorMessage = transitionErr.Error()
			} else {
				run.ErrorMessage = err.Error()
			}
		}
	} else {
		now := time.Now().UTC()
		run.FinishedAt = &now
		run.LatencyMS = int(now.Sub(run.StartedAt).Milliseconds())
	}
	if err := s.runs.Update(ctx, run); err != nil {
		slog.Default().Error("complete subagent run update failed", "run_id", run.ID, "error", err)
	}
	s.publishRunSnapshot(run, nil, nil, usageFromOutput(result.Output))
}

func decodeCheckpoint(stored *agentdomain.RunCheckpoint) (*runtimeagent.Checkpoint, error) {
	var checkpoint runtimeagent.Checkpoint
	if err := json.Unmarshal(stored.CheckpointJSON, &checkpoint); err != nil {
		return nil, fmt.Errorf("decode runtime checkpoint: %w", err)
	}
	if checkpoint.Metadata == nil {
		checkpoint.Metadata = map[string]any{}
	}
	checkpoint.Metadata["checkpoint_id"] = stored.ID
	return &checkpoint, nil
}

func (s *Service) registerCancel(runID int64, cancel context.CancelFunc) {
	s.cancelMu.Lock()
	defer s.cancelMu.Unlock()
	s.cancels[runID] = cancel
}
func (s *Service) unregisterCancel(runID int64) {
	s.cancelMu.Lock()
	defer s.cancelMu.Unlock()
	delete(s.cancels, runID)
}
func (s *Service) CancelRun(ctx context.Context, ownerID, runID int64) error {
	run, err := s.runs.FindByID(ctx, ownerID, runID)
	if err != nil {
		return mapNotFound(err)
	}
	if agentdomain.IsActiveRunStatus(run.Status) {
		finished := time.Now().UTC()
		cancelled, cancelErr := s.runs.CancelActive(ctx, run, finished)
		if cancelErr != nil {
			return cancelErr
		}
		if !cancelled {
			run, err = s.runs.FindByID(ctx, ownerID, runID)
			if err != nil {
				return mapNotFound(err)
			}
		}
	}
	if agentdomain.IsActiveRunStatus(run.Status) {
		return agenterrors.ErrConflict
	}
	if run.Status != agentdomain.RunStatusCancelled {
		return nil
	}
	var cancelErr error
	children, listErr := s.runs.ListByParent(ctx, ownerID, runID)
	if listErr != nil {
		cancelErr = fmt.Errorf("list child runs for %d: %w", runID, listErr)
	} else {
		for index := range children {
			if !agentdomain.IsActiveRunStatus(children[index].Status) {
				continue
			}
			if err := s.CancelRun(ctx, ownerID, children[index].ID); err != nil {
				cancelErr = errors.Join(cancelErr, fmt.Errorf("cancel child run %d: %w", children[index].ID, err))
			}
		}
	}
	var snapshotTurn *agentdomain.Turn
	finishedAt := time.Now().UTC()
	if run.FinishedAt != nil {
		finishedAt = *run.FinishedAt
	}
	turn, turnErr := s.turns.CancelByRun(ctx, ownerID, runID, finishedAt)
	if turnErr == nil {
		snapshotTurn = turn
	} else if !errors.Is(turnErr, agentdomain.ErrNoTurnAvailable) {
		cancelErr = errors.Join(cancelErr, fmt.Errorf("cancel run turn %d: %w", runID, turnErr))
	}
	s.cancelMu.Lock()
	cancel := s.cancels[runID]
	s.cancelMu.Unlock()
	if cancel != nil {
		cancel()
	}
	s.publishRunSnapshot(run, snapshotTurn, nil, llm.Usage{})
	return cancelErr
}

func (s *Service) ExecuteAcceptedTurn(ctx context.Context, ownerID, turnID int64) error {
	turn, err := s.turns.FindByID(ctx, ownerID, turnID)
	if err != nil {
		return mapNotFound(err)
	}
	s.executeTurnOwned(ctx, turn)
	return nil
}

func (s *Service) GetTurn(ctx context.Context, ownerID, turnID int64) (*agentdomain.Turn, error) {
	item, err := s.turns.FindByID(ctx, ownerID, turnID)
	return item, mapNotFound(err)
}

func (s *Service) GetLatestTurn(ctx context.Context, ownerID, agentID, conversationID int64) (*agentdomain.Turn, error) {
	item, err := s.turns.FindLatestByConversation(ctx, ownerID, agentID, conversationID)
	return item, mapNotFound(err)
}

func (s *Service) GetRun(ctx context.Context, ownerID, runID int64) (*agentdomain.Run, error) {
	item, err := s.runs.FindByID(ctx, ownerID, runID)
	return item, mapNotFound(err)
}

func (s *Service) ListRunEvents(ctx context.Context, ownerID, runID, afterID int64) ([]agentdomain.RunEvent, error) {
	if _, err := s.GetRun(ctx, ownerID, runID); err != nil {
		return nil, err
	}
	items, err := s.events.ListByRun(ctx, ownerID, runID)
	if err != nil || afterID <= 0 {
		return items, err
	}
	filtered := make([]agentdomain.RunEvent, 0, len(items))
	for _, item := range items {
		if item.ID > afterID {
			filtered = append(filtered, item)
		}
	}
	return filtered, nil
}

func (s *Service) ListChildRuns(ctx context.Context, ownerID, runID int64) ([]agentdomain.Run, error) {
	if _, err := s.GetRun(ctx, ownerID, runID); err != nil {
		return nil, err
	}
	return s.runs.ListByParent(ctx, ownerID, runID)
}

func (s *Service) ListRunSteps(ctx context.Context, ownerID, runID int64) ([]agentdomain.RunStep, error) {
	if _, err := s.GetRun(ctx, ownerID, runID); err != nil {
		return nil, err
	}
	return s.steps.ListByRun(ctx, ownerID, runID)
}

func (s *Service) ListApprovalRequests(ctx context.Context, ownerID int64, status string) ([]agentdomain.ApprovalRequest, error) {
	if ownerID <= 0 || s.approvals == nil {
		return nil, agenterrors.ErrInvalidInput
	}
	return s.approvals.ListApprovalRequests(ctx, ownerID, strings.TrimSpace(status))
}

func (s *Service) DecideApprovalRequest(ctx context.Context, ownerID, requestID int64, approved bool, note string) (*agentdomain.Run, error) {
	if ownerID <= 0 || requestID <= 0 || s.approvals == nil {
		return nil, agenterrors.ErrInvalidInput
	}
	request, err := s.approvals.FindApprovalRequestByID(ctx, ownerID, requestID)
	if err != nil {
		return nil, mapNotFound(err)
	}
	if request.Status != agentdomain.ApprovalStatusPending {
		return nil, agenterrors.ErrConflict
	}
	run, err := s.GetRun(ctx, ownerID, request.RunID)
	if err != nil {
		return nil, err
	}
	decidedAt := time.Now().UTC()
	request.DecidedAt, request.DecisionNote = &decidedAt, strings.TrimSpace(note)
	if approved {
		request.Status = agentdomain.ApprovalStatusApproved
	} else {
		request.Status = agentdomain.ApprovalStatusRejected
	}
	resumeInput, err := s.resumeTurnInput(ctx, run, approved, request.DecisionNote)
	if err != nil {
		return nil, err
	}
	if err := s.approvals.DecideApprovalAndClaimResume(ctx, request, resumeInput); err != nil {
		return nil, agenterrors.ErrConflict
	}
	run.Status = agentdomain.RunStatusResuming
	return s.GetRun(ctx, ownerID, run.ID)
}

func (s *Service) ResumeByID(ctx context.Context, ownerID, runID int64) (*agentdomain.Run, error) {
	if s.approvals == nil {
		return nil, agenterrors.ErrInvalidInput
	}
	run, err := s.GetRun(ctx, ownerID, runID)
	if err != nil {
		return nil, err
	}
	if run.Status != agentdomain.RunStatusPaused {
		return nil, agenterrors.ErrConflict
	}
	pending, pendingErr := s.approvals.FindPendingApprovalByRun(ctx, ownerID, runID)
	if pendingErr == nil && pending != nil {
		return nil, agenterrors.ErrForbidden
	}
	if pendingErr != nil && !errors.Is(mapNotFound(pendingErr), agenterrors.ErrNotFound) {
		return nil, pendingErr
	}
	if _, err := s.approvals.FindLatestCheckpointByRun(ctx, ownerID, runID); err != nil {
		return nil, mapNotFound(err)
	}
	resumeInput, err := s.resumeTurnInput(ctx, run, true, "")
	if err != nil {
		return nil, err
	}
	if err := s.approvals.ClaimResume(ctx, ownerID, runID, resumeInput); err != nil {
		return nil, agenterrors.ErrConflict
	}
	run.Status = agentdomain.RunStatusResuming
	return s.GetRun(ctx, ownerID, runID)
}

func (s *Service) resumeTurnInput(ctx context.Context, run *agentdomain.Run, approved bool, note string) ([]byte, error) {
	if run == nil || run.ID <= 0 {
		return nil, agenterrors.ErrInvalidInput
	}
	raw := run.InputJSON
	if run.RunType != agentdomain.RunTypeSubagent {
		turn, err := s.turns.FindByRunID(ctx, run.OwnerID, run.ID)
		if err != nil {
			return nil, mapNotFound(err)
		}
		if turn.Status != agentdomain.TurnStatusPaused && turn.Status != agentdomain.TurnStatusWaitingHuman {
			return nil, agenterrors.ErrConflict
		}
		raw = turn.InputJSON
	}
	input, err := decodeInputJSON(raw)
	if err != nil {
		return nil, fmt.Errorf("decode turn input: %w", err)
	}
	input["resume_approved"] = approved
	input["resume_note"] = strings.TrimSpace(note)
	encoded, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("encode resume input: %w", err)
	}
	return encoded, nil
}
