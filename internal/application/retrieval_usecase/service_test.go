package retrieval_usecase

import (
	"context"
	"errors"
	"testing"

	"agentcanvas/internal/domain/knowledge"
	providerdomain "agentcanvas/internal/domain/provider"
	"agentcanvas/internal/domain/retrieval"
	cryptoinfra "agentcanvas/internal/infrastructure/crypto"
	"agentcanvas/internal/infrastructure/llm"

	"gorm.io/gorm"
)

func TestSearchEmbedsQueryAndAppliesRerank(t *testing.T) {
	providerID := int64(90)
	rerankProviderID := int64(91)
	raw := &fakeRawRetriever{response: &retrieval.RetrievalResponse{Results: []retrieval.RetrievalResult{
		{ChunkID: 1, Content: "less relevant", FinalScore: 0.2},
		{ChunkID: 2, Content: "more relevant", FinalScore: 0.1},
	}}}
	service := NewService(
		&fakeKBRepo{items: map[int64]*knowledge.KnowledgeBase{
			10: {
				ID:                  10,
				OwnerID:             1,
				RetrievalMode:       knowledge.RetrievalModeHybrid,
				EmbeddingProviderID: &providerID,
				EmbeddingModel:      "text-embedding",
				EmbeddingDimensions: 3,
				HybridWeight:        0.7,
				RerankEnabled:       true,
				RerankProviderID:    &rerankProviderID,
				RerankModel:         "reranker",
			},
		}},
		&fakeProviderRepo{items: map[int64]*providerdomain.ModelProvider{
			90: {ID: 90, OwnerID: 1, ProviderType: providerdomain.TypeOpenAICompatible, Status: providerdomain.StatusActive},
			91: {ID: 91, OwnerID: 1, ProviderType: providerdomain.TypeOpenAICompatible, Status: providerdomain.StatusActive},
		}},
		raw,
		&fakeEmbedder{vectors: [][]float32{{0.1, 0.2, 0.3}}},
		&fakeReranker{order: []int64{2, 1}},
		mustSecretBox(t),
	)

	resp, err := service.Search(context.Background(), retrieval.RetrievalRequest{OwnerID: 1, KBIDs: []int64{10}, Query: "agent", TopK: 2, Mode: retrieval.ModeHybrid})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(raw.request.QueryVector) != 3 {
		t.Fatalf("query vector = %#v", raw.request.QueryVector)
	}
	if raw.request.HybridWeight != 0.7 {
		t.Fatalf("hybrid weight = %v, want 0.7", raw.request.HybridWeight)
	}
	if resp.Results[0].ChunkID != 2 || resp.Results[1].ChunkID != 1 {
		t.Fatalf("reranked results = %#v", resp.Results)
	}
}

func TestSearchFallsBackWhenRerankFails(t *testing.T) {
	providerID := int64(90)
	rerankProviderID := int64(91)
	raw := &fakeRawRetriever{response: &retrieval.RetrievalResponse{Results: []retrieval.RetrievalResult{
		{ChunkID: 1, Content: "first", FinalScore: 0.9},
		{ChunkID: 2, Content: "second", FinalScore: 0.8},
	}}}
	service := NewService(
		&fakeKBRepo{items: map[int64]*knowledge.KnowledgeBase{
			10: {
				ID:                  10,
				OwnerID:             1,
				RetrievalMode:       knowledge.RetrievalModeVector,
				EmbeddingProviderID: &providerID,
				EmbeddingModel:      "text-embedding",
				EmbeddingDimensions: 2,
				RerankEnabled:       true,
				RerankProviderID:    &rerankProviderID,
				RerankModel:         "reranker",
			},
		}},
		&fakeProviderRepo{items: map[int64]*providerdomain.ModelProvider{
			90: {ID: 90, OwnerID: 1, ProviderType: providerdomain.TypeOpenAICompatible, Status: providerdomain.StatusActive},
			91: {ID: 91, OwnerID: 1, ProviderType: providerdomain.TypeOpenAICompatible, Status: providerdomain.StatusActive},
		}},
		raw,
		&fakeEmbedder{vectors: [][]float32{{0.1, 0.2}}},
		&fakeReranker{err: errors.New("invalid json")},
		mustSecretBox(t),
	)

	resp, err := service.Search(context.Background(), retrieval.RetrievalRequest{OwnerID: 1, KBIDs: []int64{10}, Query: "agent", TopK: 2, Mode: retrieval.ModeVector})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if resp.Results[0].ChunkID != 1 || resp.Results[1].ChunkID != 2 {
		t.Fatalf("fallback results = %#v", resp.Results)
	}
}

func TestSearchKeywordDoesNotRequireEmbeddingProvider(t *testing.T) {
	raw := &fakeRawRetriever{response: &retrieval.RetrievalResponse{}}
	service := NewService(
		&fakeKBRepo{items: map[int64]*knowledge.KnowledgeBase{
			10: {ID: 10, OwnerID: 1, RetrievalMode: knowledge.RetrievalModeKeyword},
		}},
		&fakeProviderRepo{},
		raw,
		nil,
		nil,
		mustSecretBox(t),
	)

	if _, err := service.Search(context.Background(), retrieval.RetrievalRequest{OwnerID: 1, KBIDs: []int64{10}, Query: "agent", Mode: retrieval.ModeKeyword}); err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if raw.request.Mode != retrieval.ModeKeyword {
		t.Fatalf("mode = %q, want keyword", raw.request.Mode)
	}
}

func TestSearchExpandsCandidateKWhenRecallIsLow(t *testing.T) {
	raw := &sequenceRawRetriever{responses: []*retrieval.RetrievalResponse{
		{Results: []retrieval.RetrievalResult{}},
		{Results: []retrieval.RetrievalResult{{ChunkID: 1, Score: 0.8}, {ChunkID: 2, Score: 0.7}, {ChunkID: 3, Score: 0.6}}},
	}}
	service := NewService(
		&fakeKBRepo{items: map[int64]*knowledge.KnowledgeBase{10: {ID: 10, OwnerID: 1, RetrievalMode: knowledge.RetrievalModeKeyword}}},
		&fakeProviderRepo{},
		raw,
		nil,
		nil,
		mustSecretBox(t),
	)

	resp, err := service.Search(context.Background(), retrieval.RetrievalRequest{OwnerID: 1, KBIDs: []int64{10}, Query: "missing faq", TopK: 2, Mode: retrieval.ModeKeyword, CandidateK: 4})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(raw.requests) != 2 {
		t.Fatalf("expected expanded recall request, got %d requests", len(raw.requests))
	}
	if raw.requests[1].CandidateK <= raw.requests[0].CandidateK {
		t.Fatalf("candidate_k was not expanded: %+v", raw.requests)
	}
	if resp.Diagnostics == nil || !resp.Diagnostics.Expanded || resp.Diagnostics.ExpandedCandidate == 0 {
		t.Fatalf("expected expanded diagnostics, got %+v", resp.Diagnostics)
	}
	if len(resp.Results) != 2 {
		t.Fatalf("expected results truncated to top_k, got %+v", resp.Results)
	}
}

func TestSearchRewritesQueryWhenRecallIsLow(t *testing.T) {
	raw := &sequenceRawRetriever{responses: []*retrieval.RetrievalResponse{
		{Results: []retrieval.RetrievalResult{}},
		{Results: []retrieval.RetrievalResult{{ChunkID: 1, Score: 0.9}, {ChunkID: 2, Score: 0.8}}},
	}}
	service := NewService(
		&fakeKBRepo{items: map[int64]*knowledge.KnowledgeBase{10: {ID: 10, OwnerID: 1, RetrievalMode: knowledge.RetrievalModeKeyword}}},
		&fakeProviderRepo{},
		raw,
		nil,
		nil,
		mustSecretBox(t),
	)

	resp, err := service.Search(context.Background(), retrieval.RetrievalRequest{OwnerID: 1, KBIDs: []int64{10}, Query: "  AgentCanvas??  ", TopK: 2, Mode: retrieval.ModeKeyword, CandidateK: 100})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(raw.requests) != 2 {
		t.Fatalf("expected initial and rewrite requests, got %d", len(raw.requests))
	}
	if raw.requests[1].Query != "AgentCanvas" {
		t.Fatalf("rewrite query = %q, want AgentCanvas", raw.requests[1].Query)
	}
	if len(resp.Results) != 2 || resp.Trace[len(resp.Trace)-1].Stage != "query_rewrite" {
		t.Fatalf("expected rewritten response with trace, got results=%+v trace=%+v", resp.Results, resp.Trace)
	}
}

func TestSearchFallsBackToKeywordWhenVectorRecallFails(t *testing.T) {
	providerID := int64(90)
	raw := &sequenceRawRetriever{
		errors: []error{errors.New("vector backend down"), nil},
		responses: []*retrieval.RetrievalResponse{
			{Results: []retrieval.RetrievalResult{{ChunkID: 1, Score: 0.7}}},
		},
	}
	service := NewService(
		&fakeKBRepo{items: map[int64]*knowledge.KnowledgeBase{10: {ID: 10, OwnerID: 1, RetrievalMode: knowledge.RetrievalModeVector, EmbeddingProviderID: &providerID, EmbeddingModel: "text-embedding", EmbeddingDimensions: 2}}},
		&fakeProviderRepo{items: map[int64]*providerdomain.ModelProvider{90: {ID: 90, OwnerID: 1, ProviderType: providerdomain.TypeOpenAICompatible, Status: providerdomain.StatusActive}}},
		raw,
		&fakeEmbedder{vectors: [][]float32{{0.1, 0.2}}},
		nil,
		mustSecretBox(t),
	)

	resp, err := service.Search(context.Background(), retrieval.RetrievalRequest{OwnerID: 1, KBIDs: []int64{10}, Query: "agent", TopK: 1, Mode: retrieval.ModeVector})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(raw.requests) != 2 || raw.requests[1].Mode != retrieval.ModeKeyword {
		t.Fatalf("fallback requests = %+v", raw.requests)
	}
	if resp.Diagnostics == nil || resp.Diagnostics.FallbackMode != retrieval.ModeKeyword {
		t.Fatalf("expected keyword fallback diagnostics, got %+v", resp.Diagnostics)
	}
}

func TestSearchFallsBackToVectorWhenKeywordRecallIsLow(t *testing.T) {
	providerID := int64(90)
	raw := &sequenceRawRetriever{responses: []*retrieval.RetrievalResponse{
		{Results: []retrieval.RetrievalResult{}},
		{Results: []retrieval.RetrievalResult{{ChunkID: 1, Score: 0.9, VectorScore: 0.9}, {ChunkID: 2, Score: 0.8, VectorScore: 0.8}}},
	}}
	service := NewService(
		&fakeKBRepo{items: map[int64]*knowledge.KnowledgeBase{10: {ID: 10, OwnerID: 1, RetrievalMode: knowledge.RetrievalModeKeyword, EmbeddingProviderID: &providerID, EmbeddingModel: "BAAI/bge-m3", EmbeddingDimensions: 2}}},
		&fakeProviderRepo{items: map[int64]*providerdomain.ModelProvider{90: {ID: 90, OwnerID: 1, ProviderType: providerdomain.TypeOpenAICompatible, Status: providerdomain.StatusActive}}},
		raw,
		&fakeEmbedder{vectors: [][]float32{{0.1, 0.2}}},
		nil,
		mustSecretBox(t),
	)

	resp, err := service.Search(context.Background(), retrieval.RetrievalRequest{OwnerID: 1, KBIDs: []int64{10}, Query: "missing", TopK: 2, Mode: retrieval.ModeKeyword, CandidateK: 100})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(raw.requests) != 2 || raw.requests[1].Mode != retrieval.ModeVector || len(raw.requests[1].QueryVector) != 2 {
		t.Fatalf("fallback requests = %+v", raw.requests)
	}
	if resp.Diagnostics == nil || resp.Diagnostics.FallbackMode != retrieval.ModeVector {
		t.Fatalf("expected vector fallback diagnostics, got %+v", resp.Diagnostics)
	}
}

func TestAnalyzeRecallDetectsIncompleteHybridCoverage(t *testing.T) {
	diag := analyzeRecall(retrieval.RetrievalRequest{Mode: retrieval.ModeHybrid, TopK: 2, CandidateK: 4}, []retrieval.RetrievalResult{
		{ChunkID: 1, Score: 0.9, FinalScore: 0.9, KeywordScore: 0.9},
		{ChunkID: 2, Score: 0.8, FinalScore: 0.8, KeywordScore: 0.8},
	})
	if !diag.LowRecall || diag.Reason != "hybrid_coverage_incomplete" || diag.VectorCount != 0 || diag.KeywordCount != 2 {
		t.Fatalf("diagnostics = %+v", diag)
	}
}

func TestSearchEmbeddingProviderErrorUsesRequestedMode(t *testing.T) {
	service := NewService(
		&fakeKBRepo{items: map[int64]*knowledge.KnowledgeBase{
			10: {ID: 10, OwnerID: 1, RetrievalMode: knowledge.RetrievalModeKeyword},
		}},
		&fakeProviderRepo{},
		&fakeRawRetriever{},
		nil,
		nil,
		mustSecretBox(t),
	)

	_, err := service.Search(context.Background(), retrieval.RetrievalRequest{OwnerID: 1, KBIDs: []int64{10}, Query: "agent", Mode: retrieval.ModeVector})
	if err == nil {
		t.Fatal("Search() error = nil, want embedding provider error")
	}
	if got := err.Error(); got != "embedding provider is required for vector retrieval" {
		t.Fatalf("error = %q", got)
	}
}

type fakeKBRepo struct {
	items map[int64]*knowledge.KnowledgeBase
}

func (r *fakeKBRepo) Create(context.Context, *knowledge.KnowledgeBase) error {
	return nil
}

func (r *fakeKBRepo) ListByOwner(context.Context, int64) ([]knowledge.KnowledgeBase, error) {
	return nil, nil
}

func (r *fakeKBRepo) FindByID(_ context.Context, ownerID, id int64) (*knowledge.KnowledgeBase, error) {
	item, ok := r.items[id]
	if !ok || item.OwnerID != ownerID {
		return nil, gorm.ErrRecordNotFound
	}
	clone := *item
	return &clone, nil
}

func (r *fakeKBRepo) Update(context.Context, *knowledge.KnowledgeBase) error {
	return nil
}

func (r *fakeKBRepo) SoftDelete(context.Context, int64, int64) error {
	return nil
}

func (r *fakeKBRepo) AdjustCounts(context.Context, int64, int64, int, int) error {
	return nil
}

type fakeProviderRepo struct {
	items map[int64]*providerdomain.ModelProvider
}

func (r *fakeProviderRepo) Create(context.Context, *providerdomain.ModelProvider) error {
	return nil
}

func (r *fakeProviderRepo) ListByOwner(context.Context, int64) ([]providerdomain.ModelProvider, error) {
	return nil, nil
}

func (r *fakeProviderRepo) FindByID(_ context.Context, ownerID, id int64) (*providerdomain.ModelProvider, error) {
	item, ok := r.items[id]
	if !ok || item.OwnerID != ownerID {
		return nil, gorm.ErrRecordNotFound
	}
	clone := *item
	return &clone, nil
}

func (r *fakeProviderRepo) Update(context.Context, *providerdomain.ModelProvider) error {
	return nil
}

func (r *fakeProviderRepo) SoftDelete(context.Context, int64, int64) error {
	return nil
}

type fakeRawRetriever struct {
	request  retrieval.RetrievalRequest
	response *retrieval.RetrievalResponse
}

type sequenceRawRetriever struct {
	requests  []retrieval.RetrievalRequest
	responses []*retrieval.RetrievalResponse
	errors    []error
}

func (r *sequenceRawRetriever) Search(_ context.Context, req retrieval.RetrievalRequest) (*retrieval.RetrievalResponse, error) {
	r.requests = append(r.requests, req)
	if len(r.errors) > 0 {
		err := r.errors[0]
		r.errors = r.errors[1:]
		if err != nil {
			return nil, err
		}
	}
	if len(r.responses) == 0 {
		return &retrieval.RetrievalResponse{}, nil
	}
	resp := r.responses[0]
	r.responses = r.responses[1:]
	return resp, nil
}

func (r *fakeRawRetriever) Search(_ context.Context, req retrieval.RetrievalRequest) (*retrieval.RetrievalResponse, error) {
	r.request = req
	return r.response, nil
}

type fakeEmbedder struct {
	vectors [][]float32
}

func (e *fakeEmbedder) Embed(context.Context, llm.EmbeddingProviderConfig, llm.EmbeddingRequest) (*llm.EmbeddingResponse, error) {
	return &llm.EmbeddingResponse{Embeddings: e.vectors}, nil
}

type fakeReranker struct {
	order []int64
	err   error
}

func (r *fakeReranker) Rerank(_ context.Context, _ llm.RerankProviderConfig, req llm.RerankRequest) ([]retrieval.RetrievalResult, error) {
	if r.err != nil {
		return nil, r.err
	}
	byID := make(map[int64]retrieval.RetrievalResult, len(req.Results))
	for _, result := range req.Results {
		byID[result.ChunkID] = result
	}
	out := make([]retrieval.RetrievalResult, 0, len(req.Results))
	for _, id := range r.order {
		out = append(out, byID[id])
	}
	return out, nil
}

func mustSecretBox(t *testing.T) *cryptoinfra.SecretBox {
	t.Helper()
	box, err := cryptoinfra.NewSecretBox("01234567890123456789012345678901")
	if err != nil {
		t.Fatalf("NewSecretBox() error = %v", err)
	}
	return box
}
