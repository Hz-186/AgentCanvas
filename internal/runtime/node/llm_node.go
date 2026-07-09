package node

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"agentcanvas/internal/domain/conversation"
	"agentcanvas/internal/infrastructure/llm"
	"agentcanvas/internal/runtime/engine"
	runtimeevent "agentcanvas/internal/runtime/event"

	agenterrors "agentcanvas/internal/pkg/errors"
)

type LLMNode struct {
	Client    llm.ChatClient
	Providers ProviderConfigLoader
	History   MessageHistoryReader
}

type llmConfig struct {
	ProviderID  int64    `json:"provider_id"`
	Model       string   `json:"model"`
	Temperature *float64 `json:"temperature"`
	Stream      bool     `json:"stream"`
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
	messages, err := n.buildMessages(ctx, rc, prompt)
	if err != nil {
		return nil, err
	}
	req := llm.ChatRequest{
		Model:       loaded.Model,
		Temperature: cfg.Temperature,
		Messages:    messages,
	}
	content := strings.Builder{}
	usage := llm.Usage{}
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
				usage = event.Usage
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
		usage = resp.Usage
	}
	emitRuntimeEvent(ctx, rc, runtimeevent.Event{Type: runtimeevent.LLMFinished, RunID: rc.RunID, NodeType: n.Type(), Payload: map[string]any{"total_tokens": usage.TotalTokens}})
	return engine.NodeOutput{
		"content":      content.String(),
		"usage":        usage,
		"total_tokens": usage.TotalTokens,
	}, nil
}

func (n LLMNode) buildMessages(ctx context.Context, rc *engine.RunContext, prompt string) ([]llm.ChatMessage, error) {
	messages := make([]llm.ChatMessage, 0, 8)
	if n.History != nil && rc.ConversationID != nil && *rc.ConversationID > 0 {
		history, err := n.History.ListByConversation(ctx, rc.OwnerID, *rc.ConversationID)
		if err != nil {
			return nil, err
		}
		if len(history) > 20 {
			history = history[len(history)-20:]
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

func validChatRole(role string) bool {
	switch role {
	case conversation.RoleSystem, conversation.RoleUser, conversation.RoleAssistant, conversation.RoleTool:
		return true
	default:
		return false
	}
}
