package agent_usecase

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	agentdomain "agentcanvas/internal/domain/agent"
	workspacedomain "agentcanvas/internal/domain/workspace"
	"agentcanvas/internal/infrastructure/llm"
	agenterrors "agentcanvas/internal/pkg/errors"
	"agentcanvas/internal/pkg/jsonutil"
	agentruntime "agentcanvas/internal/runtime/agentruntime"
	runtimeevent "agentcanvas/internal/runtime/event"
	"agentcanvas/internal/runtime/toolruntime"
)

func (s *Service) RunSubagent(ctx context.Context, req toolruntime.SubagentRequest) (*toolruntime.SubagentResult, error) {
	maxDepth := req.MaxDepth
	if maxDepth <= 0 {
		maxDepth = 2
	}
	if req.OwnerID <= 0 || req.ParentRunID <= 0 || req.AgentID <= 0 || strings.TrimSpace(req.Definition.Task) == "" || req.DelegationDepth >= maxDepth {
		return nil, fmt.Errorf("%w: subagent call is not allowed", agenterrors.ErrForbidden)
	}
	parent, err := s.runs.FindByID(ctx, req.OwnerID, req.ParentRunID)
	if err != nil {
		return nil, mapNotFound(err)
	}
	if parent.AgentID != req.AgentID || parent.DelegationDepth != req.DelegationDepth {
		return nil, fmt.Errorf("%w: agent parent run context does not match", agenterrors.ErrForbidden)
	}
	if parent.Status == agentdomain.RunStatusCancelled || parent.Status == agentdomain.RunStatusFailed {
		return nil, fmt.Errorf("%w: parent run is not active", agenterrors.ErrForbidden)
	}
	if req.Definition.MaxParallelChildren > 0 {
		children, listErr := s.runs.ListByParent(ctx, req.OwnerID, req.ParentRunID)
		if listErr != nil {
			return nil, listErr
		}
		active := 0
		for index := range children {
			switch children[index].Status {
			case agentdomain.RunStatusQueued, agentdomain.RunStatusRunning, agentdomain.RunStatusWaitingHuman, agentdomain.RunStatusPaused, agentdomain.RunStatusResuming:
				active++
			}
		}
		if active >= req.Definition.MaxParallelChildren {
			return nil, fmt.Errorf("%w: subagent parallel limit reached", agenterrors.ErrForbidden)
		}
	}
	definition, definitionJSON, err := subagentRuntimeDefinition(req.Definition)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	inputJSON, _ := json.Marshal(map[string]any{"query": strings.TrimSpace(req.Definition.Task)})
	run := &agentdomain.Run{
		OwnerID:         req.OwnerID,
		RunType:         agentdomain.RunTypeSubagent,
		AgentID:         parent.AgentID,
		ConversationID:  req.ConversationID,
		ParentRunID:     &req.ParentRunID,
		DelegationDepth: req.DelegationDepth + 1,
		DefinitionJSON:  definitionJSON,
		DefinitionHash:  jsonutil.Hash(definitionJSON),
		Status:          agentdomain.RunStatusQueued,
		InputJSON:       inputJSON,
		StartedAt:       now,
	}
	if err := s.runs.Create(ctx, run); err != nil {
		return nil, err
	}
	var runtimeWorkspace *toolruntime.WorkspaceContext
	emitter := s.newRunEventEmitter(req.OwnerID, run.ID, req.ConversationID)
	if s.workspace != nil && parent.WorkspaceID != nil {
		var parentWorkspace *workspacedomain.Workspace
		var workspaceErr error
		parentWorkspace, workspaceErr = s.workspace.GetWorkspace(ctx, req.OwnerID, *parent.WorkspaceID)
		if workspaceErr != nil {
			emitErr := emitter.Emit(ctx, runtimeevent.Event{Type: runtimeevent.WorkspaceFailed, RunID: run.ID, Payload: workspaceEventPayload(&workspacedomain.Workspace{RunID: run.ID}, workspaceErr)})
			return nil, errors.Join(workspaceErr, emitErr, s.failSubagentRun(ctx, run, workspaceErr))
		}
		project, projectErr := s.workspace.GetProject(ctx, req.OwnerID, parentWorkspace.ProjectID)
		if projectErr != nil {
			emitErr := emitter.Emit(ctx, runtimeevent.Event{Type: runtimeevent.WorkspaceFailed, RunID: run.ID, Payload: workspaceEventPayload(&workspacedomain.Workspace{ProjectID: parentWorkspace.ProjectID, RunID: run.ID, RepositoryRoot: parentWorkspace.RepositoryRoot}, projectErr)})
			return nil, errors.Join(projectErr, emitErr, s.failSubagentRun(ctx, run, projectErr))
		}
		childWorkspace, childErr := s.workspace.PrepareChildWorkspace(ctx, req.OwnerID, parentWorkspace.ProjectID, run.ID, req.Definition.WorkspaceMode, project.Slug, req.Definition.Task, parentWorkspace)
		if childErr != nil {
			failedWorkspace := childWorkspace
			if failedWorkspace == nil {
				failedWorkspace = &workspacedomain.Workspace{ProjectID: parentWorkspace.ProjectID, RunID: run.ID, RepositoryRoot: parentWorkspace.RepositoryRoot}
			} else {
				run.WorkspaceID = &failedWorkspace.ID
			}
			emitErr := emitter.Emit(ctx, runtimeevent.Event{Type: runtimeevent.WorkspaceFailed, RunID: run.ID, Payload: workspaceEventPayload(failedWorkspace, childErr)})
			return nil, errors.Join(childErr, emitErr, s.failSubagentRun(ctx, run, childErr))
		}
		defer func() { _ = s.workspace.ReleaseRunWorkspaceLock(context.Background(), req.OwnerID, run.ID) }()
		run.WorkspaceID = &childWorkspace.ID
		if updateErr := s.runs.Update(ctx, run); updateErr != nil {
			emitErr := emitter.Emit(ctx, runtimeevent.Event{Type: runtimeevent.WorkspaceFailed, RunID: run.ID, Payload: workspaceEventPayload(childWorkspace, updateErr)})
			return nil, errors.Join(updateErr, emitErr, s.failSubagentRun(ctx, run, updateErr))
		}
		runtimeWorkspace = runtimeWorkspaceContext(s.workspace.WorkspaceContext(childWorkspace))
		_ = emitter.Emit(ctx, runtimeevent.Event{Type: runtimeevent.WorkspaceCreated, RunID: run.ID, Payload: workspaceEventPayload(childWorkspace, nil)})
		_ = emitter.Emit(ctx, runtimeevent.Event{Type: runtimeevent.WorkspaceReady, RunID: run.ID, Payload: workspaceEventPayload(childWorkspace, nil)})
	}
	if err := run.TransitionStatus(agentdomain.RunStatusRunning); err != nil {
		return nil, errors.Join(err, s.failSubagentRun(ctx, run, err))
	}
	if err := s.runs.Update(ctx, run); err != nil {
		return nil, err
	}
	result, execErr := s.runtime.Execute(ctx,
		agentruntime.RunRequest{
			RunIdentity:         agentruntime.RunIdentity{OwnerID: req.OwnerID, AgentID: parent.AgentID, RunID: run.ID, ParentRunID: &req.ParentRunID},
			ConversationContext: agentruntime.ConversationContext{ConversationID: req.ConversationID},
			ExecutionTask:       agentruntime.ExecutionTask{Task: strings.TrimSpace(req.Definition.Task)},
			RuntimeResources:    agentruntime.RuntimeResources{Definition: definition},
			RuntimePolicy:       agentruntime.RuntimePolicy{DelegationDepth: run.DelegationDepth},
			WorkspaceContext:    agentruntime.WorkspaceContext{Workspace: runtimeWorkspace},
			PersistenceHooks:    agentruntime.PersistenceHooks{StepRecorder: &runStepRecorder{repo: s.steps}},
		}, emitter)
	if run.WorkspaceID != nil {
		if refreshed := s.refreshWorkspaceSnapshot(context.WithoutCancel(ctx), req.OwnerID, *run.WorkspaceID); refreshed != nil {
			if refreshed.Kind == workspacedomain.KindShared && refreshed.RunID != run.ID {
				view := *refreshed
				view.RunID = run.ID
				refreshed = &view
			}
			_ = emitter.Emit(context.WithoutCancel(ctx), runtimeevent.Event{Type: runtimeevent.WorkspaceStatusChanged, RunID: run.ID, Payload: workspaceEventPayload(refreshed, nil)})
		}
	}
	var output map[string]any
	if result != nil {
		output = map[string]any(result.Output)
	}
	if execErr != nil {
		finished := time.Now().UTC()
		if transitionErr := run.TransitionStatus(agentdomain.RunStatusFailed); transitionErr != nil {
			run.ErrorMessage = transitionErr.Error()
		} else {
			run.ErrorMessage = execErr.Error()
		}
		run.FinishedAt, run.LatencyMS = &finished, int(finished.Sub(run.StartedAt).Milliseconds())
		if updateErr := s.runs.Update(ctx, run); updateErr == nil {
			s.publishRunSnapshot(run, nil, nil, llm.Usage{})
		} else {
			execErr = errors.Join(execErr, updateErr)
		}
	} else if result == nil {
		execErr = fmt.Errorf("agent runtime returned no result")
		finished := time.Now().UTC()
		if transitionErr := run.TransitionStatus(agentdomain.RunStatusFailed); transitionErr != nil {
			run.ErrorMessage = transitionErr.Error()
		} else {
			run.ErrorMessage = execErr.Error()
		}
		run.FinishedAt, run.LatencyMS = &finished, int(finished.Sub(run.StartedAt).Milliseconds())
		if updateErr := s.runs.Update(ctx, run); updateErr == nil {
			s.publishRunSnapshot(run, nil, nil, llm.Usage{})
		} else {
			execErr = errors.Join(execErr, updateErr)
		}
	} else {
		s.completeSubagentRun(ctx, run, result)
	}
	return &toolruntime.SubagentResult{RunID: run.ID, Status: run.Status, Output: output,
		Error: run.ErrorMessage, LatencyMS: run.LatencyMS}, execErr
}

func (s *Service) failSubagentRun(ctx context.Context, run *agentdomain.Run, cause error) error {
	if run == nil || cause == nil {
		return nil
	}
	now := time.Now().UTC()
	if err := run.TransitionStatus(agentdomain.RunStatusFailed); err != nil {
		run.ErrorMessage = err.Error()
	} else {
		run.ErrorMessage = cause.Error()
	}
	run.FinishedAt = &now
	run.LatencyMS = int(now.Sub(run.StartedAt).Milliseconds())
	if err := s.runs.Update(ctx, run); err != nil {
		return fmt.Errorf("persist failed subagent run %d: %w", run.ID, err)
	}
	s.publishRunSnapshot(run, nil, nil, llm.Usage{})
	return nil
}

func subagentRuntimeDefinition(source toolruntime.SubagentDefinition) (agentruntime.Definition, json.RawMessage, error) {
	raw, err := json.Marshal(map[string]any{
		"provider_id": source.ProviderID, "model": source.Model, "mode": source.Mode,
		"workspace_mode": source.WorkspaceMode,
		"system_prompt":  source.SystemPrompt, "tool_ids": source.ToolIDs, "skill_ids": source.SkillIDs,
		"knowledge_ids": source.KnowledgeIDs, "mcp_server_ids": source.MCPServerIDs,
		"max_iterations": source.MaxIterations, "max_tool_calls": source.MaxToolCalls,
		"max_execution_time_ms": source.MaxExecutionTimeMS, "max_parallel_sub_agents": source.MaxParallelChildren,
		"allow_subagents": true, "max_subagent_depth": source.MaxDepth,
		"require_approval_for_risk": source.RequireApprovalForRisk, "max_tool_timeout_ms": source.MaxToolTimeoutMS,
		"max_tool_output_bytes": source.MaxToolOutputBytes, "allowed_hosts": source.AllowedHosts,
		"code_execution_enabled": source.CodeExecutionEnabled,
	})
	if err != nil {
		return agentruntime.Definition{}, nil, err
	}
	definition, err := agentruntime.DecodeDefinition(raw)
	return definition, raw, err
}

var _ toolruntime.SubagentDispatcher = (*Service)(nil)

type runStepRecorder struct{ repo agentdomain.RunStepRepository }

func (r *runStepRecorder) RecordAgentStep(ctx context.Context, rc *agentruntime.RunContext, step agentruntime.AgentStepRecord) error {
	return r.repo.Create(ctx, &agentdomain.RunStep{
		OwnerID:       rc.OwnerID,
		RunID:         rc.RunID,
		StepIndex:     step.StepIndex,
		StepType:      step.StepType,
		Role:          step.Role,
		Content:       step.Content,
		ToolCallID:    step.ToolCallID,
		ToolName:      step.ToolName,
		ArgumentsJSON: step.ArgumentsJSON,
		OutputJSON:    step.OutputJSON,
		Compressed:    step.Compressed,
		ErrorMessage:  step.ErrorMessage,
		TokenCount:    step.TokenCount,
		LatencyMS:     step.LatencyMS,
		ProviderID:    step.ProviderID,
		Model:         step.Model,
		CreatedAt:     time.Now().UTC(),
	})
}
