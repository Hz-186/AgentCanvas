package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"agentcanvas/internal/domain/retrieval"
)

func TestBGERerankerPostsCompatiblePayloadAndOrdersResults(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rerank" {
			t.Fatalf("path = %q, want /rerank", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("Authorization = %q", got)
		}
		var req struct {
			Model     string   `json:"model"`
			Query     string   `json:"query"`
			Documents []string `json:"documents"`
			TopN      int      `json:"top_n"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req.Model != "BAAI/bge-reranker-v2-m3" || req.Query != "agent" || req.TopN != 2 || len(req.Documents) != 2 {
			t.Fatalf("request = %#v", req)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"results": []map[string]any{
				{"index": 1, "relevance_score": 0.91},
				{"index": 0, "relevance_score": 0.42},
			},
		})
	}))
	defer server.Close()

	reranker := &BGEReranker{Client: server.Client()}
	results, err := reranker.Rerank(context.Background(), RerankProviderConfig{BaseURL: server.URL, APIKey: "test-key"}, RerankRequest{
		Model: "BAAI/bge-reranker-v2-m3",
		Query: "agent",
		Results: []retrieval.RetrievalResult{
			{ChunkID: 1, Content: "first", FinalScore: 0.1},
			{ChunkID: 2, Content: "second", FinalScore: 0.2},
		},
	})
	if err != nil {
		t.Fatalf("Rerank() error = %v", err)
	}
	if results[0].ChunkID != 2 || results[0].FinalScore != 0.91 || results[1].ChunkID != 1 {
		t.Fatalf("results = %#v", results)
	}
}
