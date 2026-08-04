package llm

import (
	"context"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

type fakeToolAwareChatClient struct {
	chatCount       int
	toolCount       int
	response        ChatResponse
	toolResponse    ToolChatResponse
	streamResponse  ChatResponse
	lastToolRequest ToolChatRequest
}

func (f *fakeToolAwareChatClient) Chat(context.Context, ChatProviderConfig, ChatRequest) (*ChatResponse, error) {
	f.chatCount++
	resp := f.response
	return &resp, nil
}

func (f *fakeToolAwareChatClient) StreamChat(_ context.Context, _ ChatProviderConfig, _ ChatRequest, onEvent func(StreamEvent) error) error {
	if err := onEvent(StreamEvent{Delta: f.streamResponse.Content}); err != nil {
		return err
	}
	if f.streamResponse.Usage.TotalTokens > 0 {
		if err := onEvent(StreamEvent{Usage: f.streamResponse.Usage}); err != nil {
			return err
		}
	}
	return onEvent(StreamEvent{Done: true})
}

func (f *fakeToolAwareChatClient) ChatWithTools(_ context.Context, _ ChatProviderConfig, req ToolChatRequest) (*ToolChatResponse, error) {
	f.toolCount++
	f.lastToolRequest = req
	resp := f.toolResponse
	return &resp, nil
}

func newRedisClient(t *testing.T) (*redis.Client, func()) {
	t.Helper()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	return client, func() {
		_ = client.Close()
		server.Close()
	}
}

func TestCachedChatClientRedisHitAndTenantIsolation(t *testing.T) {
	redisClient, cleanup := newRedisClient(t)
	defer cleanup()
	inner := &fakeToolAwareChatClient{response: ChatResponse{Content: "hello", Usage: Usage{TotalTokens: 5}}}
	client := NewCachedChatClient(inner, inner, CachedChatClientOptions{
		Redis: redisClient,
		TTL:   time.Minute,
	})
	req := ChatRequest{Model: "gpt", Messages: []ChatMessage{{Role: "user", Content: "hi"}}}
	ctx1 := WithOwnerID(context.Background(), 1)
	ctx2 := WithOwnerID(context.Background(), 2)
	if _, err := client.Chat(ctx1, ChatProviderConfig{ProviderType: "openai_compatible", BaseURL: "http://x", APIKey: "a"}, req); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Chat(ctx1, ChatProviderConfig{ProviderType: "openai_compatible", BaseURL: "http://x", APIKey: "a"}, req); err != nil {
		t.Fatal(err)
	}
	if inner.chatCount != 1 {
		t.Fatalf("expected single inner chat call for same tenant, got %d", inner.chatCount)
	}
	if _, err := client.Chat(ctx2, ChatProviderConfig{ProviderType: "openai_compatible", BaseURL: "http://x", APIKey: "a"}, req); err != nil {
		t.Fatal(err)
	}
	if inner.chatCount != 2 {
		t.Fatalf("expected tenant isolation to bypass cache, got %d calls", inner.chatCount)
	}
}

func TestCachedChatClientChatWithToolsPassThrough(t *testing.T) {
	inner := &fakeToolAwareChatClient{toolResponse: ToolChatResponse{Message: ChatMessage{Role: "assistant", Content: "ok"}}}
	client := NewCachedChatClient(inner, inner, CachedChatClientOptions{})
	resp, err := client.ChatWithTools(context.Background(), ChatProviderConfig{}, ToolChatRequest{Model: "gpt"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Message.Content != "ok" || inner.toolCount != 1 {
		t.Fatalf("unexpected tool pass through: resp=%+v toolCount=%d", resp, inner.toolCount)
	}
}
