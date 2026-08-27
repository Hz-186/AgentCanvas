package agent

import (
	"encoding/json"
	"fmt"
	"strings"

	"agentcanvas/internal/domain/conversation"
	"agentcanvas/internal/infrastructure/llm"
	"agentcanvas/internal/runtime/harness/rules"
	"agentcanvas/internal/runtime/toolruntime"
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
	if req.Approved && req.Checkpoint.Interaction != nil && strings.HasPrefix(req.RejectionNote, "choice:") {
		choiceID := strings.TrimSpace(strings.TrimPrefix(req.RejectionNote, "choice:"))
		validChoice := false
		for _, option := range req.Checkpoint.Interaction.Options {
			if option.ID == choiceID {
				validChoice = true
				break
			}
		}
		if !validChoice {
			return nil, fmt.Errorf("unknown interaction option %q", choiceID)
		}
	}
	if req.Approved && req.Checkpoint.Interaction != nil && req.Checkpoint.Interaction.Kind == "request_user_input" && req.Checkpoint.PendingToolCall != nil {
		answers := map[string]string{}
		if strings.HasPrefix(req.RejectionNote, "answers:") {
			if err := json.Unmarshal([]byte(strings.TrimPrefix(req.RejectionNote, "answers:")), &answers); err != nil {
				return nil, fmt.Errorf("invalid request_user_input answers: %w", err)
			}
		}
		if err := ValidateUserInputAnswers(req.Checkpoint.Interaction.Questions, answers); err != nil {
			return nil, err
		}
		payload, _ := json.Marshal(map[string]any{"answers": answers})
		messages = append(messages, llm.ChatMessage{Role: conversation.RoleTool, ToolCallID: req.Checkpoint.PendingToolCall.ID, Content: string(payload)})
		if req.Checkpoint.SnapshotVersion >= 2 {
			transcript = append(transcript, messages[len(messages)-1])
		}
	}
	if req.Approved && req.Checkpoint.PendingToolCall != nil {
		if req.Checkpoint.Interaction == nil || req.Checkpoint.Interaction.Kind != "request_user_input" {
			approvedToolCallIDs = appendUniqueString(approvedToolCallIDs, req.Checkpoint.PendingToolCall.ID)
		}
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
	if req.Checkpoint.RuleHash != "" {
		hash, err := rules.HashLoadedRules(ruleItems)
		if err != nil || hash != req.Checkpoint.RuleHash {
			return nil, fmt.Errorf("checkpoint rule snapshot integrity check failed")
		}
	}
	reflectionPolicy := req.ReflectionPolicy
	recalledReflectionIDs := append([]int64(nil), req.RecalledReflectionIDs...)
	if req.Checkpoint.ReflectionPolicy.RuntimeMode != "" {
		reflectionPolicy = req.Checkpoint.ReflectionPolicy
		recalledReflectionIDs = append([]int64(nil), req.Checkpoint.RecalledReflectionIDs...)
	}
	return &RunRequest{
		OwnerID: req.OwnerID, AgentID: req.AgentID, RunID: req.RunID, InitialUserMessageID: req.InitialUserMessageID,
		DelegationDepth: req.DelegationDepth, ConversationID: req.ConversationID, ProjectID: req.ProjectID,
		MessageSink: req.MessageSink,
		Provider:    req.Provider, Model: req.Model, CompactionProvider: req.CompactionProvider, CompactionModel: req.CompactionModel, CompactionProviderID: req.CompactionProviderID,
		Mode: req.Mode, SystemPrompt: req.SystemPrompt, Task: req.Task,
		ReflectionEnabled: req.ReflectionEnabled, ReflectionPolicy: reflectionPolicy, RecalledReflectionIDs: recalledReflectionIDs,
		Temperature: req.Temperature, MaxIterations: req.MaxIterations, MaxToolCalls: req.MaxToolCalls,
		MaxExecutionTimeMS: req.MaxExecutionTimeMS, MaxParallelTools: req.MaxParallelTools, MaxInputChars: req.MaxInputChars,
		MaxInputTokens: req.MaxInputTokens, ContextWindowTokens: req.ContextWindowTokens, ReservedOutputTokens: req.ReservedOutputTokens,
		ContextSafetyMarginTokens: req.ContextSafetyMarginTokens, ModelAutoCompactTokenLimit: req.ModelAutoCompactTokenLimit,
		ModelAutoCompactTokenLimitScope: req.ModelAutoCompactTokenLimitScope, CompactPrompt: req.CompactPrompt,
		ManualCompaction: req.ManualCompaction, TokenBudgetCompaction: req.TokenBudgetCompaction, RetainClientDeveloperMessages: req.RetainClientDeveloperMessages,
		MaxRuleTokens: req.MaxRuleTokens, RuleTags: append([]string(nil), req.RuleTags...), RuleRiskLevel: req.RuleRiskLevel,
		RuleHash: firstNonEmpty(req.Checkpoint.RuleHash, req.RuleHash), Rules: ruleItems, RuleTrace: req.RuleTrace,
		ContextBlocks: req.ContextBlocks, ToolPolicy: req.ToolPolicy, ToolHookChain: req.ToolHookChain, Tools: req.Tools, GoalRepository: req.GoalRepository, GoalTokenBudgetCeiling: req.GoalTokenBudgetCeiling, DefaultModeRequestUserInput: req.DefaultModeRequestUserInput, SteeringProvider: req.SteeringProvider,
		ResumeMessages: messages, ResumeBaseMessages: baseMessages, ResumeTranscript: transcript, ResumeSteps: resumeSteps,
		ResumePersistedMessageCount: req.Checkpoint.PersistedMessageCount,
		ResumeTranscriptCursor:      req.Checkpoint.TranscriptCursor,
		ResumeContext:               req.Checkpoint.Context, ResumeIteration: iteration, ResumeToolCalls: toolCalls, ResumeApprovedToolCallIDs: approvedToolCallIDs,
	}, nil
}

func ValidateUserInputAnswers(questions []toolruntime.UserInputQuestion, answers map[string]string) error {
	if len(questions) == 0 {
		return fmt.Errorf("request_user_input checkpoint has no questions")
	}
	allowed := make(map[string]map[string]struct{}, len(questions))
	for _, question := range questions {
		if strings.TrimSpace(question.ID) == "" {
			return fmt.Errorf("request_user_input question id is required")
		}
		options := make(map[string]struct{}, len(question.Options))
		for _, option := range question.Options {
			options[option.Label] = struct{}{}
		}
		allowed[question.ID] = options
		answer, ok := answers[question.ID]
		if !ok || strings.TrimSpace(answer) == "" {
			return fmt.Errorf("missing answer for request_user_input question %q", question.ID)
		}
		if len(options) > 0 {
			if _, ok := options[answer]; !ok && !(question.IsOther && answer != "__other__") {
				return fmt.Errorf("invalid answer for request_user_input question %q", question.ID)
			}
		}
	}
	for id := range answers {
		if _, ok := allowed[id]; !ok {
			return fmt.Errorf("unknown request_user_input question %q", id)
		}
	}
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
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
