package agentruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"agentcanvas/internal/pkg/jsonutil"
	runtimeagent "agentcanvas/internal/runtime/agent"
	runtimeevent "agentcanvas/internal/runtime/event"
	"agentcanvas/internal/runtime/toolruntime"

	"agentcanvas/internal/infrastructure/llm"
)

func resolveAgentTask(template string, input RunInput) string {
	task := strings.TrimSpace(template)
	if task != "" {
		return task
	}
	for _, key := range []string{"prompt", "query", "content", "final_answer"} {
		if value, ok := input[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	if len(input) > 0 {
		data, _ := json.Marshal(input)
		return string(data)
	}
	return ""
}

func agentStepPayload(step runtimeagent.RunStep, providerID int64, model string) map[string]any {
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
	if step.Compressed {
		payload["compressed"] = true
	}
	if step.IsError {
		payload["is_error"] = true
	}
	if step.Error != "" {
		payload["error"] = step.Error
	}
	if step.TokenCount > 0 {
		payload["token_count"] = step.TokenCount
	}
	if providerID > 0 {
		payload["provider_id"] = providerID
	}
	if model != "" {
		payload["model"] = model
	}
	return payload
}

func (n runtimeCore) recordAgentStep(ctx context.Context, rc *RunContext, step runtimeagent.RunStep, providerID int64, model string) {
	if rc.AgentSteps != nil {
		_ = rc.AgentSteps.RecordAgentStep(ctx, rc, agentStepRecord(step))
	}
	emitRuntimeEvent(ctx, rc, runtimeevent.Event{
		Type:    runtimeevent.AgentStep,
		RunID:   rc.RunID,
		Payload: agentStepPayload(step, providerID, model),
	})
}

func checkpointHashMismatch(checkpoint *runtimeagent.Checkpoint, tools []toolruntime.RuntimeTool, policy runtimeagent.ToolPolicy) string {
	if checkpoint == nil || checkpoint.Metadata == nil {
		return ""
	}
	storedRegistryHash, _ := checkpoint.Metadata["tool_registry_hash"].(string)
	if storedRegistryHash != "" && storedRegistryHash != stableRuntimeJSONHash(runtimeToolNames(tools)) {
		return "tool registry changed since checkpoint"
	}
	storedPolicyHash, _ := checkpoint.Metadata["tool_policy_hash"].(string)
	if storedPolicyHash != "" && storedPolicyHash != stableRuntimeJSONHash(policy) {
		return "tool policy changed since checkpoint"
	}
	return ""
}

func pausedForCheckpointMismatch(checkpoint *runtimeagent.Checkpoint, reason string) RunOutput {
	if checkpoint.Metadata == nil {
		checkpoint.Metadata = map[string]any{}
	}
	checkpoint.Metadata["resume_blocked_reason"] = reason
	return RunOutput{
		"content":       "Agent resume paused: " + reason,
		"final_answer":  "Agent resume paused: " + reason,
		"stop_reason":   runtimeagent.StopReasonPaused,
		"checkpoint":    checkpoint,
		"context_trace": checkpoint.Context,
	}
}

func runtimeToolNames(tools []toolruntime.RuntimeTool) []string {
	names := make([]string, 0, len(tools))
	for _, item := range tools {
		if item == nil {
			continue
		}
		if name := strings.TrimSpace(item.Name()); name != "" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

func stableRuntimeJSONHash(value any) string {
	data, _ := json.Marshal(value)
	return jsonutil.Hash(data)
}

func parseAndValidateStructuredOutput(schema json.RawMessage, content string, parsed *any) error {
	if err := json.Unmarshal([]byte(content), parsed); err != nil {
		return fmt.Errorf("final_answer must be valid JSON for output_schema_json")
	}
	if err := validateSimpleJSONSchema(schema, *parsed); err != nil {
		return err
	}
	return nil
}

func (n runtimeCore) repairStructuredOutput(
	ctx context.Context,
	provider llm.ChatProviderConfig,
	model string, temperature *float64,
	task, finalAnswer string,
	schema json.RawMessage,
	validationErr error,
) (string, error) {
	if n.LLM == nil {
		return "", fmt.Errorf("llm client is not configured")
	}
	prompt := fmt.Sprintf(`Rewrite the agent final answer so it strictly matches the JSON schema.

Task:
%s

Validation error:
%s

JSON schema:
%s

Current final answer:
%s

Return only valid JSON. Do not include markdown fences or explanation.`, task, validationErr.Error(), string(schema), finalAnswer)
	resp, err := n.LLM.ChatWithTools(ctx, provider, llm.ToolChatRequest{
		Model:       model,
		Messages:    []llm.ChatMessage{{Role: "user", Content: prompt}},
		Tools:       nil,
		Temperature: temperature,
	})
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(resp.Message.Content), nil
}
