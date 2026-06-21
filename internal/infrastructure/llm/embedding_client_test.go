package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOpenAICompatibleEmbeddingClient(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/embeddings" {
			t.Fatalf("path = %q, want /v1/embeddings", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("Authorization = %q", got)
		}
		var req openAIEmbeddingRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req.Model != "text-embedding" || len(req.Input) != 2 {
			t.Fatalf("request = %#v", req)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{"index": 1, "embedding": []float32{0.3, 0.4}},
				{"index": 0, "embedding": []float32{0.1, 0.2}},
			},
			"usage": map[string]int{"prompt_tokens": 4, "total_tokens": 4},
		})
	}))
	defer server.Close()

	client := NewOpenAICompatibleEmbeddingClient()
	resp, err := client.Embed(context.Background(), EmbeddingProviderConfig{ProviderType: "openai_compatible", BaseURL: server.URL, APIKey: "test-key"}, EmbeddingRequest{
		Model: "text-embedding",
		Input: []string{"first", "second"},
	})
	if err != nil {
		t.Fatalf("Embed() error = %v", err)
	}
	if len(resp.Embeddings) != 2 || resp.Embeddings[0][0] != 0.1 || resp.Embeddings[1][0] != 0.3 {
		t.Fatalf("embeddings = %#v", resp.Embeddings)
	}
	if resp.Usage.TotalTokens != 4 {
		t.Fatalf("usage = %#v", resp.Usage)
	}
}
