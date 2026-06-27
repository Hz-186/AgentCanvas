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
	LLM    llm.ToolCallingClient
	OnStep StepEmitter
	Now    func() time.Time
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
	for _, item := range req.Tools {
		if item == nil {
			continue
		}
		name := strings.TrimSpace(item.Name())
		if name == "" {
			continue
		}
		toolByName[name] = item
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

	messages := initialMessages(req.SystemPrompt, task)
	for iteration := 0; iteration < req.MaxIterations; iteration++ {
		if err := ctx.Err(); err != nil {
			return finishWithContext(result, err, r.now()), nil
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
			result.StopReason = StopReasonLLMError
			step := r.appendStep(result, RunStep{Type: StepTypeError, Error: err.Error(), LatencyMS: latencyMS})
			_ = r.emit(ctx, step)
			return finish(result, r.now()), err
		}
		result.Usage = addUsage(result.Usage, resp.Usage)
		assistant := resp.Message
		if assistant.Role == "" {
			assistant.Role = conversation.RoleAssistant
		}
		step := r.appendStep(result, RunStep{
			Type:      StepTypeLLMResponse,
			Role:      assistant.Role,
			Content:   assistant.Content,
			LatencyMS: latencyMS,
		})
		_ = r.emit(ctx, step)
		if len(assistant.ToolCalls) == 0 {
			result.FinalAnswer = assistant.Content
			result.StopReason = StopReasonFinalAnswer
			finalStep := r.appendStep(result, RunStep{Type: StepTypeFinalAnswer, Role: conversation.RoleAssistant, Content: assistant.Content})
			_ = r.emit(ctx, finalStep)
			return finish(result, r.now()), nil
		}
		messages = append(messages, assistant)
		for _, call := range assistant.ToolCalls {
			if result.ToolCalls >= req.MaxToolCalls {
				result.StopReason = StopReasonMaxToolCalls
				result.FinalAnswer = "Agent stopped because max_tool_calls was exceeded."
				finalStep := r.appendStep(result, RunStep{Type: StepTypeFinalAnswer, Role: conversation.RoleAssistant, Content: result.FinalAnswer})
				_ = r.emit(ctx, finalStep)
				return finish(result, r.now()), nil
			}
			toolStep := r.appendStep(result, RunStep{
				Type:          StepTypeToolCall,
				ToolCallID:    call.ID,
				ToolName:      call.Name,
				ArgumentsJSON: call.Arguments,
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
				})
				_ = r.emit(ctx, resultStep)
				continue
			}
			result.ToolCalls++
			toolStarted := r.now()
			toolResult, toolErr := toolImpl.Execute(ctx, toolruntime.ToolRunContext{
				OwnerID: req.OwnerID,
				RunID:   req.RunID,
				NodeID:  req.NodeID,
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
			})
			if toolErr != nil {
				resultStep.Error = toolErr.Error()
			}
			_ = r.emit(ctx, resultStep)
		}
	}
	result.StopReason = StopReasonMaxIterations
	result.FinalAnswer = "Agent stopped because max_iterations was exceeded."
	finalStep := r.appendStep(result, RunStep{Type: StepTypeFinalAnswer, Role: conversation.RoleAssistant, Content: result.FinalAnswer})
	_ = r.emit(ctx, finalStep)
	return finish(result, r.now()), nil
}

func initialMessages(systemPrompt, task string) []llm.ChatMessage {
	messages := make([]llm.ChatMessage, 0, 2)
	if strings.TrimSpace(systemPrompt) != "" {
		messages = append(messages, llm.ChatMessage{Role: conversation.RoleSystem, Content: systemPrompt})
	}
	messages = append(messages, llm.ChatMessage{Role: conversation.RoleUser, Content: task})
	return messages
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
