package agentruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"agentcanvas/internal/domain/reflection"
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
	embeddingProviderID, embeddingModel := int64(0), ""
	if semanticProvider != nil {
		embeddingProviderID, embeddingModel = semanticProvider.ProviderID, semanticProvider.EmbeddingModel
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
	mode := agentMode(cfg.Mode)
	compactionProvider := loaded
	if cfg.CompactionProviderID > 0 || strings.TrimSpace(cfg.CompactionModel) != "" {
		providerID := cfg.CompactionProviderID
		if providerID <= 0 {
			providerID = loaded.ProviderID
		}
		if candidate, loadErr := n.Providers.LoadChatProviderConfig(ctx, rc.OwnerID, providerID, cfg.CompactionModel); loadErr == nil {
			compactionProvider = candidate
		} else {
			slog.WarnContext(ctx, "load auxiliary compaction model failed; using main model", "provider_id", providerID, "model", cfg.CompactionModel, "error", loadErr)
		}
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
		memoryBlock = n.buildAutomaticMemoryBlock(ctx, rc, cfg, semanticProvider, recallTask)
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
			return nil, err
		}
		if historyPrepared {
			observability.ContextSystemMetrics.RecordConversationSnapshot(historyTrace.Reused, historyTrace.BeforeTokens, historyTrace.AfterTokens, historyTrace.ModelCalls, historyTrace.LatencyMS)
			if historyTrace.FallbackReason != "" {
				slog.WarnContext(ctx, "conversation compaction used main-model fallback", "reason", historyTrace.FallbackReason, "provider_id", historyTrace.ProviderID, "model", historyTrace.Model, "model_calls", historyTrace.ModelCalls)
			}
			if historyTrace.Created {
				observability.ContextSystemMetrics.RecordCompaction("completed")
			} else if historyTrace.Failure != "" {
				observability.ContextSystemMetrics.RecordCompaction("fallback")
			}
		}
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
	var reflectionRecall reflection.RecallResult
	if resume == nil && n.Reflections != nil && reflectionPolicy.Active() {
		reflectionRecall, _ = n.Reflections.Recall(ctx, reflection.RecallRequest{
			OwnerID:             rc.OwnerID,
			AgentID:             rc.AgentID,
			RunID:               rc.RunID,
			Mode:                mode,
			Task:                recallTask,
			Policy:              reflectionPolicy,
			EmbeddingProviderID: embeddingProviderID,
			EmbeddingModel:      embeddingModel,
		})
		if reflectionAffectsExecution(reflectionPolicy) && strings.TrimSpace(reflectionRecall.Context) != "" {
			contextBlocks = append(contextBlocks, runtimeagent.ContextBlock{
				Name:    "reflection_memory",
				Role:    "system",
				Content: reflectionRecall.Context,
				Pinned:  false,
			})
		}
	}
	// Redis Working Memory configuration remains parse-compatible but is no
	// longer wired into Agent Runtime. Durable snapshots own cross-run continuity.
	var plan *runtimeagent.Plan
	// Generate an initial plan only for a new plan-and-execute run.
	if resume == nil && mode == "plan_execute" && task != "" {
		planner := runtimeagent.Planner{
			LLM:        n.LLM,
			MaxSteps:   8,
			ProviderID: loaded.ProviderID,
			ModelName:  loaded.Model,
		}
		lessons := ""
		if reflectionAffectsExecution(reflectionPolicy) {
			lessons = reflectionRecall.Context
		}
		generatedPlan, planErr := planner.GeneratePlanWithLessons(ctx, loaded.Config, loaded.Model, task, lessons, cfg.Temperature)
		if planErr == nil && generatedPlan != nil {
			plan = generatedPlan
		}
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
		LLM:        n.LLM,
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
		OwnerID:            rc.OwnerID,
		AgentID:            rc.AgentID,
		AgentReleaseID:     rc.AgentReleaseID,
		RunID:              rc.RunID,
		DelegationDepth:    rc.DelegationDepth,
		ConversationID:     rc.ConversationID,
		ProjectID:          projectIDFromRunContext(rc),
		Provider:           loaded.Config,
		Model:              loaded.Model,
		CompactionProvider: compactionProvider.Config,
		CompactionModel:    compactionProvider.Model,
		Mode:               mode,
		// Runner records the plan and injects it into execution context.
		Plan:                       plan,
		SystemPrompt:               systemPrompt,
		Task:                       task,
		ReflectionEnabled:          cfg.ReflectionEnabled,
		ReflectionPolicy:           reflectionPolicy,
		RecalledReflectionIDs:      reflectionLessonIDs(reflectionRecall.Lessons),
		Temperature:                cfg.Temperature,
		MaxIterations:              cfg.MaxIterations,
		MaxToolCalls:               cfg.MaxToolCalls,
		MaxExecutionTimeMS:         cfg.MaxExecutionTimeMS,
		MaxParallelTools:           cfg.MaxParallelSubAgents,
		MaxInputChars:              cfg.MaxInputChars,
		MaxInputTokens:             cfg.MaxInputTokens,
		ContextWindowTokens:        cfg.ContextWindowTokens,
		ReservedOutputTokens:       cfg.ReservedOutputTokens,
		ContextSafetyMarginTokens:  cfg.ContextSafetyMarginTokens,
		ModelAutoCompactTokenLimit: cfg.ModelAutoCompactTokenLimit,
		CompactPrompt:              cfg.CompactPrompt,
		MaxRuleTokens:              cfg.MaxRuleTokens,
		RuleTags:                   ruleTags,
		RuleRiskLevel:              ruleRisk,
		RuleHash:                   cfg.RuleHash,
		Rules:                      append([]rules.Rule(nil), cfg.Rules...),
		RuleTrace:                  ruleTrace,
		ContextBlocks:              contextBlocks,
		ToolPolicy: runtimeagent.ToolPolicy{
			RequireApprovalForRisk: cfg.RequireApprovalForRisk,
			MaxToolTimeoutMS:       cfg.MaxToolTimeoutMS,
			MaxToolOutputBytes:     cfg.MaxToolOutputBytes,
			AllowedHosts:           append([]string(nil), cfg.AllowedHosts...),
			DenyAllHosts:           cfg.DenyAllHosts,
			RuleBindings:           rules.PolicyBindingsForRules(cfg.Rules),
		},
		Tools:     tools,
		Workspace: rc.Workspace,
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
		n.finalizeReflection(ctx, rc, cfg, loaded, task, result, reflectionPolicy)
		emitAgentResultEvent(ctx, rc, result, err)
		return nil, err
	}
	if result == nil {
		err = fmt.Errorf("agent runtime returned no result")
		emitAgentResultEvent(ctx, rc, nil, err)
		return nil, err
	}
	output := RunOutput{
		"content":       result.FinalAnswer,
		"final_answer":  result.FinalAnswer,
		"stop_reason":   result.StopReason,
		"iterations":    result.Iterations,
		"tool_calls":    result.ToolCalls,
		"usage":         result.Usage,
		"total_tokens":  result.Usage.TotalTokens,
		"latency_ms":    result.LatencyMS,
		"context_trace": result.Context,
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
			n.finalizeReflection(ctx, rc, cfg, loaded, task, result, reflectionPolicy)
			err = fmt.Errorf("%w: agent runtime final_answer does not match output_schema_json: %v", agenterrors.ErrInvalidInput, schemaErr)
			emitAgentResultEvent(ctx, rc, result, err)
			return nil, err
		}
		output["structured_output"] = parsed
	}
	// Automatic memory review is handled by the candidate pipeline. Do not
	// persist the final answer into Working Memory and inject it again later.
	if result.Plan != nil {
		output["plan"] = result.Plan
	}
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
				Reason: result.Approval.Reason, Options: append([]toolruntime.ApprovalOption(nil), result.Approval.Options...),
				ToolCallID: result.Approval.ToolCallID,
			}
		}
		output["checkpoint"] = result.Checkpoint
	}
	if cfg.ReturnIntermediateSteps || cfg.OutputMode == "full" {
		output["steps"] = runtimeagent.CompactSteps(result.Steps, 8192)
	}
	n.checkExtractionTrigger(ctx, rc, result, result.Iterations, cfg.MemoryEnabled)
	n.finalizeReflection(ctx, rc, cfg, loaded, task, result, reflectionPolicy)
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
