package elasticsearch

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestHybridSearchFusesWithinElasticsearch(t *testing.T) {
	calls := 0
	store := newTestStore(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			writeSearchHits(w, []map[string]any{{"chunk_id": 100, "score": 10.0, "content": "keyword"}})
			return
		}
		writeSearchHits(w, []map[string]any{{"chunk_id": 200, "score": 5.0, "content": "vector"}})
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
	if calls != 2 || len(resp.Results) != 2 {
		t.Fatalf("elasticsearch calls/results = %d/%+v", calls, resp.Results)
	}
}

func TestUnknownSearchModeIsRejectedWithoutCallingElasticsearch(t *testing.T) {
	calls := 0
	store := newTestStore(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		t.Fatal("unknown search mode must not call elasticsearch")
	})
	_, err := store.Search(context.Background(), retrieval.RetrievalRequest{Mode: retrieval.Mode("unknown")})
	if err == nil || !strings.Contains(err.Error(), "unsupported retrieval mode: unknown") {
		t.Fatalf("Search() error = %v, want unsupported unknown mode", err)
	}
	if calls != 0 {
		t.Fatalf("elasticsearch calls = %d, want 0", calls)
	}
}

func TestVectorSearchRequiresQueryVector(t *testing.T) {
	store := &Store{}
	if _, err := store.Search(context.Background(), retrieval.RetrievalRequest{Mode: retrieval.ModeVector, TopK: 3}); err == nil {
		t.Fatal("Search() error = nil, want query vector error")
	}
}

func TestSearchExcludesDisabledDocuments(t *testing.T) {
	var captured map[string]any
	store := newTestStore(t, func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(data, &captured)
		writeSearchHits(w, nil)
	})

	if _, err := store.Search(context.Background(), retrieval.RetrievalRequest{
		OwnerID: 1,
		KBIDs:   []int64{10},
		Query:   "agent canvas",
		TopK:    3,
		Mode:    retrieval.ModeKeyword,
	}); err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	raw, _ := json.Marshal(captured)
	if !strings.Contains(string(raw), "must_not") || !strings.Contains(string(raw), "enabled") {
		t.Fatalf("search body should exclude disabled documents: %s", raw)
	}
}

func TestSetDocumentEnabledIssuesUpdateByQuery(t *testing.T) {
	var path string
	var captured map[string]any
	store := newTestStore(t, func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		data, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(data, &captured)
		w.Header().Set("X-Elastic-Product", "Elasticsearch")
		_ = json.NewEncoder(w).Encode(map[string]any{"updated": 1})
	})

	if err := store.SetDocumentEnabled(context.Background(), 1, 20, false); err != nil {
		t.Fatalf("SetDocumentEnabled() error = %v", err)
	}
	if !strings.Contains(path, "_update_by_query") {
		t.Fatalf("request path = %q, want _update_by_query", path)
	}
	raw, _ := json.Marshal(captured)
	if !strings.Contains(string(raw), "ctx._source.enabled") {
		t.Fatalf("update body should set enabled via painless script: %s", raw)
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
