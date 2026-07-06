package retrieval_usecase

import (
	"context"
	"fmt"
	"strings"

	"agentcanvas/internal/domain/knowledge"
	providerdomain "agentcanvas/internal/domain/provider"
	"agentcanvas/internal/domain/retrieval"
	cryptoinfra "agentcanvas/internal/infrastructure/crypto"
	"agentcanvas/internal/infrastructure/llm"

	"gorm.io/gorm"
)

const (
	defaultHybridWeight = 0.5
	defaultCandidateK   = 20
)

type Service struct {
	kbs       knowledge.KnowledgeBaseRepository
	providers providerdomain.Repository
	raw       retrieval.Retriever
	embedder  llm.EmbeddingClient
	reranker  llm.Reranker
	secrets   *cryptoinfra.SecretBox
}

func NewService(kbs knowledge.KnowledgeBaseRepository, providers providerdomain.Repository, raw retrieval.Retriever, embedder llm.EmbeddingClient, reranker llm.Reranker, secrets *cryptoinfra.SecretBox) *Service {
	return &Service{kbs: kbs, providers: providers, raw: raw, embedder: embedder, reranker: reranker, secrets: secrets}
}

func (s *Service) Search(ctx context.Context, req retrieval.RetrievalRequest) (*retrieval.RetrievalResponse, error) {
	if s.raw == nil {
		return nil, fmt.Errorf("retriever is not configured")
	}
	if req.Mode == "" {
		req.Mode = retrieval.ModeKeyword
	}
	if req.TopK <= 0 {
		req.TopK = 8
	}
	if req.CandidateK <= 0 {
		req.CandidateK = max(req.TopK*4, defaultCandidateK)
	}
	if req.Mode == retrieval.ModeKeyword {
		return s.searchWithDiagnostics(ctx, req, nil)
	}
	kb, err := s.primaryKnowledgeBase(ctx, req.OwnerID, req.KBIDs)
	if err != nil {
		return nil, err
	}
	if len(req.QueryVector) == 0 {
		vector, err := s.embedQuery(ctx, req.OwnerID, kb, req.Query, req.Mode)
		if err != nil {
			return nil, err
		}
		req.QueryVector = vector
	}
	if kb.EmbeddingDimensions > 0 && len(req.QueryVector) != kb.EmbeddingDimensions {
		return nil, fmt.Errorf("embedding dimensions mismatch: got %d, want %d", len(req.QueryVector), kb.EmbeddingDimensions)
	}
	if req.HybridWeight == 0 {
		req.HybridWeight = kb.HybridWeight
	}
	if req.HybridWeight == 0 {
		req.HybridWeight = defaultHybridWeight
	}
	resp, err := s.searchWithDiagnostics(ctx, req, nil)
	if err != nil {
		return nil, err
	}
	if kb.RerankEnabled {
		if reranked, err := s.rerank(ctx, req.OwnerID, kb, req.Query, resp.Results); err == nil && len(reranked) > 0 {
			resp.Results = reranked
			if resp.Diagnostics != nil {
				resp.Diagnostics.Reranked = true
			}
			resp.Trace = append(resp.Trace, retrieval.RetrievalTraceRecord{Stage: "rerank", Mode: req.Mode, Message: "rerank_applied", Metadata: map[string]any{"candidate_count": len(reranked)}})
			if len(resp.Results) > req.TopK {
				resp.Results = resp.Results[:req.TopK]
			}
		}
	}
	return resp, nil
}

func (s *Service) searchWithDiagnostics(ctx context.Context, req retrieval.RetrievalRequest, trace []retrieval.RetrievalTraceRecord) (*retrieval.RetrievalResponse, error) {
	resp, err := s.raw.Search(ctx, req)
	if err != nil {
		if fallbackResp, fallbackErr := s.tryFallbackSearch(ctx, req, nil, trace, "initial_recall_error"); fallbackErr == nil && fallbackResp != nil {
			return fallbackResp, nil
		}
		return nil, err
	}
	if resp == nil {
		resp = &retrieval.RetrievalResponse{}
	}
	diagnostics := analyzeRecall(req, resp.Results)
	resp.Diagnostics = diagnostics
	resp.Trace = append(trace, retrieval.RetrievalTraceRecord{Stage: "recall", Mode: req.Mode, Message: "initial_recall", Metadata: map[string]any{"result_count": len(resp.Results), "candidate_k": req.CandidateK, "keyword_count": diagnostics.KeywordCount, "vector_count": diagnostics.VectorCount}})
	if diagnostics.LowRecall && req.CandidateK < 100 {
		expandedReq := expandedRecallRequest(req)
		expandedResp, expandedErr := s.raw.Search(ctx, expandedReq)
		if expandedErr == nil && expandedResp != nil && len(expandedResp.Results) > len(resp.Results) {
			expandedDiagnostics := analyzeRecall(expandedReq, expandedResp.Results)
			expandedDiagnostics.Expanded = true
			expandedDiagnostics.ExpandedCandidate = expandedReq.CandidateK
			expandedResp.Results = truncateResults(expandedResp.Results, req.TopK)
			expandedResp.Diagnostics = expandedDiagnostics
			expandedResp.Trace = append(resp.Trace, retrieval.RetrievalTraceRecord{Stage: "low_recall_expand", Mode: req.Mode, Message: diagnostics.Reason, Metadata: map[string]any{"from_candidate_k": req.CandidateK, "to_candidate_k": expandedReq.CandidateK}})
			return expandedResp, nil
		}
		resp.Trace = append(resp.Trace, retrieval.RetrievalTraceRecord{Stage: "low_recall_expand", Mode: req.Mode, Message: "no_better_results", Metadata: map[string]any{"reason": diagnostics.Reason}})
	}
	if diagnostics.LowRecall {
		if rewriteResp, rewriteErr := s.rewriteSearch(ctx, req, resp); rewriteErr == nil && isBetterRecall(resp, rewriteResp) {
			return rewriteResp, nil
		}
		if fallbackResp, fallbackErr := s.tryFallbackSearch(ctx, req, diagnostics, resp.Trace, "low_recall"); fallbackErr == nil && isBetterRecall(resp, fallbackResp) {
			return fallbackResp, nil
		}
		resp.Trace = append(resp.Trace, retrieval.RetrievalTraceRecord{Stage: "fallback", Mode: req.Mode, Message: "no_better_results", Metadata: map[string]any{"reason": diagnostics.Reason}})
	}
	return resp, nil
}

func (s *Service) rewriteSearch(ctx context.Context, req retrieval.RetrievalRequest, current *retrieval.RetrievalResponse) (*retrieval.RetrievalResponse, error) {
	if current == nil || current.Diagnostics == nil || !current.Diagnostics.LowRecall {
		return nil, nil
	}
	variants := rewriteQueryVariants(req.Query)
	for _, variant := range variants {
		rewriteReq := req
		rewriteReq.Query = variant
		rewriteResp, err := s.raw.Search(ctx, rewriteReq)
		if err != nil || rewriteResp == nil {
			continue
		}
		rewriteDiagnostics := analyzeRecall(rewriteReq, rewriteResp.Results)
		rewriteResp.Results = truncateResults(rewriteResp.Results, req.TopK)
		rewriteResp.Diagnostics = rewriteDiagnostics
		rewriteResp.Trace = append(current.Trace, retrieval.RetrievalTraceRecord{Stage: "query_rewrite", Mode: req.Mode, Message: "low_recall_rewrite", Metadata: map[string]any{"from_query": req.Query, "to_query": variant, "result_count": len(rewriteResp.Results)}})
		if isBetterRecall(current, rewriteResp) {
			return rewriteResp, nil
		}
	}
	current.Trace = append(current.Trace, retrieval.RetrievalTraceRecord{Stage: "query_rewrite", Mode: req.Mode, Message: "no_effective_rewrite", Metadata: map[string]any{"variant_count": len(variants)}})
	return nil, nil
}

func (s *Service) tryFallbackSearch(ctx context.Context, req retrieval.RetrievalRequest, diagnostics *retrieval.RecallDiagnostics, trace []retrieval.RetrievalTraceRecord, reason string) (*retrieval.RetrievalResponse, error) {
	fallbackReq, ok, err := s.fallbackRequest(ctx, req, diagnostics)
	if !ok || err != nil {
		return nil, err
	}
	fallbackResp, err := s.raw.Search(ctx, fallbackReq)
	if err != nil {
		return nil, err
	}
	if fallbackResp == nil {
		fallbackResp = &retrieval.RetrievalResponse{}
	}
	fallbackDiagnostics := analyzeRecall(fallbackReq, fallbackResp.Results)
	fallbackDiagnostics.FallbackMode = fallbackReq.Mode
	fallbackResp.Results = truncateResults(fallbackResp.Results, req.TopK)
	fallbackResp.Diagnostics = fallbackDiagnostics
	fallbackResp.Trace = append(trace, retrieval.RetrievalTraceRecord{Stage: "fallback", Mode: fallbackReq.Mode, Message: reason, Metadata: map[string]any{"from_mode": req.Mode, "to_mode": fallbackReq.Mode, "result_count": len(fallbackResp.Results)}})
	return fallbackResp, nil
}

func (s *Service) fallbackRequest(ctx context.Context, req retrieval.RetrievalRequest, diagnostics *retrieval.RecallDiagnostics) (retrieval.RetrievalRequest, bool, error) {
	fallback := req
	switch req.Mode {
	case retrieval.ModeVector:
		fallback.Mode = retrieval.ModeKeyword
		fallback.QueryVector = nil
		return fallback, true, nil
	case retrieval.ModeHybrid:
		fallbackMode := retrieval.ModeKeyword
		if diagnostics != nil && diagnostics.VectorCount == 0 && len(req.QueryVector) > 0 {
			fallbackMode = retrieval.ModeVector
		}
		fallback.Mode = fallbackMode
		if fallback.Mode == retrieval.ModeKeyword {
			fallback.QueryVector = nil
		}
		return fallback, true, nil
	case retrieval.ModeKeyword:
		if s.embedder == nil || len(req.KBIDs) == 0 {
			return retrieval.RetrievalRequest{}, false, nil
		}
		kb, err := s.primaryKnowledgeBase(ctx, req.OwnerID, req.KBIDs)
		if err != nil || kb.EmbeddingProviderID == nil {
			return retrieval.RetrievalRequest{}, false, err
		}
		vector, err := s.embedQuery(ctx, req.OwnerID, kb, req.Query, retrieval.ModeVector)
		if err != nil {
			return retrieval.RetrievalRequest{}, false, err
		}
		if kb.EmbeddingDimensions > 0 && len(vector) != kb.EmbeddingDimensions {
			return retrieval.RetrievalRequest{}, false, fmt.Errorf("embedding dimensions mismatch: got %d, want %d", len(vector), kb.EmbeddingDimensions)
		}
		fallback.Mode = retrieval.ModeVector
		fallback.QueryVector = vector
		return fallback, true, nil
	default:
		return retrieval.RetrievalRequest{}, false, nil
	}
}

func isBetterRecall(current, candidate *retrieval.RetrievalResponse) bool {
	if candidate == nil || len(candidate.Results) == 0 {
		return false
	}
	if current == nil || len(candidate.Results) > len(current.Results) {
		return true
	}
	if current.Diagnostics == nil || candidate.Diagnostics == nil {
		return false
	}
	if current.Diagnostics.LowRecall && !candidate.Diagnostics.LowRecall {
		return true
	}
	return candidate.Diagnostics.MaxScore > current.Diagnostics.MaxScore
}

func (s *Service) primaryKnowledgeBase(ctx context.Context, ownerID int64, kbIDs []int64) (*knowledge.KnowledgeBase, error) {
	if len(kbIDs) == 0 {
		return nil, fmt.Errorf("kb_ids are required")
	}
	kb, err := s.kbs.FindByID(ctx, ownerID, kbIDs[0])
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, fmt.Errorf("knowledge base not found")
		}
		return nil, err
	}
	return kb, nil
}

func (s *Service) embedQuery(ctx context.Context, ownerID int64, kb *knowledge.KnowledgeBase, query string, mode retrieval.Mode) ([]float32, error) {
	if s.embedder == nil || kb.EmbeddingProviderID == nil {
		return nil, fmt.Errorf("embedding provider is required for %s retrieval", mode)
	}
	provider, err := s.providers.FindByID(ctx, ownerID, *kb.EmbeddingProviderID)
	if err != nil {
		return nil, err
	}
	if provider.Status != providerdomain.StatusActive {
		return nil, fmt.Errorf("embedding provider is disabled")
	}
	model := strings.TrimSpace(kb.EmbeddingModel)
	if model == "" {
		model = strings.TrimSpace(provider.DefaultEmbeddingModel)
	}
	if model == "" {
		return nil, fmt.Errorf("embedding model is required")
	}
	apiKey, err := s.secrets.Decrypt(provider.EncryptedAPIKey)
	if err != nil {
		return nil, err
	}
	resp, err := s.embedder.Embed(ctx, llm.EmbeddingProviderConfig{ProviderType: provider.ProviderType, BaseURL: provider.BaseURL, APIKey: apiKey}, llm.EmbeddingRequest{Model: model, Input: []string{query}})
	if err != nil {
		return nil, err
	}
	if len(resp.Embeddings) != 1 || len(resp.Embeddings[0]) == 0 {
		return nil, fmt.Errorf("embedding response is empty")
	}
	return resp.Embeddings[0], nil
}

func (s *Service) rerank(ctx context.Context, ownerID int64, kb *knowledge.KnowledgeBase, query string, results []retrieval.RetrievalResult) ([]retrieval.RetrievalResult, error) {
	if s.reranker == nil || kb.RerankProviderID == nil || len(results) == 0 {
		return nil, fmt.Errorf("rerank is not configured")
	}
	provider, err := s.providers.FindByID(ctx, ownerID, *kb.RerankProviderID)
	if err != nil {
		return nil, err
	}
	if provider.Status != providerdomain.StatusActive {
		return nil, fmt.Errorf("rerank provider is disabled")
	}
	model := strings.TrimSpace(kb.RerankModel)
	if model == "" {
		model = strings.TrimSpace(provider.DefaultChatModel)
	}
	if model == "" {
		return nil, fmt.Errorf("rerank model is required")
	}
	apiKey, err := s.secrets.Decrypt(provider.EncryptedAPIKey)
	if err != nil {
		return nil, err
	}
	return s.reranker.Rerank(ctx, llm.RerankProviderConfig{ProviderType: provider.ProviderType, BaseURL: provider.BaseURL, APIKey: apiKey}, llm.RerankRequest{Model: model, Query: query, Results: results})
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
