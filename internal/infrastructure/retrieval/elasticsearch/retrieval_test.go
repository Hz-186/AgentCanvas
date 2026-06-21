package elasticsearch

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"agentcanvas/internal/domain/retrieval"

	esclient "github.com/elastic/go-elasticsearch/v8"
)

func TestVectorSearchBuildsKNNBody(t *testing.T) {
	var captured map[string]any
	store := newTestStore(t, func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(data, &captured); err != nil {
			t.Fatalf("decode search body: %v", err)
		}
		writeSearchHits(w, []map[string]any{{"chunk_id": 100, "score": 1.7, "content": "vector result"}})
	})

	resp, err := store.Search(context.Background(), retrieval.RetrievalRequest{
		OwnerID:     1,
		KBIDs:       []int64{10},
		Query:       "agent canvas",
		TopK:        3,
		Mode:        retrieval.ModeVector,
		QueryVector: []float32{0.1, 0.2, 0.3},
	})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(resp.Results) != 1 || resp.Results[0].VectorScore != 1.7 {
		t.Fatalf("results = %#v", resp.Results)
	}
	knn, ok := captured["knn"].(map[string]any)
	if !ok {
		t.Fatalf("knn body missing: %#v", captured)
	}
	if knn["field"] != "embedding_vector" || int(knn["k"].(float64)) != 3 {
		t.Fatalf("knn = %#v", knn)
	}
	if _, ok := knn["filter"].([]any); !ok {
		t.Fatalf("knn filter missing: %#v", knn)
	}
}

func TestHybridSearchRunsKeywordAndVectorSearches(t *testing.T) {
	bodies := make([]map[string]any, 0, 2)
	store := newTestStore(t, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		data, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(data, &body); err != nil {
			t.Fatalf("decode search body: %v", err)
		}
		bodies = append(bodies, body)
		if _, ok := body["knn"]; ok {
			writeSearchHits(w, []map[string]any{{"chunk_id": 200, "score": 3.0, "content": "vector result"}})
			return
		}
		writeSearchHits(w, []map[string]any{{"chunk_id": 100, "score": 5.0, "content": "keyword result"}})
	})

	resp, err := store.Search(context.Background(), retrieval.RetrievalRequest{
		OwnerID:      1,
		KBIDs:        []int64{10},
		Query:        "agent canvas",
		TopK:         5,
		CandidateK:   8,
		Mode:         retrieval.ModeHybrid,
		QueryVector:  []float32{0.1, 0.2, 0.3},
		HybridWeight: 0.5,
	})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(bodies) != 2 {
		t.Fatalf("search calls = %d, want 2", len(bodies))
	}
	if _, ok := bodies[0]["query"]; !ok {
		t.Fatalf("first body should be keyword query: %#v", bodies[0])
	}
	if _, ok := bodies[1]["knn"]; !ok {
		t.Fatalf("second body should be knn query: %#v", bodies[1])
	}
	if len(resp.Results) != 2 {
		t.Fatalf("results = %#v", resp.Results)
	}
}

func TestVectorSearchRequiresQueryVector(t *testing.T) {
	store := &Store{}
	if _, err := store.Search(context.Background(), retrieval.RetrievalRequest{Mode: retrieval.ModeVector, TopK: 3}); err == nil {
		t.Fatal("Search() error = nil, want query vector error")
	}
}

func newTestStore(t *testing.T, handler http.HandlerFunc) *Store {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client, err := esclient.NewClient(esclient.Config{Addresses: []string{server.URL}})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	return &Store{client: client, index: "chunks"}
}

func writeSearchHits(w http.ResponseWriter, hits []map[string]any) {
	w.Header().Set("X-Elastic-Product", "Elasticsearch")
	items := make([]map[string]any, 0, len(hits))
	for _, hit := range hits {
		chunkID := int64(hit["chunk_id"].(int))
		items = append(items, map[string]any{
			"_score": hit["score"],
			"_source": map[string]any{
				"owner_id":      1,
				"kb_id":         10,
				"document_id":   20,
				"chunk_id":      chunkID,
				"chunk_index":   0,
				"document_name": "guide.md",
				"file_type":     "md",
				"section_title": "",
				"content":       hit["content"],
				"content_hash":  "hash",
				"token_count":   3,
				"metadata":      map[string]any{},
			},
		})
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"hits": map[string]any{"hits": items}})
}
