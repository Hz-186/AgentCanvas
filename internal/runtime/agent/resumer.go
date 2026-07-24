package agent

import (
	"encoding/json"
	"fmt"

	"agentcanvas/internal/domain/conversation"
	"agentcanvas/internal/infrastructure/llm"
	"agentcanvas/internal/runtime/harness/rules"
)

type ResumeRequest struct {
	RunRequest
	Checkpoint    *Checkpoint
	Approved      bool
	RejectionNote string
}

func BuildResumeRequest(req ResumeRequest) (*RunRequest, error) {
	if req.Checkpoint == nil {
		return nil, fmt.Errorf("checkpoint is required")
	}
	messages := append([]llm.ChatMessage(nil), req.Checkpoint.Messages...)
	baseMessages := append([]llm.ChatMessage(nil), req.Checkpoint.BaseMessages...)
	transcript := append([]llm.ChatMessage(nil), req.Checkpoint.Transcript...)
	resumeSteps := append([]RunStep(nil), req.Checkpoint.Steps...)
	iteration, toolCalls := 0, 0
	approvedToolCallIDs := metadataStringSlice(req.Checkpoint.Metadata["approved_tool_call_ids"])
	if req.Approved && req.Checkpoint.PendingToolCall != nil {
		approvedToolCallIDs = appendUniqueString(approvedToolCallIDs, req.Checkpoint.PendingToolCall.ID)
	}
	if value, ok := req.Checkpoint.Metadata["iteration"].(float64); ok {
		iteration = int(value)
	}
	if value, ok := req.Checkpoint.Metadata["tool_calls"].(float64); ok {
		toolCalls = int(value)
	}
	if !req.Approved && req.Checkpoint.PendingToolCall != nil {
		content := "Human rejected the request to execute tool " + req.Checkpoint.PendingToolCall.Name
		if req.RejectionNote != "" {
			content = "Human rejected: " + req.RejectionNote
		}
		messages = append(messages, llm.ChatMessage{Role: conversation.RoleTool, ToolCallID: req.Checkpoint.PendingToolCall.ID, Content: content})
		if req.Checkpoint.SnapshotVersion >= 2 {
			transcript = append(transcript, messages[len(messages)-1])
		}
	}
	if len(baseMessages) == 0 {
		baseMessages = append([]llm.ChatMessage(nil), messages...)
		transcript = nil
	}
	ruleItems := append([]rules.Rule(nil), req.Checkpoint.Rules...)
	if len(ruleItems) == 0 {
		ruleItems = append([]rules.Rule(nil), req.Rules...)
	}
	if _, err := rules.ValidateLoadedRules(ruleItems); err != nil {
		return nil, fmt.Errorf("checkpoint rules are invalid: %w", err)
	}
	if req.Checkpoint.RuleSetHash != "" {
		set, err := rules.NewRuleSet(customRules(ruleItems), req.Checkpoint.RuleSetID, req.Checkpoint.RuleSetVersion)
		if err != nil || set.Hash != req.Checkpoint.RuleSetHash {
			return nil, fmt.Errorf("checkpoint rule set integrity check failed")
		}
	}
	reflectionPolicy := req.ReflectionPolicy
	recalledReflectionIDs := append([]int64(nil), req.RecalledReflectionIDs...)
	plan := req.Plan
	if req.Checkpoint.ReflectionPolicy.RuntimeMode != "" {
		reflectionPolicy = req.Checkpoint.ReflectionPolicy
		recalledReflectionIDs = append([]int64(nil), req.Checkpoint.RecalledReflectionIDs...)
	}
	if req.Checkpoint.Plan != nil {
		cloned := clonePlan(req.Checkpoint.Plan)
		plan = &cloned
	}
	return &RunRequest{
		OwnerID: req.OwnerID, WorkflowID: req.WorkflowID, RunID: req.RunID, NodeID: req.NodeID,
		CallDepth: req.CallDepth, WorkflowCallChain: req.WorkflowCallChain, ConversationID: req.ConversationID,
		Provider: req.Provider, Model: req.Model, Mode: req.Mode, Plan: plan, SystemPrompt: req.SystemPrompt, Task: req.Task,
		ReflectionEnabled: req.ReflectionEnabled, ReflectionPolicy: reflectionPolicy, RecalledReflectionIDs: recalledReflectionIDs,
		Temperature: req.Temperature, MaxIterations: req.MaxIterations, MaxToolCalls: req.MaxToolCalls,
		MaxExecutionTimeMS: req.MaxExecutionTimeMS, MaxParallelTools: req.MaxParallelTools, MaxInputChars: req.MaxInputChars,
		MaxInputTokens: req.MaxInputTokens, ContextWindowTokens: req.ContextWindowTokens, ReservedOutputTokens: req.ReservedOutputTokens,
		ContextSafetyMarginTokens: req.ContextSafetyMarginTokens, ModelAutoCompactTokenLimit: req.ModelAutoCompactTokenLimit,
		ModelAutoCompactTokenLimitScope: req.ModelAutoCompactTokenLimitScope, CompactPrompt: req.CompactPrompt,
		MaxRuleTokens: req.MaxRuleTokens, RuleTags: append([]string(nil), req.RuleTags...), RuleRiskLevel: req.RuleRiskLevel,
		RuleSetVersion: firstNonEmpty(req.Checkpoint.RuleSetVersion, req.RuleSetVersion), RuleSetID: firstPositive(req.Checkpoint.RuleSetID, req.RuleSetID),
		RuleSetHash: firstNonEmpty(req.Checkpoint.RuleSetHash, req.RuleSetHash), Rules: ruleItems, RuleTrace: req.RuleTrace,
		ContextBlocks: req.ContextBlocks, ToolPolicy: req.ToolPolicy, ToolHookChain: req.ToolHookChain, Tools: req.Tools,
		ResumeMessages: messages, ResumeBaseMessages: baseMessages, ResumeTranscript: transcript, ResumeSteps: resumeSteps,
		ResumeIteration: iteration, ResumeToolCalls: toolCalls, ResumeApprovedToolCallIDs: approvedToolCallIDs,
	}, nil
}

func customRules(items []rules.Rule) []rules.Rule {
	custom := make([]rules.Rule, 0, len(items))
	for _, rule := range items {
		if rule.ID != "safety.output.boundary" && rule.ID != "core.task.completion" {
			custom = append(custom, rule)
		}
	}
	return custom
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
func firstPositive(values ...int64) int64 {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

func metadataStringSlice(value any) []string {
	if values, ok := value.([]string); ok {
		return append([]string(nil), values...)
	}
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		if text, ok := item.(string); ok && text != "" {
			out = appendUniqueString(out, text)
		}
	}
	return out
}

func appendUniqueString(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func CheckpointFromJSON(data json.RawMessage) (*Checkpoint, error) {
	var checkpoint Checkpoint
	if err := json.Unmarshal(data, &checkpoint); err != nil {
		return nil, err
	}
	return &checkpoint, nil
}
