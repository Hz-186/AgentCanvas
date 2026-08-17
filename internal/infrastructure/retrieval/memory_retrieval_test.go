package retrieval

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"agentcanvas/internal/domain/memory"
	"agentcanvas/internal/infrastructure/llm"
	"agentcanvas/internal/infrastructure/vectorstore"

	esclient "github.com/elastic/go-elasticsearch/v8"
)

func TestMemoryStoreIndexIncludesFilterFields(t *testing.T) {
	var captured map[string]any
	store := newTestMemoryStore(t, func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(data, &captured); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		w.Header().Set("X-Elastic-Product", "Elasticsearch")
		_ = json.NewEncoder(w).Encode(map[string]any{"result": "created"})
	})

	err := store.Index(context.Background(), memory.Memory{
		ID: 42, OwnerID: 7, MemoryType: memory.TypeProfile, MemoryLevel: memory.LevelLongTerm,
		Title: "Preference", Content: "user likes Go", Importance: 0.8,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"owner_id", "memory_id", "memory_type", "memory_level", "title", "content", "importance", "created_at", "updated_at"} {
		if _, ok := captured[key]; !ok {
			t.Fatalf("indexed document missing %s: %#v", key, captured)
		}
	}
}

func TestMemoryStoreSearchFiltersByOwnerAndType(t *testing.T) {
	var captured map[string]any
	store := newTestMemoryStore(t, func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(data, &captured); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		w.Header().Set("X-Elastic-Product", "Elasticsearch")
		_ = json.NewEncoder(w).Encode(map[string]any{"hits": map[string]any{"hits": []any{map[string]any{"_source": map[string]any{"memory_id": 42}}}}})
	})

	ids, err := store.Search(context.Background(), 7, "go", []string{memory.TypeProfile}, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != 42 {
		t.Fatalf("unexpected ids: %v", ids)
	}
	raw, _ := json.Marshal(captured)
	body := string(raw)
	if !strings.Contains(body, "owner_id") || !strings.Contains(body, "memory_type") || !strings.Contains(body, memory.TypeProfile) {
		t.Fatalf("search body missing owner/type filters: %s", body)
	}
}

func TestArchivalMemoryIndexSeparatesEmbeddingProfiles(t *testing.T) {
	store := &archivalCollectionStore{}
	indexes := []ArchivalMemoryIndex{
		{Store: store, Embedder: archivalEmbeddingClient{}, ProviderID: 1, Model: "embedding-a"},
		{Store: store, Embedder: archivalEmbeddingClient{}, ProviderID: 2, Model: "embedding-a"},
		{Store: store, Embedder: archivalEmbeddingClient{}, ProviderID: 1, Model: "embedding-b"},
	}
	for _, index := range indexes {
		if err := index.Index(context.Background(), memory.Memory{ID: 1, OwnerID: 1, Content: "memory"}); err != nil {
			t.Fatal(err)
		}
	}
	if len(store.collections) != len(indexes) {
		t.Fatalf("collections = %v", store.collections)
	}
	seen := map[string]struct{}{}
	for _, collection := range store.collections {
		seen[collection] = struct{}{}
	}
	if len(seen) != len(indexes) {
		t.Fatalf("embedding profiles shared a collection: %v", store.collections)
	}
	if err := indexes[0].Delete(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	if store.deletedCollection != store.collections[0] {
		t.Fatalf("delete collection = %q, index collection = %q", store.deletedCollection, store.collections[0])
	}
}

type archivalEmbeddingClient struct{}

func (archivalEmbeddingClient) Embed(context.Context, llm.EmbeddingProviderConfig, llm.EmbeddingRequest) (*llm.EmbeddingResponse, error) {
	return &llm.EmbeddingResponse{Embeddings: [][]float32{{1, 2, 3}}}, nil
}

type archivalCollectionStore struct {
	collections       []string
	deletedCollection string
}

func (s *archivalCollectionStore) EnsureCollection(_ context.Context, collection string, _ int, _ vectorstore.HNSWConfig) error {
	s.collections = append(s.collections, collection)
	return nil
}
func (*archivalCollectionStore) Upsert(context.Context, string, []vectorstore.VectorDocument) error {
	return nil
}
func (*archivalCollectionStore) Delete(context.Context, string, []string) error { return nil }
func (s *archivalCollectionStore) DeleteByFilter(_ context.Context, collection string, _ map[string]any) error {
	s.deletedCollection = collection
	return nil
}
func (*archivalCollectionStore) Search(context.Context, vectorstore.SearchRequest) ([]vectorstore.SearchResult, error) {
	return nil, nil
}

func newTestMemoryStore(t *testing.T, handler http.HandlerFunc) *MemoryStore {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client, err := esclient.NewClient(esclient.Config{Addresses: []string{server.URL}})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	return &MemoryStore{client: client}
}
