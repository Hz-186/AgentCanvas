package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"agentcanvas/internal/domain/conversation"
	"agentcanvas/internal/infrastructure/llm"
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
		result.Context = ContextTrace{MaxChars: defaultMaxInputChars, Strategy: "resumed_from_checkpoint"}
		unresolved := findUnresolvedToolCalls(messages, toolByName)
		for _, call := range unresolved {
			toolImpl := toolByName[call.Name]
			if result.ToolCalls >= req.MaxToolCalls {
				result.StopReason = StopReasonMaxToolCalls
				result.FinalAnswer = "Agent stopped because max_tool_calls was exceeded after resume."
				finalStep := r.appendStep(result, RunStep{Type: StepTypeFinalAnswer, Role: conversation.RoleAssistant, Content: result.FinalAnswer, ProviderID: r.ProviderID, Model: r.ModelName})
				_ = r.emit(ctx, finalStep)
				return finish(result, r.now()), nil
			}
			result.ToolCalls++
			toolStarted := r.now()
			toolResult, toolErr := toolImpl.Execute(ctx, toolruntime.ToolRunContext{
				OwnerID:           req.OwnerID,
				WorkflowID:        req.WorkflowID,
				RunID:             req.RunID,
				NodeID:            req.NodeID,
				CallDepth:         req.CallDepth,
				WorkflowCallChain: append([]int64(nil), req.WorkflowCallChain...),
				ConversationID:    req.ConversationID,
			}, call.Arguments)
			toolLatencyMS := int(r.now().Sub(toolStarted).Milliseconds())
			r.appendStep(result, RunStep{
				Type:          StepTypeToolCall,
				ToolCallID:    call.ID,
				ToolName:      call.Name,
				ArgumentsJSON: call.Arguments,
				ProviderID:    r.ProviderID,
				Model:         r.ModelName,
			})
			content := toolResult.ContentText
			if content == "" && len(toolResult.ContentJSON) > 0 {
				content = string(toolResult.ContentJSON)
			}
			if toolErr != nil {
				if content == "" {
					content = toolErr.Error()
				}
			}
			messages = append(messages, toolMessage(call.ID, content))
			r.appendStep(result, RunStep{
				Type:       StepTypeToolResult,
				ToolCallID: call.ID,
				ToolName:   call.Name,
				Content:    content,
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
				result.Checkpoint = checkpointFromMessages(req, messages, contextTrace, toolNames, nil, result.StopReason)
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
				result.Checkpoint = checkpointFromMessages(req, messages, contextTrace, toolNames, nil, result.StopReason)
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
			finalStep := r.appendStep(result, RunStep{Type: StepTypeFinalAnswer, Role: conversation.RoleAssistant, Content: assistant.Content, ProviderID: r.ProviderID, Model: r.ModelName})
			_ = r.emit(ctx, finalStep)
			return finish(result, r.now()), nil
		}
		messages = append(messages, assistant)
		for _, call := range assistant.ToolCalls {
			if result.ToolCalls >= req.MaxToolCalls {
				result.StopReason = StopReasonMaxToolCalls
				result.FinalAnswer = "Agent stopped because max_tool_calls was exceeded."
				finalStep := r.appendStep(result, RunStep{Type: StepTypeFinalAnswer, Role: conversation.RoleAssistant, Content: result.FinalAnswer, ProviderID: r.ProviderID, Model: r.ModelName})
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
			if approval := requiredApproval(call.ID, call.Name, toolruntime.MetadataOf(toolImpl), req.ToolPolicy); approval != nil {
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
			result.ToolCalls++
			toolStarted := r.now()
			toolResult, toolErr := toolImpl.Execute(ctx, toolruntime.ToolRunContext{
				OwnerID:           req.OwnerID,
				WorkflowID:        req.WorkflowID,
				RunID:             req.RunID,
				NodeID:            req.NodeID,
				CallDepth:         req.CallDepth,
				WorkflowCallChain: append([]int64(nil), req.WorkflowCallChain...),
				ConversationID:    req.ConversationID,
			}, call.Arguments)
			toolLatencyMS := int(r.now().Sub(toolStarted).Milliseconds())
			if toolResult == nil {
				toolResult = &toolruntime.ToolResult{}
			}
			content := toolResult.ContentText
			if content == "" && len(toolResult.ContentJSON) > 0 {
				content = string(toolResult.ContentJSON)
			}
			if toolErr != nil {
				toolResult.IsError = true
				if content == "" {
					content = toolErr.Error()
				}
			}
			messages = append(messages, toolMessage(call.ID, content))
			resultStep := r.appendStep(result, RunStep{
				Type:       StepTypeToolResult,
				ToolCallID: call.ID,
				ToolName:   call.Name,
				Content:    content,
				OutputJSON: toolResult.ContentJSON,
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
	finalStep2 := r.appendStep(result, RunStep{Type: StepTypeFinalAnswer, Role: conversation.RoleAssistant, Content: result.FinalAnswer, ProviderID: r.ProviderID, Model: r.ModelName})
	_ = r.emit(ctx, finalStep2)
	return finish(result, r.now()), nil
}

func checkpointFromMessages(req RunRequest, messages []llm.ChatMessage, contextTrace ContextTrace, toolNames []string, pending *llm.ToolCall, stopReason string) *Checkpoint {
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

func requiredApproval(toolCallID, toolName string, metadata toolruntime.ToolMetadata, policy ToolPolicy) *Approval {
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
	return &Approval{
		ToolCallID: toolCallID,
		ToolName:   toolName,
		RiskLevel:  risk,
		Reason:     fmt.Sprintf("tool %s requires human approval because risk level is %s", toolName, risk),
		Metadata:   metadata,
	}
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
		out[i].Content = compactString(out[i].Content, maxContentBytes)
		out[i].OutputJSON = compactRawJSON(out[i].OutputJSON, maxContentBytes)
	}
	return out
}

func compactString(value string, maxBytes int) string {
	if len(value) <= maxBytes {
		return value
	}
	return value[:maxBytes] + "...[truncated]"
}

func compactRawJSON(raw json.RawMessage, maxBytes int) json.RawMessage {
	if len(raw) <= maxBytes {
		return raw
	}
	return json.RawMessage(strconvQuote(compactString(string(raw), maxBytes)))
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
