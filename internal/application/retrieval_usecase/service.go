package retrieval_usecase

import (
	"context"
	"fmt"
	"strings"

	"agentcanvas/internal/domain/knowledge"
	providerdomain "agentcanvas/internal/domain/provider"
	"agentcanvas/internal/domain/retrieval"
	"agentcanvas/internal/domain/retrieval/fusion"
	"agentcanvas/internal/infrastructure/llm"

	"gorm.io/gorm"
)

const (
	defaultVectorWeight = 0.5
	defaultCandidateK   = 20
)

type Service struct {
	kbs           knowledge.KnowledgeBaseRepository
	providers     providerdomain.Repository
	raw           retrieval.Retriever
	backends      map[string]retrieval.Retriever
	embedder      llm.EmbeddingClient
	reranker      llm.Reranker
	secrets       providerdomain.SecretCodec
	rewriter      QueryRewriter
	recordMetrics func(lowRecall, clarification, rewrite bool)
}

func NewService(kbs knowledge.KnowledgeBaseRepository, providers providerdomain.Repository, raw retrieval.Retriever, embedder llm.EmbeddingClient, reranker llm.Reranker, secrets providerdomain.SecretCodec) *Service {
	return &Service{kbs: kbs, providers: providers, raw: raw, embedder: embedder, reranker: reranker, secrets: secrets}
}

func (s *Service) WithQueryRewriter(rewriter QueryRewriter) *Service {
	s.rewriter = rewriter
	return s
}

func (s *Service) WithBackends(backends map[string]retrieval.Retriever) *Service {
	s.backends = make(map[string]retrieval.Retriever, len(backends))
	for name, backend := range backends {
		if backend != nil {
			s.backends[strings.TrimSpace(name)] = backend
		}
	}
	return s
}

func (s *Service) WithMetrics(record func(bool, bool, bool)) *Service {
	s.recordMetrics = record
	return s
}

func (s *Service) PlanQuery(ctx context.Context, req retrieval.RetrievalRequest) (retrieval.QueryPlan, error) {
	plan := BuildQueryPlan(req.Query, req.Conversation)
	if plan.NeedsClarification && s.rewriter != nil && req.RewriteProviderID > 0 && !plan.RewriteInvoked {
		rewritten, err := s.rewriter.Rewrite(ctx, QueryRewriteRequest{OwnerID: req.OwnerID, ProviderID: req.RewriteProviderID, Model: req.RewriteModel, Plan: plan, Conversation: req.Conversation, Reason: "ambiguous_reference"})
		if err == nil {
			applyRewrite(&plan, rewritten)
		}
	}
	return plan, nil
}

func (s *Service) Search(ctx context.Context, req retrieval.RetrievalRequest) (response *retrieval.RetrievalResponse, err error) {
	defer func() {
		if response == nil {
			return
		}
		lowRecall := response.Diagnostics != nil && response.Diagnostics.LowRecall
		clarification := response.Clarification != nil && response.Clarification.Required
		rewrite := response.QueryPlan != nil && response.QueryPlan.RewriteInvoked
		if s.recordMetrics != nil {
			s.recordMetrics(lowRecall, clarification, rewrite)
		}
	}()
	if s.raw == nil && len(s.backends) == 0 {
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
	plan, _ := s.PlanQuery(ctx, req)
	if plan.NeedsClarification {
		return &retrieval.RetrievalResponse{
			QueryPlan:     &plan,
			Clarification: &retrieval.Clarification{Required: true, Question: plan.ClarificationQuestion, References: plan.UnresolvedReferences},
			Trace:         []retrieval.RetrievalTraceRecord{{Stage: "query_clarification", Message: "ambiguous_reference"}},
		}, nil
	}
	if strings.TrimSpace(plan.PreciseQuery) != "" {
		req.Query = plan.PreciseQuery
	}
	if req.Mode == retrieval.ModeKeyword {
		resp, err := s.searchWithDiagnostics(ctx, req, nil, &plan)
		if resp != nil {
			resp.QueryPlan = &plan
		}
		return resp, err
	}
	kb, err := s.primaryKnowledgeBase(ctx, req.OwnerID, req.KnowledgeBaseIDs)
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
	req.EmbeddingProfile = kb.EmbeddingProfile().Key()
	if req.VectorWeight == 0 {
		req.VectorWeight = kb.VectorWeight
	}
	if req.VectorWeight == 0 {
		req.VectorWeight = defaultVectorWeight
	}
	resp, err := s.searchWithDiagnostics(ctx, req, nil, &plan)
	if err != nil {
		return nil, err
	}
	resp.QueryPlan = &plan
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

func (s *Service) searchWithDiagnostics(ctx context.Context, req retrieval.RetrievalRequest, trace []retrieval.RetrievalTraceRecord, plan *retrieval.QueryPlan) (*retrieval.RetrievalResponse, error) {
	backend, err := s.backendFor(ctx, req)
	if err != nil {
		return nil, err
	}
	resp, err := backend.Search(ctx, req)
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
		expandedResp, expandedErr := backend.Search(ctx, expandedReq)
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
		if rewriteResp, rewriteErr := s.rewriteSearch(ctx, req, resp, plan); rewriteErr == nil && isBetterRecall(resp, rewriteResp) {
			return rewriteResp, nil
		}
		if fallbackResp, fallbackErr := s.tryFallbackSearch(ctx, req, diagnostics, resp.Trace, "low_recall"); fallbackErr == nil && isBetterRecall(resp, fallbackResp) {
			return fallbackResp, nil
		}
		resp.Trace = append(resp.Trace, retrieval.RetrievalTraceRecord{Stage: "fallback", Mode: req.Mode, Message: "no_better_results", Metadata: map[string]any{"reason": diagnostics.Reason}})
	}
	return resp, nil
}

func (s *Service) rewriteSearch(ctx context.Context, req retrieval.RetrievalRequest, current *retrieval.RetrievalResponse, plan *retrieval.QueryPlan) (*retrieval.RetrievalResponse, error) {
	if current == nil || current.Diagnostics == nil || !current.Diagnostics.LowRecall {
		return nil, nil
	}
	if plan == nil {
		fallback := BuildQueryPlan(req.Query, req.Conversation)
		plan = &fallback
	}
	if s.rewriter != nil && req.RewriteProviderID > 0 && !plan.RewriteInvoked {
		if rewritten, err := s.rewriter.Rewrite(ctx, QueryRewriteRequest{OwnerID: req.OwnerID, ProviderID: req.RewriteProviderID, Model: req.RewriteModel, Plan: *plan, Conversation: req.Conversation, Reason: current.Diagnostics.Reason}); err == nil {
			applyRewrite(plan, rewritten)
		}
	}
	variants := appendUnique(rewriteQueryVariants(req.Query), sortedPlanQueries(*plan)...)
	variants = limitStrings(variants, 10)
	responses := [][]retrieval.RetrievalResult{append([]retrieval.RetrievalResult(nil), current.Results...)}
	for _, variant := range variants {
		if strings.EqualFold(strings.TrimSpace(variant), strings.TrimSpace(req.Query)) {
			continue
		}
		rewriteReq := req
		rewriteReq.Query = variant
		if rewriteReq.Mode != retrieval.ModeKeyword {
			rewriteReq.QueryVector = nil
			kb, err := s.primaryKnowledgeBase(ctx, rewriteReq.OwnerID, rewriteReq.KnowledgeBaseIDs)
			if err != nil {
				continue
			}
			vector, err := s.embedQuery(ctx, rewriteReq.OwnerID, kb, variant, rewriteReq.Mode)
			if err != nil {
				continue
			}
			rewriteReq.QueryVector = vector
		}
		backend, backendErr := s.backendFor(ctx, rewriteReq)
		if backendErr != nil {
			continue
		}
		rewriteResp, err := backend.Search(ctx, rewriteReq)
		if err != nil || rewriteResp == nil {
			continue
		}
		responses = append(responses, rewriteResp.Results)
		current.Trace = append(current.Trace, retrieval.RetrievalTraceRecord{Stage: "query_rewrite", Mode: req.Mode, Message: "low_recall_rewrite", Metadata: map[string]any{"from_query": req.Query, "to_query": variant, "result_count": len(rewriteResp.Results)}})
	}
	if len(responses) <= 1 {
		current.Trace = append(current.Trace, retrieval.RetrievalTraceRecord{Stage: "query_rewrite", Mode: req.Mode, Message: "no_effective_rewrite", Metadata: map[string]any{"variant_count": len(variants)}})
		return nil, nil
	}
	fused := fusion.RRFRetrievalResults(responses, fusion.DefaultRankConstant, req.CandidateK, req.TopK)
	return &retrieval.RetrievalResponse{
		Results:     fused,
		Diagnostics: analyzeRecall(req, fused),
		Trace:       append(current.Trace, retrieval.RetrievalTraceRecord{Stage: "rrf_fusion", Mode: req.Mode, Message: "multi_query_fused", Metadata: map[string]any{"query_count": len(responses), "result_count": len(fused), "rrf_k": 60}}),
		QueryPlan:   plan,
	}, nil
}

func (s *Service) tryFallbackSearch(ctx context.Context, req retrieval.RetrievalRequest, diagnostics *retrieval.RecallDiagnostics, trace []retrieval.RetrievalTraceRecord, reason string) (*retrieval.RetrievalResponse, error) {
	fallbackReq, ok, err := s.fallbackRequest(ctx, req, diagnostics)
	if !ok || err != nil {
		return nil, err
	}
	backend, err := s.backendFor(ctx, fallbackReq)
	if err != nil {
		return nil, err
	}
	fallbackResp, err := backend.Search(ctx, fallbackReq)
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
		if s.embedder == nil || len(req.KnowledgeBaseIDs) == 0 {
			return retrieval.RetrievalRequest{}, false, nil
		}
		kb, err := s.primaryKnowledgeBase(ctx, req.OwnerID, req.KnowledgeBaseIDs)
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
		fallback.EmbeddingProfile = kb.EmbeddingProfile().Key()
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
		return nil, fmt.Errorf("knowledge_base_ids are required")
	}
	kb, err := s.kbs.FindByID(ctx, ownerID, kbIDs[0])
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, fmt.Errorf("knowledge base not found")
		}
		return nil, err
	}
	profile := kb.EmbeddingProfile()
	for _, id := range kbIDs[1:] {
		candidate, err := s.kbs.FindByID(ctx, ownerID, id)
		if err != nil {
			return nil, err
		}
		if candidate.EmbeddingProfile() != profile {
			return nil, fmt.Errorf("knowledge bases use incompatible embedding profiles")
		}
		if strings.TrimSpace(candidate.RetrievalBackend) != strings.TrimSpace(kb.RetrievalBackend) {
			return nil, fmt.Errorf("knowledge bases use different retrieval backends")
		}
	}
	return kb, nil
}

func (s *Service) backendFor(ctx context.Context, req retrieval.RetrievalRequest) (retrieval.Retriever, error) {
	if len(req.KnowledgeBaseIDs) == 0 {
		if s.raw == nil {
			return nil, fmt.Errorf("retriever is not configured")
		}
		return s.raw, nil
	}
	kb, err := s.kbs.FindByID(ctx, req.OwnerID, req.KnowledgeBaseIDs[0])
	if err != nil {
		return nil, err
	}
	backendName := strings.TrimSpace(kb.RetrievalBackend)
	for _, id := range req.KnowledgeBaseIDs[1:] {
		candidate, findErr := s.kbs.FindByID(ctx, req.OwnerID, id)
		if findErr != nil {
			return nil, findErr
		}
		if strings.TrimSpace(candidate.RetrievalBackend) != backendName {
			return nil, fmt.Errorf("knowledge bases use different retrieval backends")
		}
	}
	backend := s.backends[backendName]
	if len(s.backends) == 0 && backendName == "" {
		return s.raw, nil
	}
	if backend == nil {
		return nil, fmt.Errorf("retriever for knowledge base backend %q is not configured", backendName)
	}
	return backend, nil
}

func (s *Service) embedQuery(ctx context.Context, ownerID int64, kb *knowledge.KnowledgeBase, query string, mode retrieval.Mode) ([]float32, error) {
	if s.embedder == nil || kb.EmbeddingProviderID == nil {
		return nil, fmt.Errorf("embedding provider is required for %s retrieval", mode)
	}
	provider, err := s.providers.FindByID(ctx, ownerID, *kb.EmbeddingProviderID)
	if err != nil {
		return nil, err
	}
	if !provider.Enabled {
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
	if !provider.Enabled {
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
