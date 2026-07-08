package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"agentcanvas/internal/domain/conversation"
	"agentcanvas/internal/infrastructure/llm"
	"agentcanvas/internal/runtime/harness/hooks"
	"agentcanvas/internal/runtime/toolruntime"
)

var ErrNoToolCallingClient = errors.New("llm client does not support tool calling")

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
	result := &RunResult{StartedAt: started}
	if req.MaxIterations <= 0 {
		req.MaxIterations = 8
	}
	if req.MaxToolCalls <= 0 {
		req.MaxToolCalls = 16
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

	messages, contextTrace := ContextAssembler{}.Build(req)
	if req.Plan != nil && len(req.Plan.Steps) > 0 {
		plan := clonePlan(req.Plan)
		result.Plan = &plan
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
	result.Context = contextTrace
	startIteration := 0
	startToolCalls := 0
	if len(req.ResumeMessages) > 0 {
		messages = req.ResumeMessages
		startIteration = req.ResumeIteration
		startToolCalls = req.ResumeToolCalls
		result.Iterations = startIteration
		result.ToolCalls = startToolCalls
		if req.ResumeIteration > 0 {
			startIteration = req.ResumeIteration
		}
		result.Context = ContextTrace{
			MaxChars: defaultMaxInputChars,
			Strategy: "resumed_from_checkpoint",
		}
		unresolved := findUnresolvedToolCalls(messages, toolByName)
		for _, call := range unresolved {
			toolImpl := toolByName[call.Name]
			metadata := toolruntime.MetadataOf(toolImpl)
			if result.ToolCalls >= req.MaxToolCalls {
				result.StopReason = StopReasonMaxToolCalls
				result.FinalAnswer = "Agent stopped because max_tool_calls was exceeded after resume."
				finalStep := r.appendStep(result,
					RunStep{
						Type:       StepTypeFinalAnswer,
						Role:       conversation.RoleAssistant,
						Content:    result.FinalAnswer,
						ProviderID: r.ProviderID,
						Model:      r.ModelName,
					},
				)
				_ = r.emit(ctx, finalStep)
				return finish(result, r.now()), nil
			}
			execCtx, execCancel, policyErr := toolExecutionContext(ctx, metadata, req.ToolPolicy)
			if policyErr != nil {
				result.StopReason = StopReasonReflectionFailed
				messages = append(messages, toolMessage(call.ID, policyErr.Error()))
				resultStep := r.appendStep(result, RunStep{
					Type:       StepTypeToolResult,
					ToolCallID: call.ID,
					ToolName:   call.Name,
					Content:    policyErr.Error(),
					IsError:    true,
					Error:      policyErr.Error(),
					ProviderID: r.ProviderID,
					Model:      r.ModelName,
				})
				_ = r.emit(ctx, resultStep)
				continue
			}
			result.ToolCalls++
			toolStarted := r.now()
			toolResult, toolErr := toolImpl.Execute(execCtx, toolruntime.ToolRunContext{
				OwnerID:           req.OwnerID,
				WorkflowID:        req.WorkflowID,
				RunID:             req.RunID,
				NodeID:            req.NodeID,
				CallDepth:         req.CallDepth,
				WorkflowCallChain: append([]int64(nil), req.WorkflowCallChain...),
				ConversationID:    req.ConversationID,
			}, call.Arguments)
			if execCancel != nil {
				execCancel()
			}
			toolLatencyMS := int(r.now().Sub(toolStarted).Milliseconds())
			if toolResult == nil {
				toolResult = &toolruntime.ToolResult{}
			}
			r.appendStep(result, RunStep{
				Type:          StepTypeToolCall,
				ToolCallID:    call.ID,
				ToolName:      call.Name,
				ArgumentsJSON: call.Arguments,
				ProviderID:    r.ProviderID,
				Model:         r.ModelName,
			})
			content := toolResultContent(toolResult, toolErr)
			post := toolHooks.AfterToolUse(ctx,
				hooks.PostToolUseRequest{
					ToolName:   call.Name,
					Content:    content,
					OutputJSON: toolResult.ContentJSON,
					Metadata:   metadata,
					Policy:     req.ToolPolicy,
				},
			)
			result.HookTrace = appendHookTrace(result.HookTrace, call.Name, post.Traces)
			content, outputJSON, compressed := post.Content, post.OutputJSON, post.Compressed
			messages = append(messages, toolMessage(call.ID, content))
			r.appendStep(result, RunStep{
				Type:       StepTypeToolResult,
				ToolCallID: call.ID,
				ToolName:   call.Name,
				Content:    content,
				OutputJSON: outputJSON,
				Compressed: compressed,
				ProviderID: r.ProviderID,
				Model:      r.ModelName,
				LatencyMS:  toolLatencyMS,
			})
		}
	}
	for iteration := startIteration; iteration < req.MaxIterations; iteration++ {
		if err := ctx.Err(); err != nil {
			result = finishWithContext(result, err, r.now())
			if errors.Is(err, context.Canceled) {
				result.Checkpoint = checkpointFromMessages(
					req, messages, contextTrace, toolNames, nil,
					result.StopReason, result.Iterations, result.ToolCalls)
			}
			return result, nil
		}
		result.Iterations = iteration + 1
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
				result.Checkpoint = checkpointFromMessages(
					req, messages, contextTrace, toolNames, nil,
					result.StopReason, result.Iterations, result.ToolCalls,
				)
				return result, nil
			}
			result.StopReason = StopReasonLLMError
			step := r.appendStep(result, RunStep{Type: StepTypeError, Error: err.Error(), LatencyMS: latencyMS, ProviderID: r.ProviderID, Model: r.ModelName})
			_ = r.emit(ctx, step)
			return finish(result, r.now()), err
		}
		result.Usage = addUsage(result.Usage, resp.Usage)
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
			result.FinalAnswer = assistant.Content
			result.StopReason = StopReasonFinalAnswer
			if result.Plan != nil {
				result.Plan.Finish()
				result.StopReason = StopReasonPlanCompleted
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
		for _, call := range assistant.ToolCalls {
			if result.ToolCalls >= req.MaxToolCalls {
				result.StopReason = StopReasonMaxToolCalls
				result.FinalAnswer = "Agent stopped because max_tool_calls was exceeded."
				finalStep := r.appendStep(result,
					RunStep{
						Type:       StepTypeFinalAnswer,
						Role:       conversation.RoleAssistant,
						Content:    result.FinalAnswer,
						ProviderID: r.ProviderID,
						Model:      r.ModelName,
					},
				)
				_ = r.emit(ctx, finalStep)
				return finish(result, r.now()), nil
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
				resultStep := r.appendStep(result, RunStep{
					Type:       StepTypeToolResult,
					ToolCallID: call.ID,
					ToolName:   call.Name,
					Content:    errMessage,
					IsError:    true,
					ProviderID: r.ProviderID,
					Model:      r.ModelName,
				})
				_ = r.emit(ctx, resultStep)
				continue
			}
			metadata := toolruntime.MetadataOf(toolImpl)
			pre := toolHooks.BeforeToolUse(ctx,
				hooks.PreToolUseRequest{
					ToolCallID: call.ID,
					ToolName:   call.Name,
					Arguments:  call.Arguments,
					Metadata:   metadata,
					Policy:     req.ToolPolicy,
				},
			)
			result.HookTrace = appendHookTrace(result.HookTrace, call.Name, pre.Traces)
			if pre.Approval != nil {
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
				result.Checkpoint = &Checkpoint{
					Messages:        append([]llm.ChatMessage(nil), messages...),
					MessagesSummary: summarizeMessages(messages),
					PendingToolCall: &pending,
					Context:         contextTrace,
					ToolPolicy:      req.ToolPolicy,
					ToolNames:       append([]string(nil), toolNames...),
					Metadata: map[string]any{
						"run_id":              req.RunID,
						"workflow_id":         req.WorkflowID,
						"node_id":             req.NodeID,
						"call_depth":          req.CallDepth,
						"workflow_call_chain": append([]int64(nil), req.WorkflowCallChain...),
						"stop_reason":         StopReasonWaitingHuman,
						"iteration":           result.Iterations,
						"tool_calls":          result.ToolCalls,
					},
				}
				approvalStep := r.appendStep(result, RunStep{
					Type:       StepTypeApproval,
					ToolCallID: call.ID,
					ToolName:   call.Name,
					Content:    approval.Reason,
					ProviderID: r.ProviderID,
					Model:      r.ModelName,
				})
				_ = r.emit(ctx, approvalStep)
				finalStep := r.appendStep(result, RunStep{Type: StepTypeFinalAnswer, Role: conversation.RoleAssistant, Content: result.FinalAnswer, ProviderID: r.ProviderID, Model: r.ModelName})
				_ = r.emit(ctx, finalStep)
				return finish(result, r.now()), nil
			}
			if pre.Denied != nil {
				result.StopReason = StopReasonReflectionFailed
				messages = append(messages, toolMessage(call.ID, pre.Denied.Error()))
				resultStep := r.appendStep(result, RunStep{
					Type:       StepTypeToolResult,
					ToolCallID: call.ID,
					ToolName:   call.Name,
					Content:    pre.Denied.Error(),
					IsError:    true,
					Error:      pre.Denied.Error(),
					ProviderID: r.ProviderID,
					Model:      r.ModelName,
				})
				_ = r.emit(ctx, resultStep)
				continue
			}
			execCtx := pre.Context
			if execCtx == nil {
				execCtx = ctx
			}
			execCancel := pre.Cancel
			result.ToolCalls++
			toolStarted := r.now()
			toolResult, toolErr := toolImpl.Execute(execCtx, toolruntime.ToolRunContext{
				OwnerID:           req.OwnerID,
				WorkflowID:        req.WorkflowID,
				RunID:             req.RunID,
				NodeID:            req.NodeID,
				CallDepth:         req.CallDepth,
				WorkflowCallChain: append([]int64(nil), req.WorkflowCallChain...),
				ConversationID:    req.ConversationID,
			}, call.Arguments)
			if execCancel != nil {
				execCancel()
			}
			toolLatencyMS := int(r.now().Sub(toolStarted).Milliseconds())
			if toolResult == nil {
				toolResult = &toolruntime.ToolResult{}
			}
			content := toolResultContent(toolResult, toolErr)
			post := toolHooks.AfterToolUse(ctx, hooks.PostToolUseRequest{ToolName: call.Name, Content: content, OutputJSON: toolResult.ContentJSON, Metadata: metadata, Policy: req.ToolPolicy})
			result.HookTrace = appendHookTrace(result.HookTrace, call.Name, post.Traces)
			content, outputJSON, compressed := post.Content, post.OutputJSON, post.Compressed
			if toolErr != nil {
				toolResult.IsError = true
			}
			messages = append(messages, toolMessage(call.ID, content))
			resultStep := r.appendStep(result, RunStep{
				Type:       StepTypeToolResult,
				ToolCallID: call.ID,
				ToolName:   call.Name,
				Content:    content,
				OutputJSON: outputJSON,
				Compressed: compressed,
				IsError:    toolResult.IsError,
				LatencyMS:  toolLatencyMS,
				ProviderID: r.ProviderID,
				Model:      r.ModelName,
			})
			if toolErr != nil {
				resultStep.Error = toolErr.Error()
			}
			_ = r.emit(ctx, resultStep)
		}
	}
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

func checkpointFromMessages(req RunRequest, messages []llm.ChatMessage, contextTrace ContextTrace, toolNames []string, pending *llm.ToolCall, stopReason string, iteration int, toolCalls int) *Checkpoint {
	return &Checkpoint{
		Messages:        append([]llm.ChatMessage(nil), messages...),
		MessagesSummary: summarizeMessages(messages),
		PendingToolCall: pending,
		Context:         contextTrace,
		ToolPolicy:      req.ToolPolicy,
		ToolNames:       append([]string(nil), toolNames...),
		Metadata: map[string]any{
			"run_id":              req.RunID,
			"workflow_id":         req.WorkflowID,
			"node_id":             req.NodeID,
			"call_depth":          req.CallDepth,
			"workflow_call_chain": append([]int64(nil), req.WorkflowCallChain...),
			"stop_reason":         stopReason,
			"iteration":           iteration,
			"tool_calls":          toolCalls,
		},
	}
}

func toolMessage(toolCallID, content string) llm.ChatMessage {
	return llm.ChatMessage{Role: conversation.RoleTool, ToolCallID: toolCallID, Content: content}
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

func compactToolObservation(content string, raw json.RawMessage, maxBytes int) (string, json.RawMessage, bool) {
	redactedRaw := redactToolObservation(raw)
	if len(redactedRaw) > 0 && string(redactedRaw) != string(raw) && strings.TrimSpace(content) == strings.TrimSpace(string(raw)) {
		content = string(redactedRaw)
	}
	raw = redactedRaw
	if maxBytes <= 0 {
		return content, raw, false
	}
	compactContent, contentCompressed := compactStringWithFlag(content, maxBytes)
	compactJSON, jsonCompressed := compactRawJSONWithFlag(raw, maxBytes)
	return compactContent, compactJSON, contentCompressed || jsonCompressed
}

func redactToolObservation(raw json.RawMessage) json.RawMessage {
	return RedactSensitiveFields(raw, defaultSensitiveToolFields())
}

func defaultSensitiveToolFields() []string {
	return []string{"api_key", "apikey", "authorization", "access_token", "refresh_token", "token", "password", "secret"}
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

func effectiveMaxOutputBytes(metadata toolruntime.ToolMetadata, policy ToolPolicy) int {
	maxBytes := metadata.MaxOutputBytes
	if policy.MaxToolOutputBytes > 0 && (maxBytes <= 0 || policy.MaxToolOutputBytes < maxBytes) {
		maxBytes = policy.MaxToolOutputBytes
	}
	return maxBytes
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
		existing = append(existing, HookTrace{Hook: trace.Hook, Action: trace.Stage + ":" + trace.Decision, Reason: trace.Reason, ToolName: toolName, Metadata: map[string]any{"compressed": trace.Compressed}})
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

func requiredApproval(toolCallID, toolName string, arguments json.RawMessage, metadata toolruntime.ToolMetadata, policy ToolPolicy) *Approval {
	risk := strings.TrimSpace(metadata.RiskLevel)
	if risk == "" {
		risk = toolruntime.RiskLow
	}
	required := metadata.RequiresApproval
	for _, item := range policy.RequireApprovalForRisk {
		if strings.EqualFold(strings.TrimSpace(item), risk) {
			required = true
			break
		}
	}
	if !required {
		return nil
	}
	reason := fmt.Sprintf("tool %s requires human approval because risk level is %s", toolName, risk)
	if strings.EqualFold(toolName, "request_human_approval") {
		var args struct {
			Action string `json:"action"`
			Reason string `json:"reason"`
		}
		if err := json.Unmarshal(arguments, &args); err == nil {
			parts := make([]string, 0, 2)
			if strings.TrimSpace(args.Action) != "" {
				parts = append(parts, "action: "+strings.TrimSpace(args.Action))
			}
			if strings.TrimSpace(args.Reason) != "" {
				parts = append(parts, "reason: "+strings.TrimSpace(args.Reason))
			}
			if len(parts) > 0 {
				reason = strings.Join(parts, "; ")
			}
		}
	}
	return &Approval{
		ToolCallID: toolCallID,
		ToolName:   toolName,
		RiskLevel:  risk,
		Reason:     reason,
		Metadata:   metadata,
	}
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
	cloned := Plan{Finished: plan.Finished}
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
	compact, _ := compactStringWithFlag(value, maxBytes)
	return compact
}

func compactStringWithFlag(value string, maxBytes int) (string, bool) {
	if len(value) <= maxBytes {
		return value, false
	}
	return value[:maxBytes] + "...[truncated]", true
}

func compactRawJSON(raw json.RawMessage, maxBytes int) json.RawMessage {
	compact, _ := compactRawJSONWithFlag(raw, maxBytes)
	return compact
}

func compactRawJSONWithFlag(raw json.RawMessage, maxBytes int) (json.RawMessage, bool) {
	if len(raw) <= maxBytes {
		return raw, false
	}
	return json.RawMessage(strconvQuote(compactString(string(raw), maxBytes))), true
}

func strconvQuote(value string) string {
	data, _ := json.Marshal(value)
	return string(data)
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
