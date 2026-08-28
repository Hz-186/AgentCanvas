package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"agentcanvas/internal/domain/conversation"
	"agentcanvas/internal/domain/memory"
	"agentcanvas/internal/infrastructure/llm"
	"agentcanvas/internal/observability"
	"agentcanvas/internal/pkg/logger"
	"agentcanvas/internal/pkg/strutil"
	"agentcanvas/internal/runtime/compaction"
	"agentcanvas/internal/runtime/harness/hooks"
	"agentcanvas/internal/runtime/harness/rules"
	"agentcanvas/internal/runtime/toolruntime"
)

var ErrNoToolCallingClient = errors.New("llm client does not support tool calling")
var ErrMandatoryRuleBudgetExceeded = errors.New("mandatory rules exceed the configured input context budget")
var ErrContextOverflow = errors.New("context exceeds the model input window")
var ErrDuplicateToolName = errors.New("duplicate tool name")
var ErrRunPaused = errors.New("run paused")

type StepEmitter func(ctx context.Context, step RunStep) error

type Runner struct {
	LLM          llm.ToolCallingClient
	OnStep       StepEmitter
	OnModelEvent ModelEventEmitter
	Now          func() time.Time
	ProviderID   int64
	ModelName    string
	Snapshots    conversation.SnapshotRepository
	// Logger is the optional diagnostics seam for tool/LLM/compaction
	// lifecycle events. Nil keeps production behavior via slog.Default.
	Logger *slog.Logger
}

func NewRunner(client llm.ToolCallingClient) *Runner {
	return &Runner{LLM: client}
}

// diagnosticsLogger is the fail-open observation seam for lifecycle
// diagnostics. Diagnostics never change runtime results.
func (r *Runner) diagnosticsLogger() *slog.Logger {
	if r.Logger != nil {
		return r.Logger
	}
	return slog.Default()
}

func (r *Runner) Run(ctx context.Context, req RunRequest) (*RunResult, error) {
	if r.LLM == nil {
		return nil, ErrNoToolCallingClient
	}
	if strings.TrimSpace(req.Model) == "" {
		return nil, fmt.Errorf("agent model is required")
	}
	task := strings.TrimSpace(req.Task)
	if task == "" {
		return nil, fmt.Errorf("agent task is required")
	}
	req.Task = task
	started := r.now()
	result := &RunResult{
		StartedAt:  started,
		Steps:      append([]RunStep(nil), req.ResumeSteps...),
		Reflection: ReflectionTrace{},
	}
	if req.MaxIterations <= 0 {
		req.MaxIterations = 8
	}
	if req.MaxToolCalls <= 0 {
		req.MaxToolCalls = 16
	}
	if req.MaxParallelTools <= 0 {
		req.MaxParallelTools = 8
	}
	if req.MaxParallelTools > 64 {
		req.MaxParallelTools = 64
	}
	if req.MaxExecutionTimeMS > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(req.MaxExecutionTimeMS)*time.Millisecond)
		defer cancel()
	}
	toolHooks := req.ToolHookChain
	if len(toolHooks.Pre) == 0 && len(toolHooks.Post) == 0 {
		toolHooks = hooks.DefaultToolHookChain()
	}

	tools := make([]llm.ToolDefinition, 0, len(req.Tools))
	toolByName := make(map[string]toolruntime.RuntimeTool, len(req.Tools))
	toolNames := make([]string, 0, len(req.Tools))
	for _, item := range req.Tools {
		if item == nil {
			continue
		}
		name := strings.TrimSpace(item.Name())
		if name == "" {
			continue
		}
		if _, exists := toolByName[name]; exists {
			return nil, fmt.Errorf("%w: %s", ErrDuplicateToolName, name)
		}
		toolByName[name] = item
		toolNames = append(toolNames, name)
		tools = append(tools, llm.ToolDefinition{
			Type: "function",
			Function: llm.ToolFunctionDefinition{
				Name:        name,
				Description: item.Description(),
				Parameters:  item.Parameters(),
				Strict:      true,
			},
		})
	}

	var baseMessages []llm.ChatMessage
	var contextTrace ContextTrace
	if len(req.ResumeMessages) > 0 {
		if len(req.ResumeBaseMessages) > 0 {
			baseMessages = append([]llm.ChatMessage(nil), req.ResumeBaseMessages...)
		} else {
			baseMessages = append([]llm.ChatMessage(nil), req.ResumeMessages...)
		}
		contextTrace = req.ResumeContext
		contextTrace.Strategy = "resumed_from_checkpoint"
	} else {
		baseMessages, contextTrace = ContextAssembler{}.Build(req)
		counter := modelTokenCount(req, "token counter probe")
		contextTrace.TokenCounterMethod = counter.Method
		contextTrace.TokenCounterError = counter.Error
		contextTrace.AutoCompactTokenLimit = autoCompactLimit(req)
		contextTrace.AutoCompactLimitScope = autoCompactScope(req)
		contextTrace.RuleHash = req.RuleHash
		if contextTrace.CoreOverflow {
			observability.RuleSystemMetrics.RecordMandatoryOverflow()
			return nil, fmt.Errorf("%w: mandatory_tokens=%d budget_tokens=%d deficit_tokens=%d",
				ErrMandatoryRuleBudgetExceeded, contextTrace.MandatoryTokens, contextTrace.MandatoryBudgetTokens, contextTrace.MandatoryDeficitTokens)
		}
	}
	messages := baseMessages
	transcript := make([]llm.ChatMessage, 0, req.MaxIterations*2)
	// Cursor over transcript entries that are exempt from sink writes:
	// already-persisted entries (resume), steering messages, and the
	// post-compaction window all count without needing a row.
	persistedCount := 0
	if req.ResumePersistedMessageCount > 0 {
		persistedCount = req.ResumePersistedMessageCount
	}
	transcriptCursor := req.ResumeTranscriptCursor
	previousRuleIDs := make([]string, 0)
	result.Context = contextTrace
	startIteration := 0
	startToolCalls := 0
	needsFollowUp := false
	if len(req.ResumeMessages) > 0 {
		transcript = append([]llm.ChatMessage(nil), req.ResumeTranscript...)
		if transcriptCursor < len(transcript) {
			// Legacy checkpoints did not carry a cursor. The visible transcript
			// is the only safe lower bound; newer checkpoints preserve a larger
			// monotonic value across compaction.
			transcriptCursor = len(transcript)
		}
		// Entries the resumer appended past the persisted cursor (approval
		// answers, rejection notes) still need rows so tool pairings stay
		// complete; legacy checkpoints with a zero cursor replay everything.
		if persistedCount > len(transcript) {
			persistedCount = len(transcript)
		}
		newEntries := transcript[persistedCount:]
		persistedCount, _ = r.persistTranscriptEntries(ctx, result, req, newEntries, persistedCount, transcriptCursor)
		transcriptCursor += len(newEntries)
		messages = assembleRoundMessages(baseMessages, nil, transcript)
		startIteration = req.ResumeIteration
		startToolCalls = req.ResumeToolCalls
		result.Iterations = startIteration
		result.ToolCalls = startToolCalls
		if req.ResumeIteration > 0 {
			startIteration = req.ResumeIteration
		}
		result.Context = contextTrace
		unresolved := findUnresolvedToolCalls(messages, toolByName)
		if len(unresolved) > 0 {
			messageCountBeforeTools := len(messages)
			stepStart := len(result.Steps)
			stop, updatedMessages := r.executeToolBatch(ctx, req, result, messages, unresolved, toolHooks, contextTrace, toolNames, req.ResumeApprovedToolCallIDs, persistedCount)
			messages = updatedMessages
			if len(updatedMessages) > messageCountBeforeTools {
				newEntries := updatedMessages[messageCountBeforeTools:]
				transcript = append(transcript, newEntries...)
				persistedCount, _ = r.persistTranscriptEntries(ctx, result, req, newEntries, persistedCount, transcriptCursor)
				transcriptCursor += len(newEntries)
			}
			if !stop {
				if feedback := r.maybeReflect(ctx, req, result, result.Steps[stepStart:]); feedback != nil {
					messages = append(messages, *feedback)
					transcript = append(transcript, *feedback)
					persistedCount, _ = r.persistTranscriptEntries(ctx, result, req, []llm.ChatMessage{*feedback}, persistedCount, transcriptCursor)
					transcriptCursor++
				}
			}
			if stop {
				hydrateCheckpoint(result.Checkpoint, baseMessages, transcript, result.Steps, messages, persistedCount, transcriptCursor)
				return finish(result, r.now()), nil
			}
		}
	}
	// One run contains multiple LLM/tool iterations.
	for iteration := startIteration; iteration < req.MaxIterations; iteration++ {
		if req.SteeringProvider != nil {
			for _, steering := range req.SteeringProvider() {
				if strings.TrimSpace(steering) == "" {
					continue
				}
				message := llm.ChatMessage{Role: conversation.RoleDeveloper, Content: steering}
				messages = append(messages, message)
				transcript = append(transcript, message)
				persistedCount++
				transcriptCursor++
			}
		}
		if err := ctx.Err(); err != nil {
			result = finishWithContext(result, err, r.now())
			if errors.Is(context.Cause(ctx), ErrRunPaused) {
				result.StopReason = StopReasonPaused
				result.Checkpoint = checkpointFromMessages(
					req, messages, contextTrace, toolNames, nil,
					result.StopReason, result.Iterations, result.ToolCalls)
				hydrateCheckpoint(result.Checkpoint, baseMessages, transcript, result.Steps, messages, persistedCount, transcriptCursor)
			}
			return result, nil
		}
		if needsFollowUp {
			status := runtimeTokenStatus(req, baseMessages, transcript, tools)
			if status.TokenLimitReached {
				previousTranscript := append([]llm.ChatMessage(nil), transcript...)
				compactedRuntime, compactUsage, runtimeCompaction := r.compactRuntimeTranscript(ctx, req, transcript)
				transcript = compactedRuntime
				// Retained user entries and the SUMMARY entry are exempt
				// (user rows already exist; SUMMARY never persists), so the
				// sink cursor restarts at the compacted length.
				persistedCount = len(transcript)
				result.Usage = addUsage(result.Usage, compactUsage)
				if runtimeCompaction != nil {
					runtimeCompaction.BeforeTokens = status.Measured
					runtimeCompaction.Threshold = status.Limit
					runtimeCompaction.AfterTokens = modelMessagesTokens(req, compactedRuntime)
					if runtimeCompaction.Status == "completed" {
						if err := r.persistRuntimeCompaction(ctx, req, runtimeCompaction, previousTranscript, compactedRuntime); err != nil {
							runtimeCompaction.Status = "failed"
							runtimeCompaction.Error = fmt.Sprintf("persist runtime compaction: %v", err)
						}
						if runtimeCompaction.Status == "completed" && req.TokenBudgetCompaction {
							baseMessages = baseMessagesAfterTokenBudget(req, baseMessages, req.RetainClientDeveloperMessages)
						}
					}
					observability.ContextSystemMetrics.RecordCompaction(runtimeCompaction.Status)
					contextTrace.Compactions = append(contextTrace.Compactions, *runtimeCompaction)
					contextTrace.SavedTokens += runtimeCompaction.SavedTokens
					contextTrace.Compressed = appendUniqueStrings(contextTrace.Compressed, []string{"runtime_model_compaction"})
					if runtimeCompaction.Status == "failed" {
						result.StopReason = StopReasonLLMError
						step := r.appendStep(result, RunStep{Type: StepTypeError, Error: runtimeCompaction.Error, ProviderID: r.ProviderID, Model: r.ModelName})
						_ = r.emit(ctx, step)
						result.Context = contextTrace
						return finish(result, r.now()), errors.New(runtimeCompaction.Error)
					}
				}
			}
		}
		{
			plan := (RulePlanner{}).Plan(RulePlanningState{
				Iteration:      iteration + 1,
				SystemPrompt:   req.SystemPrompt,
				Task:           req.Task,
				Mode:           req.Mode,
				RiskLevel:      req.RuleRiskLevel,
				Tags:           req.RuleTags,
				UsedToolNames:  toolNamesFromMessages(transcript),
				AvailableTools: tools,
				BaseMessages:   baseMessages,
				Transcript:     transcript,
				MaxInputTokens: req.MaxInputTokens,
				ContextWindow:  req.ContextWindowTokens,
				ReservedOutput: req.ReservedOutputTokens,
				SafetyMargin:   req.ContextSafetyMarginTokens,
				MaxRuleTokens:  req.MaxRuleTokens,
				ProviderType:   req.Provider.ProviderType,
				Model:          req.Model,
				Rules:          req.Rules,
			})
			messages = assembleRoundMessages(baseMessages, ruleMessages(plan.Rules), transcript)
			contextTrace.RuleTrace = mergeRuleTraces(req.RuleTrace, plan.Trace)
			contextTrace.RuleBudget = plan.Budget
			contextTrace.RuleRounds = append(contextTrace.RuleRounds, RuleRoundTrace{
				Iteration:             iteration + 1,
				Loaded:                stringDifference(plan.Trace.Loaded, previousRuleIDs),
				Removed:               stringDifference(previousRuleIDs, plan.Trace.Loaded),
				Trace:                 plan.Trace,
				Budget:                plan.Budget,
				EstimatedPromptTokens: estimateMessagesTokens(messages),
			})
			contextTrace.EstimatedPromptTokens += contextTrace.RuleRounds[len(contextTrace.RuleRounds)-1].EstimatedPromptTokens
			previousRuleIDs = append(previousRuleIDs[:0], plan.Trace.Loaded...)
			result.Context = contextTrace
		}
		// // ***

		//// ***
		result.Iterations = iteration + 1
		needsFollowUp = false
		//// ***

		llmStarted := r.now()
		resp, err := r.executeModelTurn(ctx, req.Provider, llm.ToolChatRequest{
			Model:       req.Model,
			Messages:    messages,
			Tools:       tools,
			ToolChoice:  "auto",
			Temperature: req.Temperature,
		})
		latencyMS := int(r.now().Sub(llmStarted).Milliseconds())
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				result = finishWithContext(result, err, r.now())
				if errors.Is(context.Cause(ctx), ErrRunPaused) {
					result.StopReason = StopReasonPaused
					result.Checkpoint = checkpointFromMessages(
						req, messages, contextTrace, toolNames, nil,
						result.StopReason, result.Iterations, result.ToolCalls,
					)
					hydrateCheckpoint(result.Checkpoint, baseMessages, transcript, result.Steps, messages, persistedCount, transcriptCursor)
				}
				return result, nil
			}
			result.StopReason = StopReasonLLMError
			step := r.appendStep(result, RunStep{
				Type:       StepTypeError,
				Error:      err.Error(),
				LatencyMS:  latencyMS,
				ProviderID: r.ProviderID,
				Model:      r.ModelName,
			})
			_ = r.emit(ctx, step)
			return finish(result, r.now()), err
		}
		result.Usage = addUsage(result.Usage, resp.Usage)
		if resp.Usage.PromptTokens > 0 {
			contextTrace.ProviderPromptTokens += resp.Usage.PromptTokens
			contextTrace.TokenEstimationError = contextTrace.ProviderPromptTokens - contextTrace.EstimatedPromptTokens
			if len(contextTrace.RuleRounds) > 0 {
				contextTrace.RuleRounds[len(contextTrace.RuleRounds)-1].ProviderPromptTokens = resp.Usage.PromptTokens
			}
			result.Context = contextTrace
		}
		assistant := resp.Message
		if assistant.Role == "" {
			assistant.Role = conversation.RoleAssistant
		}
		step := r.appendStep(result, RunStep{
			Type:       StepTypeLLMResponse,
			Role:       assistant.Role,
			Content:    assistant.Content,
			LatencyMS:  latencyMS,
			ProviderID: r.ProviderID,
			Model:      r.ModelName,
			TokenCount: resp.Usage.TotalTokens,
		})
		_ = r.emit(ctx, step)
		if resp.ProposedPlan != "" {
			planStep := r.appendStep(result, RunStep{
				Type:       StepTypeProposedPlan,
				Role:       conversation.RoleAssistant,
				Content:    resp.ProposedPlan,
				ProviderID: r.ProviderID,
				Model:      r.ModelName,
			})
			_ = r.emit(ctx, planStep)
		}
		if len(assistant.ToolCalls) == 0 {
			// A final model response ends a guided plan without claiming verification.
			// result.FinalAnswer keeps the RAW text so finalization can still parse
			// the citation block for owner-validated usage accounting. The emitted
			// and stored final step carries the stripped text instead; the
			// persisted transcript row is sanitized inside persistTranscriptEntries.
			result.FinalAnswer = assistant.Content
			result.StopReason = StopReasonFinalAnswer
			visible := memory.StripCitations(assistant.Content)
			_, result.AssistantMessageID = r.persistTranscriptEntries(ctx, result, req, []llm.ChatMessage{assistant}, persistedCount, transcriptCursor)
			transcriptCursor++
			finalStep := r.appendStep(result,
				RunStep{
					Type:       StepTypeFinalAnswer,
					Role:       conversation.RoleAssistant,
					Content:    visible,
					ProviderID: r.ProviderID,
					Model:      r.ModelName,
				},
			)
			_ = r.emit(ctx, finalStep)
			return finish(result, r.now()), nil
		}
		messages = append(messages, assistant)
		messageCountBeforeTools := len(messages)
		stepStart := len(result.Steps)
		stop, updatedMessages := r.executeToolBatch(ctx, req, result, messages, assistant.ToolCalls, toolHooks, contextTrace, toolNames, nil, persistedCount)
		messages = updatedMessages
		transcript = append(transcript, assistant)
		persistedCount, _ = r.persistTranscriptEntries(ctx, result, req, []llm.ChatMessage{assistant}, persistedCount, transcriptCursor)
		transcriptCursor++
		if len(updatedMessages) > messageCountBeforeTools {
			newEntries := updatedMessages[messageCountBeforeTools:]
			transcript = append(transcript, newEntries...)
			persistedCount, _ = r.persistTranscriptEntries(ctx, result, req, newEntries, persistedCount, transcriptCursor)
			transcriptCursor += len(newEntries)
		}
		if !stop {
			if feedback := r.maybeReflect(ctx, req, result, result.Steps[stepStart:]); feedback != nil {
				messages = append(messages, *feedback)
				transcript = append(transcript, *feedback)
				persistedCount, _ = r.persistTranscriptEntries(ctx, result, req, []llm.ChatMessage{*feedback}, persistedCount, transcriptCursor)
				transcriptCursor++
			}
		}
		if stop {
			hydrateCheckpoint(result.Checkpoint, baseMessages, transcript, result.Steps, messages, persistedCount, transcriptCursor)
			return finish(result, r.now()), nil
		}
		needsFollowUp = true
	}
	//
	result.StopReason = StopReasonMaxIterations
	result.FinalAnswer = "Agent stopped because max_iterations was exceeded."
	finalStep2 := r.appendStep(result,
		RunStep{
			Type:       StepTypeFinalAnswer,
			Role:       conversation.RoleAssistant,
			Content:    result.FinalAnswer,
			ProviderID: r.ProviderID,
			Model:      r.ModelName,
		},
	)
	_ = r.emit(ctx, finalStep2)
	return finish(result, r.now()), nil
}

func assembleRoundMessages(base, ruleMessages, transcript []llm.ChatMessage) []llm.ChatMessage {
	messages := make([]llm.ChatMessage, 0, len(base)+len(ruleMessages)+len(transcript))
	for _, message := range base {
		if message.Role != conversation.RoleSystem {
			continue
		}
		messages = append(messages, message)
	}
	messages = append(messages, ruleMessages...)
	skipTask := ""
	if len(transcript) > 0 && transcript[0].Role == conversation.RoleUser {
		skipTask = transcript[0].Content
	}
	skipIndex := -1
	if skipTask != "" {
		for i := len(base) - 1; i >= 0; i-- {
			if base[i].Role != conversation.RoleSystem && base[i].Role == conversation.RoleUser && base[i].Content == skipTask {
				skipIndex = i
				break
			}
		}
	}
	for index, message := range base {
		// The compaction result already carries the initial task as a user
		// message; do not inject the same task block twice.
		if index == skipIndex {
			continue
		}
		if message.Role != conversation.RoleSystem {
			messages = append(messages, message)
		}
	}
	messages = append(messages, transcript...)
	return messages
}

func toolNamesFromMessages(messages []llm.ChatMessage) []string {
	seen := map[string]bool{}
	names := make([]string, 0)
	for _, message := range messages {
		for _, call := range message.ToolCalls {
			name := strings.TrimSpace(call.Name)
			if name == "" || seen[name] {
				continue
			}
			seen[name] = true
			names = append(names, name)
		}
	}
	return names
}

func stringDifference(values, baseline []string) []string {
	seen := make(map[string]bool, len(baseline))
	for _, value := range baseline {
		seen[value] = true
	}
	difference := make([]string, 0)
	for _, value := range values {
		if value != "" && !seen[value] {
			difference = append(difference, value)
		}
	}
	return difference
}

func mergeRuleTraces(persistent, dynamic rules.Trace) rules.Trace {
	merged := dynamic
	merged.Loaded = appendUniqueStrings(persistent.Loaded, dynamic.Loaded)
	merged.EstimatedUsed += persistent.EstimatedUsed
	return merged
}

func appendUniqueStrings(left, right []string) []string {
	seen := map[string]bool{}
	merged := make([]string, 0, len(left)+len(right))
	for _, values := range [][]string{left, right} {
		for _, value := range values {
			if value != "" && !seen[value] {
				seen[value] = true
				merged = append(merged, value)
			}
		}
	}
	return merged
}

type preparedToolCall struct {
	call       llm.ToolCall
	tool       toolruntime.RuntimeTool
	metadata   toolruntime.ToolMetadata
	execCtx    context.Context
	execCancel context.CancelFunc
	result     *toolruntime.ToolResult
	err        error
	latencyMS  int
	stepIndex  int
}

func (r *Runner) executeToolBatch(
	ctx context.Context,
	req RunRequest,
	result *RunResult,
	messages []llm.ChatMessage,
	calls []llm.ToolCall,
	toolHooks hooks.ToolHookChain,
	contextTrace ContextTrace,
	toolNames []string,
	approvedToolCallIDs []string,
	persistedCount int,
) (bool, []llm.ChatMessage) {
	normalizer, normalizeErr := NewToolCallNormalizer(req.Tools, nil)
	prepared := make([]preparedToolCall, 0, len(calls))
	normalizedCalls := make([]NormalizedToolCall, len(calls))
	if normalizeErr == nil {
		normalizedCalls = normalizer.NormalizeBatch(calls)
	} else {
		// The request was already checked for duplicate names above. Keep a
		// defensive fallback so malformed callers still receive a tool result
		// instead of bypassing the execution boundary.
		for index, call := range calls {
			normalizedCalls[index] = NormalizedToolCall{Call: call, Issue: &ToolCallIssue{Code: ToolCallIssueInvalidAlias, Message: normalizeErr.Error()}}
		}
	}
	for _, normalized := range normalizedCalls {
		call := normalized.Call
		if result.ToolCalls+len(prepared) >= req.MaxToolCalls {
			result.StopReason = StopReasonMaxToolCalls
			result.FinalAnswer = "Agent stopped because max_tool_calls was exceeded."
			step := r.appendStep(result, RunStep{
				Type:       StepTypeFinalAnswer,
				Role:       conversation.RoleAssistant,
				Content:    result.FinalAnswer,
				ProviderID: r.ProviderID,
				Model:      r.ModelName,
			})
			_ = r.emit(ctx, step)
			return true, messages
		}
		toolStep := r.appendStep(result, RunStep{
			Type:          StepTypeToolCall,
			ToolCallID:    call.ID,
			ToolName:      call.Name,
			ArgumentsJSON: call.Arguments,
			ProviderID:    r.ProviderID,
			Model:         r.ModelName,
		})
		toolStepIndex := toolStep.Index
		_ = r.emit(ctx, toolStep)
		toolImpl := normalized.Tool
		if normalized.Issue != nil || toolImpl == nil {
			result.StopReason = StopReasonToolNameNotFound
			errMessage := fmt.Sprintf("tool %s is not available", call.Name)
			if normalized.Issue != nil {
				errMessage = normalized.Issue.Message
			}
			messages = append(messages, toolMessage(call.ID, errMessage))
			step := r.appendStep(result, RunStep{
				Type:       StepTypeToolResult,
				ToolCallID: call.ID,
				ToolName:   call.Name,
				Content:    errMessage,
				IsError:    true,
				ProviderID: r.ProviderID,
				Model:      r.ModelName,
			})
			_ = r.emit(ctx, step)
			continue
		}
		metadata := toolruntime.MetadataOf(toolImpl)
		// whether first time or second time
		pre := toolHooks.BeforeToolUse(ctx, hooks.PreToolUseRequest{
			ToolCallID: call.ID,
			ToolName:   call.Name,
			Arguments:  call.Arguments,
			Metadata:   metadata,
			Policy:     req.ToolPolicy,
		})
		result.HookTrace = appendHookTrace(result.HookTrace, call.Name, pre.Traces)
		if pre.Approval != nil && !slices.Contains(approvedToolCallIDs, call.ID) {
			// // ***
			for i := range prepared {
				if prepared[i].execCancel != nil {
					prepared[i].execCancel()
				}
			}
			approval := &Approval{
				ToolCallID: pre.Approval.ToolCallID,
				ToolName:   pre.Approval.ToolName,
				RiskLevel:  pre.Approval.RiskLevel,
				Reason:     pre.Approval.Reason,
				Metadata:   pre.Approval.Metadata,
			}
			result.StopReason = StopReasonWaitingHuman
			result.FinalAnswer = "Agent is waiting for human approval before executing tool " + call.Name + "."
			result.Approval = approval
			pending := call
			result.Checkpoint = checkpointFromMessages(req, messages, contextTrace,
				toolNames, &pending, StopReasonWaitingHuman, result.Iterations, result.ToolCalls)
			result.Checkpoint.Metadata["approved_tool_call_ids"] = append([]string(nil), approvedToolCallIDs...)
			approvalStep := r.appendStep(result, RunStep{
				Type:       StepTypeApproval,
				ToolCallID: call.ID,
				ToolName:   call.Name,
				Content:    approval.Reason,
				ProviderID: r.ProviderID,
				Model:      r.ModelName,
			})
			_ = r.emit(ctx, approvalStep)
			finalStep := r.appendStep(result, RunStep{
				Type:       StepTypeFinalAnswer,
				Role:       conversation.RoleAssistant,
				Content:    result.FinalAnswer,
				ProviderID: r.ProviderID,
				Model:      r.ModelName,
			})
			_ = r.emit(ctx, finalStep)
			return true, messages
		}
		if pre.Denied != nil {
			messages = append(messages, toolMessage(call.ID, pre.Denied.Error()))
			step := r.appendStep(result, RunStep{
				Type:       StepTypeToolResult,
				ToolCallID: call.ID,
				ToolName:   call.Name,
				Content:    pre.Denied.Error(),
				IsError:    true,
				Error:      pre.Denied.Error(),
				ProviderID: r.ProviderID,
				Model:      r.ModelName,
			})
			_ = r.emit(ctx, step)
			continue
		}
		execCtx := pre.Context
		if execCtx == nil {
			execCtx = ctx
		}
		prepared = append(prepared, preparedToolCall{
			call:       call,
			tool:       toolImpl,
			metadata:   metadata,
			execCtx:    execCtx,
			execCancel: pre.Cancel,
			stepIndex:  toolStepIndex,
		})
	}

	plannedCalls := make([]NormalizedToolCall, len(prepared))
	for index := range prepared {
		item := &prepared[index]
		plannedCalls[index] = NormalizedToolCall{
			Call:     item.call,
			Tool:     item.tool,
			Metadata: item.metadata,
		}
	}
	segments := PlanToolBatch(plannedCalls, nil)
	executions := ExecuteToolBatch(ctx, segments, req.MaxParallelTools, func(_ context.Context, batchItem ToolBatchItem) (*toolruntime.ToolResult, error) {
		item := &prepared[batchItem.Index]
		r.logToolStarted(ctx, item)
		started := r.now()
		toolResult, toolErr := item.tool.Execute(item.execCtx, toolruntime.ToolRunContext{
			OwnerID:                     req.OwnerID,
			AgentID:                     req.AgentID,
			RunID:                       req.RunID,
			Mode:                        req.Mode,
			DelegationDepth:             req.DelegationDepth,
			ConversationID:              req.ConversationID,
			ProjectID:                   req.ProjectID,
			Task:                        req.Task,
			Workspace:                   req.Workspace,
			EmitEvent:                   req.EmitEvent,
			GoalRepository:              req.GoalRepository,
			GoalTokenBudgetCeiling:      req.GoalTokenBudgetCeiling,
			DefaultModeRequestUserInput: req.DefaultModeRequestUserInput,
		}, item.call.Arguments)
		if item.execCancel != nil {
			item.execCancel()
			item.execCancel = nil
		}
		item.latencyMS = int(r.now().Sub(started).Milliseconds())
		r.logToolCompleted(ctx, item, toolResult, toolErr)
		return toolResult, toolErr
	})
	for _, execution := range executions {
		item := &prepared[execution.Index]
		item.result = execution.Result
		item.err = execution.Err
		if item.execCancel != nil {
			item.execCancel()
		}
	}
	for i := range prepared {
		item := &prepared[i]
		if item.result == nil || item.result.Approval == nil {
			continue
		}
		approval := item.result.Approval
		result.StopReason = StopReasonWaitingHuman
		result.FinalAnswer = "Agent is waiting for your input before continuing."
		result.Approval = &Approval{
			ToolCallID: item.call.ID, ToolName: item.call.Name, RiskLevel: toolruntime.RiskLow,
			Reason: approval.Reason, Kind: approval.Kind, Title: approval.Title, IsBlocking: approval.IsBlocking,
			Options: append([]toolruntime.ApprovalOption(nil), approval.Options...), Questions: append([]toolruntime.UserInputQuestion(nil), approval.Questions...),
		}
		pending := item.call
		result.Checkpoint = checkpointFromMessages(req, messages, contextTrace, toolNames, &pending, StopReasonWaitingHuman, result.Iterations, result.ToolCalls)
		result.Checkpoint.Metadata["interaction_kind"] = approval.Kind
		result.Checkpoint.Interaction = &Interaction{ID: fmt.Sprintf("%s:%s", approval.Kind, item.call.ID), Kind: approval.Kind, Title: approval.Title, Reason: approval.Reason, IsBlocking: approval.IsBlocking, Options: append([]toolruntime.ApprovalOption(nil), approval.Options...), Questions: append([]toolruntime.UserInputQuestion(nil), approval.Questions...), ToolCallID: item.call.ID}
		approvalStep := r.appendStep(result, RunStep{Type: StepTypeApproval, ToolCallID: item.call.ID, ToolName: item.call.Name, Content: approval.Reason, ProviderID: r.ProviderID, Model: r.ModelName})
		_ = r.emit(ctx, approvalStep)
		finalStep := r.appendStep(result, RunStep{Type: StepTypeFinalAnswer, Role: conversation.RoleAssistant, Content: result.FinalAnswer, ProviderID: r.ProviderID, Model: r.ModelName})
		_ = r.emit(ctx, finalStep)
		return true, messages
	}
	result.ToolCalls += len(prepared)
	for i := range prepared {
		item := &prepared[i]
		if item.result == nil {
			item.result = &toolruntime.ToolResult{}
		}
		content := toolResultContent(item.result, item.err)
		post := toolHooks.AfterToolUse(ctx, hooks.PostToolUseRequest{
			ToolName:   item.call.Name,
			Content:    content,
			OutputJSON: item.result.ContentJSON,
			Metadata:   item.metadata,
			Policy:     req.ToolPolicy,
		})
		result.HookTrace = appendHookTrace(result.HookTrace, item.call.Name, post.Traces)
		messages = append(messages, toolMessage(item.call.ID, post.Content))
		step := r.appendStep(result, r.newToolResultStep(item, post))
		_ = r.emit(ctx, step)
	}
	return false, messages
}

// newToolResultStep builds the tool_result step for one executed batch item.
// The structured error code is lifted from the result metadata where the batch
// executor records it (call issues, cancellation); plain execution errors keep
// their text in Error and leave the code empty.
func (r *Runner) newToolResultStep(item *preparedToolCall, post hooks.PostToolUseResult) RunStep {
	step := RunStep{
		Type:       StepTypeToolResult,
		ToolCallID: item.call.ID,
		ToolName:   item.call.Name,
		Content:    post.Content,
		OutputJSON: post.OutputJSON,
		Compressed: post.Compressed,
		IsError:    item.result.IsError,
		ErrorCode:  toolResultErrorCode(item.result),
		LatencyMS:  item.latencyMS,
		ProviderID: r.ProviderID,
		Model:      r.ModelName,
	}
	if item.err != nil {
		step.Error = item.err.Error()
	}
	return step
}

// toolResultErrorCode extracts the structured error code the batch executor
// stores in the result metadata. Success results and plain execution errors
// carry no code.
func toolResultErrorCode(result *toolruntime.ToolResult) string {
	if result == nil {
		return ""
	}
	code, _ := result.Metadata["error_code"].(string)
	return code
}

// logToolStarted emits the bounded, metadata-only tool.started diagnostic.
// It never includes tool arguments.
func (r *Runner) logToolStarted(ctx context.Context, item *preparedToolCall) {
	r.diagnosticsLogger().Log(ctx, slog.LevelInfo, "tool.started",
		"event", "tool.started", "phase", "tool", "result", "ok", "latency_ms", 0,
		"tool_name", item.call.Name, "tool_call_id", item.call.ID, "step_index", item.stepIndex)
}

// logToolCompleted emits the bounded, metadata-only tool.completed diagnostic.
// It classifies from the callback-scope toolResult/toolErr: item.result is
// only assigned after ExecuteToolBatch returns, so it is unusable here. On
// failure it reports the error TYPE (or the structured error_code for
// IsError results without a Go error); tool output never enters diagnostics
// and the original result/error still flow to the caller unchanged.
func (r *Runner) logToolCompleted(ctx context.Context, item *preparedToolCall, toolResult *toolruntime.ToolResult, toolErr error) {
	resultValue := "ok"
	errorClass := ""
	switch {
	case toolErr != nil:
		resultValue, errorClass = "error", logger.ErrorClass(toolErr)
	case toolResult != nil && toolResult.IsError:
		resultValue, errorClass = "error", toolResultErrorCode(toolResult)
	}
	level := slog.LevelInfo
	if resultValue == "error" {
		level = slog.LevelWarn
	}
	attrs := []any{"event", "tool.completed", "phase", "tool", "result", resultValue,
		"tool_name", item.call.Name, "tool_call_id", item.call.ID, "step_index", item.stepIndex,
		"latency_ms", item.latencyMS}
	if errorClass != "" {
		attrs = append(attrs, "error_class", errorClass)
	}
	r.diagnosticsLogger().Log(ctx, level, "tool.completed", attrs...)
}

func checkpointFromMessages(
	req RunRequest,
	messages []llm.ChatMessage,
	contextTrace ContextTrace,
	toolNames []string,
	pending *llm.ToolCall,
	stopReason string,
	iteration int,
	toolCalls int,
) *Checkpoint {
	return &Checkpoint{
		SnapshotVersion:  2,
		Messages:         append([]llm.ChatMessage(nil), messages...),
		MessagesSummary:  summarizeMessages(messages),
		PendingToolCall:  pending,
		Context:          contextTrace,
		ToolPolicy:       req.ToolPolicy,
		ToolNames:        append([]string(nil), toolNames...),
		ReflectionPolicy: req.ReflectionPolicy,
		Metadata: map[string]any{
			"run_id":           req.RunID,
			"agent_id":         req.AgentID,
			"delegation_depth": req.DelegationDepth,
			"stop_reason":      stopReason,
			"iteration":        iteration,
			"tool_calls":       toolCalls,
			"rule_hash":        req.RuleHash,
		},
		RuleHash: req.RuleHash,
		Rules:    append([]rules.Rule(nil), req.Rules...),
	}
}

func hydrateCheckpoint(checkpoint *Checkpoint, baseMessages, transcript []llm.ChatMessage, steps []RunStep, messages []llm.ChatMessage, persistedCount, transcriptCursor int) {
	if checkpoint == nil {
		return
	}
	checkpoint.SnapshotVersion = 2
	checkpoint.PersistedMessageCount = persistedCount
	checkpoint.TranscriptCursor = transcriptCursor
	checkpoint.BaseMessages = append([]llm.ChatMessage(nil), baseMessages...)
	checkpoint.Transcript = append([]llm.ChatMessage(nil), transcript...)
	checkpoint.Steps = append([]RunStep(nil), steps...)
	checkpoint.Messages = append([]llm.ChatMessage(nil), messages...)
	checkpoint.MessagesSummary = summarizeMessages(messages)
}

// persistTranscriptEntries feeds the given transcript slice to the message
// sink and returns the new persisted-entry cursor plus the first written row
// ID (0 when nothing was written). Sink failures degrade to the pre-sink
// behavior: the cursor still advances (entries stay visible within the run)
// and the error is surfaced once as an error step.
func (r *Runner) persistTranscriptEntries(ctx context.Context, result *RunResult, req RunRequest, entries []llm.ChatMessage, persistedCount, transcriptStart int) (int, int64) {
	if len(entries) == 0 {
		return persistedCount, 0
	}
	if req.MessageSink == nil {
		return persistedCount + len(entries), 0
	}
	persisted := compaction.FromChatAt(sanitizePersistedEntries(entries), transcriptStart)
	EnrichTranscriptEntries(persisted, result.Steps)
	firstID, err := req.MessageSink.PersistEntries(ctx, persisted)
	if err != nil {
		step := r.appendStep(result, RunStep{
			Type:       StepTypeError,
			Error:      fmt.Sprintf("persist transcript entries: %v", err),
			ProviderID: r.ProviderID,
			Model:      r.ModelName,
		})
		_ = r.emit(ctx, step)
	}
	return persistedCount + len(entries), firstID
}

// EnrichTranscriptEntries fills tool error state on function_call_output
// entries by exact ToolCallID lookup against the run's tool_result steps.
// Entries without a matching step keep zero error fields so their persisted
// rows stay byte-compatible with legacy rows; readers treat a missing is_error
// key as unknown. Replay determinism holds because resumed runs restore the
// same steps through the checkpoint before their transcript is re-persisted.
func EnrichTranscriptEntries(entries []compaction.Entry, steps []RunStep) {
	byCallID := make(map[string]RunStep)
	for _, step := range steps {
		if step.Type != StepTypeToolResult || step.ToolCallID == "" {
			continue
		}
		byCallID[step.ToolCallID] = step
	}
	for i := range entries {
		entry := &entries[i]
		if entry.ContentType != conversation.ContentTypeFunctionCallOutput || entry.ToolCallID == "" {
			continue
		}
		step, ok := byCallID[entry.ToolCallID]
		if !ok {
			continue
		}
		isError := step.IsError
		entry.IsError = &isError
		entry.ErrorCode = step.ErrorCode
	}
}

// sanitizePersistedEntries copies entries and strips citation block lines from
// assistant text content. Persisted transcript rows are user-visible display
// artifacts, so citation markup must never reach them, regardless of which
// call site (final answer, tool-batch assistant message, resume replay) is
// persisting. The in-memory transcript and messages keep the raw content so
// model context and finalization usage accounting stay intact.
func sanitizePersistedEntries(entries []llm.ChatMessage) []llm.ChatMessage {
	sanitized := append([]llm.ChatMessage(nil), entries...)
	for i := range sanitized {
		if sanitized[i].Role == conversation.RoleAssistant && sanitized[i].Content != "" {
			sanitized[i].Content = memory.StripCitations(sanitized[i].Content)
		}
	}
	return sanitized
}

func toolMessage(toolCallID, content string) llm.ChatMessage {
	return llm.ChatMessage{
		Role:       conversation.RoleTool,
		ToolCallID: toolCallID,
		Content:    content,
	}
}

func addUsage(left, right llm.Usage) llm.Usage {
	return llm.Usage{
		PromptTokens:      left.PromptTokens + right.PromptTokens,
		CompletionTokens:  left.CompletionTokens + right.CompletionTokens,
		TotalTokens:       left.TotalTokens + right.TotalTokens,
		CachedInputTokens: left.CachedInputTokens + right.CachedInputTokens,
		ReasoningTokens:   left.ReasoningTokens + right.ReasoningTokens,
	}
}

func toolResultContent(result *toolruntime.ToolResult, toolErr error) string {
	if result == nil {
		result = &toolruntime.ToolResult{}
	}
	content := result.ContentText
	if content == "" && len(result.ContentJSON) > 0 {
		content = string(result.ContentJSON)
	}
	if toolErr != nil && content == "" {
		content = toolErr.Error()
	}
	return content
}

func toolExecutionContext(ctx context.Context, metadata toolruntime.ToolMetadata, policy ToolPolicy) (context.Context, context.CancelFunc, error) {
	if err := toolruntime.ValidateAllowedHosts(metadata.AllowedHosts, policy.AllowedHosts, false); err != nil {
		return ctx, nil, err
	}
	timeoutMS := toolruntime.EffectiveLimit(metadata.TimeoutMS, policy.MaxToolTimeoutMS)
	if timeoutMS <= 0 {
		return ctx, nil, nil
	}
	execCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutMS)*time.Millisecond)
	return execCtx, cancel, nil
}

func summarizeMessages(messages []llm.ChatMessage) string {
	if len(messages) == 0 {
		return ""
	}
	parts := make([]string, 0, len(messages))
	for _, message := range messages {
		content := strings.TrimSpace(message.Content)
		if len(content) > 160 {
			content = content[:160] + "..."
		}
		if content == "" && len(message.ToolCalls) > 0 {
			content = fmt.Sprintf("%d tool call(s)", len(message.ToolCalls))
		}
		parts = append(parts, strings.TrimSpace(message.Role+": "+content))
	}
	summary := strings.Join(parts, "\n")
	if len(summary) > 2048 {
		return summary[:2048] + "..."
	}
	return summary
}

func (r *Runner) appendStep(result *RunResult, step RunStep) RunStep {
	step.Index = len(result.Steps) + 1
	step.CreatedAt = r.now()
	if len(step.ArgumentsJSON) == 0 {
		step.ArgumentsJSON = nil
	}
	if len(step.OutputJSON) == 0 {
		step.OutputJSON = nil
	}
	result.Steps = append(result.Steps, step)
	return step
}

func appendHookTrace(existing []HookTrace, toolName string, traces []hooks.Trace) []HookTrace {
	for _, trace := range traces {
		if trace.Hook == "" && trace.Decision == "" {
			continue
		}
		existing = append(existing, HookTrace{
			Hook:     trace.Hook,
			Action:   trace.Stage + ":" + trace.Decision,
			Reason:   trace.Reason,
			ToolName: toolName,
			Metadata: map[string]any{"compressed": trace.Compressed},
		})
	}
	return existing
}

func (r *Runner) emit(ctx context.Context, step RunStep) error {
	if r.OnStep == nil {
		return nil
	}
	return r.OnStep(ctx, step)
}

func (r *Runner) now() time.Time {
	if r.Now != nil {
		return r.Now().UTC()
	}
	return time.Now().UTC()
}

func finishWithContext(result *RunResult, err error, now time.Time) *RunResult {
	switch {
	case errors.Is(err, context.Canceled):
		result.StopReason = StopReasonCancelled
	case errors.Is(err, context.DeadlineExceeded):
		result.StopReason = StopReasonTimeout
	default:
		result.StopReason = err.Error()
	}
	result.FinalAnswer = fmt.Sprintf("Agent stopped: %s", result.StopReason)
	return finish(result, now)
}

func finish(result *RunResult, now time.Time) *RunResult {
	result.FinishedAt = now
	result.LatencyMS = int(result.FinishedAt.Sub(result.StartedAt).Milliseconds())
	return result
}

func CompactSteps(steps []RunStep, maxContentBytes int) []RunStep {
	if maxContentBytes <= 0 {
		return steps
	}
	out := make([]RunStep, len(steps))
	copy(out, steps)
	for i := range out {
		var contentCompressed bool
		var jsonCompressed bool
		out[i].Content, contentCompressed = strutil.TruncateWithSuffixFlag(out[i].Content, maxContentBytes, "...[truncated]")
		out[i].OutputJSON, jsonCompressed = strutil.TruncateRawJSONWithSuffix(out[i].OutputJSON, maxContentBytes, "...[truncated]")
		out[i].Compressed = out[i].Compressed || contentCompressed || jsonCompressed
	}
	return out
}

func compactString(value string, maxBytes int) string {
	return strutil.TruncateWithSuffix(value, maxBytes, "...[truncated]")
}

func findUnresolvedToolCalls(messages []llm.ChatMessage, toolByName map[string]toolruntime.RuntimeTool) []llm.ToolCall {
	toolResultIDs := make(map[string]bool)
	for _, m := range messages {
		if m.Role == conversation.RoleTool && m.ToolCallID != "" {
			toolResultIDs[m.ToolCallID] = true
		}
	}
	var unresolved []llm.ToolCall
	for i := len(messages) - 1; i >= 0; i-- {
		m := messages[i]
		if m.Role == conversation.RoleAssistant && len(m.ToolCalls) > 0 {
			for _, call := range m.ToolCalls {
				if !toolResultIDs[call.ID] {
					if _, ok := toolByName[call.Name]; ok {
						unresolved = append(unresolved, call)
					}
				}
			}
			break
		}
	}
	return unresolved
}
