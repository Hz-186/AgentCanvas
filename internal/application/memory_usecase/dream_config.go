package memory_usecase

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"agentcanvas/internal/domain"
	"agentcanvas/internal/domain/memory"
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
func NewDreamTrigger(jobQueue queueinfra.JobQueue, redisClient *redis.Client, cfg DreamConfig, repositories ...memory.ExtractionJobRepository) func(context.Context, int64, int64, int) {
	var jobs memory.ExtractionJobRepository
	if len(repositories) > 0 {
		jobs = repositories[0]
	}
	if !cfg.Enabled || (jobQueue == nil && jobs == nil) {
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
		jobID := fmt.Sprintf("dream-%d-%d-%d", ownerID, conversationID, time.Now().UnixNano())
		payload := map[string]any{"owner_id": ownerID, "conversation_id": conversationID, "round_number": roundNumber}
		if jobs != nil {
			key := fmt.Sprintf("dream:%d:%d:%d", ownerID, conversationID, roundNumber)
			job := &memory.ExtractionJob{BaseModel: domain.BaseModel{OwnerID: ownerID}, ConversationID: conversationID, IdempotencyKey: key, TriggerReason: "turns", Status: string(memory.ExtractionPending), DueAt: ptrTime(time.Now().UTC())}
			if err := jobs.Create(ctx, job); err != nil {
				if existing, findErr := jobs.FindByIdempotencyKey(ctx, ownerID, key); findErr == nil {
					job = existing
				} else {
					if redisClient != nil {
						pendingKey := "dream:pending:" + strconv.FormatInt(ownerID, 10) + ":" + strconv.FormatInt(conversationID, 10)
						_, _ = redisClient.Del(context.Background(), pendingKey).Result()
					}
					return
				}
			}
			payload["job_id"] = job.ID
			jobID = fmt.Sprintf("dream-job-%d", job.ID)
		}
		if jobQueue == nil {
			return
		}
		if err := jobQueue.Publish(ctx, queueinfra.Job{ID: jobID, Type: DreamJobType, Payload: payload}); err != nil && redisClient != nil {
			_, _ = redisClient.Del(context.Background(), "dream:pending:"+strconv.FormatInt(ownerID, 10)+":"+strconv.FormatInt(conversationID, 10)).Result()
		}
	}
}

func ptrTime(value time.Time) *time.Time { return &value }
