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
