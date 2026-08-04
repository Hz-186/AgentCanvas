package memory_usecase

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"agentcanvas/internal/infrastructure/llm"
	queueinfra "agentcanvas/internal/infrastructure/queue"
	"agentcanvas/internal/pkg/config"

	"github.com/redis/go-redis/v9"
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

// NewDreamTrigger connects successful Agent turns to the asynchronous memory
// extraction worker. Redis coalesces bursts for the same conversation.
func NewDreamTrigger(jobQueue queueinfra.JobQueue, redisClient *redis.Client, cfg DreamConfig) func(context.Context, int64, int64, int) {
	if jobQueue == nil || !cfg.Enabled {
		return nil
	}
	return func(ctx context.Context, ownerID, conversationID int64, roundNumber int) {
		if ownerID <= 0 || conversationID <= 0 {
			return
		}
		if redisClient != nil {
			ttl := cfg.IdleTimeout
			if ttl <= 0 {
				ttl = time.Minute
			}
			key := "dream:pending:" + strconv.FormatInt(ownerID, 10) + ":" + strconv.FormatInt(conversationID, 10)
			locked, err := redisClient.SetNX(ctx, key, 1, ttl).Result()
			if err != nil || !locked {
				return
			}
		}
		_ = jobQueue.Publish(ctx, queueinfra.Job{
			ID: fmt.Sprintf("dream-%d-%d-%d", ownerID, conversationID, time.Now().UnixNano()), Type: DreamJobType,
			Payload: map[string]any{"owner_id": ownerID, "conversation_id": conversationID, "round_number": roundNumber},
		})
	}
}
