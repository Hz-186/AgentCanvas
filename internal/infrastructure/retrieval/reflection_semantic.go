package retrieval

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	providerdomain "agentcanvas/internal/domain/provider"
	"agentcanvas/internal/domain/reflection"
	cryptoinfra "agentcanvas/internal/infrastructure/crypto"
	"agentcanvas/internal/infrastructure/llm"
	"agentcanvas/internal/infrastructure/vectorstore"
)

const reflectionCollectionPrefix = "agentcanvas_reflections_v1"

type ReflectionSemanticIndex struct {
	Store       vectorstore.Store
	Embedder    llm.EmbeddingClient
	Providers   providerdomain.Repository
	Secrets     *cryptoinfra.SecretBox
	Reflections reflection.Repository
	HNSW        vectorstore.HNSWConfig
	collections sync.Map
}

func NewReflectionSemanticIndex(store vectorstore.Store, embedder llm.EmbeddingClient, providers providerdomain.Repository, secrets *cryptoinfra.SecretBox, reflections reflection.Repository, hnsw vectorstore.HNSWConfig) *ReflectionSemanticIndex {
	return &ReflectionSemanticIndex{Store: store, Embedder: embedder, Providers: providers, Secrets: secrets, Reflections: reflections, HNSW: vectorstore.NormalizeHNSWConfig(hnsw)}
}

func (s *ReflectionSemanticIndex) Index(ctx context.Context, item reflection.Reflection) error {
	if s == nil || s.Store == nil || item.ID <= 0 || item.EmbeddingProviderID <= 0 {
		return nil
	}
	vector, model, err := s.embed(ctx, item.OwnerID, item.EmbeddingProviderID, item.EmbeddingModel, reflectionText(item))
	if err != nil {
		return err
	}
	collection := reflectionCollection(len(vector))
	if err := s.Store.EnsureCollection(ctx, collection, len(vector), s.HNSW); err != nil {
		return err
	}
	s.collections.Store(collection, struct{}{})
	return s.Store.Upsert(ctx, collection, []vectorstore.VectorDocument{{ID: strconv.FormatInt(item.ID, 10), Vector: vector, Metadata: map[string]any{
		"owner_id": item.OwnerID, "agent_id": item.AgentID, "scope": item.Scope,
		"mode": item.Mode, "status": item.Status, "embedding_model": model, "embedding_dimensions": len(vector),
	}}})
}

func (s *ReflectionSemanticIndex) Search(ctx context.Context, req reflection.SearchRequest) ([]reflection.SearchResult, error) {
	if s == nil || s.Store == nil || s.Reflections == nil || req.OwnerID <= 0 || req.EmbeddingProviderID <= 0 || strings.TrimSpace(req.Task) == "" {
		return nil, nil
	}
	vector, _, err := s.embed(ctx, req.OwnerID, req.EmbeddingProviderID, req.EmbeddingModel, req.Task)
	if err != nil {
		return nil, err
	}
	collection := reflectionCollection(len(vector))
	limit := req.TopK
	if limit <= 0 {
		limit = 12
	}
	filter := map[string]any{"owner_id": req.OwnerID}
	if req.AgentID > 0 {
		if req.IncludeGlobal {
			filter["agent_id"] = []int64{0, req.AgentID}
		} else {
			filter["agent_id"] = req.AgentID
		}
	}
	items, err := s.Store.Search(ctx, vectorstore.SearchRequest{Collection: collection, Vector: vector, TopK: limit * 5, Filter: filter, HNSW: s.HNSW})
	if err != nil {
		return nil, err
	}
	results := make([]reflection.SearchResult, 0, limit)
	for _, hit := range items {
		id, parseErr := strconv.ParseInt(hit.ID, 10, 64)
		if parseErr != nil {
			continue
		}
		item, findErr := s.Reflections.FindByID(ctx, req.OwnerID, id)
		if findErr != nil || item == nil || !reflectionMatches(*item, req.CandidateQuery) {
			continue
		}
		quality := .10*item.Importance + .08*item.Confidence
		denom := item.SuccessfulUseCount + item.HarmfulCount
		if denom > 0 {
			quality += .07 * float64(item.SuccessfulUseCount) / float64(denom)
		}
		results = append(results, reflection.SearchResult{Reflection: *item, Score: .75*hit.Score + quality})
		if len(results) >= limit {
			break
		}
	}
	return results, nil
}

func (s *ReflectionSemanticIndex) Delete(ctx context.Context, id int64) error {
	if s == nil || s.Store == nil || id <= 0 {
		return nil
	}
	var firstErr error
	s.collections.Range(func(key, _ any) bool {
		if err := s.Store.Delete(ctx, key.(string), []string{strconv.FormatInt(id, 10)}); err != nil && firstErr == nil {
			firstErr = err
		}
		return true
	})
	return firstErr
}

func (s *ReflectionSemanticIndex) embed(ctx context.Context, ownerID, providerID int64, requestedModel, text string) ([]float32, string, error) {
	if s.Embedder == nil || s.Providers == nil || s.Secrets == nil {
		return nil, "", fmt.Errorf("reflection embedding dependencies are not configured")
	}
	provider, err := s.Providers.FindByID(ctx, ownerID, providerID)
	if err != nil {
		return nil, "", err
	}
	if provider.Status != providerdomain.StatusActive {
		return nil, "", fmt.Errorf("reflection embedding provider is disabled")
	}
	model := strings.TrimSpace(requestedModel)
	if model == "" {
		model = strings.TrimSpace(provider.DefaultEmbeddingModel)
	}
	if model == "" {
		return nil, "", fmt.Errorf("reflection embedding model is required")
	}
	apiKey, err := s.Secrets.Decrypt(provider.EncryptedAPIKey)
	if err != nil {
		return nil, "", err
	}
	response, err := s.Embedder.Embed(ctx, llm.EmbeddingProviderConfig{ProviderType: provider.ProviderType, BaseURL: provider.BaseURL, APIKey: apiKey}, llm.EmbeddingRequest{Model: model, Input: []string{text}})
	if err != nil {
		return nil, "", err
	}
	if response == nil || len(response.Embeddings) != 1 || len(response.Embeddings[0]) == 0 {
		return nil, "", fmt.Errorf("reflection embedding response is empty")
	}
	return response.Embeddings[0], model, nil
}

func reflectionCollection(dimensions int) string {
	return fmt.Sprintf("%s_%d", reflectionCollectionPrefix, dimensions)
}

func reflectionText(item reflection.Reflection) string {
	return strings.Join([]string{item.TaskSummary, item.RootCauseCategory, item.RootCause, item.Lesson, item.CorrectiveAction, item.Applicability}, "\n")
}

func reflectionMatches(item reflection.Reflection, query reflection.CandidateQuery) bool {
	if item.DeletedAt != nil || (item.Status != reflection.StatusActive && item.Status != reflection.StatusValidated) || (item.ExpiresAt != nil && !item.ExpiresAt.After(time.Now().UTC())) {
		return false
	}
	if query.Mode != "" && item.Mode != "" && item.Mode != query.Mode {
		return false
	}
	if query.AgentID > 0 {
		if item.AgentID == query.AgentID {
			return true
		}
		return query.IncludeGlobal && item.Scope == reflection.ScopeGlobal && item.Status == reflection.StatusValidated
	}
	return query.IncludeGlobal && item.Scope == reflection.ScopeGlobal && item.Status == reflection.StatusValidated
}

var _ reflection.SearchIndex = (*ReflectionSemanticIndex)(nil)
