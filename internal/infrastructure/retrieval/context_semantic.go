package retrieval

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"agentcanvas/internal/domain/contextresource"
	providerdomain "agentcanvas/internal/domain/provider"
	cryptoinfra "agentcanvas/internal/infrastructure/crypto"
	"agentcanvas/internal/infrastructure/llm"
	"agentcanvas/internal/infrastructure/vectorstore"
)

const (
	contextResourceCollectionPrefix  = "agentcanvas_context_resources_v1"
	contextResourceKeywordCollection = contextResourceCollectionPrefix + "_keyword"
)

type ContextSemanticIndex struct {
	Store             vectorstore.Store
	Embedder          llm.EmbeddingClient
	Providers         providerdomain.Repository
	Secrets           *cryptoinfra.SecretBox
	DefaultProviderID int64
	DefaultModel      string
	HNSW              vectorstore.HNSWConfig
}

func NewContextSemanticIndex(
	store vectorstore.Store,
	embedder llm.EmbeddingClient,
	providers providerdomain.Repository,
	secrets *cryptoinfra.SecretBox,
	defaultProviderID int64,
	defaultModel string,
	hnsw vectorstore.HNSWConfig,
) *ContextSemanticIndex {
	return &ContextSemanticIndex{
		Store:             store,
		Embedder:          embedder,
		Providers:         providers,
		Secrets:           secrets,
		DefaultProviderID: defaultProviderID,
		DefaultModel:      strings.TrimSpace(defaultModel),
		HNSW:              vectorstore.NormalizeHNSWConfig(hnsw),
	}
}

func (s *ContextSemanticIndex) Upsert(ctx context.Context, document contextresource.Document, profile contextresource.EmbeddingProfile) (contextresource.EmbeddingProfile, error) {
	if s == nil || s.Store == nil {
		return profile, fmt.Errorf("context semantic index is not configured")
	}
	vector, profile, err := s.embed(ctx, document.OwnerID, profile, document.Content)
	if err != nil {
		return profile, err
	}
	collection := contextResourceCollection(profile)
	if err := s.Store.EnsureCollection(ctx, collection, len(vector), s.HNSW); err != nil {
		return profile, err
	}
	metadata := cloneContextMetadata(document.Metadata)
	metadata["owner_id"] = document.OwnerID
	metadata["agent_id"] = document.AgentID
	metadata["conversation_id"] = document.ConversationID
	metadata["resource_type"] = document.ResourceType
	metadata["resource_id"] = document.ResourceID
	metadata["content_hash"] = document.ContentHash
	metadata["embedding_profile_hash"] = profile.Hash
	vectorDocument := vectorstore.VectorDocument{
		ID:       contextresource.DocumentID(document.OwnerID, document.ResourceType, document.ResourceID),
		Text:     document.Content,
		Vector:   vector,
		Metadata: metadata,
	}
	err = s.Store.Upsert(ctx, collection, []vectorstore.VectorDocument{vectorDocument})
	if err != nil {
		return profile, err
	}
	if _, supportsText := s.Store.(interface {
		SearchText(context.Context, vectorstore.SearchRequest) ([]vectorstore.SearchResult, error)
	}); supportsText {
		if err := s.Store.EnsureCollection(ctx, contextResourceKeywordCollection, 0, s.HNSW); err != nil {
			return profile, err
		}
		return profile, s.Store.Upsert(ctx, contextResourceKeywordCollection, []vectorstore.VectorDocument{{
			ID:       vectorDocument.ID,
			Text:     vectorDocument.Text,
			Metadata: vectorDocument.Metadata,
		}})
	}
	return profile, nil
}

func (s *ContextSemanticIndex) Delete(ctx context.Context, item contextresource.OutboxItem) error {
	if s == nil || s.Store == nil {
		return nil
	}
	profile := (contextresource.EmbeddingProfile{ProviderID: item.EmbeddingProviderID, Model: item.EmbeddingModel, Dimensions: item.EmbeddingDimensions, Hash: item.EmbeddingProfileHash}).Normalized()
	if profile.Dimensions <= 0 || profile.Hash == "" {
		// No completed upsert exists for this resource/profile, so there is no
		// addressable vector to remove.
		return nil
	}
	id := contextresource.DocumentID(item.OwnerID, item.ResourceType, item.ResourceID)
	if err := s.Store.Delete(ctx, contextResourceCollection(profile), []string{id}); err != nil {
		return err
	}
	if _, supportsText := s.Store.(interface {
		SearchText(context.Context, vectorstore.SearchRequest) ([]vectorstore.SearchResult, error)
	}); supportsText {
		return s.Store.Delete(ctx, contextResourceKeywordCollection, []string{id})
	}
	return nil
}

func (s *ContextSemanticIndex) Search(ctx context.Context, request contextresource.SearchRequest) ([]contextresource.SearchResult, error) {
	if s == nil || s.Store == nil || request.OwnerID <= 0 || strings.TrimSpace(request.Query) == "" {
		return nil, nil
	}
	limit := request.TopK
	if limit <= 0 {
		limit = 12
	}
	filter := contextScopeFilter(request)
	mode := strings.ToLower(strings.TrimSpace(request.Mode))
	switch mode {
	case "keyword":
		return s.searchKeyword(ctx, request, filter, limit)
	case "", "vector", "hybrid":
	default:
		return nil, fmt.Errorf("unsupported context retrieval mode: %s", request.Mode)
	}
	vector, profile, err := s.embed(ctx, request.OwnerID, request.Profile, request.Query)
	if err != nil {
		return nil, err
	}
	vectorHits, err := s.Store.Search(ctx, vectorstore.SearchRequest{Collection: contextResourceCollection(profile), Vector: vector, TopK: limit * 4, Filter: filter, HNSW: s.HNSW})
	if err != nil {
		return nil, err
	}
	vectorResults := s.contextResults(request, vectorHits, limit*4)
	if mode != "hybrid" {
		if len(vectorResults) > limit {
			vectorResults = vectorResults[:limit]
		}
		return vectorResults, nil
	}
	keywordResults, keywordErr := s.searchKeyword(ctx, request, filter, limit*4)
	if keywordErr != nil {
		return nil, keywordErr
	}
	return fuseContextResults(keywordResults, vectorResults, limit), nil
}

func (s *ContextSemanticIndex) searchKeyword(ctx context.Context, request contextresource.SearchRequest, filter map[string]any, limit int) ([]contextresource.SearchResult, error) {
	searcher, ok := s.Store.(interface {
		SearchText(context.Context, vectorstore.SearchRequest) ([]vectorstore.SearchResult, error)
	})
	if !ok {
		return nil, fmt.Errorf("context keyword search is not configured")
	}
	hits, err := searcher.SearchText(ctx, vectorstore.SearchRequest{Collection: contextResourceKeywordCollection, QueryText: request.Query, TopK: limit, Filter: filter})
	if err != nil {
		return nil, err
	}
	return s.contextResults(request, hits, limit), nil
}

func (s *ContextSemanticIndex) contextResults(request contextresource.SearchRequest, hits []vectorstore.SearchResult, limit int) []contextresource.SearchResult {
	results := make([]contextresource.SearchResult, 0, limit)
	for _, hit := range hits {
		agentID := contextMetadataInt64(hit.Metadata["agent_id"])
		conversationID := contextMetadataInt64(hit.Metadata["conversation_id"])
		resourceType, _ := hit.Metadata["resource_type"].(string)
		resourceID := fmt.Sprint(hit.Metadata["resource_id"])
		if resourceType == "" || resourceID == "" {
			continue
		}
		if resourceType == contextresource.TypeConversationMessage {
			if request.AgentID <= 0 || request.ConversationID <= 0 || agentID != request.AgentID || conversationID != request.ConversationID {
				continue
			}
		} else {
			if request.AgentID > 0 && agentID != 0 && agentID != request.AgentID {
				continue
			}
			if request.ConversationID > 0 && conversationID != 0 && conversationID != request.ConversationID {
				continue
			}
		}
		results = append(results, contextresource.SearchResult{ResourceType: resourceType, ResourceID: resourceID, Score: hit.Score, Metadata: hit.Metadata})
		if len(results) >= limit {
			break
		}
	}
	return results
}

// contextScopeFilter is deliberately shared by the semantic and keyword
// implementations' contract: global resources use the zero sentinel while
// conversation messages must match both dimensions exactly.
func contextScopeFilter(request contextresource.SearchRequest) map[string]any {
	filter := map[string]any{"owner_id": request.OwnerID}
	if len(request.ResourceTypes) == 1 {
		filter["resource_type"] = request.ResourceTypes[0]
	} else if len(request.ResourceTypes) > 1 {
		filter["resource_type"] = request.ResourceTypes
	}
	messageOnly := len(request.ResourceTypes) == 1 && request.ResourceTypes[0] == contextresource.TypeConversationMessage
	if request.AgentID > 0 {
		if messageOnly {
			filter["agent_id"] = request.AgentID
		} else {
			filter["agent_id"] = []int64{0, request.AgentID}
		}
	}
	if request.ConversationID > 0 {
		if messageOnly {
			filter["conversation_id"] = request.ConversationID
		} else {
			filter["conversation_id"] = []int64{0, request.ConversationID}
		}
	}
	return filter
}

func (s *ContextSemanticIndex) embed(ctx context.Context, ownerID int64, requested contextresource.EmbeddingProfile, text string) ([]float32, contextresource.EmbeddingProfile, error) {
	if s.Embedder == nil || s.Providers == nil || s.Secrets == nil {
		return nil, requested, fmt.Errorf("context embedding dependencies are not configured")
	}
	providerID := requested.ProviderID
	if providerID <= 0 {
		providerID = s.DefaultProviderID
	}
	if providerID <= 0 {
		return nil, requested, fmt.Errorf("context embedding provider is not configured")
	}
	provider, err := s.Providers.FindByID(ctx, ownerID, providerID)
	if err != nil {
		return nil, requested, err
	}
	if provider.Status != providerdomain.StatusActive {
		return nil, requested, fmt.Errorf("context embedding provider is disabled")
	}
	model := strings.TrimSpace(requested.Model)
	if model == "" {
		model = s.DefaultModel
	}
	if model == "" {
		model = strings.TrimSpace(provider.DefaultEmbeddingModel)
	}
	if model == "" {
		return nil, requested, fmt.Errorf("context embedding model is not configured")
	}
	apiKey, err := s.Secrets.Decrypt(provider.EncryptedAPIKey)
	if err != nil {
		return nil, requested, err
	}
	response, err := s.Embedder.Embed(ctx, llm.EmbeddingProviderConfig{ProviderType: provider.ProviderType, BaseURL: provider.BaseURL, APIKey: apiKey}, llm.EmbeddingRequest{Model: model, Input: []string{text}})
	if err != nil {
		return nil, requested, err
	}
	if response == nil || len(response.Embeddings) != 1 || len(response.Embeddings[0]) == 0 {
		return nil, requested, fmt.Errorf("context embedding response is empty")
	}
	profile := (contextresource.EmbeddingProfile{ProviderID: providerID, Model: model, Dimensions: len(response.Embeddings[0])}).Normalized()
	if requested.Dimensions > 0 && requested.Dimensions != profile.Dimensions {
		return nil, requested, fmt.Errorf("context embedding dimensions changed: expected=%d actual=%d", requested.Dimensions, profile.Dimensions)
	}
	if requested.Hash != "" && requested.Dimensions > 0 && requested.Hash != profile.Hash {
		return nil, requested, fmt.Errorf("context embedding profile changed")
	}
	return response.Embeddings[0], profile, nil
}

func contextResourceCollection(profile contextresource.EmbeddingProfile) string {
	profile = profile.Normalized()
	hash := profile.Hash
	if len(hash) > 12 {
		hash = hash[:12]
	}
	return fmt.Sprintf("%s_%d_%s", contextResourceCollectionPrefix, profile.Dimensions, hash)
}

func cloneContextMetadata(source map[string]any) map[string]any {
	result := make(map[string]any, len(source)+8)
	for key, value := range source {
		result[key] = value
	}
	return result
}

func contextMetadataInt64(value any) int64 {
	switch typed := value.(type) {
	case int64:
		return typed
	case int:
		return int64(typed)
	case float64:
		return int64(typed)
	case string:
		parsed, _ := strconv.ParseInt(typed, 10, 64)
		return parsed
	default:
		return 0
	}
}

var _ contextresource.Index = (*ContextSemanticIndex)(nil)
