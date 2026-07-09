package memory_usecase

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"agentcanvas/internal/domain/conversation"
	"agentcanvas/internal/domain/memory"
	"agentcanvas/internal/infrastructure/llm"
	"agentcanvas/internal/infrastructure/vectorstore"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

type DreamPayload struct {
	OwnerID        int64 `json:"owner_id"`
	ConversationID int64 `json:"conversation_id"`
}

const dreamArchivalCollection = "agent_archival_memories"

type DreamMessageRepository interface {
	ListActiveByConversation(ctx context.Context, ownerID, conversationID int64) ([]conversation.Message, error)
	ArchiveConversationMessages(ctx context.Context, ownerID, conversationID int64, archivedAt time.Time) (int64, error)
}

type DreamWorker struct {
	chatClient llm.ChatClient
	embedder   llm.EmbeddingClient
	memories   memory.Repository
	memoryLogs memory.WriteLogRepository
	messages   DreamMessageRepository
	vecStore   vectorstore.Store
	redis      *redis.Client
	dreamCfg   DreamConfig
	workerID   string
}

type dreamLLMResult struct {
	CoreUpdates []struct {
		MemoryType string `json:"memory_type"`
		Title      string `json:"title"`
		Content    string `json:"content"`
		Action     string `json:"action"`
		MemoryID   int64  `json:"memory_id"`
	} `json:"core_updates"`
	ArchivalInserts []struct {
		Content string `json:"content"`
	} `json:"archival_inserts"`
}

func NewDreamWorker(chatClient llm.ChatClient, embedder llm.EmbeddingClient, memories memory.Repository, memoryLogs memory.WriteLogRepository, messages DreamMessageRepository, vecStore vectorstore.Store, redisClient *redis.Client, dreamCfg DreamConfig, workerID string) *DreamWorker {
	return &DreamWorker{chatClient: chatClient, embedder: embedder, memories: memories, memoryLogs: memoryLogs, messages: messages, vecStore: vecStore, redis: redisClient, dreamCfg: dreamCfg, workerID: workerID}
}

func (w *DreamWorker) HandleDreamJob(ctx context.Context, payload DreamPayload) error {
	if !w.dreamCfg.Enabled || payload.OwnerID <= 0 || payload.ConversationID <= 0 {
		return nil
	}
	unlock, err := w.acquireLock(ctx, payload.ConversationID)
	if err != nil || unlock == nil {
		return err
	}
	defer unlock()
	if w.messages == nil || w.memories == nil || w.chatClient == nil {
		return nil
	}
	messages, err := w.messages.ListActiveByConversation(ctx, payload.OwnerID, payload.ConversationID)
	if err != nil || len(messages) == 0 {
		return err
	}
	coreItems, err := w.memories.ListForRead(ctx, payload.OwnerID, []string{memory.TypeProfile, memory.TypeTask}, &payload.ConversationID, 20)
	if err != nil {
		return err
	}
	analysis, err := w.analyze(ctx, payload, messages, coreItems)
	if err != nil {
		return err
	}
	if err := w.applyCoreUpdates(ctx, payload, analysis); err != nil {
		return err
	}
	if err := w.applyArchivalUpdates(ctx, payload, analysis); err != nil {
		return err
	}
	_, err = w.messages.ArchiveConversationMessages(ctx, payload.OwnerID, payload.ConversationID, time.Now().UTC())
	return err
}

func (w *DreamWorker) acquireLock(ctx context.Context, conversationID int64) (func(), error) {
	if w.redis == nil {
		return func() {}, nil
	}
	lockKey := fmt.Sprintf("dream:lock:%d", conversationID)
	locked, err := w.redis.SetNX(ctx, lockKey, w.workerID, 120*time.Second).Result()
	if err != nil || !locked {
		return nil, err
	}
	return func() { _, _ = w.redis.Del(context.Background(), lockKey).Result() }, nil
}

func (w *DreamWorker) analyze(ctx context.Context, payload DreamPayload, messages []conversation.Message, coreItems []memory.Memory) (*dreamLLMResult, error) {
	prompt := buildDreamPrompt(messages, coreItems)
	resp, err := w.chatClient.Chat(ctx, w.dreamCfg.Provider, llm.ChatRequest{Model: w.dreamCfg.Model, Messages: []llm.ChatMessage{{Role: conversation.RoleUser, Content: prompt}}})
	if err != nil {
		return nil, err
	}
	var parsed dreamLLMResult
	if err := json.Unmarshal([]byte(extractJSON(resp.Content)), &parsed); err != nil {
		return nil, err
	}
	return &parsed, nil
}

func (w *DreamWorker) applyCoreUpdates(ctx context.Context, payload DreamPayload, analysis *dreamLLMResult) error {
	if analysis == nil {
		return nil
	}
	for _, item := range analysis.CoreUpdates {
		memoryType := strings.TrimSpace(item.MemoryType)
		content := strings.TrimSpace(item.Content)
		action := strings.TrimSpace(item.Action)
		switch action {
		case "delete":
			if item.MemoryID > 0 {
				if err := w.memories.SoftDelete(ctx, payload.OwnerID, item.MemoryID); err != nil {
					return err
				}
			}
		case "update":
			if item.MemoryID <= 0 {
				continue
			}
			existing, err := w.memories.FindByID(ctx, payload.OwnerID, item.MemoryID)
			if err != nil {
				return err
			}
			existing.MemoryType = memoryType
			existing.Title = strings.TrimSpace(item.Title)
			existing.Content = content
			existing.Importance = 0.9
			if err := w.memories.Update(ctx, existing); err != nil {
				return err
			}
		default:
			if memoryType == "" || content == "" {
				continue
			}
			if err := w.memories.Create(ctx, &memory.Memory{OwnerID: payload.OwnerID, ConversationID: &payload.ConversationID, MemoryType: memoryType, MemoryLevel: memory.LevelLongTerm, Title: strings.TrimSpace(item.Title), Content: content, Importance: 0.9, Source: "dream_worker"}); err != nil {
				return err
			}
		}
	}
	return nil
}

func (w *DreamWorker) applyArchivalUpdates(ctx context.Context, payload DreamPayload, analysis *dreamLLMResult) error {
	if analysis == nil {
		return nil
	}
	for _, item := range analysis.ArchivalInserts {
		content := strings.TrimSpace(item.Content)
		if content == "" {
			continue
		}
		stored := &memory.Memory{OwnerID: payload.OwnerID, ConversationID: &payload.ConversationID, MemoryType: memory.TypeArchival, MemoryLevel: memory.LevelLongTerm, Content: content, Importance: 0.8, Source: "dream_worker"}
		if err := w.memories.Create(ctx, stored); err != nil {
			return err
		}
		if w.vecStore == nil || w.embedder == nil || strings.TrimSpace(w.dreamCfg.Model) == "" {
			continue
		}
		resp, err := w.embedder.Embed(ctx, llm.EmbeddingProviderConfig(w.dreamCfg.Provider), llm.EmbeddingRequest{Model: w.dreamCfg.Model, Input: []string{content}})
		if err != nil || resp == nil || len(resp.Embeddings) == 0 || len(resp.Embeddings[0]) == 0 {
			continue
		}
		_ = w.vecStore.EnsureCollection(ctx, dreamArchivalCollection, len(resp.Embeddings[0]), vectorstore.DefaultHNSWConfig())
		_ = w.vecStore.Upsert(ctx, dreamArchivalCollection, []vectorstore.VectorDocument{{ID: uuid.NewString(), Vector: resp.Embeddings[0], Metadata: map[string]any{"owner_id": payload.OwnerID, "conversation_id": payload.ConversationID, "memory_id": stored.ID, "content": content}}})
	}
	return nil
}

func buildDreamPrompt(messages []conversation.Message, coreItems []memory.Memory) string {
	var conversationText strings.Builder
	for _, item := range messages {
		content := strings.TrimSpace(item.Content)
		if content == "" {
			continue
		}
		conversationText.WriteString(item.Role)
		conversationText.WriteString(": ")
		conversationText.WriteString(content)
		conversationText.WriteString("\n")
	}
	var coreText strings.Builder
	for _, item := range coreItems {
		coreText.WriteString("[")
		coreText.WriteString(item.MemoryType)
		coreText.WriteString("] ")
		coreText.WriteString(strings.TrimSpace(item.Content))
		coreText.WriteString("\n")
	}
	return fmt.Sprintf("Analyze the conversation and update durable memory. Existing core memory:\n%s\nConversation:\n%s\nReturn JSON only with this schema: {\"core_updates\":[{\"memory_type\":\"profile_memory\",\"title\":\"\",\"content\":\"\",\"action\":\"create\"}],\"archival_inserts\":[{\"content\":\"\"}]}", coreText.String(), conversationText.String())
}

func extractJSON(raw string) string {
	raw = strings.TrimSpace(raw)
	start := strings.Index(raw, "{")
	end := strings.LastIndex(raw, "}")
	if start >= 0 && end > start {
		return raw[start : end+1]
	}
	return raw
}
