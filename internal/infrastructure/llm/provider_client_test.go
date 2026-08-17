package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHTTPProviderTesterCallsConfiguredCapabilities(t *testing.T) {
	paths := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer secret" {
			t.Fatalf("request = %s %s auth=%q", r.Method, r.URL.Path, r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/chat/completions":
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": "OK"}}}})
		case "/v1/embeddings":
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"index": 0, "embedding": []float64{0.1, 0.2}}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	tester := &HTTPProviderTester{Client: server.Client()}
	err := tester.Test(context.Background(), ProviderTestConfig{
		ProviderType: "openai_compatible", BaseURL: server.URL, APIKey: "secret",
		ChatModel: "chat-model", EmbeddingModel: "embedding-model",
	})
	if err != nil {
		t.Fatalf("Test() error = %v", err)
	}
	if len(paths) != 2 || paths[0] != "/v1/chat/completions" || paths[1] != "/v1/embeddings" {
		t.Fatalf("capability endpoints = %+v", paths)
	}
}

func TestHTTPProviderTesterRejectsUnimplementedOrUntestableProvider(t *testing.T) {
	tester := NewHTTPProviderTester()
	for _, cfg := range []ProviderTestConfig{
		{ProviderType: "local", ChatModel: "model"},
		{ProviderType: "ollama", ChatModel: "model"},
		{ProviderType: "openai_compatible", BaseURL: "https://example.invalid"},
	} {
		if err := tester.Test(context.Background(), cfg); err == nil {
			t.Fatalf("Test(%+v) error = nil", cfg)
		}
	}
}
