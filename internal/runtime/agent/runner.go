package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"agentcanvas/internal/domain/conversation"
	"agentcanvas/internal/infrastructure/llm"
	"agentcanvas/internal/observability"
	"agentcanvas/internal/pkg/strutil"
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
	LLM        llm.ToolCallingClient
	OnStep     StepEmitter
	Now        func() time.Time
	ProviderID int64
	ModelName  string
}

func NewRunner(client llm.ToolCallingClient) *Runner {
	return &Runner{LLM: client}
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
	started := r.now()
	result := &RunResult{
		StartedAt: started,
		Steps:     append([]RunStep(nil), req.ResumeSteps...),
		Reflection: ReflectionTrace{
			RecalledIDs: append([]int64(nil), req.RecalledReflectionIDs...),
		},
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
		compactedBlocks, compactionUsage, initialCompaction := r.compactInitialHistory(ctx, req, tools)
		req.ContextBlocks = compactedBlocks
		result.Usage = addUsage(result.Usage, compactionUsage)
		baseMessages, contextTrace = ContextAssembler{}.Build(req)
		counter := modelTokenCount(req, "token counter probe")
		contextTrace.TokenCounterMethod = counter.Method
		contextTrace.TokenCounterError = counter.Error
		contextTrace.AutoCompactTokenLimit = autoCompactLimit(req)
		contextTrace.AutoCompactLimitScope = autoCompactScope(req)
		if initialCompaction != nil {
			observability.ContextSystemMetrics.RecordCompaction(initialCompaction.Status)
			contextTrace.Compactions = append(contextTrace.Compactions, *initialCompaction)
			contextTrace.SavedTokens += initialCompaction.SavedTokens
		}
		contextTrace.RuleHash = req.RuleHash
		if contextTrace.CoreOverflow {
			observability.RuleSystemMetrics.RecordMandatoryOverflow()
			return nil, fmt.Errorf("%w: mandatory_tokens=%d budget_tokens=%d deficit_tokens=%d",
				ErrMandatoryRuleBudgetExceeded, contextTrace.MandatoryTokens, contextTrace.MandatoryBudgetTokens, contextTrace.MandatoryDeficitTokens)
		}
	}
	messages := baseMessages
	transcript := make([]llm.ChatMessage, 0, req.MaxIterations*2)
	previousRuleIDs := make([]string, 0)
	// Plans guide the model through context; they do not drive a step scheduler.
	// // ***
	if req.Plan != nil && len(req.Plan.Steps) > 0 {
		plan := clonePlan(req.Plan)
		if plan.ExecutionState == "" {
			plan.ExecutionState = "active"
		}
		result.Plan = &plan
		if len(req.ResumeMessages) == 0 {
			planJSON, _ := json.Marshal(plan)
			planStep := r.appendStep(result,
				RunStep{
					Type:       StepTypePlan,
					Content:    plan.PlanContext(),
					OutputJSON: planJSON,
					ProviderID: r.ProviderID,
					Model:      r.ModelName,
				},
			)
			_ = r.emit(ctx, planStep)
		}
	}
	// // ***
	result.Context = contextTrace
	if len(req.ResumeMessages) == 0 && len(req.RecalledReflectionIDs) > 0 {
		recalledJSON, _ := json.Marshal(req.RecalledReflectionIDs)
		step := r.appendStep(result, RunStep{
			Type: StepTypeReflectionRecall,
			Content: fmt.Sprintf("Recalled %d prior reflection(s).",
				len(req.RecalledReflectionIDs)),
			OutputJSON: recalledJSON,
			ProviderID: r.ProviderID,
			Model:      r.ModelName,
		})
		_ = r.emit(ctx, step)
	}
	startIteration := 0
	startToolCalls := 0
	if len(req.ResumeMessages) > 0 {
		transcript = append([]llm.ChatMessage(nil), req.ResumeTranscript...)
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
			stop, updatedMessages := r.executeToolBatch(ctx, req, result, messages, unresolved, toolByName, toolHooks, contextTrace, toolNames, req.ResumeApprovedToolCallIDs)
			messages = updatedMessages
			if len(updatedMessages) > messageCountBeforeTools {
				transcript = append(transcript, updatedMessages[messageCountBeforeTools:]...)
			}
			if !stop {
				if feedback := r.maybeReflect(ctx, req, result, result.Steps[stepStart:]); feedback != nil {
					messages = append(messages, *feedback)
					transcript = append(transcript, *feedback)
				}
			}
			if stop {
				hydrateCheckpoint(result.Checkpoint, baseMessages, transcript, result.Steps, messages)
				return finish(result, r.now()), nil
			}
		}
	}
	// One run contains multiple LLM/tool iterations.
	for iteration := startIteration; iteration < req.MaxIterations; iteration++ {
		if err := ctx.Err(); err != nil {
			result = finishWithContext(result, err, r.now())
			if errors.Is(context.Cause(ctx), ErrRunPaused) {
				result.StopReason = StopReasonPaused
				result.Checkpoint = checkpointFromMessages(
					req, messages, contextTrace, toolNames, nil,
					result.StopReason, result.Iterations, result.ToolCalls, result.Plan)
				hydrateCheckpoint(result.Checkpoint, baseMessages, transcript, result.Steps, messages)
			}
			return result, nil
		}
		{
			compactedRuntime, compactUsage, runtimeCompaction := r.compactRuntimeTranscript(ctx, req, baseMessages, transcript, tools)
			transcript = compactedRuntime
			result.Usage = addUsage(result.Usage, compactUsage)
			if runtimeCompaction != nil {
				observability.ContextSystemMetrics.RecordCompaction(runtimeCompaction.Status)
				contextTrace.Compactions = append(contextTrace.Compactions, *runtimeCompaction)
				contextTrace.SavedTokens += runtimeCompaction.SavedTokens
				contextTrace.Compressed = appendUniqueStrings(contextTrace.Compressed, []string{"runtime_model_compaction"})
			}
			compactedTranscript, savedTokens := compactTranscriptForBudget(transcript, transcriptBudget(RulePlanningState{
				MaxInputTokens: req.MaxInputTokens,
				ContextWindow:  req.ContextWindowTokens,
				ReservedOutput: req.ReservedOutputTokens,
				SafetyMargin:   req.ContextSafetyMarginTokens,
				BaseMessages:   baseMessages,
				AvailableTools: tools,
			}))
			if savedTokens > 0 {
				transcript = compactedTranscript
				contextTrace.SavedTokens += savedTokens
				contextTrace.Compressed = appendUniqueStrings(contextTrace.Compressed, []string{"runtime_transcript"})
			}
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
		estimatedPromptTokens := modelMessagesTokens(req, messages) + modelToolSchemaTokens(req, tools)
		allowedPromptTokens := hardPromptTokenLimit(req)
		if estimatedPromptTokens > allowedPromptTokens {
			observability.ContextSystemMetrics.RecordContextOverflow()
			contextTrace.EstimatedPromptTokens += estimatedPromptTokens
			result.Context = contextTrace
			result.StopReason = StopReasonContextOverflow
			overflowErr := fmt.Errorf("%w: estimated_prompt_tokens=%d allowed_prompt_tokens=%d", ErrContextOverflow, estimatedPromptTokens, allowedPromptTokens)
			step := r.appendStep(result, RunStep{Type: StepTypeError, Error: overflowErr.Error(), ProviderID: r.ProviderID, Model: r.ModelName})
			_ = r.emit(ctx, step)
			return finish(result, r.now()), overflowErr
		}
		//// ***

		llmStarted := r.now()
		resp, err := r.LLM.ChatWithTools(ctx, req.Provider, llm.ToolChatRequest{
			Model:       req.Model,
			Messages:    messages,
			Tools:       tools,
			ToolChoice:  "auto",
			Temperature: req.Temperature,
		})
		latencyMS := int(r.now().Sub(llmStarted).Milliseconds())
		if err != nil {
			if errors.Is(err, context.Canceled) {
				result = finishWithContext(result, err, r.now())
				if errors.Is(context.Cause(ctx), ErrRunPaused) {
					result.StopReason = StopReasonPaused
					result.Checkpoint = checkpointFromMessages(
						req, messages, contextTrace, toolNames, nil,
						result.StopReason, result.Iterations, result.ToolCalls, result.Plan,
					)
					hydrateCheckpoint(result.Checkpoint, baseMessages, transcript, result.Steps, messages)
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
		if len(assistant.ToolCalls) == 0 {
			// A final model response ends a guided plan without claiming verification.
			result.FinalAnswer = assistant.Content
			result.StopReason = StopReasonFinalAnswer
			if result.Plan != nil {
				result.Plan.EndUnverified()
			}
			finalStep := r.appendStep(result,
				RunStep{
					Type:       StepTypeFinalAnswer,
					Role:       conversation.RoleAssistant,
					Content:    assistant.Content,
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
		stop, updatedMessages := r.executeToolBatch(ctx, req, result, messages, assistant.ToolCalls, toolByName, toolHooks, contextTrace, toolNames, nil)
		messages = updatedMessages
		transcript = append(transcript, assistant)
		if len(updatedMessages) > messageCountBeforeTools {
			transcript = append(transcript, updatedMessages[messageCountBeforeTools:]...)
		}
		if !stop {
			if feedback := r.maybeReflect(ctx, req, result, result.Steps[stepStart:]); feedback != nil {
				messages = append(messages, *feedback)
				transcript = append(transcript, *feedback)
			}
		}
		if stop {
			hydrateCheckpoint(result.Checkpoint, baseMessages, transcript, result.Steps, messages)
			return finish(result, r.now()), nil
		}
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
	for _, message := range base {
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

// compactTranscriptForBudget preserves complete assistant/tool exchanges from
// newest to oldest. Tool messages remain paired with their tool-call message,
// which keeps the provider protocol valid after context pressure grows.
type transcriptExchange struct {
	messages []llm.ChatMessage
	tokens   int
}

func compactTranscriptForBudget(messages []llm.ChatMessage, budget int) ([]llm.ChatMessage, int) {
	original := estimateMessagesTokens(messages)
	if original <= budget {
		return messages, 0
	}
	if budget <= 0 {
		return nil, original
	}
	exchanges := splitTranscriptExchanges(messages)
	kept := make([]transcriptExchange, 0, len(exchanges))
	used := 0
	for index := len(exchanges) - 1; index >= 0; index-- {
		item := exchanges[index]
		if used+item.tokens <= budget {
			kept = append(kept, item)
			used += item.tokens
			continue
		}
		if len(kept) == 0 {
			item = truncateExchange(item, budget)
			if item.tokens > 0 {
				kept = append(kept, item)
				used += item.tokens
			}
		}
		break
	}
	result := make([]llm.ChatMessage, 0)
	for index := len(kept) - 1; index >= 0; index-- {
		result = append(result, kept[index].messages...)
	}
	saved := original - estimateMessagesTokens(result)
	if saved < 0 {
		saved = 0
	}
	return result, saved
}

func truncateExchange(item transcriptExchange, budget int) transcriptExchange {
	if budget <= 0 || len(item.messages) == 0 {
		return transcriptExchange{}
	}
	result := append([]llm.ChatMessage(nil), item.messages...)
	used := 0
	for index := range result {
		if result[index].Role != conversation.RoleAssistant {
			continue
		}
		used += estimateContextTokens(result[index].Content)
	}
	for index := range result {
		if result[index].Role != conversation.RoleTool {
			continue
		}
		remaining := budget - used
		if remaining <= 0 {
			result[index].Content = ""
			continue
		}
		content := truncateToEstimatedTokens(result[index].Content, remaining)
		result[index].Content = content
		used += estimateContextTokens(content)
	}
	return transcriptExchange{messages: result, tokens: estimateMessagesTokens(result)}
}

func truncateToEstimatedTokens(content string, maxTokens int) string {
	if maxTokens <= 0 || estimateContextTokens(content) <= maxTokens {
		return content
	}
	runes := []rune(content)
	low, high := 0, len(runes)
	for low < high {
		mid := (low + high + 1) / 2
		if estimateContextTokens(string(runes[:mid])) <= maxTokens {
			low = mid
		} else {
			high = mid - 1
		}
	}
	return string(runes[:low])
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
	preTraces  []hooks.Trace
	result     *toolruntime.ToolResult
	err        error
	latencyMS  int
}

func (r *Runner) executeToolBatch(
	ctx context.Context,
	req RunRequest,
	result *RunResult,
	messages []llm.ChatMessage,
	calls []llm.ToolCall,
	toolByName map[string]toolruntime.RuntimeTool,
	toolHooks hooks.ToolHookChain,
	contextTrace ContextTrace,
	toolNames []string,
	approvedToolCallIDs []string,
) (bool, []llm.ChatMessage) {
	prepared := make([]preparedToolCall, 0, len(calls))
	for _, call := range calls {
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
		_ = r.emit(ctx, toolStep)
		toolImpl, ok := toolByName[call.Name]
		if !ok {
			result.StopReason = StopReasonToolNameNotFound
			errMessage := fmt.Sprintf("tool %s is not available", call.Name)
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
				toolNames, &pending, StopReasonWaitingHuman, result.Iterations, result.ToolCalls, result.Plan)
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
		})
	}

	allDelegation := len(prepared) > 1
	for i := range prepared {
		allDelegation = allDelegation && prepared[i].metadata.ExecutionClass == toolruntime.ExecutionDelegation
	}
	execute := func(item *preparedToolCall) {
		started := r.now()
		item.result, item.err = item.tool.Execute(item.execCtx, toolruntime.ToolRunContext{
			OwnerID:         req.OwnerID,
			AgentID:         req.AgentID,
			AgentReleaseID:  req.AgentReleaseID,
			RunID:           req.RunID,
			DelegationDepth: req.DelegationDepth,
			ConversationID:  req.ConversationID,
		}, item.call.Arguments)
		if item.execCancel != nil {
			item.execCancel()
		}
		item.latencyMS = int(r.now().Sub(started).Milliseconds())
		if item.result == nil {
			item.result = &toolruntime.ToolResult{}
		}
		if item.err != nil {
			item.result.IsError = true
		}
	}
	if allDelegation {
		sem := make(chan struct{}, req.MaxParallelTools)
		var wg sync.WaitGroup
		for i := range prepared {
			wg.Add(1)
			go func(item *preparedToolCall) {
				defer wg.Done()
				select {
				case sem <- struct{}{}:
					defer func() { <-sem }()
					execute(item)
				case <-ctx.Done():
					item.result = &toolruntime.ToolResult{ContentText: ctx.Err().Error(), IsError: true}
					item.err = ctx.Err()
				}
			}(&prepared[i])
		}
		wg.Wait()
	} else {
		for i := range prepared {
			execute(&prepared[i])
		}
	}
	result.ToolCalls += len(prepared)
	for i := range prepared {
		item := &prepared[i]
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
		step := r.appendStep(result, RunStep{
			Type:       StepTypeToolResult,
			ToolCallID: item.call.ID,
			ToolName:   item.call.Name,
			Content:    post.Content,
			OutputJSON: post.OutputJSON,
			Compressed: post.Compressed,
			IsError:    item.result.IsError,
			LatencyMS:  item.latencyMS,
			ProviderID: r.ProviderID,
			Model:      r.ModelName,
		})
		if item.err != nil {
			step.Error = item.err.Error()
		}
		_ = r.emit(ctx, step)
	}
	return false, messages
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
	plan *Plan,
) *Checkpoint {
	var checkpointPlan *Plan
	if plan != nil {
		// Snapshot the plan to preserve resume semantics.
		cloned := clonePlan(plan)
		checkpointPlan = &cloned
	}
	return &Checkpoint{
		SnapshotVersion:       2,
		Messages:              append([]llm.ChatMessage(nil), messages...),
		MessagesSummary:       summarizeMessages(messages),
		PendingToolCall:       pending,
		Context:               contextTrace,
		ToolPolicy:            req.ToolPolicy,
		ToolNames:             append([]string(nil), toolNames...),
		Plan:                  checkpointPlan,
		ReflectionPolicy:      req.ReflectionPolicy,
		RecalledReflectionIDs: append([]int64(nil), req.RecalledReflectionIDs...),
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

func hydrateCheckpoint(checkpoint *Checkpoint, baseMessages, transcript []llm.ChatMessage, steps []RunStep, messages []llm.ChatMessage) {
	if checkpoint == nil {
		return
	}
	checkpoint.SnapshotVersion = 2
	checkpoint.BaseMessages = append([]llm.ChatMessage(nil), baseMessages...)
	checkpoint.Transcript = append([]llm.ChatMessage(nil), transcript...)
	checkpoint.Steps = append([]RunStep(nil), steps...)
	checkpoint.Messages = append([]llm.ChatMessage(nil), messages...)
	checkpoint.MessagesSummary = summarizeMessages(messages)
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
		PromptTokens:     left.PromptTokens + right.PromptTokens,
		CompletionTokens: left.CompletionTokens + right.CompletionTokens,
		TotalTokens:      left.TotalTokens + right.TotalTokens,
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
	if err := validateAllowedHosts(metadata.AllowedHosts, policy.AllowedHosts); err != nil {
		return ctx, nil, err
	}
	timeoutMS := effectiveTimeoutMS(metadata, policy)
	if timeoutMS <= 0 {
		return ctx, nil, nil
	}
	execCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutMS)*time.Millisecond)
	return execCtx, cancel, nil
}

func effectiveTimeoutMS(metadata toolruntime.ToolMetadata, policy ToolPolicy) int {
	timeoutMS := metadata.TimeoutMS
	if policy.MaxToolTimeoutMS > 0 && (timeoutMS <= 0 || policy.MaxToolTimeoutMS < timeoutMS) {
		timeoutMS = policy.MaxToolTimeoutMS
	}
	return timeoutMS
}

func validateAllowedHosts(toolHosts []string, policyHosts []string) error {
	allowed := normalizedSet(policyHosts)
	if len(allowed) == 0 || len(toolHosts) == 0 {
		return nil
	}
	for _, host := range toolHosts {
		normalized := normalizeHost(host)
		if normalized == "" {
			continue
		}
		if !slices.Contains(allowed, normalized) {
			return fmt.Errorf("tool host %s is not allowed by policy", normalized)
		}
	}
	return nil
}

func normalizedSet(hosts []string) []string {
	out := make([]string, 0, len(hosts))
	for _, host := range hosts {
		normalized := normalizeHost(host)
		if normalized == "" || slices.Contains(out, normalized) {
			continue
		}
		out = append(out, normalized)
	}
	return out
}

func normalizeHost(host string) string {
	host = strings.TrimSpace(strings.ToLower(host))
	host = strings.TrimPrefix(host, "http://")
	host = strings.TrimPrefix(host, "https://")
	if idx := strings.IndexByte(host, '/'); idx >= 0 {
		host = host[:idx]
	}
	if idx := strings.LastIndexByte(host, ':'); idx > 0 {
		host = host[:idx]
	}
	return host
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

func clonePlan(plan *Plan) Plan {
	if plan == nil {
		return Plan{}
	}
	cloned := Plan{Finished: plan.Finished, Version: plan.Version, RevisionReason: plan.RevisionReason}
	if len(plan.Steps) > 0 {
		cloned.Steps = append([]PlanStep(nil), plan.Steps...)
	}
	return cloned
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
		out[i].Content, contentCompressed = compactStringWithFlag(out[i].Content, maxContentBytes)
		out[i].OutputJSON, jsonCompressed = compactRawJSONWithFlag(out[i].OutputJSON, maxContentBytes)
		out[i].Compressed = out[i].Compressed || contentCompressed || jsonCompressed
	}
	return out
}

func compactString(value string, maxBytes int) string {
	return strutil.TruncateWithSuffix(value, maxBytes, "...[truncated]")
}

func compactStringWithFlag(value string, maxBytes int) (string, bool) {
	return strutil.TruncateWithSuffixFlag(value, maxBytes, "...[truncated]")
}

func compactRawJSONWithFlag(raw json.RawMessage, maxBytes int) (json.RawMessage, bool) {
	return strutil.TruncateRawJSONWithSuffix(raw, maxBytes, "...[truncated]")
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
