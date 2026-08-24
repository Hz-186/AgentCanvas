package milvus

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"agentcanvas/internal/domain/retrieval"
	"agentcanvas/internal/domain/retrieval/fusion"
	"agentcanvas/internal/infrastructure/vectorstore"
)

type Store struct {
	store      vectorstore.Store
	collection string
	dimensions int
	hnsw       vectorstore.HNSWConfig
}

func NewStore(store vectorstore.Store, collection string, dimensions int, hnsw vectorstore.HNSWConfig) *Store {
	collection = strings.TrimSpace(collection)
	if collection == "" {
		collection = "agentcanvas_chunks_v2"
	}
	return &Store{store: store, collection: collection, dimensions: dimensions, hnsw: vectorstore.NormalizeHNSWConfig(hnsw)}
}

func (s *Store) EnsureIndex(ctx context.Context) error {
	if s == nil || s.store == nil {
		return nil
	}
	return s.store.EnsureCollection(ctx, s.collection, s.dimensions, s.hnsw)
}

func (s *Store) IndexChunks(ctx context.Context, docs []retrieval.ChunkIndexDocument) error {
	if s == nil || s.store == nil {
		return nil
	}
	vectors := make([]vectorstore.VectorDocument, 0, len(docs))
	for _, doc := range docs {
		if len(doc.EmbeddingVector) > 0 {
			if s.dimensions <= 0 {
				s.dimensions = len(doc.EmbeddingVector)
			} else if len(doc.EmbeddingVector) != s.dimensions {
				return fmt.Errorf("milvus vector dimensions mismatch: got %d, want %d", len(doc.EmbeddingVector), s.dimensions)
			}
		}
		hasVector := len(doc.EmbeddingVector) > 0
		metadata := map[string]any{
			"owner_id":              doc.OwnerID,
			"knowledge_base_id":     doc.KnowledgeBaseID,
			"document_id":           doc.DocumentID,
			"generation_id":         doc.GenerationID,
			"chunk_id":              doc.ChunkID,
			"chunk_index":           doc.ChunkIndex,
			"document_name":         doc.DocumentName,
			"file_type":             doc.FileType,
			"section_title":         doc.SectionTitle,
			"content":               doc.Content,
			"content_hash":          doc.ContentHash,
			"enabled":               doc.Enabled,
			"embedding_model":       doc.EmbeddingModel,
			"embedding_dimensions":  doc.EmbeddingDimensions,
			"embedding_provider_id": doc.EmbeddingProviderID,
			"embedding_metric":      doc.EmbeddingMetric,
			"embedding_profile":     doc.EmbeddingProfile,
			"page_number":           doc.PageNumber,
			"token_count":           doc.TokenCount,
			"has_vector":            hasVector,
			"source_metadata":       doc.Metadata,
		}
		vector := doc.EmbeddingVector
		if len(vector) == 0 && s.dimensions > 0 {
			// Milvus vector fields are required, so keyword-only chunks use a
			// filtered zero vector without invoking the embedding provider.
			vector = make([]float32, s.dimensions)
		}
		vectors = append(vectors, vectorstore.VectorDocument{ID: strconv.FormatInt(doc.ChunkID, 10), Text: doc.Content, Vector: vector, Metadata: metadata})
	}
	if len(vectors) == 0 {
		return nil
	}
	if s.dimensions <= 0 {
		return fmt.Errorf("milvus dimensions are required before indexing chunks")
	}
	if err := s.EnsureIndex(ctx); err != nil {
		return err
	}
	return s.store.Upsert(ctx, s.collection, vectors)
}

func (s *Store) DeleteByDocument(ctx context.Context, ownerID, documentID int64) error {
	return s.deleteByFilter(ctx, map[string]any{"owner_id": ownerID, "document_id": documentID})
}

func (s *Store) DeleteByKnowledgeBase(ctx context.Context, ownerID, kbID int64) error {
	return s.deleteByFilter(ctx, map[string]any{"owner_id": ownerID, "knowledge_base_id": kbID})
}

func (s *Store) DeleteInactiveGenerations(ctx context.Context, ownerID, documentID int64, activeGeneration string) error {
	if s == nil || s.store == nil {
		return nil
	}
	cleaner, ok := s.store.(interface {
		DeleteByFilterExcept(context.Context, string, map[string]any, string, any) error
	})
	if !ok {
		return fmt.Errorf("milvus store does not support generation cleanup")
	}
	return cleaner.DeleteByFilterExcept(ctx, s.collection, map[string]any{"owner_id": ownerID, "document_id": documentID}, "generation_id", activeGeneration)
}

func (s *Store) SetDocumentEnabled(ctx context.Context, ownerID, documentID int64, enabled bool) error {
	if s == nil || s.store == nil {
		return nil
	}
	updater, ok := s.store.(interface {
		UpdateMetadataByFilter(context.Context, string, map[string]any, func(map[string]any) map[string]any) error
	})
	if !ok {
		return nil
	}
	return updater.UpdateMetadataByFilter(ctx, s.collection, map[string]any{"owner_id": ownerID, "document_id": documentID}, func(metadata map[string]any) map[string]any {
		metadata["enabled"] = enabled
		return metadata
	})
}

func (s *Store) Search(ctx context.Context, req retrieval.RetrievalRequest) (*retrieval.RetrievalResponse, error) {
	if s == nil || s.store == nil {
		return nil, fmt.Errorf("milvus vector retriever is not configured")
	}
	if req.TopK <= 0 {
		req.TopK = 8
	}
	if req.CandidateK <= 0 {
		req.CandidateK = max(req.TopK*4, 20)
	}
	filter := map[string]any{"owner_id": req.OwnerID}
	if len(req.KnowledgeBaseIDs) == 1 {
		filter["knowledge_base_id"] = req.KnowledgeBaseIDs[0]
	} else if len(req.KnowledgeBaseIDs) > 1 {
		filter["knowledge_base_id"] = append([]int64(nil), req.KnowledgeBaseIDs...)
	}
	if strings.TrimSpace(req.GenerationID) != "" {
		filter["generation_id"] = req.GenerationID
	}
	if strings.TrimSpace(req.EmbeddingProfile) != "" {
		filter["embedding_profile"] = req.EmbeddingProfile
	}
	activeGenerations := make([]map[string]any, 0, len(req.ActiveGenerationIDs))
	for documentID, generationID := range req.ActiveGenerationIDs {
		if generationID = strings.TrimSpace(generationID); generationID != "" {
			activeGenerations = append(activeGenerations, map[string]any{"document_id": documentID, "generation_id": generationID})
		}
	}
	filter["enabled"] = true
	for key, value := range req.Filters {
		filter[key] = value
	}
	keywordSearch, hasKeywordSearch := s.store.(interface {
		SearchText(context.Context, vectorstore.SearchRequest) ([]vectorstore.SearchResult, error)
	})
	searchVector := func(topK int) ([]retrieval.RetrievalResult, error) {
		if len(req.QueryVector) == 0 {
			return nil, fmt.Errorf("query vector is required for milvus retrieval")
		}
		vectorFilter := make(map[string]any, len(filter)+1)
		for key, value := range filter {
			vectorFilter[key] = value
		}
		vectorFilter["has_vector"] = true
		items, err := s.store.Search(ctx, vectorstore.SearchRequest{Collection: s.collection, Vector: req.QueryVector, TopK: topK, Filter: vectorFilter, AnyFilters: activeGenerations, HNSW: s.hnsw})
		if err != nil {
			return nil, err
		}
		results := make([]retrieval.RetrievalResult, 0, len(items))
		for _, item := range items {
			results = append(results, resultFromMetadata(item))
		}
		return results, nil
	}
	searchKeyword := func(topK int) ([]retrieval.RetrievalResult, error) {
		if !hasKeywordSearch {
			return nil, fmt.Errorf("milvus keyword retriever is not configured")
		}
		items, err := keywordSearch.SearchText(ctx, vectorstore.SearchRequest{Collection: s.collection, QueryText: req.Query, TopK: topK, Filter: filter, AnyFilters: activeGenerations})
		if err != nil {
			return nil, err
		}
		results := make([]retrieval.RetrievalResult, 0, len(items))
		for _, item := range items {
			result := resultFromMetadata(item)
			result.KeywordScore = item.Score
			result.FinalScore = item.Score
			results = append(results, result)
		}
		return results, nil
	}
	var results []retrieval.RetrievalResult
	var err error
	switch req.Mode {
	case "", retrieval.ModeVector:
		results, err = searchVector(req.TopK)
	case retrieval.ModeKeyword:
		results, err = searchKeyword(req.TopK)
	case retrieval.ModeHybrid:
		keywordResults, keywordErr := searchKeyword(req.CandidateK)
		if keywordErr != nil {
			err = keywordErr
			break
		}
		vectorResults, vectorErr := searchVector(req.CandidateK)
		if vectorErr != nil {
			err = vectorErr
			break
		}
		results = fusion.WeightedRetrievalResults(keywordResults, vectorResults, req.VectorWeight, req.TopK)
	default:
		err = fmt.Errorf("unsupported retrieval mode: %s", req.Mode)
	}
	if err != nil {
		return nil, err
	}
	return &retrieval.RetrievalResponse{Results: results}, nil
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func (s *Store) deleteByFilter(ctx context.Context, filter map[string]any) error {
	if s == nil || s.store == nil {
		return nil
	}
	if deleter, ok := s.store.(interface {
		DeleteByFilter(context.Context, string, map[string]any) error
	}); ok {
		return deleter.DeleteByFilter(ctx, s.collection, filter)
	}
	return nil
}

func resultFromMetadata(item vectorstore.SearchResult) retrieval.RetrievalResult {
	metadata := item.Metadata
	if nested, ok := metadata["source_metadata"].(map[string]any); ok {
		metadata = nested
	}
	result := retrieval.RetrievalResult{Score: item.Score, VectorScore: item.Score, FinalScore: item.Score, Metadata: metadata}
	result.ChunkID = int64Value(item.Metadata["chunk_id"])
	if result.ChunkID == 0 {
		result.ChunkID, _ = strconv.ParseInt(item.ID, 10, 64)
	}
	result.DocumentID = int64Value(item.Metadata["document_id"])
	result.KnowledgeBaseID = int64Value(item.Metadata["knowledge_base_id"])
	result.GenerationID = stringValue(item.Metadata["generation_id"])
	result.Content = stringValue(item.Metadata["content"])
	result.DocumentName = stringValue(item.Metadata["document_name"])
	if pageNumber := int64Value(item.Metadata["page_number"]); pageNumber > 0 {
		page := int(pageNumber)
		result.PageNumber = &page
	}
	return result
}

func int64Value(value any) int64 {
	switch v := value.(type) {
	case int64:
		return v
	case int:
		return int64(v)
	case float64:
		return int64(v)
	case string:
		parsed, _ := strconv.ParseInt(v, 10, 64)
		return parsed
	default:
		return 0
	}
}

func stringValue(value any) string {
	if value == nil {
		return ""
	}
	return fmt.Sprint(value)
}
