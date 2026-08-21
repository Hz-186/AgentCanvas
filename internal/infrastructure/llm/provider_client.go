package llm

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type ProviderTestConfig struct {
	ProviderType   string
	BaseURL        string
	APIKey         string
	ChatModel      string
	EmbeddingModel string
}

type ProviderTester interface {
	Test(ctx context.Context, cfg ProviderTestConfig) error
}

type HTTPProviderTester struct {
	Client *http.Client
}

func NewHTTPProviderTester() *HTTPProviderTester {
	return &HTTPProviderTester{Client: &http.Client{Timeout: 10 * time.Second}}
}

func (t *HTTPProviderTester) Test(ctx context.Context, cfg ProviderTestConfig) error {
	switch cfg.ProviderType {
	case "local":
		return fmt.Errorf("local provider adapter is not implemented")
	case "ollama":
		return fmt.Errorf("ollama provider adapter is not implemented")
	case "deepseek", "qwen", "openai_compatible", "azure_openai":
	default:
		return fmt.Errorf("unsupported provider type: %s", cfg.ProviderType)
	}
	tested := false
	provider := ChatProviderConfig{ProviderType: cfg.ProviderType, BaseURL: cfg.BaseURL, APIKey: cfg.APIKey}
	if model := strings.TrimSpace(cfg.ChatModel); model != "" {
		tested = true
		client := &OpenAICompatibleChatClient{Client: t.Client}
		if _, err := client.Chat(ctx, provider, ChatRequest{Model: model, Messages: []ChatMessage{{Role: "user", Content: "Reply with OK."}}}); err != nil {
			return fmt.Errorf("chat capability test: %w", err)
		}
	}
	if model := strings.TrimSpace(cfg.EmbeddingModel); model != "" {
		tested = true
		client := &OpenAICompatibleEmbeddingClient{Client: t.Client}
		resp, err := client.Embed(ctx, EmbeddingProviderConfig(provider), EmbeddingRequest{Model: model, Input: []string{"capability test"}})
		if err != nil {
			return fmt.Errorf("embedding capability test: %w", err)
		}
		if len(resp.Embeddings) != 1 || len(resp.Embeddings[0]) == 0 {
			return fmt.Errorf("embedding capability test returned no vector")
		}
	}
	if !tested {
		return fmt.Errorf("default chat or embedding model is required for provider test")
	}
	return nil
}
