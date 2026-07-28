package composite

import (
	"context"
	"errors"
	"testing"

	"agentcanvas/internal/domain/retrieval"
)

func TestHybridSearchFusesKeywordAndVectorBackends(t *testing.T) {
	keyword := &fakeBackend{results: []retrieval.RetrievalResult{{ChunkID: 1, Score: 10, Content: "keyword"}}}
	vector := &fakeBackend{results: []retrieval.RetrievalResult{{ChunkID: 2, Score: 5, Content: "vector"}}}
	store := New(keyword, vector)

	resp, err := store.Search(context.Background(), retrieval.RetrievalRequest{Mode: retrieval.ModeHybrid, TopK: 2, CandidateK: 8, HybridWeight: 0.5, QueryVector: []float32{0.1}})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if keyword.lastMode != retrieval.ModeKeyword || vector.lastMode != retrieval.ModeVector {
		t.Fatalf("backend modes = %s/%s", keyword.lastMode, vector.lastMode)
	}
	if keyword.lastTopK != 8 || vector.lastTopK != 8 {
		t.Fatalf("backend top_k = %d/%d, want candidate_k 8", keyword.lastTopK, vector.lastTopK)
	}
	if len(resp.Results) != 2 || resp.Results[0].FinalScore == 0 || resp.Results[1].FinalScore == 0 {
		t.Fatalf("unexpected fused results: %+v", resp.Results)
	}
	if len(resp.Trace) == 0 || resp.Trace[len(resp.Trace)-1].Stage != "hybrid_fusion" {
		t.Fatalf("expected hybrid fusion trace, got %+v", resp.Trace)
	}
}

func TestHybridSearchCandidateKIsNotSmallerThanTopK(t *testing.T) {
	keyword := &fakeBackend{}
	vector := &fakeBackend{}
	store := New(keyword, vector)

	if _, err := store.Search(context.Background(), retrieval.RetrievalRequest{Mode: retrieval.ModeHybrid, TopK: 5, CandidateK: 2}); err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if keyword.lastTopK != 5 || vector.lastTopK != 5 {
		t.Fatalf("backend top_k = %d/%d, want 5", keyword.lastTopK, vector.lastTopK)
	}
}

func TestSearchRejectsUnknownMode(t *testing.T) {
	store := New(&fakeBackend{}, &fakeBackend{})
	if _, err := store.Search(context.Background(), retrieval.RetrievalRequest{Mode: retrieval.Mode("unknown")}); err == nil {
		t.Fatal("Search() error = nil, want unsupported mode error")
	}
}

func TestSearchRoutesAtomicModes(t *testing.T) {
	keyword := &fakeBackend{results: []retrieval.RetrievalResult{{ChunkID: 1}}}
	vector := &fakeBackend{results: []retrieval.RetrievalResult{{ChunkID: 2}}}
	store := New(keyword, vector)

	keywordResp, err := store.Search(context.Background(), retrieval.RetrievalRequest{Mode: retrieval.ModeKeyword, TopK: 3})
	if err != nil || len(keywordResp.Results) != 1 || keywordResp.Results[0].ChunkID != 1 {
		t.Fatalf("keyword Search() response=%+v error=%v", keywordResp, err)
	}
	vectorResp, err := store.Search(context.Background(), retrieval.RetrievalRequest{Mode: retrieval.ModeVector, TopK: 4})
	if err != nil || len(vectorResp.Results) != 1 || vectorResp.Results[0].ChunkID != 2 {
		t.Fatalf("vector Search() response=%+v error=%v", vectorResp, err)
	}
	if keyword.lastMode != retrieval.ModeKeyword || keyword.lastTopK != 3 {
		t.Fatalf("keyword backend request = mode:%s top_k:%d", keyword.lastMode, keyword.lastTopK)
	}
	if vector.lastMode != retrieval.ModeVector || vector.lastTopK != 4 {
		t.Fatalf("vector backend request = mode:%s top_k:%d", vector.lastMode, vector.lastTopK)
	}
}

func TestVectorSearchRequiresVectorBackend(t *testing.T) {
	store := New(&fakeBackend{}, nil)
	if _, err := store.Search(context.Background(), retrieval.RetrievalRequest{Mode: retrieval.ModeVector}); err == nil {
		t.Fatal("Search() error = nil, want missing vector backend error")
	}
}

func TestSharedBackendWritesLifecycleOnce(t *testing.T) {
	backend := &fakeBackend{}
	store := NewShared(backend)
	docs := []retrieval.ChunkIndexDocument{{ChunkID: 1}}

	if err := store.EnsureIndex(context.Background()); err != nil {
		t.Fatalf("EnsureIndex() error = %v", err)
	}
	if err := store.IndexChunks(context.Background(), docs); err != nil {
		t.Fatalf("IndexChunks() error = %v", err)
	}
	if err := store.SetDocumentEnabled(context.Background(), 1, 2, true); err != nil {
		t.Fatalf("SetDocumentEnabled() error = %v", err)
	}
	if err := store.DeleteByDocument(context.Background(), 1, 2); err != nil {
		t.Fatalf("DeleteByDocument() error = %v", err)
	}
	if err := store.DeleteByKnowledgeBase(context.Background(), 1, 3); err != nil {
		t.Fatalf("DeleteByKnowledgeBase() error = %v", err)
	}

	if backend.ensureCalls != 1 || backend.indexCalls != 1 || backend.enableCalls != 1 || backend.deleteDocumentCalls != 1 || backend.deleteKBCalls != 1 {
		t.Fatalf("shared lifecycle calls = ensure:%d index:%d enable:%d delete_document:%d delete_kb:%d", backend.ensureCalls, backend.indexCalls, backend.enableCalls, backend.deleteDocumentCalls, backend.deleteKBCalls)
	}
}

func TestHybridSearchFallsBackToKeywordWhenVectorBackendFails(t *testing.T) {
	keyword := &fakeBackend{results: []retrieval.RetrievalResult{{ChunkID: 1, Score: 10, Content: "keyword"}}}
	vector := &fakeBackend{err: errors.New("milvus down")}
	store := New(keyword, vector)

	resp, err := store.Search(context.Background(), retrieval.RetrievalRequest{Mode: retrieval.ModeHybrid, TopK: 2, QueryVector: []float32{0.1}})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(resp.Results) != 1 || resp.Results[0].ChunkID != 1 {
		t.Fatalf("expected keyword fallback results, got %+v", resp.Results)
	}
	if len(resp.Trace) == 0 || resp.Trace[len(resp.Trace)-1].Message != "vector_backend_failed" {
		t.Fatalf("expected vector failure trace, got %+v", resp.Trace)
	}
}

func TestCompositeIndexWritesPrimaryAndVector(t *testing.T) {
	keyword := &fakeBackend{}
	vector := &fakeBackend{}
	store := New(keyword, vector)
	docs := []retrieval.ChunkIndexDocument{{ChunkID: 1, Content: "a"}}

	if err := store.IndexChunks(context.Background(), docs); err != nil {
		t.Fatalf("IndexChunks() error = %v", err)
	}
	if len(keyword.indexed) != 1 || len(vector.indexed) != 1 {
		t.Fatalf("indexed keyword=%+v vector=%+v", keyword.indexed, vector.indexed)
	}
}

type fakeBackend struct {
	results             []retrieval.RetrievalResult
	lastMode            retrieval.Mode
	lastTopK            int
	indexed             []retrieval.ChunkIndexDocument
	err                 error
	ensureCalls         int
	indexCalls          int
	enableCalls         int
	deleteDocumentCalls int
	deleteKBCalls       int
}

func (f *fakeBackend) EnsureIndex(context.Context) error {
	f.ensureCalls++
	return nil
}
func (f *fakeBackend) IndexChunks(_ context.Context, docs []retrieval.ChunkIndexDocument) error {
	f.indexCalls++
	f.indexed = append(f.indexed, docs...)
	return nil
}
func (f *fakeBackend) SetDocumentEnabled(context.Context, int64, int64, bool) error {
	f.enableCalls++
	return nil
}
func (f *fakeBackend) DeleteByDocument(context.Context, int64, int64) error {
	f.deleteDocumentCalls++
	return nil
}
func (f *fakeBackend) DeleteByKnowledgeBase(context.Context, int64, int64) error {
	f.deleteKBCalls++
	return nil
}
func (f *fakeBackend) Search(_ context.Context, req retrieval.RetrievalRequest) (*retrieval.RetrievalResponse, error) {
	f.lastMode = req.Mode
	f.lastTopK = req.TopK
	if f.err != nil {
		return nil, f.err
	}
	return &retrieval.RetrievalResponse{Results: f.results}, nil
}
