package llm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"agentcanvas/internal/infrastructure/vectorstore"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

const semanticCacheCollection = "llm_semantic_cache"

type EmbeddingConfigResolver func(ctx context.Context, ownerID int64) (EmbeddingProviderConfig, string, error)

type CachedChatClient struct {
	chatInner  ChatClient
	toolInner  ToolCallingClient
	redis      *redis.Client
	l2Store    vectorstore.Store
	embedder   EmbeddingClient
	resolveEmb EmbeddingConfigResolver
	threshold  float64
	ttl        time.Duration
	l1Enabled  bool
	l2Enabled  bool
}

type CachedChatClientOptions struct {
	Redis        *redis.Client
	L2Store      vectorstore.Store
	Embedder     EmbeddingClient
	ResolveEmbed EmbeddingConfigResolver
	TTL          time.Duration
	L1Enabled    bool
	L2Enabled    bool
	Similarity   float64
}

type cachedChatPayload struct {
	Response ChatResponse `json:"response"`
	CachedAt time.Time    `json:"cached_at"`
}

func NewCachedChatClient(chatInner ChatClient, toolInner ToolCallingClient, opts CachedChatClientOptions) *CachedChatClient {
	threshold := opts.Similarity
	if threshold <= 0 || threshold >= 1 {
		threshold = 0.96
	}
	return &CachedChatClient{
		chatInner:  chatInner,
		toolInner:  toolInner,
		redis:      opts.Redis,
		l2Store:    opts.L2Store,
		embedder:   opts.Embedder,
		resolveEmb: opts.ResolveEmbed,
		threshold:  threshold,
		ttl:        opts.TTL,
		l1Enabled:  opts.L1Enabled,
		l2Enabled:  opts.L2Enabled,
	}
}

var _ ChatClient = (*CachedChatClient)(nil)
var _ ToolCallingClient = (*CachedChatClient)(nil)

func (c *CachedChatClient) Chat(ctx context.Context, cfg ChatProviderConfig, req ChatRequest) (*ChatResponse, error) {
	if c.chatInner == nil {
		return nil, fmt.Errorf("chat client is not configured")
	}
	ownerID, ok := OwnerIDFromContext(ctx)
	if ok {
		if payload, hit, err := c.getL1(ctx, ownerID, cfg, req); err == nil && hit {
			resp := payload.Response
			return &resp, nil
		}
		if payload, hit, err := c.getL2(ctx, ownerID, req); err == nil && hit {
			if err := c.setL1(ctx, ownerID, cfg, req, payload.Response); err == nil {
				resp := payload.Response
				return &resp, nil
			}
			resp := payload.Response
			return &resp, nil
		}
	}
	resp, err := c.chatInner.Chat(ctx, cfg, req)
	if err != nil {
		return nil, err
	}
	if ok {
		_ = c.writeCaches(ctx, ownerID, cfg, req, *resp)
	}
	return resp, nil
}

func (c *CachedChatClient) StreamChat(ctx context.Context, cfg ChatProviderConfig, req ChatRequest, onEvent func(StreamEvent) error) error {
	if c.chatInner == nil {
		return fmt.Errorf("chat client is not configured")
	}
	ownerID, ok := OwnerIDFromContext(ctx)
	if ok {
		if payload, hit, err := c.getL1(ctx, ownerID, cfg, req); err == nil && hit {
			return replayCachedStream(payload.Response, onEvent)
		}
		if payload, hit, err := c.getL2(ctx, ownerID, req); err == nil && hit {
			_ = c.setL1(ctx, ownerID, cfg, req, payload.Response)
			return replayCachedStream(payload.Response, onEvent)
		}
	}
	var content strings.Builder
	usage := Usage{}
	err := c.chatInner.StreamChat(ctx, cfg, req, func(event StreamEvent) error {
		if event.Delta != "" {
			content.WriteString(event.Delta)
		}
		if event.Usage.TotalTokens > 0 || event.Usage.PromptTokens > 0 || event.Usage.CompletionTokens > 0 {
			usage = event.Usage
		}
		return onEvent(event)
	})
	if err != nil {
		return err
	}
	if ok {
		_ = c.writeCaches(ctx, ownerID, cfg, req, ChatResponse{Content: content.String(), Usage: usage})
	}
	return nil
}

func (c *CachedChatClient) ChatWithTools(ctx context.Context, cfg ChatProviderConfig, req ToolChatRequest) (*ToolChatResponse, error) {
	if c.toolInner == nil {
		return nil, fmt.Errorf("tool calling client is not configured")
	}
	return c.toolInner.ChatWithTools(ctx, cfg, req)
}

func (c *CachedChatClient) getL1(ctx context.Context, ownerID int64, cfg ChatProviderConfig, req ChatRequest) (*cachedChatPayload, bool, error) {
	if !c.l1Enabled || c.redis == nil || ownerID <= 0 {
		return nil, false, nil
	}
	key, err := c.l1Key(ownerID, cfg, req)
	if err != nil {
		return nil, false, err
	}
	raw, err := c.redis.Get(ctx, key).Bytes()
	if err == redis.Nil {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	var payload cachedChatPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, false, err
	}
	return &payload, true, nil
}

func (c *CachedChatClient) setL1(ctx context.Context, ownerID int64, cfg ChatProviderConfig, req ChatRequest, resp ChatResponse) error {
	if !c.l1Enabled || c.redis == nil || ownerID <= 0 {
		return nil
	}
	key, err := c.l1Key(ownerID, cfg, req)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(cachedChatPayload{Response: resp, CachedAt: time.Now().UTC()})
	if err != nil {
		return err
	}
	return c.redis.Set(ctx, key, payload, c.ttl).Err()
}

func (c *CachedChatClient) getL2(ctx context.Context, ownerID int64, req ChatRequest) (*cachedChatPayload, bool, error) {
	if !c.l2Enabled || c.l2Store == nil || c.embedder == nil || c.resolveEmb == nil || ownerID <= 0 {
		return nil, false, nil
	}
	query := lastUserMessage(req)
	if query == "" {
		return nil, false, nil
	}
	embedCfg, model, err := c.resolveEmb(ctx, ownerID)
	if err != nil || strings.TrimSpace(model) == "" {
		return nil, false, nil
	}
	embedding, err := c.embedQuery(ctx, embedCfg, model, query)
	if err != nil || len(embedding) == 0 {
		return nil, false, nil
	}
	results, err := c.l2Store.Search(ctx, vectorstore.SearchRequest{
		Collection: semanticCacheCollection,
		Vector:     embedding,
		TopK:       1,
		Filter:     map[string]any{"owner_id": ownerID},
	})
	if err != nil || len(results) == 0 {
		return nil, false, err
	}
	if results[0].Score > (1 - c.threshold) {
		return nil, false, nil
	}
	raw, ok := results[0].Metadata["response_json"]
	if !ok {
		return nil, false, nil
	}
	rawText, ok := raw.(string)
	if !ok || strings.TrimSpace(rawText) == "" {
		return nil, false, nil
	}
	var payload cachedChatPayload
	if err := json.Unmarshal([]byte(rawText), &payload); err != nil {
		return nil, false, err
	}
	return &payload, true, nil
}

func (c *CachedChatClient) writeCaches(ctx context.Context, ownerID int64, cfg ChatProviderConfig, req ChatRequest, resp ChatResponse) error {
	if ownerID <= 0 {
		return nil
	}
	if err := c.setL1(ctx, ownerID, cfg, req, resp); err != nil {
		return err
	}
	if !c.l2Enabled || c.l2Store == nil || c.embedder == nil || c.resolveEmb == nil {
		return nil
	}
	query := lastUserMessage(req)
	if query == "" {
		return nil
	}
	embedCfg, model, err := c.resolveEmb(ctx, ownerID)
	if err != nil || strings.TrimSpace(model) == "" {
		return nil
	}
	embedding, err := c.embedQuery(ctx, embedCfg, model, query)
	if err != nil || len(embedding) == 0 {
		return nil
	}
	if err := c.l2Store.EnsureCollection(ctx, semanticCacheCollection, len(embedding), vectorstore.DefaultHNSWConfig()); err != nil {
		return nil
	}
	payloadJSON, err := json.Marshal(cachedChatPayload{Response: resp, CachedAt: time.Now().UTC()})
	if err != nil {
		return nil
	}
	requestHash, err := c.requestHash(cfg, req)
	if err != nil {
		return nil
	}
	docID := uuid.NewString()
	if err := c.l2Store.Upsert(ctx, semanticCacheCollection, []vectorstore.VectorDocument{{
		ID:     docID,
		Vector: embedding,
		Metadata: map[string]any{
			"owner_id":      ownerID,
			"query":         query,
			"request_hash":  requestHash,
			"response_json": string(payloadJSON),
		},
	}}); err != nil {
		return nil
	}
	if expirer, ok := c.l2Store.(interface {
		Expire(ctx context.Context, collection, id string, ttl time.Duration) error
	}); ok {
		_ = expirer.Expire(ctx, semanticCacheCollection, docID, c.ttl)
	}
	return nil
}

func (c *CachedChatClient) embedQuery(ctx context.Context, cfg EmbeddingProviderConfig, model, query string) ([]float32, error) {
	resp, err := c.embedder.Embed(ctx, cfg, EmbeddingRequest{Model: model, Input: []string{query}})
	if err != nil || resp == nil || len(resp.Embeddings) == 0 {
		return nil, err
	}
	return resp.Embeddings[0], nil
}

func (c *CachedChatClient) l1Key(ownerID int64, cfg ChatProviderConfig, req ChatRequest) (string, error) {
	hash, err := c.requestHash(cfg, req)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("cache:llm:l1:%d:%s", ownerID, hash), nil
}

func (c *CachedChatClient) requestHash(cfg ChatProviderConfig, req ChatRequest) (string, error) {
	canonical, err := json.Marshal(struct {
		Provider ChatProviderConfig `json:"provider"`
		Request  ChatRequest        `json:"request"`
	}{Provider: cfg, Request: req})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:]), nil
}

func lastUserMessage(req ChatRequest) string {
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if strings.TrimSpace(req.Messages[i].Role) == "user" {
			content := strings.TrimSpace(req.Messages[i].Content)
			if content != "" {
				return content
			}
		}
	}
	return ""
}

func replayCachedStream(resp ChatResponse, onEvent func(StreamEvent) error) error {
	if err := onEvent(StreamEvent{Delta: resp.Content}); err != nil {
		return err
	}
	if resp.Usage.TotalTokens > 0 || resp.Usage.PromptTokens > 0 || resp.Usage.CompletionTokens > 0 {
		if err := onEvent(StreamEvent{Usage: resp.Usage}); err != nil {
			return err
		}
	}
	return onEvent(StreamEvent{Done: true})
}
