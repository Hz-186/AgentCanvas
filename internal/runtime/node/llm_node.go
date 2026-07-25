package node

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"agentcanvas/internal/domain/conversation"
	"agentcanvas/internal/infrastructure/llm"
	"agentcanvas/internal/observability"
	"agentcanvas/internal/pkg/tokencounter"
	"agentcanvas/internal/runtime/conversationcontext"
	"agentcanvas/internal/runtime/engine"
	runtimeevent "agentcanvas/internal/runtime/event"

	agenterrors "agentcanvas/internal/pkg/errors"
)

type LLMNode struct {
	Client      llm.ChatClient
	Providers   ProviderConfigLoader
	History     MessageHistoryReader
	Coordinator *conversationcontext.Coordinator
}

type llmConfig struct {
	ProviderID                 int64    `json:"provider_id"`
	Model                      string   `json:"model"`
	Temperature                *float64 `json:"temperature"`
	Stream                     bool     `json:"stream"`
	ContextWindowTokens        int      `json:"context_window_tokens"`
	ReservedOutputTokens       int      `json:"reserved_output_tokens"`
	ContextSafetyMarginTokens  int      `json:"context_safety_margin_tokens"`
	ModelAutoCompactTokenLimit int      `json:"model_auto_compact_token_limit"`
	CompactPrompt              string   `json:"compact_prompt"`
}

func (LLMNode) Type() string { return "llm" }

func (LLMNode) Validate(config json.RawMessage) error {
	var cfg llmConfig
	if err := json.Unmarshal(config, &cfg); err != nil {
		return fmt.Errorf("%w: invalid llm config", agenterrors.ErrInvalidInput)
	}
	if cfg.ProviderID <= 0 {
		return fmt.Errorf("%w: llm provider_id is required", agenterrors.ErrInvalidInput)
	}
	return nil
}

func (n LLMNode) Run(ctx context.Context, rc *engine.RunContext, input engine.NodeInput, config json.RawMessage) (engine.NodeOutput, error) {
	if n.Client == nil || n.Providers == nil {
		return nil, fmt.Errorf("llm dependencies are not configured")
	}
	ctx = llm.WithOwnerID(ctx, rc.OwnerID)
	var cfg llmConfig
	if err := json.Unmarshal(config, &cfg); err != nil {
		return nil, err
	}
	loaded, err := n.Providers.LoadChatProviderConfig(ctx, rc.OwnerID, cfg.ProviderID, cfg.Model)
	if err != nil {
		return nil, err
	}
	prompt, _ := input["prompt"].(string)
	if strings.TrimSpace(prompt) == "" {
		prompt, _ = rc.Input["query"].(string)
	}
	emitRuntimeEvent(ctx, rc, runtimeevent.Event{
		Type:     runtimeevent.LLMStarted,
		RunID:    rc.RunID,
		NodeType: n.Type(),
		Payload: map[string]any{
			"provider_id": loaded.ProviderID,
			"model":       loaded.Model,
		},
	})
	var messages []llm.ChatMessage
	var compaction any
	compactUsage := llm.Usage{}
	if n.Coordinator != nil && rc.ConversationID != nil && *rc.ConversationID > 0 {
		prepared, prepareErr := n.Coordinator.Prepare(ctx, conversationcontext.Request{
			OwnerID: rc.OwnerID, ConversationID: *rc.ConversationID, ProviderID: loaded.ProviderID, Provider: loaded.Config, Model: loaded.Model,
			WindowTokens: cfg.ContextWindowTokens, ReservedOutput: cfg.ReservedOutputTokens, SafetyMargin: cfg.ContextSafetyMarginTokens,
			AutoLimit: cfg.ModelAutoCompactTokenLimit, Trigger: conversation.CompactionTriggerAuto, CompactPrompt: cfg.CompactPrompt,
			Render: func(window conversationcontext.Window) ([]llm.ChatMessage, int, error) {
				return n.messagesForWindow(window, prompt), 0, nil
			},
		})
		if prepareErr != nil {
			return nil, prepareErr
		}
		messages, compaction, compactUsage = prepared.Messages, prepared.Trace, prepared.Trace.Usage
	} else {
		messages, err = n.buildMessages(ctx, rc, prompt)
		if err != nil {
			return nil, err
		}
	}
	estimatedPrompt := llmNodeMessageTokens(loaded.Config.ProviderType, loaded.Model, messages)
	if estimatedPrompt > llmNodeHardLimit(cfg) {
		observability.ContextSystemMetrics.RecordContextOverflow()
		return nil, fmt.Errorf("context_overflow: estimated_prompt_tokens=%d allowed_prompt_tokens=%d", estimatedPrompt, llmNodeHardLimit(cfg))
	}
	req := llm.ChatRequest{
		Model:       loaded.Model,
		Temperature: cfg.Temperature,
		Messages:    messages,
	}
	content := strings.Builder{}
	usage := compactUsage
	if cfg.Stream {
		err = n.Client.StreamChat(ctx, loaded.Config, req, func(event llm.StreamEvent) error {
			if event.Delta != "" {
				content.WriteString(event.Delta)
				emitRuntimeEvent(ctx, rc, runtimeevent.Event{
					Type:     runtimeevent.LLMDelta,
					RunID:    rc.RunID,
					NodeType: n.Type(),
					Payload:  map[string]any{"delta": event.Delta},
				})
				return nil
			}
			if event.Usage.TotalTokens > 0 || event.Usage.PromptTokens > 0 || event.Usage.CompletionTokens > 0 {
				usage = addLLMNodeUsage(usage, event.Usage)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	} else {
		resp, err := n.Client.Chat(ctx, loaded.Config, req)
		if err != nil {
			return nil, err
		}
		content.WriteString(resp.Content)
		usage = addLLMNodeUsage(usage, resp.Usage)
	}
	emitRuntimeEvent(ctx, rc, runtimeevent.Event{Type: runtimeevent.LLMFinished, RunID: rc.RunID, NodeType: n.Type(), Payload: map[string]any{"total_tokens": usage.TotalTokens}})
	return engine.NodeOutput{
		"content":      content.String(),
		"usage":        usage,
		"total_tokens": usage.TotalTokens,
		"compaction":   compaction,
	}, nil
}

func (n LLMNode) buildMessages(ctx context.Context, rc *engine.RunContext, prompt string) ([]llm.ChatMessage, error) {
	messages := make([]llm.ChatMessage, 0, 8)
	if n.History != nil && rc.ConversationID != nil && *rc.ConversationID > 0 {
		history, err := n.History.ListByConversation(ctx, rc.OwnerID, *rc.ConversationID)
		if err != nil {
			return nil, err
		}
		for _, item := range history {
			role := strings.TrimSpace(item.Role)
			content := strings.TrimSpace(item.Content)
			if content == "" || !validChatRole(role) {
				continue
			}
			messages = append(messages, llm.ChatMessage{Role: role, Content: content})
		}
	}
	messages = append(messages, llm.ChatMessage{Role: conversation.RoleUser, Content: prompt})
	return messages, nil
}

func (n LLMNode) messagesForWindow(window conversationcontext.Window, prompt string) []llm.ChatMessage {
	messages := make([]llm.ChatMessage, 0, len(window.Messages)+2)
	if window.Snapshot != nil && strings.TrimSpace(window.Snapshot.Summary) != "" {
		messages = append(messages, llm.ChatMessage{Role: conversation.RoleSystem, Content: "EARLIER CONVERSATION SNAPSHOT:\n" + window.Snapshot.Summary})
	}
	for _, item := range window.Messages {
		if role, content := strings.TrimSpace(item.Role), strings.TrimSpace(item.Content); content != "" && validChatRole(role) {
			if role == conversation.RoleUser && content == strings.TrimSpace(prompt) {
				continue
			}
			messages = append(messages, llm.ChatMessage{Role: role, Content: content})
		}
	}
	return append(messages, llm.ChatMessage{Role: conversation.RoleUser, Content: prompt})
}

func llmNodeMessageTokens(providerType, model string, messages []llm.ChatMessage) int {
	raw, err := json.Marshal(messages)
	if err != nil {
		total := 0
		for i := range messages {
			total += tokencounter.Count(providerType, model, messages[i].Content).Tokens
		}
		return total
	}
	return tokencounter.Count(providerType, model, string(raw)).Tokens
}

func llmNodeHardLimit(cfg llmConfig) int {
	window := cfg.ContextWindowTokens
	if window <= 0 {
		window = 128000
	}
	reserved := cfg.ReservedOutputTokens
	if reserved <= 0 {
		reserved = min(8000, max(1, window/8))
	}
	margin := cfg.ContextSafetyMarginTokens
	if margin <= 0 {
		margin = min(1024, max(1, window/100))
	}
	return max(1, window-reserved-margin)
}

func addLLMNodeUsage(left, right llm.Usage) llm.Usage {
	return llm.Usage{PromptTokens: left.PromptTokens + right.PromptTokens, CompletionTokens: left.CompletionTokens + right.CompletionTokens, TotalTokens: left.TotalTokens + right.TotalTokens}
}

func validChatRole(role string) bool {
	switch role {
	case conversation.RoleSystem, conversation.RoleUser, conversation.RoleAssistant, conversation.RoleTool:
		return true
	default:
		return false
	}
}
