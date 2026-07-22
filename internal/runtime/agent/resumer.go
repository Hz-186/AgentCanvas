package agent

import (
	"encoding/json"
	"fmt"

	"agentcanvas/internal/domain/conversation"
	"agentcanvas/internal/infrastructure/llm"
	"agentcanvas/internal/observability"
	"agentcanvas/internal/runtime/harness/rules"
)

type ResumeRequest struct {
	RunRequest
	Checkpoint    *Checkpoint
	Approved      bool
	RejectionNote string
}

func BuildResumeRequest(req ResumeRequest) (*RunRequest, error) {
	messages := req.Checkpoint.Messages
	baseMessages := append([]llm.ChatMessage(nil), req.Checkpoint.BaseMessages...)
	transcript := append([]llm.ChatMessage(nil), req.Checkpoint.Transcript...)
	resumeSteps := append([]RunStep(nil), req.Checkpoint.Steps...)
	iteration := 0
	toolCalls := 0
	approvedToolCallIDs := metadataStringSlice(req.Checkpoint.Metadata["approved_tool_call_ids"])
	if req.Approved && req.Checkpoint.PendingToolCall != nil {
		approvedToolCallIDs = appendUniqueString(approvedToolCallIDs, req.Checkpoint.PendingToolCall.ID)
	}
	if v, ok := req.Checkpoint.Metadata["iteration"]; ok {
		if i, ok := v.(float64); ok {
			iteration = int(i)
		}
	}
	if v, ok := req.Checkpoint.Metadata["tool_calls"]; ok {
		if i, ok := v.(float64); ok {
			toolCalls = int(i)
		}
	}
	if !req.Approved && req.Checkpoint.PendingToolCall != nil {
		rejectionContent := "Human rejected the request to execute tool " + req.Checkpoint.PendingToolCall.Name
		if req.RejectionNote != "" {
			rejectionContent = "Human rejected: " + req.RejectionNote
		}
		messages = append(messages, llm.ChatMessage{
			Role:       conversation.RoleTool,
			ToolCallID: req.Checkpoint.PendingToolCall.ID,
			Content:    rejectionContent,
		})
		if req.Checkpoint.SnapshotVersion >= 2 {
			transcript = append(transcript, messages[len(messages)-1])
		}
		pending := req.Checkpoint.PendingToolCall
		_ = pending
	}
	if len(baseMessages) == 0 {
		// Legacy checkpoints only stored the already assembled message list.
		baseMessages = append([]llm.ChatMessage(nil), messages...)
		transcript = nil
	}
	contextBlocks := req.ContextBlocks
	if len(contextBlocks) == 0 {
		contextBlocks = nil
	}
	compiledRules, err := checkpointCompiledRules(req.Checkpoint, req.CompiledRules)
	if err != nil {
		return nil, err
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
		OwnerID:                         req.OwnerID,
		WorkflowID:                      req.WorkflowID,
		RunID:                           req.RunID,
		NodeID:                          req.NodeID,
		CallDepth:                       req.CallDepth,
		WorkflowCallChain:               req.WorkflowCallChain,
		ConversationID:                  req.ConversationID,
		Provider:                        req.Provider,
		Model:                           req.Model,
		Mode:                            req.Mode,
		Plan:                            plan,
		SystemPrompt:                    req.SystemPrompt,
		Task:                            req.Task,
		ReflectionEnabled:               req.ReflectionEnabled,
		ReflectionPolicy:                reflectionPolicy,
		RecalledReflectionIDs:           recalledReflectionIDs,
		Temperature:                     req.Temperature,
		MaxIterations:                   req.MaxIterations,
		MaxToolCalls:                    req.MaxToolCalls,
		MaxExecutionTimeMS:              req.MaxExecutionTimeMS,
		MaxParallelTools:                req.MaxParallelTools,
		MaxInputChars:                   req.MaxInputChars,
		MaxInputTokens:                  req.MaxInputTokens,
		ContextWindowTokens:             req.ContextWindowTokens,
		ReservedOutputTokens:            req.ReservedOutputTokens,
		ContextSafetyMarginTokens:       req.ContextSafetyMarginTokens,
		ModelAutoCompactTokenLimit:      req.ModelAutoCompactTokenLimit,
		ModelAutoCompactTokenLimitScope: req.ModelAutoCompactTokenLimitScope,
		CompactPrompt:                   req.CompactPrompt,
		MaxRuleTokens:                   req.MaxRuleTokens,
		RuleTags:                        append([]string(nil), req.RuleTags...),
		RuleRiskLevel:                   req.RuleRiskLevel,
		RuleSetVersion:                  firstNonEmpty(req.Checkpoint.RuleSetVersion, req.RuleSetVersion),
		RuleSetID:                       firstPositive(req.Checkpoint.RuleSetID, req.RuleSetID),
		CompiledRuleHash:                firstNonEmpty(req.Checkpoint.CompiledHash, req.CompiledRuleHash),
		CompiledRules:                   compiledRules,
		CustomRules:                     checkpointRules(req.Checkpoint, req.CustomRules),
		RuleTrace:                       req.RuleTrace,
		ContextBlocks:                   contextBlocks,
		ToolPolicy:                      req.ToolPolicy,
		ToolHookChain:                   req.ToolHookChain,
		Tools:                           req.Tools,
		ResumeMessages:                  messages,
		ResumeBaseMessages:              baseMessages,
		ResumeTranscript:                transcript,
		ResumeSteps:                     resumeSteps,
		ResumeIteration:                 iteration,
		ResumeToolCalls:                 toolCalls,
		ResumeApprovedToolCallIDs:       approvedToolCallIDs,
	}, nil
}

func checkpointCompiledRules(checkpoint *Checkpoint, fallback *rules.CompiledRuleSet) (*rules.CompiledRuleSet, error) {
	if checkpoint != nil && checkpoint.CompiledRules != nil {
		if checkpoint.RuleSetID > 0 && checkpoint.CompiledRules.ID != checkpoint.RuleSetID {
			observability.RuleSystemMetrics.RecordSnapshotIntegrityFailure()
			return nil, fmt.Errorf("checkpoint compiled rule set id mismatch")
		}
		if checkpoint.RuleSetVersion != "" && checkpoint.CompiledRules.Version != checkpoint.RuleSetVersion {
			observability.RuleSystemMetrics.RecordSnapshotIntegrityFailure()
			return nil, fmt.Errorf("checkpoint compiled rule set version mismatch")
		}
		if err := rules.VerifyCompiledHash(checkpoint.CompiledRules); err != nil {
			observability.RuleSystemMetrics.RecordSnapshotIntegrityFailure()
			return nil, fmt.Errorf("checkpoint compiled rule integrity check failed: %w", err)
		}
		checkpoint.CompiledRules.Prepare()
		return checkpoint.CompiledRules, nil
	}
	if fallback != nil {
		if err := rules.VerifyCompiledHash(fallback); err != nil {
			observability.RuleSystemMetrics.RecordSnapshotIntegrityFailure()
			return nil, fmt.Errorf("fallback compiled rule integrity check failed: %w", err)
		}
		fallback.Prepare()
	}
	return fallback, nil
}

func checkpointRules(checkpoint *Checkpoint, fallback []rules.Rule) []rules.Rule {
	if checkpoint != nil && len(checkpoint.CustomRules) > 0 {
		return append([]rules.Rule(nil), checkpoint.CustomRules...)
	}
	return append([]rules.Rule(nil), fallback...)
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
	items, ok := value.([]any)
	if !ok {
		if strings, ok := value.([]string); ok {
			return append([]string(nil), strings...)
		}
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
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, err
	}
	compiledRaw := fields["compiled_rules"]
	customRaw := fields["custom_rules"]
	delete(fields, "compiled_rules")
	delete(fields, "custom_rules")
	base, err := json.Marshal(fields)
	if err != nil {
		return nil, err
	}
	var cp Checkpoint
	if err := json.Unmarshal(base, &cp); err != nil {
		return nil, err
	}
	if len(compiledRaw) > 0 && string(compiledRaw) != "null" {
		compiled, decodeErr := rules.DecodeCompiledRuleSet(compiledRaw)
		if decodeErr != nil {
			return nil, decodeErr
		}
		cp.CompiledRules = compiled
	}
	if len(customRaw) > 0 && string(customRaw) != "null" {
		var legacy []rules.LegacyRuleDTO
		if err := json.Unmarshal(customRaw, &legacy); err != nil {
			return nil, err
		}
		converted, _, convertErr := rules.ConvertLegacyRules(legacy)
		if convertErr != nil {
			return nil, convertErr
		}
		cp.CustomRules = converted
	}
	return &cp, nil
}
