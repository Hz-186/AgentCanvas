package agentruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"agentcanvas/internal/domain/conversation"
	"agentcanvas/internal/domain/memory"
	"agentcanvas/internal/domain/retrieval"
	"agentcanvas/internal/observability"
	runtimeagent "agentcanvas/internal/runtime/agent"
	"agentcanvas/internal/runtime/conversationcontext"
	runtimeevent "agentcanvas/internal/runtime/event"
	"agentcanvas/internal/runtime/harness/rules"
	"agentcanvas/internal/runtime/toolruntime"

	"agentcanvas/internal/infrastructure/llm"
	agenterrors "agentcanvas/internal/pkg/errors"
)

func (n runtimeCore) runAgent(
	ctx context.Context,
	rc *RunContext,
	input RunInput,
	cfg agentRuntimeConfig,
	resume *AgentResumeOptions,
) (RunOutput, error) {
	if n.LLM == nil || n.Providers == nil {
		return nil, fmt.Errorf("agent runtime dependencies are not configured")
	}
	if len(cfg.ToolPackIDs) > 0 && n.ToolPacks != nil {
		cfg.ToolIDs = mergeInt64IDs(cfg.ToolIDs, n.toolIDsFromPacks(ctx, rc.OwnerID, cfg.ToolPackIDs))
	}
	cfg = applyRuntimeMemoryPolicy(cfg, cfg.MemoryPolicyJSON)
	cfg = applyRuntimeContextPolicy(cfg, cfg.ContextPolicyJSON)
	cfg = applyRuntimeToolPolicy(cfg, cfg.ToolPolicyJSON)
	mode, modeErr := normalizeRuntimeMode(cfg.Mode)
	if modeErr != nil {
		return nil, modeErr
	}
	cfg.Mode = mode
	cfg.RequestUserInputEnabled = rc != nil && rc.DelegationDepth == 0 && (mode == conversation.ModePlan || n.DefaultModeRequestUserInput)
	cfg.GoalToolsEnabled = rc != nil && rc.ConversationID != nil && *rc.ConversationID > 0
	if err := validateAgentRuntimeConfig(cfg, true); err != nil {
		return nil, err
	}
	// Planning and execution share this resolved provider configuration.
	loaded, err := n.Providers.LoadChatProviderConfig(ctx, rc.OwnerID, cfg.ProviderID, cfg.Model)
	if err != nil {
		return nil, err
	}
	semanticProvider := loaded
	if cfg.RetrievalPolicy.Enabled != nil && !*cfg.RetrievalPolicy.Enabled {
		semanticProvider = nil
	} else {
		if cfg.RetrievalPolicy.EmbeddingProviderID > 0 && cfg.RetrievalPolicy.EmbeddingProviderID != loaded.ProviderID {
			if candidate, loadErr := n.Providers.LoadChatProviderConfig(ctx, rc.OwnerID, cfg.RetrievalPolicy.EmbeddingProviderID, ""); loadErr == nil {
				semanticProvider = candidate
			}
		}
		if semanticProvider != nil && strings.TrimSpace(cfg.RetrievalPolicy.EmbeddingModel) != "" {
			copyProvider := *semanticProvider
			copyProvider.EmbeddingModel = strings.TrimSpace(cfg.RetrievalPolicy.EmbeddingModel)
			semanticProvider = &copyProvider
		}
	}
	tools, err := n.loadTools(ctx, rc.OwnerID, cfg, semanticProvider, rc.Workspace)
	if err != nil {
		return nil, err
	}
	// Task is the run-level objective, not an individual plan step.
	task := resolveAgentTask(cfg.TaskTemplate, input)
	if task == "" {
		return nil, fmt.Errorf("%w: agent runtime task is required", agenterrors.ErrInvalidInput)
	}
	recallTask := task
	if resume == nil {
		if planner, ok := n.Retriever.(queryUnderstandingPlanner); ok {
			turns := queryTurnsFromConversation(ctx, n.MessageHistory, rc)
			rewriteProviderID := loaded.ProviderID
			if strings.TrimSpace(cfg.RetrievalPolicy.QueryRewriteMode) == "disabled" {
				rewriteProviderID = 0
			}
			queryPlan, planErr := planner.PlanQuery(ctx, retrieval.RetrievalRequest{
				OwnerID:           rc.OwnerID,
				Query:             task,
				Conversation:      turns,
				RewriteProviderID: rewriteProviderID,
				RewriteModel:      loaded.Model,
			})
			if planErr == nil {
				if queryPlan.NeedsClarification {
					question := strings.TrimSpace(queryPlan.ClarificationQuestion)
					if question == "" {
						question = "请明确你指的是哪个产品、服务或对象。"
					}
					emitRuntimeEvent(ctx, rc, runtimeevent.Event{Type: runtimeevent.ClarificationRequired, RunID: rc.RunID,
						Payload: map[string]any{"question": question, "unresolved_references": queryPlan.UnresolvedReferences}})
					return RunOutput{"content": question, "final_answer": question, "stop_reason": runtimeagent.StopReasonClarification, "clarification": map[string]any{"required": true, "question": question, "unresolved_references": queryPlan.UnresolvedReferences}}, nil
				}
				if strings.TrimSpace(queryPlan.PreciseQuery) != "" {
					recallTask = queryPlan.PreciseQuery
				}
			}
		}
	}
	systemPrompt := cfg.SystemPrompt
	compactionProvider := loaded
	if cfg.CompactionProviderID > 0 || strings.TrimSpace(cfg.CompactionModel) != "" {
		providerID := cfg.CompactionProviderID
		if providerID <= 0 {
			providerID = loaded.ProviderID
		}
		candidate, loadErr := n.Providers.LoadChatProviderConfig(ctx, rc.OwnerID, providerID, cfg.CompactionModel)
		if loadErr != nil {
			return nil, fmt.Errorf("load compaction model: %w", loadErr)
		}
		compactionProvider = candidate
	}
	// Conversation blocks provide context; they do not track plan progress.
	conversationBlocks := []runtimeagent.ContextBlock(nil)
	skillBlocks := []runtimeagent.ContextBlock(nil)
	var workspaceBlock, memoryBlock *runtimeagent.ContextBlock
	historyPrepared := false
	var historyTrace conversationcontext.Trace
	if resume == nil {
		tools = n.semanticShortlistTools(ctx, semanticProvider, recallTask, tools)
		skillBlocks = n.buildSkillContextBlocks(ctx, rc.OwnerID, cfg, semanticProvider, recallTask)
		workspaceBlock = n.workspaceCodingContext(ctx, rc)
		memoryBlock = n.buildAutomaticMemoryBlock(ctx, rc, cfg)
		budgetBlocks := append([]runtimeagent.ContextBlock(nil), skillBlocks...)
		budgetBlocks = append(budgetBlocks, cfg.AdditionalContextBlocks...)
		if workspaceBlock != nil {
			budgetBlocks = append(budgetBlocks, *workspaceBlock)
		}
		if memoryBlock != nil {
			budgetBlocks = append(budgetBlocks, *memoryBlock)
		}
		conversationBlocks, historyTrace, historyPrepared, err = buildPreparedConversationContext(ctx, n, rc, recallTask, cfg, loaded.Config, loaded.ProviderID, loaded.Model, compactionProvider.Config, compactionProvider.ProviderID, compactionProvider.Model, systemPrompt, runtimeToolDefinitions(tools), budgetBlocks)
		if err != nil {
			if historyPrepared || historyTrace.BeforeTokens > 0 {
				observability.ContextSystemMetrics.RecordConversationSnapshot(historyTrace.Reused, historyTrace.BeforeTokens, historyTrace.AfterTokens, historyTrace.ModelCalls, historyTrace.LatencyMS)
			}
			observability.ContextSystemMetrics.RecordCompaction("failed")
			if errors.Is(err, conversationcontext.ErrOverflow) {
				observability.ContextSystemMetrics.RecordContextOverflow()
			}
			emitRuntimeEvent(ctx, rc, runtimeevent.Event{Type: runtimeevent.AgentStep, RunID: rc.RunID, Payload: map[string]any{"type": runtimeagent.StepTypeError, "error": err.Error()}})
			return RunOutput{"stop_reason": runtimeagent.StopReasonLLMError, "error": err.Error()}, nil
		}
		if historyPrepared {
			observability.ContextSystemMetrics.RecordConversationSnapshot(historyTrace.Reused, historyTrace.BeforeTokens, historyTrace.AfterTokens, historyTrace.ModelCalls, historyTrace.LatencyMS)
			if historyTrace.Created {
				observability.ContextSystemMetrics.RecordCompaction("completed")
			} else if historyTrace.Failure != "" {
				observability.ContextSystemMetrics.RecordCompaction("fallback")
			}
		}
	}
	if cfg.ManualCompaction {
		content := "Context compacted."
		return RunOutput{"content": content, "final_answer": content, "stop_reason": runtimeagent.StopReasonFinalAnswer, "iterations": 0, "tool_calls": 0, "usage": historyTrace.Usage, "total_tokens": historyTrace.Usage.TotalTokens, "context_trace": historyTrace}, nil
	}
	var ruleErr error
	cfg.Rules, ruleErr = rules.RuntimeRules(cfg.Rules, len(cfg.Rules) == 0)
	if ruleErr != nil {
		return nil, fmt.Errorf("load runtime rules: %w", ruleErr)
	}
	cfg.RuleHash, ruleErr = rules.HashLoadedRules(cfg.Rules)
	if ruleErr != nil {
		return nil, fmt.Errorf("hash runtime rules: %w", ruleErr)
	}
	// // ***
	ruleBlocks, ruleTrace, ruleTags, ruleRisk := buildRuleContextBlocks(systemPrompt, task, mode, cfg, tools, conversationBlocks)
	// // ***
	contextBlocks := append(ruleBlocks, skillBlocks...)
	// // ***
	contextBlocks = append(contextBlocks, cfg.AdditionalContextBlocks...)
	// // ***
	contextBlocks = append(contextBlocks, conversationBlocks...)
	if workspaceBlock != nil {
		contextBlocks = append(contextBlocks, *workspaceBlock)
	}
	if memoryBlock != nil {
		contextBlocks = append(contextBlocks, *memoryBlock)
	}
	reflectionPolicy, policyErr := effectiveReflectionPolicy(cfg)
	if policyErr != nil {
		return nil, fmt.Errorf("%w: reflection policy is invalid: %v", agenterrors.ErrInvalidInput, policyErr)
	}
	if resume != nil && resume.Checkpoint != nil && resume.Checkpoint.ReflectionPolicy.RuntimeMode != "" {
		reflectionPolicy = resume.Checkpoint.ReflectionPolicy.Normalize()
	}
	if mode == "plan" {
		reflectionPolicy.Enabled = false
	}
	emitRuntimeEvent(ctx, rc, runtimeevent.Event{
		Type:  runtimeevent.AgentStarted,
		RunID: rc.RunID,
		Payload: map[string]any{
			"provider_id": loaded.ProviderID,
			"model":       loaded.Model,
			"mode":        agentMode(cfg.Mode),
			"tool_count":  len(tools),
			"skill_count": len(cfg.SkillIDs),
		},
	})
	runner := runtimeagent.Runner{
		LLM: n.LLM,
		Snapshots: func() conversation.SnapshotRepository {
			if n.Coordinator != nil {
				return n.Coordinator.Snapshots
			}
			return nil
		}(),
		ProviderID: loaded.ProviderID,
		ModelName:  loaded.Model,
		OnModelEvent: func(ctx context.Context, event llm.ModelStreamEvent) error {
			if emitter, ok := rc.Events.(ModelStreamEmitter); ok {
				return emitter.EmitModelEvent(ctx, event)
			}
			return nil
		},
		OnStep: func(ctx context.Context, step runtimeagent.RunStep) error {
			if rc.AgentSteps != nil {
				_ = rc.AgentSteps.RecordAgentStep(ctx, rc, agentStepRecord(step))
			}
			emitRuntimeEvent(ctx, rc, runtimeevent.Event{
				Type:    runtimeevent.AgentStep,
				RunID:   rc.RunID,
				Payload: agentStepPayload(step, loaded.ProviderID, loaded.Model),
			})
			return nil
		},
	}
	runRequest := runtimeagent.RunRequest{
		OwnerID:                         rc.OwnerID,
		AgentID:                         rc.AgentID,
		RunID:                           rc.RunID,
		InitialUserMessageID:            rc.UserMessageID,
		DelegationDepth:                 rc.DelegationDepth,
		ConversationID:                  rc.ConversationID,
		ProjectID:                       projectIDFromRunContext(rc),
		MessageSink:                     n.messageSinkForRun(rc),
		Provider:                        loaded.Config,
		Model:                           loaded.Model,
		CompactionProvider:              compactionProvider.Config,
		CompactionModel:                 compactionProvider.Model,
		CompactionProviderID:            compactionProvider.ProviderID,
		Mode:                            mode,
		SystemPrompt:                    systemPrompt,
		Task:                            task,
		EnforceContextPrecedence:        true,
		ReflectionEnabled:               cfg.ReflectionEnabled,
		ReflectionPolicy:                reflectionPolicy,
		Temperature:                     cfg.Temperature,
		MaxIterations:                   cfg.MaxIterations,
		MaxToolCalls:                    cfg.MaxToolCalls,
		MaxExecutionTimeMS:              cfg.MaxExecutionTimeMS,
		MaxParallelTools:                cfg.MaxParallelSubAgents,
		MaxInputChars:                   cfg.MaxInputChars,
		MaxInputTokens:                  cfg.MaxInputTokens,
		ContextWindowTokens:             cfg.ContextWindowTokens,
		ReservedOutputTokens:            cfg.ReservedOutputTokens,
		ContextSafetyMarginTokens:       cfg.ContextSafetyMarginTokens,
		ModelAutoCompactTokenLimit:      cfg.ModelAutoCompactTokenLimit,
		ModelAutoCompactTokenLimitScope: cfg.ModelAutoCompactTokenLimitScope,
		CompactPrompt:                   cfg.CompactPrompt,
		ManualCompaction:                cfg.ManualCompaction,
		TokenBudgetCompaction:           cfg.CompactionMode == "token_budget",
		RetainClientDeveloperMessages:   cfg.RetainClientDeveloperMessages,
		MaxRuleTokens:                   cfg.MaxRuleTokens,
		RuleTags:                        ruleTags,
		RuleRiskLevel:                   ruleRisk,
		RuleHash:                        cfg.RuleHash,
		Rules:                           append([]rules.Rule(nil), cfg.Rules...),
		RuleTrace:                       ruleTrace,
		ContextBlocks:                   contextBlocks,
		ToolPolicy: runtimeagent.ToolPolicy{
			RequireApprovalForRisk: cfg.RequireApprovalForRisk,
			MaxToolTimeoutMS:       cfg.MaxToolTimeoutMS,
			MaxToolOutputBytes:     cfg.MaxToolOutputBytes,
			AllowedHosts:           append([]string(nil), cfg.AllowedHosts...),
			DenyAllHosts:           cfg.DenyAllHosts,
			RuleBindings:           rules.PolicyBindingsForRules(cfg.Rules),
		},
		Tools:                       tools,
		Workspace:                   rc.Workspace,
		GoalRepository:              n.Goals,
		GoalTokenBudgetCeiling:      n.GoalTokenBudgetCeiling,
		DefaultModeRequestUserInput: n.DefaultModeRequestUserInput,
		SteeringProvider: func() []string {
			if rc.Steering == nil {
				return nil
			}
			return rc.Steering()
		},
		EmitEvent: func(eventCtx context.Context, eventType string, payload map[string]any) error {
			emitRuntimeEvent(eventCtx, rc, runtimeevent.Event{Type: eventType, RunID: rc.RunID, Payload: payload})
			return nil
		},
	}
	if resume != nil && resume.Checkpoint != nil {
		// Restore the checkpoint snapshot instead of generating a new plan.
		if mismatch := checkpointHashMismatch(resume.Checkpoint, tools, runRequest.ToolPolicy); mismatch != "" {
			return pausedForCheckpointMismatch(resume.Checkpoint, mismatch), nil
		}
		resumeRequest, buildErr := runtimeagent.BuildResumeRequest(runtimeagent.ResumeRequest{
			RunRequest:    runRequest,
			Checkpoint:    resume.Checkpoint,
			Approved:      resume.Approved,
			RejectionNote: resume.RejectionNote,
		})
		if buildErr != nil {
			return nil, buildErr
		}
		runRequest = *resumeRequest
	}
	result, err := runner.Run(ctx, runRequest)
	if err != nil {
		n.finalizeReflection(ctx, rc, task, result, reflectionPolicy)
		emitAgentResultEvent(ctx, rc, result, err)
		return nil, err
	}
	if result == nil {
		err = fmt.Errorf("agent runtime returned no result")
		emitAgentResultEvent(ctx, rc, nil, err)
		return nil, err
	}
	// Strip the citation block from the user-visible answer and record
	// owner-validated usage for cited memories before any downstream
	// consumer (output, schema validation, ad-hoc note, result event) sees
	// the final text.
	n.finalizeCitations(ctx, rc, result)
	output := RunOutput{
		"content":              result.FinalAnswer,
		"final_answer":         result.FinalAnswer,
		"stop_reason":          result.StopReason,
		"iterations":           result.Iterations,
		"tool_calls":           result.ToolCalls,
		"usage":                result.Usage,
		"total_tokens":         result.Usage.TotalTokens,
		"latency_ms":           result.LatencyMS,
		"context_trace":        result.Context,
		"assistant_message_id": result.AssistantMessageID,
	}
	if len(cfg.OutputSchemaJSON) > 0 &&
		string(bytes.TrimSpace(cfg.OutputSchemaJSON)) != "{}" &&
		string(bytes.TrimSpace(cfg.OutputSchemaJSON)) != "null" {
		var parsed any
		schemaErr := parseAndValidateStructuredOutput(cfg.OutputSchemaJSON, result.FinalAnswer, &parsed)
		if schemaErr != nil {
			schemaErrText := schemaErr.Error()
			repaired, repairErr := n.repairStructuredOutput(
				ctx, loaded.Config, loaded.Model, cfg.Temperature, task, result.FinalAnswer, cfg.OutputSchemaJSON, schemaErr,
			)
			if repairErr == nil {
				var repairedParsed any
				if repairedErr := parseAndValidateStructuredOutput(cfg.OutputSchemaJSON, repaired, &repairedParsed); repairedErr == nil {
					result.FinalAnswer = repaired
					output["content"] = repaired
					output["final_answer"] = repaired
					parsed = repairedParsed
					schemaErr = nil
					reflectionStep := runtimeagent.RunStep{
						Type:       runtimeagent.StepTypeReflection,
						Content:    "Repaired final_answer to satisfy output_schema_json after validation failed: " + schemaErrText,
						OutputJSON: json.RawMessage(repaired),
						ProviderID: loaded.ProviderID,
						Model:      loaded.Model,
						CreatedAt:  time.Now().UTC(),
					}
					reflectionStep.Index = len(result.Steps) + 1
					result.Steps = append(result.Steps, reflectionStep)
					n.recordAgentStep(ctx, rc, reflectionStep, loaded.ProviderID, loaded.Model)
				} else {
					schemaErr = repairedErr
				}
			}
		}
		if schemaErr != nil {
			result.StopReason = runtimeagent.StopReasonReflectionFailed
			n.finalizeReflection(ctx, rc, task, result, reflectionPolicy)
			err = fmt.Errorf("%w: agent runtime final_answer does not match output_schema_json: %v", agenterrors.ErrInvalidInput, schemaErr)
			emitAgentResultEvent(ctx, rc, result, err)
			return nil, err
		}
		output["structured_output"] = parsed
	}
	// Durable memory is never written by the ReAct loop. An explicit user
	// request is recorded as one append-only note for the background
	// consolidation pipeline to consume.
	if result.Approval != nil {
		if strings.TrimSpace(result.Approval.Kind) == "" {
			result.Approval.Kind = "tool_approval"
		}
		if strings.TrimSpace(result.Approval.Title) == "" {
			result.Approval.Title = "Approve tool " + result.Approval.ToolName
		}
		output["approval"] = result.Approval
	}
	if result.Checkpoint != nil {
		if result.Checkpoint.Metadata == nil {
			result.Checkpoint.Metadata = map[string]any{}
		}
		result.Checkpoint.Metadata["tool_registry_hash"] = stableRuntimeJSONHash(runtimeToolNames(tools))
		result.Checkpoint.Metadata["tool_policy_hash"] = stableRuntimeJSONHash(runRequest.ToolPolicy)
		if result.Approval != nil {
			interactionHash := stableRuntimeJSONHash(map[string]any{"run_id": rc.RunID, "tool_call_id": result.Approval.ToolCallID})
			result.Checkpoint.Interaction = &runtimeagent.Interaction{
				ID: "approval-" + interactionHash[:24], Kind: result.Approval.Kind, Title: result.Approval.Title,
				Reason: result.Approval.Reason, IsBlocking: result.Approval.IsBlocking, Options: append([]toolruntime.ApprovalOption(nil), result.Approval.Options...),
				ToolCallID: result.Approval.ToolCallID, Questions: append([]toolruntime.UserInputQuestion(nil), result.Approval.Questions...),
			}
		}
		output["checkpoint"] = result.Checkpoint
	}
	if result.StopReason == runtimeagent.StopReasonFinalAnswer && n.AdHocMemoryNoteWriter != nil &&
		rc.ParentRunID == nil && rc.DelegationDepth == 0 && memory.HasExplicitMemoryIntent(task) {
		conversationID := int64(0)
		if rc.ConversationID != nil {
			conversationID = *rc.ConversationID
		}
		if _, noteErr := n.AdHocMemoryNoteWriter.AppendAdHocNote(ctx, rc.OwnerID, conversationID, rc.RunID, task, result.FinalAnswer); noteErr != nil {
			// A note is auxiliary input to consolidation; never turn a successful
			// user run into a failed run because the note store is unavailable.
			emitRuntimeEvent(ctx, rc, runtimeevent.Event{Type: runtimeevent.AgentStep, RunID: rc.RunID,
				Payload: map[string]any{"type": runtimeagent.StepTypeError, "error": "append ad-hoc memory note: " + noteErr.Error()}})
		}
	}
	if cfg.ReturnIntermediateSteps || cfg.OutputMode == "full" {
		output["steps"] = runtimeagent.CompactSteps(result.Steps, 8192)
	}
	n.checkExtractionTrigger(ctx, rc, result, result.Iterations, cfg.MemoryEnabled)
	n.finalizeReflection(ctx, rc, task, result, reflectionPolicy)
	emitAgentResultEvent(ctx, rc, result, nil)
	return output, nil
}

// emitAgentResultEvent runs after output validation/repair and reflection
// finalization, so the durable audit event always describes the outcome that
// the application layer will persist and expose in its terminal snapshot.
func emitAgentResultEvent(ctx context.Context, rc *RunContext, result *runtimeagent.RunResult, runErr error) {
	eventType := runtimeevent.AgentFinished
	payload := map[string]any{
		"stop_reason":  "",
		"iterations":   0,
		"tool_calls":   0,
		"latency_ms":   0,
		"total_tokens": 0,
	}
	if result != nil {
		payload["stop_reason"] = result.StopReason
		payload["iterations"] = result.Iterations
		payload["tool_calls"] = result.ToolCalls
		payload["latency_ms"] = result.LatencyMS
		payload["total_tokens"] = result.Usage.TotalTokens
	}
	if runErr != nil {
		eventType = runtimeevent.AgentFailed
		payload["error"] = runErr.Error()
	}
	emitRuntimeEvent(ctx, rc, runtimeevent.Event{Type: eventType, RunID: rc.RunID, Payload: payload})
}

// finalizeCitations strips the Codex-compatible <oai-mem-citation> block from
// the final answer before user-visible emission and records owner-validated,
// per-run deduplicated usage (usage_count / last_used_at) for the cited
// memories. Malformed lines, missing IDs and foreign-owner IDs are dropped
// individually and surfaced as runtime warning events; nothing here fails the
// run. RecallLog stays the returned-candidate audit and is never touched.
func (n runtimeCore) finalizeCitations(ctx context.Context, rc *RunContext, result *runtimeagent.RunResult) {
	if rc == nil || result == nil {
		return
	}
	outcome := memory.AccountCitations(ctx, rc.OwnerID, result.FinalAnswer, n.Memories)
	for _, warning := range outcome.Warnings {
		emitRuntimeEvent(ctx, rc, runtimeevent.Event{Type: runtimeevent.AgentStep, RunID: rc.RunID,
			Payload: map[string]any{"type": runtimeagent.StepTypeError, "error": warning}})
	}
	result.FinalAnswer = outcome.VisibleText
	// Full-output modes re-emit the recorded steps; align the final answer
	// step with the stripped text and strip any citation lines from stored
	// llm_response steps so the citation block never reaches the user-visible
	// step payload either. The raw text was already captured for accounting.
	for i := range result.Steps {
		switch result.Steps[i].Type {
		case runtimeagent.StepTypeFinalAnswer:
			result.Steps[i].Content = outcome.VisibleText
		case runtimeagent.StepTypeLLMResponse:
			result.Steps[i].Content = memory.StripCitations(result.Steps[i].Content)
		}
	}
}
