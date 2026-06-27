package node

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"agentcanvas/internal/domain/retrieval"
	runtimeagent "agentcanvas/internal/runtime/agent"
	"agentcanvas/internal/runtime/engine"
	runtimeevent "agentcanvas/internal/runtime/event"
	"agentcanvas/internal/runtime/toolruntime"

	"agentcanvas/internal/infrastructure/llm"
	agenterrors "agentcanvas/internal/pkg/errors"
)

type AgentLoopNode struct {
	LLM       llm.ToolCallingClient
	Providers ProviderConfigLoader
	Tools     toolruntime.Registry
	Retriever retrieval.Retriever
}

type agentLoopConfig struct {
	ProviderID              int64    `json:"provider_id"`
	Model                   string   `json:"model"`
	SystemPrompt            string   `json:"system_prompt"`
	TaskTemplate            string   `json:"task_template"`
	ToolIDs                 []int64  `json:"tool_ids"`
	KnowledgeIDs            []int64  `json:"knowledge_ids"`
	KnowledgeTopK           int      `json:"knowledge_top_k"`
	KnowledgeMode           string   `json:"knowledge_mode"`
	MaxIterations           int      `json:"max_iterations"`
	MaxToolCalls            int      `json:"max_tool_calls"`
	MaxExecutionTimeMS      int      `json:"max_execution_time_ms"`
	Temperature             *float64 `json:"temperature"`
	ReturnIntermediateSteps bool     `json:"return_intermediate_steps"`
	OutputMode              string   `json:"output_mode"`
}

func (AgentLoopNode) Type() string { return "agent_loop" }

func (AgentLoopNode) Validate(config json.RawMessage) error {
	var cfg agentLoopConfig
	if err := json.Unmarshal(config, &cfg); err != nil {
		return fmt.Errorf("%w: invalid agent_loop config", agenterrors.ErrInvalidInput)
	}
	if cfg.ProviderID <= 0 {
		return fmt.Errorf("%w: agent_loop provider_id is required", agenterrors.ErrInvalidInput)
	}
	if cfg.MaxIterations < 0 || cfg.MaxIterations > 50 {
		return fmt.Errorf("%w: agent_loop max_iterations must be <= 50", agenterrors.ErrInvalidInput)
	}
	if cfg.MaxToolCalls < 0 || cfg.MaxToolCalls > 100 {
		return fmt.Errorf("%w: agent_loop max_tool_calls must be <= 100", agenterrors.ErrInvalidInput)
	}
	if cfg.MaxExecutionTimeMS < 0 || cfg.MaxExecutionTimeMS > 10*60*1000 {
		return fmt.Errorf("%w: agent_loop max_execution_time_ms must be <= 600000", agenterrors.ErrInvalidInput)
	}
	if cfg.OutputMode != "" && cfg.OutputMode != "final_answer" && cfg.OutputMode != "full" {
		return fmt.Errorf("%w: agent_loop output_mode must be final_answer or full", agenterrors.ErrInvalidInput)
	}
	if cfg.KnowledgeTopK < 0 || cfg.KnowledgeTopK > 20 {
		return fmt.Errorf("%w: agent_loop knowledge_top_k must be <= 20", agenterrors.ErrInvalidInput)
	}
	if cfg.KnowledgeMode != "" && cfg.KnowledgeMode != string(retrieval.ModeKeyword) && cfg.KnowledgeMode != string(retrieval.ModeVector) && cfg.KnowledgeMode != string(retrieval.ModeHybrid) {
		return fmt.Errorf("%w: unsupported agent_loop knowledge_mode", agenterrors.ErrInvalidInput)
	}
	return nil
}

func (n AgentLoopNode) Run(ctx context.Context, rc *engine.RunContext, input engine.NodeInput, config json.RawMessage) (engine.NodeOutput, error) {
	if n.LLM == nil || n.Providers == nil {
		return nil, fmt.Errorf("agent_loop dependencies are not configured")
	}
	var cfg agentLoopConfig
	if err := json.Unmarshal(config, &cfg); err != nil {
		return nil, err
	}
	loaded, err := n.Providers.LoadChatProviderConfig(ctx, rc.OwnerID, cfg.ProviderID, cfg.Model)
	if err != nil {
		return nil, err
	}
	tools, err := n.loadTools(ctx, rc.OwnerID, cfg)
	if err != nil {
		return nil, err
	}
	task := resolveAgentTask(cfg.TaskTemplate, rc, input)
	if task == "" {
		return nil, fmt.Errorf("%w: agent_loop task is required", agenterrors.ErrInvalidInput)
	}
	emitRuntimeEvent(ctx, rc, runtimeevent.Event{
		Type:     runtimeevent.AgentStarted,
		RunID:    rc.RunID,
		NodeID:   rc.CurrentNodeID,
		NodeType: n.Type(),
		Payload: map[string]any{
			"provider_id": loaded.ProviderID,
			"model":       loaded.Model,
			"tool_count":  len(tools),
		},
	})
	runner := runtimeagent.Runner{
		LLM: n.LLM,
		OnStep: func(ctx context.Context, step runtimeagent.RunStep) error {
			emitRuntimeEvent(ctx, rc, runtimeevent.Event{
				Type:     runtimeevent.AgentStep,
				RunID:    rc.RunID,
				NodeID:   rc.CurrentNodeID,
				NodeType: n.Type(),
				Payload:  agentStepPayload(step),
			})
			return nil
		},
	}
	result, err := runner.Run(ctx, runtimeagent.RunRequest{
		OwnerID:            rc.OwnerID,
		RunID:              rc.RunID,
		NodeID:             rc.CurrentNodeID,
		Provider:           loaded.Config,
		Model:              loaded.Model,
		SystemPrompt:       cfg.SystemPrompt,
		Task:               task,
		Temperature:        cfg.Temperature,
		MaxIterations:      cfg.MaxIterations,
		MaxToolCalls:       cfg.MaxToolCalls,
		MaxExecutionTimeMS: cfg.MaxExecutionTimeMS,
		Tools:              tools,
	})
	if result != nil {
		eventType := runtimeevent.AgentFinished
		if err != nil {
			eventType = runtimeevent.AgentFailed
		}
		emitRuntimeEvent(ctx, rc, runtimeevent.Event{
			Type:     eventType,
			RunID:    rc.RunID,
			NodeID:   rc.CurrentNodeID,
			NodeType: n.Type(),
			Payload: map[string]any{
				"stop_reason":  result.StopReason,
				"iterations":   result.Iterations,
				"tool_calls":   result.ToolCalls,
				"latency_ms":   result.LatencyMS,
				"total_tokens": result.Usage.TotalTokens,
			},
		})
	}
	if err != nil {
		return nil, err
	}
	output := engine.NodeOutput{
		"content":      result.FinalAnswer,
		"final_answer": result.FinalAnswer,
		"stop_reason":  result.StopReason,
		"iterations":   result.Iterations,
		"tool_calls":   result.ToolCalls,
		"usage":        result.Usage,
		"total_tokens": result.Usage.TotalTokens,
		"latency_ms":   result.LatencyMS,
	}
	if cfg.ReturnIntermediateSteps || cfg.OutputMode == "full" {
		output["steps"] = runtimeagent.CompactSteps(result.Steps, 8192)
	}
	return output, nil
}

func (n AgentLoopNode) loadTools(ctx context.Context, ownerID int64, cfg agentLoopConfig) ([]toolruntime.RuntimeTool, error) {
	tools := make([]toolruntime.RuntimeTool, 0, len(cfg.ToolIDs)+1)
	if len(cfg.KnowledgeIDs) > 0 {
		if n.Retriever == nil {
			return nil, fmt.Errorf("agent_loop retriever is not configured")
		}
		tools = append(tools, toolruntime.KnowledgeSearchTool{
			Retriever: n.Retriever,
			KBIDs:     cfg.KnowledgeIDs,
			DefaultK:  cfg.KnowledgeTopK,
			Mode:      retrieval.Mode(cfg.KnowledgeMode),
		})
	}
	if len(cfg.ToolIDs) == 0 {
		return tools, nil
	}
	if n.Tools == nil {
		return nil, fmt.Errorf("agent_loop tool registry is not configured")
	}
	loaded, err := n.Tools.LoadForAgent(ctx, ownerID, cfg.ToolIDs)
	if err != nil {
		return nil, err
	}
	return append(tools, loaded...), nil
}

func resolveAgentTask(template string, rc *engine.RunContext, input engine.NodeInput) string {
	task := strings.TrimSpace(engine.ResolveTemplate(template, rc))
	if task != "" {
		return task
	}
	for _, key := range []string{"prompt", "query", "content", "final_answer"} {
		if value, ok := input[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
		if value, ok := rc.Input[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	if len(input) > 0 {
		data, _ := json.Marshal(input)
		return string(data)
	}
	return ""
}

func agentStepPayload(step runtimeagent.RunStep) map[string]any {
	payload := map[string]any{
		"index":      step.Index,
		"type":       step.Type,
		"latency_ms": step.LatencyMS,
	}
	if step.Role != "" {
		payload["role"] = step.Role
	}
	if step.Content != "" {
		payload["content"] = step.Content
	}
	if step.ToolCallID != "" {
		payload["tool_call_id"] = step.ToolCallID
	}
	if step.ToolName != "" {
		payload["tool_name"] = step.ToolName
	}
	if len(step.ArgumentsJSON) > 0 {
		payload["arguments_json"] = json.RawMessage(step.ArgumentsJSON)
	}
	if len(step.OutputJSON) > 0 {
		payload["output_json"] = json.RawMessage(step.OutputJSON)
	}
	if step.IsError {
		payload["is_error"] = true
	}
	if step.Error != "" {
		payload["error"] = step.Error
	}
	return payload
}
