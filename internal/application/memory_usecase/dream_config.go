package memory_usecase

import (
	"time"

	"agentcanvas/internal/infrastructure/llm"
	"agentcanvas/internal/pkg/config"
)

const DreamJobType = "memory:dream"

type DreamConfig struct {
	Enabled            bool
	TriggerEveryNTurns int
	IdleTimeout        time.Duration
	Provider           llm.ChatProviderConfig
	Model              string
	EmbeddingProvider  llm.EmbeddingProviderConfig
	EmbeddingModel     string
}

func NewDreamConfig(cfg config.MemoryDreamConfig) DreamConfig {
	return DreamConfig{
		Enabled:            cfg.Enabled,
		TriggerEveryNTurns: cfg.TriggerEveryNTurns,
		IdleTimeout:        time.Duration(cfg.IdleTimeoutSeconds) * time.Second,
		Provider: llm.ChatProviderConfig{
			ProviderType: cfg.LLMProviderType,
			BaseURL:      cfg.LLMBaseURL,
			APIKey:       cfg.LLMAPIKey,
		},
		Model:             cfg.LLMModel,
		EmbeddingProvider: llm.EmbeddingProviderConfig{ProviderType: cfg.EmbeddingProviderType, BaseURL: cfg.EmbeddingBaseURL, APIKey: cfg.EmbeddingAPIKey},
		EmbeddingModel:    cfg.EmbeddingModel,
	}
}
