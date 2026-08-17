package milvus

import (
	"context"
	"testing"

	"agentcanvas/internal/domain/retrieval"
	"agentcanvas/internal/infrastructure/vectorstore"
)

func TestIndexChunksUpsertsVectorsWithRetrievalMetadata(t *testing.T) {
	backend := &fakeVectorStore{}
	store := NewStore(backend, "docs", 0, vectorstore.HNSWConfig{})
	page := 2

	err := store.IndexChunks(context.Background(), []retrieval.ChunkIndexDocument{{
		OwnerID: 1, KBID: 10, DocumentID: 20, ChunkID: 30, ChunkIndex: 1,
		DocumentName: "manual.pdf", FileType: "pdf", Content: "content", Enabled: true,
		PageNo: &page, EmbeddingVector: []float32{0.1, 0.2}, Metadata: map[string]any{"block_type": "text"},
	}})
	if err != nil {
		t.Fatalf("IndexChunks() error = %v", err)
	}
	if backend.dimensions != 2 || len(backend.upserts) != 1 || backend.upserts[0].ID != "30" {
		t.Fatalf("unexpected upserts: dims=%d docs=%+v", backend.dimensions, backend.upserts)
	}
	if backend.upserts[0].Metadata["content"] != "content" || backend.upserts[0].Metadata["page_no"] == nil {
		t.Fatalf("metadata = %+v", backend.upserts[0].Metadata)
	}
	if backend.upserts[0].Text != "content" || backend.upserts[0].Metadata["has_vector"] != true {
		t.Fatalf("text/vector metadata = %+v", backend.upserts[0])
	}
}

func TestIndexChunksWritesKeywordTextWithoutEmbedding(t *testing.T) {
	backend := &fakeVectorStore{}
	store := NewStore(backend, "docs", 2, vectorstore.HNSWConfig{})

	err := store.IndexChunks(context.Background(), []retrieval.ChunkIndexDocument{{
		OwnerID: 1, KBID: 10, DocumentID: 20, ChunkID: 30, Content: "keyword content", Enabled: true,
	}})
	if err != nil {
		t.Fatalf("IndexChunks() error = %v", err)
	}
	if len(backend.upserts) != 1 || backend.upserts[0].Text != "keyword content" || len(backend.upserts[0].Vector) != 2 {
		t.Fatalf("keyword upsert = %+v", backend.upserts)
	}
	if backend.upserts[0].Metadata["has_vector"] != false {
		t.Fatalf("keyword chunk must not be eligible for vector search: %+v", backend.upserts[0].Metadata)
	}
}

func TestKeywordSearchUsesMilvusBM25WithoutQueryVector(t *testing.T) {
	backend := &fakeVectorStore{textSearchResults: []vectorstore.SearchResult{{ID: "30", Score: 0.7, Metadata: map[string]any{
		"chunk_id": float64(30), "document_id": float64(20), "kb_id": float64(10), "content": "keyword content",
	}}}}
	store := NewStore(backend, "docs", 2, vectorstore.HNSWConfig{})

	resp, err := store.Search(context.Background(), retrieval.RetrievalRequest{OwnerID: 1, KBIDs: []int64{10}, Query: "keyword", Mode: retrieval.ModeKeyword, TopK: 1})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(resp.Results) != 1 || resp.Results[0].KeywordScore != 0.7 || backend.textSearchRequest.QueryText != "keyword" {
		t.Fatalf("keyword results/request = %+v / %+v", resp.Results, backend.textSearchRequest)
	}
}

func TestSearchConvertsMilvusMetadataToRetrievalResults(t *testing.T) {
	backend := &fakeVectorStore{searchResults: []vectorstore.SearchResult{{ID: "30", Score: 0.8, Metadata: map[string]any{"chunk_id": float64(30), "document_id": float64(20), "kb_id": float64(10), "content": "content", "document_name": "manual.pdf", "page_no": float64(2), "source_metadata": map[string]any{"block_type": "text"}}}}}
	store := NewStore(backend, "docs", 2, vectorstore.HNSWConfig{})

	resp, err := store.Search(context.Background(), retrieval.RetrievalRequest{OwnerID: 1, KBIDs: []int64{10}, Mode: retrieval.ModeVector, TopK: 1, QueryVector: []float32{0.1, 0.2}, EmbeddingProfile: "profile-a"})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(resp.Results) != 1 || resp.Results[0].ChunkID != 30 || resp.Results[0].PageNo == nil || *resp.Results[0].PageNo != 2 {
		t.Fatalf("results = %+v", resp.Results)
	}
	if resp.Results[0].Metadata["block_type"] != "text" {
		t.Fatalf("metadata = %+v", resp.Results[0].Metadata)
	}
	if backend.searchRequest.Filter["enabled"] != true || backend.searchRequest.Filter["kb_id"] != int64(10) || backend.searchRequest.Filter["embedding_profile"] != "profile-a" {
		t.Fatalf("search filter = %+v", backend.searchRequest.Filter)
	}
}

func TestSearchUsesMultiKnowledgeBaseFilter(t *testing.T) {
	backend := &fakeVectorStore{}
	store := NewStore(backend, "docs", 2, vectorstore.HNSWConfig{})

	_, err := store.Search(context.Background(), retrieval.RetrievalRequest{OwnerID: 1, KBIDs: []int64{10, 11}, Mode: retrieval.ModeVector, TopK: 1, QueryVector: []float32{0.1, 0.2}})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	kbIDs, ok := backend.searchRequest.Filter["kb_id"].([]int64)
	if !ok || len(kbIDs) != 2 || kbIDs[0] != 10 || kbIDs[1] != 11 {
		t.Fatalf("search filter = %+v", backend.searchRequest.Filter)
	}
}

func TestSearchFiltersEachDocumentToItsActiveGeneration(t *testing.T) {
	backend := &fakeVectorStore{}
	store := NewStore(backend, "docs", 2, vectorstore.HNSWConfig{})

	_, err := store.Search(context.Background(), retrieval.RetrievalRequest{
		OwnerID: 1, KBIDs: []int64{10}, Mode: retrieval.ModeVector, TopK: 1, QueryVector: []float32{0.1, 0.2},
		ActiveGenerations: map[int64]string{20: "gen-a", 21: "gen-b"},
	})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(backend.searchRequest.AnyFilters) != 2 {
		t.Fatalf("active generation filters = %+v", backend.searchRequest.AnyFilters)
	}
	found := map[int64]string{}
	for _, filter := range backend.searchRequest.AnyFilters {
		found[filter["document_id"].(int64)] = filter["generation"].(string)
	}
	if found[20] != "gen-a" || found[21] != "gen-b" {
		t.Fatalf("active generation filters = %+v", backend.searchRequest.AnyFilters)
	}
}

func TestDeleteByDocumentUsesMetadataFilterWhenSupported(t *testing.T) {
	backend := &fakeVectorStore{}
	store := NewStore(backend, "docs", 2, vectorstore.HNSWConfig{})

	if err := store.DeleteByDocument(context.Background(), 1, 20); err != nil {
		t.Fatalf("DeleteByDocument() error = %v", err)
	}
	if backend.deleteCollection != "docs" || backend.deleteFilter["owner_id"] != int64(1) || backend.deleteFilter["document_id"] != int64(20) {
		t.Fatalf("delete filter = collection=%s filter=%+v", backend.deleteCollection, backend.deleteFilter)
	}
}

func TestDeleteInactiveGenerationsKeepsActiveGeneration(t *testing.T) {
	backend := &fakeVectorStore{}
	store := NewStore(backend, "docs", 2, vectorstore.HNSWConfig{})

	if err := store.DeleteInactiveGenerations(context.Background(), 1, 20, "gen-active"); err != nil {
		t.Fatalf("DeleteInactiveGenerations() error = %v", err)
	}
	if backend.excludedField != "generation" || backend.excludedValue != "gen-active" || backend.deleteFilter["document_id"] != int64(20) {
		t.Fatalf("delete-except request: filter=%+v field=%q value=%v", backend.deleteFilter, backend.excludedField, backend.excludedValue)
	}
}

func TestSetDocumentEnabledUpdatesMilvusMetadataWhenSupported(t *testing.T) {
	backend := &fakeVectorStore{}
	store := NewStore(backend, "docs", 2, vectorstore.HNSWConfig{})

	if err := store.SetDocumentEnabled(context.Background(), 1, 20, false); err != nil {
		t.Fatalf("SetDocumentEnabled() error = %v", err)
	}
	if backend.updateCollection != "docs" || backend.updateFilter["owner_id"] != int64(1) || backend.updateFilter["document_id"] != int64(20) {
		t.Fatalf("update filter = collection=%s filter=%+v", backend.updateCollection, backend.updateFilter)
	}
	metadata := backend.updateMutate(map[string]any{"enabled": true, "kb_id": int64(10)})
	if metadata["enabled"] != false || metadata["kb_id"] != int64(10) {
		t.Fatalf("mutated metadata = %+v", metadata)
	}
}

type fakeVectorStore struct {
	dimensions        int
	upserts           []vectorstore.VectorDocument
	searchResults     []vectorstore.SearchResult
	searchRequest     vectorstore.SearchRequest
	textSearchResults []vectorstore.SearchResult
	textSearchRequest vectorstore.SearchRequest
	deleteCollection  string
	deleteFilter      map[string]any
	excludedField     string
	excludedValue     any
	updateCollection  string
	updateFilter      map[string]any
	updateMutate      func(map[string]any) map[string]any
}

func (f *fakeVectorStore) EnsureCollection(_ context.Context, _ string, dimensions int, _ vectorstore.HNSWConfig) error {
	f.dimensions = dimensions
	return nil
}

func (f *fakeVectorStore) Upsert(_ context.Context, _ string, docs []vectorstore.VectorDocument) error {
	f.upserts = append(f.upserts, docs...)
	return nil
}

func (f *fakeVectorStore) Delete(context.Context, string, []string) error { return nil }

func (f *fakeVectorStore) DeleteByFilter(_ context.Context, collection string, filter map[string]any) error {
	f.deleteCollection = collection
	f.deleteFilter = filter
	return nil
}

func (f *fakeVectorStore) DeleteByFilterExcept(_ context.Context, collection string, filter map[string]any, field string, value any) error {
	f.deleteCollection = collection
	f.deleteFilter = filter
	f.excludedField = field
	f.excludedValue = value
	return nil
}

func (f *fakeVectorStore) UpdateMetadataByFilter(_ context.Context, collection string, filter map[string]any, mutate func(map[string]any) map[string]any) error {
	f.updateCollection = collection
	f.updateFilter = filter
	f.updateMutate = mutate
	return nil
}

func (f *fakeVectorStore) Search(_ context.Context, req vectorstore.SearchRequest) ([]vectorstore.SearchResult, error) {
	f.searchRequest = req
	return f.searchResults, nil
}

func (f *fakeVectorStore) SearchText(_ context.Context, req vectorstore.SearchRequest) ([]vectorstore.SearchResult, error) {
	f.textSearchRequest = req
	return f.textSearchResults, nil
}
