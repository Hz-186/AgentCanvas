package llm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

type CachedChatClient struct {
	chatInner ChatClient
	toolInner ToolCallingClient
	redis     *redis.Client
	ttl       time.Duration
}

type CachedChatClientOptions struct {
	Redis *redis.Client
	TTL   time.Duration
}

type cachedChatPayload struct {
	Response ChatResponse `json:"response"`
	CachedAt time.Time    `json:"cached_at"`
}

func NewCachedChatClient(chatInner ChatClient, toolInner ToolCallingClient, opts CachedChatClientOptions) *CachedChatClient {
	return &CachedChatClient{
		chatInner: chatInner,
		toolInner: toolInner,
		redis:     opts.Redis,
		ttl:       opts.TTL,
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
		if payload, hit, err := c.get(ctx, ownerID, cfg, req); err == nil && hit {
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
		if payload, hit, err := c.get(ctx, ownerID, cfg, req); err == nil && hit {
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

func (c *CachedChatClient) get(ctx context.Context, ownerID int64, cfg ChatProviderConfig, req ChatRequest) (*cachedChatPayload, bool, error) {
	if c.redis == nil || ownerID <= 0 {
		return nil, false, nil
	}
	key, err := c.cacheKey(ownerID, cfg, req)
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

func (c *CachedChatClient) set(ctx context.Context, ownerID int64, cfg ChatProviderConfig, req ChatRequest, resp ChatResponse) error {
	if c.redis == nil || ownerID <= 0 {
		return nil
	}
	key, err := c.cacheKey(ownerID, cfg, req)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(cachedChatPayload{Response: resp, CachedAt: time.Now().UTC()})
	if err != nil {
		return err
	}
	return c.redis.Set(ctx, key, payload, c.ttl).Err()
}

func (c *CachedChatClient) writeCaches(ctx context.Context, ownerID int64, cfg ChatProviderConfig, req ChatRequest, resp ChatResponse) error {
	if ownerID <= 0 {
		return nil
	}
	return c.set(ctx, ownerID, cfg, req, resp)
}

func (c *CachedChatClient) cacheKey(ownerID int64, cfg ChatProviderConfig, req ChatRequest) (string, error) {
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
