package agent_usecase

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	workspaceusecase "agentcanvas/internal/application/workspace_usecase"
	"agentcanvas/internal/domain"
	agentdomain "agentcanvas/internal/domain/agent"
	"agentcanvas/internal/domain/conversation"
	"agentcanvas/internal/domain/goal"
	workspacedomain "agentcanvas/internal/domain/workspace"
	"agentcanvas/internal/infrastructure/llm"
	agenterrors "agentcanvas/internal/pkg/errors"
	runtimeagent "agentcanvas/internal/runtime/agent"
	agentruntime "agentcanvas/internal/runtime/agentruntime"
	runtimeevent "agentcanvas/internal/runtime/event"
	"agentcanvas/internal/runtime/toolruntime"
)

type turnWorker struct{ *Service }

func (w turnWorker) execute(ctx context.Context, turn *agentdomain.Turn) {
	s := w.Service
	run, err := s.runs.FindByID(ctx, turn.OwnerID, *turn.RunID)
	if err != nil {
		s.failTurn(ctx, turn, nil, err)
		return
	}
	if run.Status == agentdomain.RunStatusCancelled {
		finished := time.Now().UTC()
		turn.Status, turn.FinishedAt = agentdomain.TurnStatusCancelled, &finished
		if err := s.turns.Update(ctx, turn); err != nil && !errors.Is(err, agentdomain.ErrLeaseLost) {
			slog.Default().Error("cancelled agent turn update failed", "turn_id", turn.ID, "run_id", run.ID, "error", err)
		}
		return
	}
	if run.Status == agentdomain.RunStatusResuming {
		if err := w.resume(ctx, turn, run); err != nil && run.Status == agentdomain.RunStatusResuming {
			s.failTurn(ctx, turn, run, err)
		}
		return
	}
	run.ErrorMessage = ""
	execCtx, cancel := context.WithCancel(ctx)
	s.registerCancel(run.ID, cancel)

	defer func() { cancel(); s.unregisterCancel(run.ID) }()

	if turn.LeaseToken != "" {
		go s.heartbeatLease(execCtx, turn.ID, turn.LeaseToken, cancel)
	}
	ctx = execCtx
	definition, err := agentruntime.DecodeDefinition(run.DefinitionJSON)
	if err != nil {
		s.failTurn(ctx, turn, run, err)
		return
	}
	input, err := decodeInputJSON(turn.InputJSON)
	if err != nil {
		s.failTurn(ctx, turn, run, fmt.Errorf("decode turn input: %w", err))
		return
	}
	task, _ := input["query"].(string)
	manualCompaction, _ := input["manual_compaction"].(bool)
	if mode, modeErr := normalizeAgentMode(fmt.Sprint(input["mode"])); modeErr == nil {
		definition.Mode = mode
	}
	emitter := s.newRunEventEmitter(turn.OwnerID, run.ID, &turn.ConversationID)

	var runtimeWorkspace *toolruntime.WorkspaceContext
	var preparedWorkspace *workspacedomain.Workspace

	projectID := int64(0)
	if s.conversations != nil && run.ConversationID != nil {
		conv, convErr := s.conversations.FindByID(ctx, turn.OwnerID, *run.ConversationID)
		if convErr != nil {
			s.failTurn(ctx, turn, run, convErr)
			return
		}
		if conv.ProjectID != nil {
			projectID = *conv.ProjectID
		}
		if conv.ProjectID != nil && s.workspace != nil && s.workspace.Enabled() {
			project, projectErr := s.workspace.GetProject(ctx, turn.OwnerID, *conv.ProjectID)
			if projectErr != nil {
				_ = emitter.Emit(ctx, runtimeevent.Event{Type: runtimeevent.WorkspaceFailed, RunID: run.ID, Payload: workspaceEventPayload(&workspacedomain.Workspace{ProjectID: *conv.ProjectID, RunID: run.ID}, projectErr)})
				s.failTurn(ctx, turn, run, projectErr)
				return
			}
			ws, wsErr := s.workspace.PrepareRunWorkspace(ctx, turn.OwnerID, *conv.ProjectID, run.ID, conv.WorkspaceMode, project.Slug, task, nil)
			if wsErr != nil {
				failedWorkspace := ws
				if failedWorkspace == nil {
					failedWorkspace = &workspacedomain.Workspace{ProjectID: project.ID, RunID: run.ID, RepositoryRoot: project.RepositoryRoot}
				} else {
					run.WorkspaceID = &failedWorkspace.ID
				}
				_ = emitter.Emit(ctx, runtimeevent.Event{Type: runtimeevent.WorkspaceFailed, RunID: run.ID, Payload: workspaceEventPayload(failedWorkspace, wsErr)})
				s.failTurn(ctx, turn, run, wsErr)
				return
			}
			preparedWorkspace = ws
			defer func() { _ = s.workspace.ReleaseRunWorkspaceLock(context.Background(), turn.OwnerID, run.ID) }()
			run.WorkspaceID = &ws.ID
			if updateErr := s.turns.UpdateRunOwned(ctx, turn, run, false); updateErr != nil {
				_ = emitter.Emit(ctx, runtimeevent.Event{Type: runtimeevent.WorkspaceFailed, RunID: run.ID, Payload: workspaceEventPayload(ws, updateErr)})
				s.failTurn(ctx, turn, run, updateErr)
				return
			}
			runtimeWorkspace = runtimeWorkspaceContext(s.workspace.WorkspaceContext(ws))
			_ = emitter.Emit(ctx, runtimeevent.Event{Type: runtimeevent.WorkspaceCreated, RunID: run.ID, Payload: workspaceEventPayload(ws, nil)})
		}
	}
	if preparedWorkspace != nil {
		_ = emitter.Emit(ctx, runtimeevent.Event{Type: runtimeevent.WorkspaceReady, RunID: run.ID, Payload: workspaceEventPayload(preparedWorkspace, nil)})
	}
	if err := run.TransitionStatus(agentdomain.RunStatusRunning); err != nil {
		s.failTurn(ctx, turn, run, err)
		return
	}
	if err := s.turns.UpdateRunOwned(ctx, turn, run, false); err != nil {
		s.failTurn(ctx, turn, run, err)
		return
	}
	result, execErr := s.runtime.Execute(ctx,
		agentruntime.RunRequest{
			RunIdentity:         agentruntime.RunIdentity{OwnerID: turn.OwnerID, AgentID: turn.AgentID, RunID: run.ID, UserMessageID: turn.UserMessageID, ParentRunID: run.ParentRunID},
			ConversationContext: agentruntime.ConversationContext{ConversationID: &turn.ConversationID, ProjectID: projectID},
			ExecutionTask:       agentruntime.ExecutionTask{Task: task, ManualCompaction: manualCompaction},
			RuntimeResources:    agentruntime.RuntimeResources{Definition: definition},
			RuntimePolicy:       agentruntime.RuntimePolicy{RuleHash: run.RuleHash},
			WorkspaceContext:    agentruntime.WorkspaceContext{Workspace: runtimeWorkspace},
			PersistenceHooks:    agentruntime.PersistenceHooks{StepRecorder: &runStepRecorder{repo: s.steps}},
			Steering:            func() []string { return s.consumeSteering(run.ID) },
		}, emitter)
	if preparedWorkspace != nil {
		if refreshed := s.refreshWorkspaceSnapshot(context.WithoutCancel(ctx), turn.OwnerID, preparedWorkspace.ID); refreshed != nil {
			_ = emitter.Emit(context.WithoutCancel(ctx), runtimeevent.Event{Type: runtimeevent.WorkspaceStatusChanged, RunID: run.ID, Payload: workspaceEventPayload(refreshed, nil)})
		}
	}
	if execErr != nil {
		s.retryTurn(ctx, turn, run, execErr)
		return
	}
	s.completeTurn(ctx, turn, run, result)
}

func (w turnWorker) resume(ctx context.Context, turn *agentdomain.Turn, run *agentdomain.Run) error {
	s := w.Service
	if s.approvals == nil || turn == nil || run == nil || run.ID <= 0 {
		return fmt.Errorf("resume dependencies are not configured")
	}
	checkpoint, err := s.approvals.FindLatestCheckpointByRun(ctx, run.OwnerID, run.ID)
	if err != nil {
		return mapNotFound(err)
	}

	input, err := decodeInputJSON(turn.InputJSON)
	if err != nil {
		return fmt.Errorf("decode resume input: %w", err)
	}
	var decision *agentdomain.ApprovalRequest
	if approved, ok := input["resume_approved"].(bool); ok {
		decision = &agentdomain.ApprovalRequest{Status: agentdomain.ApprovalStatusRejected}
		if approved {
			decision.Status = agentdomain.ApprovalStatusApproved
		}
		if note, ok := input["resume_note"].(string); ok {
			decision.DecisionNote = note
		}
		if answers, ok := input["resume_answers"].(map[string]any); ok {
			encoded, _ := json.Marshal(answers)
			decision.DecisionNote = "answers:" + string(encoded)
		}
	}
	execCtx, cancel := context.WithCancel(ctx)
	s.registerCancel(run.ID, cancel)
	defer func() { cancel(); s.unregisterCancel(run.ID) }()
	if turn.LeaseToken != "" {
		go s.heartbeatLease(execCtx, turn.ID, turn.LeaseToken, cancel)
	}
	_, err = s.resumeRun(execCtx, run, checkpoint, decision, turn)
	return err
}

func (s *Service) ConfigureWorker(leaseDuration time.Duration) {
	if leaseDuration >= 10*time.Second {
		s.leaseDuration = leaseDuration
	}
}

func (s *Service) ConfigureImprovement(enqueuer TurnReviewEnqueuer) {
	s.improvement = enqueuer
}

func (s *Service) ConfigureSessionSearch(index conversation.MessageSearchIndex) {
	s.sessionSearch = index
}

func (s *Service) ConfigureWorkspace(service *workspaceusecase.Service) {
	s.workspace = service
}

func (s *Service) refreshWorkspaceSnapshot(ctx context.Context, ownerID, workspaceID int64) *workspacedomain.Workspace {
	if s.workspace == nil || ownerID <= 0 || workspaceID <= 0 {
		return nil
	}
	item, err := s.workspace.GetWorkspace(ctx, ownerID, workspaceID)
	if err == nil {
		var refreshed *workspacedomain.Workspace
		refreshed, err = s.workspace.RefreshGitStatus(ctx, item)
		if refreshed != nil {
			item = refreshed
		}
	}
	if err != nil {
		slog.Default().Error("refresh run workspace status failed", "owner_id", ownerID, "workspace_id", workspaceID, "error", err)
	}
	return item
}

// EmitWorkspaceEvent lets HTTP lifecycle actions use the same durable Run
// Event/SSE lane as runtime tool actions.
func (s *Service) EmitWorkspaceEvent(ctx context.Context, ownerID, runID int64, eventType string, payload map[string]any) error {
	if ownerID <= 0 || runID <= 0 {
		return agenterrors.ErrInvalidInput
	}
	if s.events == nil {
		return nil
	}
	return s.newRunEventEmitter(ownerID, runID, nil).Emit(ctx, runtimeevent.Event{Type: eventType, RunID: runID, Payload: payload})
}

func (s *Service) effectiveLeaseDuration() time.Duration {
	if s.leaseDuration < 10*time.Second {
		return 30 * time.Second
	}
	return s.leaseDuration
}

func (s *Service) RunWorker(ctx context.Context, workerID string, concurrency int) {
	if concurrency <= 0 {
		concurrency = 1
	}
	for i := 0; i < concurrency; i++ {
		go s.workerLoop(ctx, fmt.Sprintf("%s-%d", workerID, i+1))
	}
	go s.recoveryLoop(ctx)
}

func (s *Service) workerLoop(ctx context.Context, workerID string) {
	ticker := time.NewTicker(300 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		token := newLeaseToken()
		turn, err := s.turns.ClaimNext(ctx, workerID, token, time.Now().UTC().Add(s.effectiveLeaseDuration()))
		if errors.Is(err, agentdomain.ErrNoTurnAvailable) {
			continue
		}
		if err != nil {
			slog.Default().Error("claim agent turn failed", "worker_id", workerID, "error", err)
			continue
		}
		s.executeTurnOwned(ctx, turn)
	}
}

func (s *Service) heartbeatLease(ctx context.Context, turnID int64, token string, cancel context.CancelFunc) {
	leaseDuration := s.effectiveLeaseDuration()
	ticker := time.NewTicker(leaseDuration / 3)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.turns.RenewLease(ctx, turnID, token, time.Now().UTC().Add(leaseDuration)); err != nil {
				cancel()
				return
			}
		}
	}
}

func (s *Service) recoveryLoop(ctx context.Context) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	if err := s.recoverExpired(ctx); err != nil {
		slog.Default().Error("recover expired agent turns failed", "error", err)
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.recoverExpired(ctx); err != nil {
				slog.Default().Error("recover expired agent turns failed", "error", err)
			}
		}
	}
}

func (s *Service) recoverExpired(ctx context.Context) error {
	items, err := s.turns.ListExpiredRunning(ctx, time.Now().UTC(), 100)
	if err != nil {
		return err
	}
	for i := range items {
		turn := &items[i]
		if turn.RunID == nil {
			return fmt.Errorf("expired agent turn %d has no run", turn.ID)
		}
		run, err := s.runs.FindByID(ctx, turn.OwnerID, *turn.RunID)
		if err != nil {
			return err
		}
		hasToolSideEffect, sideEffectErr := s.runHasToolSideEffects(ctx, turn.OwnerID, turn.RunID)
		if sideEffectErr != nil {
			return sideEffectErr
		}
		recoveredTurn, recoveredRun := *turn, *run
		maxAttempts := turn.MaxAttempts
		if maxAttempts <= 0 {
			maxAttempts = 3
		}
		if hasToolSideEffect || turn.AttemptCount >= maxAttempts {
			now := time.Now().UTC()
			reason := "expired worker lease requires checkpoint/manual review to avoid replaying tool side effects"
			recoveredTurn.Status, recoveredTurn.ErrorMessage, recoveredTurn.FinishedAt = agentdomain.TurnStatusPaused, reason, &now
			if err := recoveredRun.TransitionStatus(agentdomain.RunStatusPaused); err != nil {
				return err
			}
			recoveredRun.ErrorMessage, recoveredRun.FinishedAt = reason, nil
		} else {
			reason := "requeued after expired worker lease before any tool call"
			retryAt := time.Now().UTC().Add(time.Second << min(max(turn.AttemptCount-1, 0), 6))
			recoveredTurn.Status, recoveredTurn.ErrorMessage, recoveredTurn.RetryAt, recoveredTurn.FinishedAt = agentdomain.TurnStatusRetryWait, reason, &retryAt, nil
			if recoveredRun.Status == agentdomain.RunStatusRunning {
				if err := recoveredRun.TransitionStatus(agentdomain.RunStatusQueued); err != nil {
					return err
				}
			} else if recoveredRun.Status != agentdomain.RunStatusResuming {
				return fmt.Errorf("cannot recover run %d from status %s", recoveredRun.ID, recoveredRun.Status)
			}
			recoveredRun.ErrorMessage, recoveredRun.FinishedAt = reason, nil
		}
		if err := s.turns.RecoverExpired(ctx, &recoveredTurn, &recoveredRun); err != nil {
			if errors.Is(err, agentdomain.ErrLeaseLost) {
				continue
			}
			return err
		}
		*turn, *run = recoveredTurn, recoveredRun
		s.publishRunSnapshot(run, turn, nil, llm.Usage{})
	}
	return nil
}

func (s *Service) runHasToolSideEffects(ctx context.Context, ownerID int64, runID *int64) (bool, error) {
	if runID == nil || s.steps == nil {
		return false, nil
	}
	steps, err := s.steps.ListByRun(ctx, ownerID, *runID)
	if err != nil {
		return false, err
	}
	for _, step := range steps {
		if step.StepType == runtimeagent.StepTypeToolCall || step.StepType == runtimeagent.StepTypeToolResult {
			return true, nil
		}
	}
	return false, nil
}

func newLeaseToken() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return fmt.Sprintf("lease-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(value[:])
}

func (s *Service) retryTurn(ctx context.Context, turn *agentdomain.Turn, run *agentdomain.Run, cause error) {
	if turn == nil || run == nil || cause == nil {
		return
	}
	maxAttempts := turn.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 3
	}
	hasToolSideEffect, err := s.runHasToolSideEffects(ctx, turn.OwnerID, turn.RunID)
	if err != nil {
		s.failTurn(ctx, turn, run, errors.Join(cause, fmt.Errorf("inspect persisted tool steps: %w", err)))
		return
	}
	if hasToolSideEffect || turn.AttemptCount >= maxAttempts {
		s.failTurn(ctx, turn, run, cause)
		return
	}

	retryTurn, retryRun := *turn, *run
	backoff := time.Second << min(max(turn.AttemptCount-1, 0), 6)
	retryAt := time.Now().UTC().Add(backoff)
	retryTurn.Status, retryTurn.ErrorMessage, retryTurn.RetryAt, retryTurn.FinishedAt = agentdomain.TurnStatusRetryWait, cause.Error(), &retryAt, nil
	if retryRun.Status == agentdomain.RunStatusRunning {
		if err := retryRun.TransitionStatus(agentdomain.RunStatusQueued); err != nil {
			s.failTurn(ctx, turn, run, err)
			return
		}
	} else if retryRun.Status != agentdomain.RunStatusResuming {
		s.failTurn(ctx, turn, run, cause)
		return
	}
	retryRun.ErrorMessage, retryRun.FinishedAt = cause.Error(), nil
	if err := s.turns.UpdateRunOwned(ctx, &retryTurn, &retryRun, true); err != nil {
		if !errors.Is(err, agentdomain.ErrLeaseLost) {
			s.failTurn(ctx, turn, run, errors.Join(cause, fmt.Errorf("persist agent turn retry: %w", err)))
		}
		return
	}
	*turn, *run = retryTurn, retryRun
	s.publishRunSnapshot(run, turn, nil, llm.Usage{})
}

func (s *Service) failTurn(ctx context.Context, turn *agentdomain.Turn, run *agentdomain.Run, cause error) {
	if turn == nil || cause == nil {
		return
	}
	if run != nil {
		if current, err := s.runs.FindByID(ctx, turn.OwnerID, run.ID); err == nil && current.Status == agentdomain.RunStatusCancelled {
			now := time.Now().UTC()
			turn.Status, turn.FinishedAt = agentdomain.TurnStatusCancelled, &now
			if err := s.turns.Update(ctx, turn); err == nil {
				s.publishRunSnapshot(current, turn, nil, llm.Usage{})
			} else if !errors.Is(err, agentdomain.ErrLeaseLost) {
				slog.Default().Error("cancelled agent turn update failed", "turn_id", turn.ID, "run_id", run.ID, "error", err)
			}
			return
		}
	}
	now := time.Now().UTC()
	failedTurn := *turn
	failedTurn.Status, failedTurn.ErrorMessage, failedTurn.FinishedAt = agentdomain.TurnStatusFailed, cause.Error(), &now
	if run == nil {
		if err := s.turns.Update(ctx, &failedTurn); err == nil {
			*turn = failedTurn
		} else if !errors.Is(err, agentdomain.ErrLeaseLost) {
			slog.Default().Error("fail agent turn update failed", "turn_id", turn.ID, "run_id", turn.RunID, "error", err)
		}
		return
	}
	failedRun := *run
	if err := failedRun.TransitionStatus(agentdomain.RunStatusFailed); err != nil {
		failedRun.ErrorMessage = err.Error()
	} else {
		failedRun.ErrorMessage, failedRun.FinishedAt = cause.Error(), &now
	}
	failedRun.LatencyMS = int(now.Sub(failedRun.StartedAt).Milliseconds())
	if err := s.turns.UpdateRunOwned(ctx, &failedTurn, &failedRun, true); err != nil {
		if !errors.Is(err, agentdomain.ErrLeaseLost) {
			slog.Default().Error("fail agent turn and run update failed", "turn_id", turn.ID, "run_id", run.ID, "error", err)
		}
		return
	}
	*turn, *run = failedTurn, failedRun
	// Flush wall-clock and any usage events already received before dropping the
	// run accounting marker; error/abort paths are billable just like success.
	s.accountGoalRun(ctx, run, &agentruntime.RunResult{Output: agentruntime.RunOutput{}})
	s.clearSteering(run.ID)
	if errors.Is(cause, llm.ErrRateLimited) {
		s.markGoalStatus(ctx, run, goal.StatusUsageLimited)
	} else {
		s.markGoalBlocked(ctx, run)
	}
	s.clearGoalUsage(run.ID)
	s.publishRunSnapshot(run, turn, nil, llm.Usage{})
}

func (s *Service) markGoalBlocked(ctx context.Context, run *agentdomain.Run) {
	s.markGoalStatus(ctx, run, goal.StatusBlocked)
}

func (s *Service) markGoalStatus(ctx context.Context, run *agentdomain.Run, status string) {
	if s.goals == nil || run == nil || run.ConversationID == nil {
		return
	}
	item, err := s.goals.Get(ctx, run.OwnerID, *run.ConversationID)
	if err != nil || item == nil || item.Status != goal.StatusActive {
		return
	}
	item.Status = status
	if err := s.goals.Update(ctx, item, item.GoalID); err != nil {
		return
	}
	s.emitGoalUpdated(ctx, run.OwnerID, *run.ConversationID, item)
}

func (s *Service) completeTurn(ctx context.Context, turn *agentdomain.Turn, run *agentdomain.Run, result *agentruntime.RunResult) {
	if result == nil {
		s.failTurn(ctx, turn, run, fmt.Errorf("agent runtime returned no result"))
		return
	}
	s.accountGoalRun(ctx, run, result)
	s.clearGoalUsage(run.ID)
	s.clearSteering(run.ID)
	if current, err := s.runs.FindByID(ctx, turn.OwnerID, run.ID); err == nil && current.Status == agentdomain.RunStatusCancelled {
		now := time.Now().UTC()
		turn.Status, turn.FinishedAt = agentdomain.TurnStatusCancelled, &now
		if err := s.turns.Update(ctx, turn); err == nil {
			s.publishRunSnapshot(current, turn, nil, llm.Usage{})
		}
		return
	}
	stopReason, _ := result.Output["stop_reason"].(string)
	if stopReason == runtimeagent.StopReasonLLMError {
		message, _ := result.Output["error"].(string)
		if strings.TrimSpace(message) == "" {
			message = "agent runtime returned an LLM error"
		}
		s.failTurn(ctx, turn, run, errors.New(message))
		return
	}
	if stopReason == runtimeagent.StopReasonWaitingHuman || stopReason == runtimeagent.StopReasonPaused {
		if stopReason == runtimeagent.StopReasonWaitingHuman {
			turn.Status = agentdomain.TurnStatusWaitingHuman
			if err := run.TransitionStatus(agentdomain.RunStatusWaitingHuman); err != nil {
				s.failTurn(ctx, turn, run, err)
				return
			}
		} else {
			turn.Status = agentdomain.TurnStatusPaused
			if err := run.TransitionStatus(agentdomain.RunStatusPaused); err != nil {
				s.failTurn(ctx, turn, run, err)
				return
			}
		}
		output, _ := json.Marshal(result.Output)
		turn.OutputJSON, run.OutputJSON = output, output
		approval, checkpoint, err := checkpointArtifacts(run, result.Output, run.Status)
		if err != nil {
			s.failTurn(ctx, turn, run, err)
			return
		}
		if checkpoint == nil {
			s.failTurn(ctx, turn, run, fmt.Errorf("runtime paused without checkpoint"))
			return
		}
		if err := s.approvals.SavePausedRun(ctx, turn, run, approval, checkpoint); err != nil {
			s.failTurn(ctx, turn, run, err)
			return
		}
		if approval != nil {
			s.publishApprovalRequired(run, approval)
		}
		s.publishRunSnapshot(run, turn, nil, usageFromOutput(result.Output))
		return
	}
	if run.RunType == agentdomain.RunTypeSubagent {
		now := time.Now().UTC()
		output, _ := json.Marshal(result.Output)
		turn.Status, turn.OutputJSON, turn.FinishedAt = agentdomain.TurnStatusSucceeded, output, &now
		if err := run.TransitionStatus(agentdomain.RunStatusSucceeded); err != nil {
			s.failTurn(ctx, turn, run, err)
			return
		}
		run.OutputJSON, run.FinishedAt = output, &now
		if value, ok := result.Output["total_tokens"].(int); ok {
			run.TotalTokens = value
		}
		run.LatencyMS = int(now.Sub(run.StartedAt).Milliseconds())
		if err := s.turns.UpdateRunOwned(ctx, turn, run, true); err != nil {
			if !errors.Is(err, agentdomain.ErrLeaseLost) {
				s.failTurn(ctx, turn, run, err)
			}
			return
		}
		s.publishRunSnapshot(run, turn, nil, usageFromOutput(result.Output))
		return
	}
	content, _ := result.Output["final_answer"].(string)
	totalTokens, _ := result.Output["total_tokens"].(int)
	// Realtime-written final answer: reference the row the sink already
	// created instead of inserting a duplicate. JSON-decoded outputs carry
	// the id as float64, direct runs as int64.
	existingMessageID, _ := result.Output["assistant_message_id"].(int64)
	if existingMessageID <= 0 {
		if value, ok := result.Output["assistant_message_id"].(float64); ok {
			existingMessageID = int64(value)
		}
	}
	assistant := &conversation.Message{
		ImmutableModel: domain.ImmutableModel{OwnerID: turn.OwnerID},
		ConversationID: turn.ConversationID,
		Role:           conversation.RoleAssistant,
		Content:        content,
		RunID:          &run.ID,
		TokenCount:     totalTokens,
		ContentType:    conversation.ContentTypeText,
	}
	manualCompaction := false
	if manualInput, manualErr := decodeInputJSON(run.InputJSON); manualErr == nil {
		manualCompaction, _ = manualInput["manual_compaction"].(bool)
	}
	if manualCompaction {
		// Zero-iteration /compact ack, not a model answer.
		assistant.ContentType = conversation.ContentTypeSystemEcho
	}
	now := time.Now().UTC()
	output, _ := json.Marshal(result.Output)
	turn.Status, turn.AssistantMessageID, turn.OutputJSON, turn.FinishedAt = agentdomain.TurnStatusSucceeded, &assistant.ID, output, &now
	if err := run.TransitionStatus(agentdomain.RunStatusSucceeded); err != nil {
		s.failTurn(ctx, turn, run, err)
		return
	}
	run.OutputJSON, run.FinishedAt = output, &now
	if value, ok := result.Output["total_tokens"].(int); ok {
		run.TotalTokens = value
	}
	run.LatencyMS = int(now.Sub(run.StartedAt).Milliseconds())
	if existingMessageID > 0 {
		assistant.ID = int64(existingMessageID)
		if err := s.turns.UpdateRunOwned(ctx, turn, run, true); err != nil {
			if errors.Is(err, agentdomain.ErrLeaseLost) {
				return
			}
			s.failTurn(ctx, turn, run, err)
			return
		}
	} else if err := s.turns.CompleteWithMessage(ctx, turn, assistant, run); err != nil {
		if errors.Is(err, agentdomain.ErrLeaseLost) {
			return
		}
		s.failTurn(ctx, turn, run, err)
		return
	}
	input, _ := decodeInputJSON(run.InputJSON)
	mode, _ := normalizeAgentMode(fmt.Sprint(input["mode"]))
	if s.improvement != nil && mode != "plan" {
		if definition, err := agentruntime.DecodeDefinition(run.DefinitionJSON); err == nil {
			_ = s.improvement.EnqueueTurnReview(ctx, turn, definition)
		}
	}
	if s.sessionSearch != nil && assistant.ContentType == conversation.ContentTypeText {
		_ = s.sessionSearch.IndexMessage(ctx, turn.OwnerID, turn.AgentID, assistant)
	}
	s.maybeContinueGoal(ctx, run)
	s.publishRunSnapshot(run, turn, assistant, usageFromOutput(result.Output))
}

func (s *Service) accountGoalRun(ctx context.Context, run *agentdomain.Run, result *agentruntime.RunResult) {
	if s.goals == nil || run == nil || result == nil || run.ConversationID == nil {
		return
	}
	input, _ := decodeInputJSON(run.InputJSON)
	mode, _ := normalizeAgentMode(fmt.Sprint(input["mode"]))
	if mode == "plan" {
		return
	}
	usage := usageFromOutput(result.Output)
	tokens := goalTokenDelta(usage)
	seconds := int64(0)
	latencyMS, _ := result.Output["latency_ms"].(int)
	if latencyMS <= 0 {
		latencyMS = run.LatencyMS
	}
	if latencyMS > 0 {
		seconds = int64(latencyMS / 1000)
	}
	if seconds < 0 {
		seconds = 0
	}
	modeForAccounting := "active_only"
	if current, getErr := s.goals.Get(ctx, run.OwnerID, *run.ConversationID); getErr == nil && current != nil {
		switch current.Status {
		case goal.StatusComplete:
			modeForAccounting = "active_or_complete"
		case goal.StatusBlocked:
			modeForAccounting = "active_or_stopped"
		}
	}
	s.goalUsageMu.Lock()
	state := s.goalUsageByRun[run.ID]
	residualTokens := int64(tokens) - state.TokensAccounted
	if residualTokens < 0 {
		residualTokens = 0
	}
	residualSeconds := seconds - state.SecondsAccounted
	if residualSeconds < 0 {
		residualSeconds = 0
	}
	expectedGoalID := state.GoalID
	s.goalUsageMu.Unlock()
	updated, err := s.accountGoal(ctx, run.OwnerID, *run.ConversationID, residualSeconds, residualTokens, modeForAccounting, expectedGoalID)
	if err != nil && !errors.Is(err, goal.ErrNotFound) {
		slog.Default().Warn("account goal usage failed", "run_id", run.ID, "error", err)
	}
	if err == nil && updated != nil && updated.Status == goal.StatusBudgetLimited {
		s.emitGoalUpdated(ctx, run.OwnerID, *run.ConversationID, updated)
	}
}

func (s *Service) persistCheckpoint(ctx context.Context, run *agentdomain.Run, output agentruntime.RunOutput, status string) error {
	if s.approvals == nil {
		return fmt.Errorf("approval repository is not configured")
	}
	approval, checkpoint, err := checkpointArtifacts(run, output, status)
	if err != nil {
		return err
	}
	if approval != nil {
		if err := s.approvals.CreateApprovalRequest(ctx, approval); err != nil {
			return err
		}
	}
	if checkpoint != nil {
		return s.approvals.CreateCheckpoint(ctx, checkpoint)
	}
	return nil
}

func checkpointArtifacts(run *agentdomain.Run, output agentruntime.RunOutput, status string) (*agentdomain.ApprovalRequest, *agentdomain.RunCheckpoint, error) {
	if run == nil {
		return nil, nil, fmt.Errorf("run is nil")
	}
	decode := func(key string, target any) bool {
		value, ok := output[key]
		if !ok {
			return false
		}
		raw, _ := json.Marshal(value)
		return json.Unmarshal(raw, target) == nil
	}
	var approval runtimeagent.Approval
	var checkpoint runtimeagent.Checkpoint
	hasApproval, hasCheckpoint := decode("approval", &approval), decode("checkpoint", &checkpoint)
	interactionID := ""
	if checkpoint.Interaction != nil {
		interactionID = checkpoint.Interaction.ID
	}
	var approvalItem *agentdomain.ApprovalRequest
	if hasApproval {
		raw, _ := json.Marshal(approval)
		approvalItem = &agentdomain.ApprovalRequest{
			BaseModel:     domain.BaseModel{OwnerID: run.OwnerID},
			RunID:         run.ID,
			ToolCallID:    approval.ToolCallID,
			InteractionID: interactionID,
			ToolName:      approval.ToolName,
			RiskLevel:     approval.RiskLevel,
			Reason:        approval.Reason,
			IsBlocking:    approval.IsBlocking,
			RequestJSON:   raw,
			Questions:     approval.Questions,
			Status:        agentdomain.ApprovalStatusPending,
		}
	}
	if hasCheckpoint {
		runtimeCheckpoint, _ := json.Marshal(checkpoint)
		return approvalItem,
			&agentdomain.RunCheckpoint{
				ImmutableModel: domain.ImmutableModel{OwnerID: run.OwnerID}, RunID: run.ID, CheckpointJSON: runtimeCheckpoint,
			}, nil
	}
	return approvalItem, nil, nil
}
