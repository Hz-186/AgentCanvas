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

	resp, err := store.Search(context.Background(), retrieval.RetrievalRequest{Mode: retrieval.ModeHybrid, TopK: 2, HybridWeight: 0.5, QueryVector: []float32{0.1}})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if keyword.lastMode != retrieval.ModeKeyword || vector.lastMode != retrieval.ModeVector {
		t.Fatalf("backend modes = %s/%s", keyword.lastMode, vector.lastMode)
	}
	if len(resp.Results) != 2 || resp.Results[0].FinalScore == 0 || resp.Results[1].FinalScore == 0 {
		t.Fatalf("unexpected fused results: %+v", resp.Results)
	}
	if len(resp.Trace) == 0 || resp.Trace[len(resp.Trace)-1].Stage != "hybrid_fusion" {
		t.Fatalf("expected hybrid fusion trace, got %+v", resp.Trace)
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
	results  []retrieval.RetrievalResult
	lastMode retrieval.Mode
	indexed  []retrieval.ChunkIndexDocument
	err      error
}

func (f *fakeBackend) EnsureIndex(context.Context) error { return nil }
func (f *fakeBackend) IndexChunks(_ context.Context, docs []retrieval.ChunkIndexDocument) error {
	f.indexed = append(f.indexed, docs...)
	return nil
}
func (f *fakeBackend) SetDocumentEnabled(context.Context, int64, int64, bool) error { return nil }
func (f *fakeBackend) DeleteByDocument(context.Context, int64, int64) error         { return nil }
func (f *fakeBackend) DeleteByKnowledgeBase(context.Context, int64, int64) error    { return nil }
func (f *fakeBackend) Search(_ context.Context, req retrieval.RetrievalRequest) (*retrieval.RetrievalResponse, error) {
	f.lastMode = req.Mode
	if f.err != nil {
		return nil, f.err
	}
	return &retrieval.RetrievalResponse{Results: f.results}, nil
}
