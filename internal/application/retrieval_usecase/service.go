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
		return s.raw.Search(ctx, req)
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
	resp, err := s.raw.Search(ctx, req)
	if err != nil {
		return nil, err
	}
	if kb.RerankEnabled {
		if reranked, err := s.rerank(ctx, req.OwnerID, kb, req.Query, resp.Results); err == nil && len(reranked) > 0 {
			resp.Results = reranked
			if len(resp.Results) > req.TopK {
				resp.Results = resp.Results[:req.TopK]
			}
		}
	}
	return resp, nil
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
