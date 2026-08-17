package vectorstore

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNormalizeHNSWConfigFillsDefaults(t *testing.T) {
	got := NormalizeHNSWConfig(HNSWConfig{M: 32})
	if got.M != 32 || got.EFConstruction == 0 || got.EFSearch == 0 || got.MetricType == "" {
		t.Fatalf("expected defaults to be filled, got %+v", got)
	}
}

func TestMilvusStoreValidatesConfiguration(t *testing.T) {
	store := NewMilvusStore("", "", HNSWConfig{})
	if err := store.EnsureCollection(context.Background(), "docs", 1024, HNSWConfig{}); err == nil {
		t.Fatal("expected missing address error")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v2/vectordb/collections/has":
			_, _ = w.Write([]byte(`{"code":0,"data":{"has":true}}`))
		default:
			_, _ = w.Write([]byte(`{"code":0,"data":{}}`))
		}
	}))
	defer server.Close()
	store = NewMilvusStore(server.URL, "", HNSWConfig{})
	if err := store.EnsureCollection(context.Background(), "docs", 1024, HNSWConfig{}); err != nil {
		t.Fatalf("EnsureCollection() error = %v", err)
	}
	if store.Default.M == 0 || store.Default.MetricType == "" {
		t.Fatalf("expected normalized default HNSW config, got %+v", store.Default)
	}
}

func TestMilvusStoreUsesRESTAPIForCollectionAndSearch(t *testing.T) {
	requests := make([]map[string]any, 0)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer token" {
			t.Fatalf("Authorization = %q", got)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		payload["path"] = r.URL.Path
		requests = append(requests, payload)
		switch r.URL.Path {
		case "/v2/vectordb/collections/has":
			_, _ = w.Write([]byte(`{"code":0,"data":{"has":false}}`))
		case "/v2/vectordb/entities/query":
			_, _ = w.Write([]byte(`{"code":0,"data":[{"id":"chunk-1","vector":[0.1,0.2],"metadata":"{\"enabled\":true,\"document_id\":20}"}]}`))
		case "/v2/vectordb/entities/search":
			_, _ = w.Write([]byte(`{"code":0,"data":[{"id":"chunk-1","score":0.91,"metadata":"{\"page_no\":2}"}]}`))
		default:
			_, _ = w.Write([]byte(`{"code":0,"data":{}}`))
		}
	}))
	defer server.Close()

	store := NewMilvusStore(server.URL, "token", HNSWConfig{M: 32, EFConstruction: 128, EFSearch: 80, MetricType: "COSINE"})
	if err := store.EnsureCollection(context.Background(), "docs", 1024, HNSWConfig{}); err != nil {
		t.Fatalf("EnsureCollection() error = %v", err)
	}
	if err := store.Upsert(context.Background(), "docs", []VectorDocument{{ID: "chunk-1", Vector: []float32{0.1, 0.2}, Metadata: map[string]any{"kb_id": 10}}}); err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}
	if err := store.DeleteByFilter(context.Background(), "docs", map[string]any{"document_id": 20}); err != nil {
		t.Fatalf("DeleteByFilter() error = %v", err)
	}
	docs, err := store.QueryByFilter(context.Background(), "docs", map[string]any{"document_id": 20}, 10)
	if err != nil {
		t.Fatalf("QueryByFilter() error = %v", err)
	}
	if len(docs) != 1 || docs[0].ID != "chunk-1" || len(docs[0].Vector) != 2 || docs[0].Metadata["enabled"] != true {
		t.Fatalf("QueryByFilter() = %+v", docs)
	}
	if err := store.UpdateMetadataByFilter(context.Background(), "docs", map[string]any{"document_id": 20}, func(metadata map[string]any) map[string]any {
		metadata["enabled"] = false
		return metadata
	}); err != nil {
		t.Fatalf("UpdateMetadataByFilter() error = %v", err)
	}
	got, err := store.Search(context.Background(), SearchRequest{
		Collection: "docs", Vector: []float32{0.1, 0.2}, TopK: 3,
		Filter:     map[string]any{"kb_id": []int64{10, 11}, "enabled": true},
		AnyFilters: []map[string]any{{"document_id": int64(20), "generation": "gen-a"}, {"document_id": int64(21), "generation": "gen-b"}},
	})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(got) != 1 || got[0].ID != "chunk-1" || got[0].Score != 0.91 {
		t.Fatalf("Search() = %+v", got)
	}
	if len(requests) != 10 {
		t.Fatalf("request count = %d, want 10: %+v", len(requests), requests)
	}
	indexReq := requests[2]
	encoded, _ := json.Marshal(indexReq)
	if !strings.Contains(string(encoded), "HNSW") || !strings.Contains(string(encoded), "efConstruction") {
		t.Fatalf("index request missing HNSW config: %s", encoded)
	}
	createReq, _ := json.Marshal(requests[1])
	if !strings.Contains(string(createReq), "SparseFloatVector") || !strings.Contains(string(createReq), "BM25") || !strings.Contains(string(createReq), "text_bm25_emb") {
		t.Fatalf("collection request missing BM25 schema: %s", createReq)
	}
	deleteReq := requests[5]
	if !strings.Contains(deleteReq["filter"].(string), "metadata['document_id']") {
		t.Fatalf("unexpected delete request: %+v", deleteReq)
	}
	queryReq := requests[6]
	if queryReq["filter"].(string) != "metadata['document_id'] == 20" {
		t.Fatalf("unexpected query request: %+v", queryReq)
	}
	updateQueryReq := requests[7]
	if updateQueryReq["filter"].(string) != "metadata['document_id'] == 20" {
		t.Fatalf("unexpected update query request: %+v", updateQueryReq)
	}
	upsertReq := requests[8]
	upsertData, ok := upsertReq["data"].([]any)
	if !ok || len(upsertData) != 1 {
		t.Fatalf("unexpected update upsert request: %+v", upsertReq)
	}
	upsertItem, ok := upsertData[0].(map[string]any)
	if !ok {
		t.Fatalf("unexpected upsert payload item: %+v", upsertData[0])
	}
	metadata, ok := upsertItem["metadata"].(map[string]any)
	if !ok || metadata["enabled"] != false {
		t.Fatalf("expected updated enabled metadata, got %+v", upsertItem)
	}
	searchReq := requests[9]
	filter := searchReq["filter"].(string)
	if filter == "" || searchReq["limit"].(float64) != 3 || !strings.Contains(filter, "metadata['kb_id'] in [10, 11]") || !strings.Contains(filter, "metadata['enabled'] == true") || !strings.Contains(filter, "metadata['document_id'] == 20") || !strings.Contains(filter, "metadata['generation'] == 'gen-a'") || !strings.Contains(filter, " || ") {
		t.Fatalf("unexpected search request: %+v", searchReq)
	}
}

func TestMilvusStoreRejectsUnsafeFilterField(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":0,"data":[]}`))
	}))
	defer server.Close()

	store := NewMilvusStore(server.URL, "", HNSWConfig{})
	_, err := store.Search(context.Background(), SearchRequest{Collection: "docs", Vector: []float32{0.1}, Filter: map[string]any{"kb_id'] == 1 || metadata['owner_id": 2}})
	if err == nil || !strings.Contains(err.Error(), "invalid milvus filter field") {
		t.Fatalf("expected invalid filter field error, got %v", err)
	}
}

func TestMilvusStoreDeleteByFilterExceptBuildsSafeExpression(t *testing.T) {
	var filter string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		_ = json.NewDecoder(r.Body).Decode(&payload)
		filter, _ = payload["filter"].(string)
		_, _ = w.Write([]byte(`{"code":0,"data":{}}`))
	}))
	defer server.Close()

	store := NewMilvusStore(server.URL, "", HNSWConfig{})
	if err := store.DeleteByFilterExcept(context.Background(), "docs", map[string]any{"owner_id": int64(1), "document_id": int64(20)}, "generation", "gen-active"); err != nil {
		t.Fatalf("DeleteByFilterExcept() error = %v", err)
	}
	if !strings.Contains(filter, "metadata['owner_id'] == 1") || !strings.Contains(filter, "metadata['document_id'] == 20") || !strings.Contains(filter, "metadata['generation'] != 'gen-active'") {
		t.Fatalf("delete filter = %q", filter)
	}
}

func TestMilvusStoreSearchTextUsesBM25Field(t *testing.T) {
	var payload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/vectordb/entities/search" {
			_, _ = w.Write([]byte(`{"code":0,"data":{}}`))
			return
		}
		_ = json.NewDecoder(r.Body).Decode(&payload)
		_, _ = w.Write([]byte(`{"code":0,"data":[{"id":"1","score":0.7,"metadata":"{\"content\":\"hello\"}"}]}`))
	}))
	defer server.Close()

	store := NewMilvusStore(server.URL, "", HNSWConfig{})
	results, err := store.SearchText(context.Background(), SearchRequest{Collection: "docs", QueryText: "hello world", TopK: 3})
	if err != nil || len(results) != 1 || results[0].Score != 0.7 {
		t.Fatalf("SearchText() results=%+v err=%v", results, err)
	}
	if payload["annsField"] != "sparse" || payload["data"].([]any)[0] != "hello world" {
		t.Fatalf("unexpected BM25 request: %+v", payload)
	}
}

func TestMilvusStoreTreatsExistingIndexAndLoadedCollectionAsIdempotent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v2/vectordb/collections/has":
			_, _ = w.Write([]byte(`{"code":0,"data":{"has":true}}`))
		case "/v2/vectordb/indexes/create":
			_, _ = w.Write([]byte(`{"code":1100,"message":"index already exists"}`))
		case "/v2/vectordb/collections/load":
			_, _ = w.Write([]byte(`{"code":1100,"message":"collection already loaded"}`))
		default:
			_, _ = w.Write([]byte(`{"code":0,"data":{}}`))
		}
	}))
	defer server.Close()

	store := NewMilvusStore(server.URL, "", HNSWConfig{})
	if err := store.EnsureCollection(context.Background(), "docs", 128, HNSWConfig{}); err != nil {
		t.Fatalf("EnsureCollection() error = %v", err)
	}
}

func TestMilvusStoreSurfacesAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":65535,"message":"boom"}`))
	}))
	defer server.Close()

	store := NewMilvusStore(server.URL, "", HNSWConfig{})
	err := store.Upsert(context.Background(), "docs", []VectorDocument{{ID: "chunk-1", Vector: []float32{0.1}}})
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("expected milvus API error, got %v", err)
	}
}
