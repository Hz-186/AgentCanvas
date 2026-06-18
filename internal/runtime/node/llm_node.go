package node

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"agentcanvas/internal/infrastructure/llm"
	"agentcanvas/internal/runtime/engine"
	runtimeevent "agentcanvas/internal/runtime/event"

	agenterrors "agentcanvas/internal/pkg/errors"
)

type LLMNode struct {
	Client    llm.ChatClient
	Providers ProviderConfigLoader
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
	emitRuntimeEvent(ctx, rc, runtimeevent.Event{Type: runtimeevent.LLMStarted, RunID: rc.RunID, NodeType: n.Type(), Payload: map[string]any{"provider_id": loaded.ProviderID, "model": loaded.Model}})
	req := llm.ChatRequest{Model: loaded.Model, Temperature: cfg.Temperature, Messages: []llm.ChatMessage{{Role: "user", Content: prompt}}}
	content := strings.Builder{}
	usage := llm.Usage{}
	if cfg.Stream {
		err = n.Client.StreamChat(ctx, loaded.Config, req, func(event llm.StreamEvent) error {
			if event.Delta != "" {
				content.WriteString(event.Delta)
				emitRuntimeEvent(ctx, rc, runtimeevent.Event{Type: runtimeevent.LLMDelta, RunID: rc.RunID, NodeType: n.Type(), Payload: map[string]any{"delta": event.Delta}})
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
	return engine.NodeOutput{"content": content.String(), "usage": usage, "total_tokens": usage.TotalTokens}, nil
}
