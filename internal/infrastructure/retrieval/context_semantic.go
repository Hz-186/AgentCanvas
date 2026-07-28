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

const contextResourceCollectionPrefix = "agentcanvas_context_resources_v1"

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
	metadata["workflow_id"] = document.WorkflowID
	metadata["conversation_id"] = document.ConversationID
	metadata["resource_type"] = document.ResourceType
	metadata["resource_id"] = document.ResourceID
	metadata["content_hash"] = document.ContentHash
	metadata["embedding_profile_hash"] = profile.Hash
	err = s.Store.Upsert(ctx, collection, []vectorstore.VectorDocument{
		{
			ID:       contextresource.DocumentID(document.OwnerID, document.ResourceType, document.ResourceID),
			Vector:   vector,
			Metadata: metadata,
		},
	})
	return profile, err
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
	return s.Store.Delete(ctx, contextResourceCollection(profile), []string{contextresource.DocumentID(item.OwnerID, item.ResourceType, item.ResourceID)})
}

func (s *ContextSemanticIndex) Search(ctx context.Context, request contextresource.SearchRequest) ([]contextresource.SearchResult, error) {
	if s == nil || s.Store == nil || request.OwnerID <= 0 || strings.TrimSpace(request.Query) == "" {
		return nil, nil
	}
	vector, profile, err := s.embed(ctx, request.OwnerID, request.Profile, request.Query)
	if err != nil {
		return nil, err
	}
	limit := request.TopK
	if limit <= 0 {
		limit = 12
	}
	filter := map[string]any{"owner_id": request.OwnerID}
	if len(request.ResourceTypes) == 1 {
		filter["resource_type"] = request.ResourceTypes[0]
	} else if len(request.ResourceTypes) > 1 {
		filter["resource_type"] = request.ResourceTypes
	}
	hits, err := s.Store.Search(ctx, vectorstore.SearchRequest{
		Collection: contextResourceCollection(profile),
		Vector:     vector,
		TopK:       limit * 4,
		Filter:     filter,
		HNSW:       s.HNSW,
	})
	if err != nil {
		return nil, err
	}
	results := make([]contextresource.SearchResult, 0, limit)
	for _, hit := range hits {
		workflowID := contextMetadataInt64(hit.Metadata["workflow_id"])
		conversationID := contextMetadataInt64(hit.Metadata["conversation_id"])
		if request.WorkflowID > 0 && workflowID != 0 && workflowID != request.WorkflowID {
			continue
		}
		if request.ConversationID > 0 && conversationID != request.ConversationID {
			continue
		}
		resourceType, _ := hit.Metadata["resource_type"].(string)
		resourceID := fmt.Sprint(hit.Metadata["resource_id"])
		if resourceType == "" || resourceID == "" {
			continue
		}
		results = append(results, contextresource.SearchResult{ResourceType: resourceType, ResourceID: resourceID, Score: hit.Score, Metadata: hit.Metadata})
		if len(results) >= limit {
			break
		}
	}
	return results, nil
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
