package milvus

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"agentcanvas/internal/domain/retrieval"
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
		collection = "agentcanvas_chunks"
	}
	return &Store{store: store, collection: collection, dimensions: dimensions, hnsw: vectorstore.NormalizeHNSWConfig(hnsw)}
}

func (s *Store) EnsureIndex(ctx context.Context) error {
	if s == nil || s.store == nil || s.dimensions <= 0 {
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
		if len(doc.EmbeddingVector) == 0 {
			continue
		}
		metadata := map[string]any{
			"owner_id":             doc.OwnerID,
			"kb_id":                doc.KBID,
			"document_id":          doc.DocumentID,
			"chunk_id":             doc.ChunkID,
			"chunk_index":          doc.ChunkIndex,
			"document_name":        doc.DocumentName,
			"file_type":            doc.FileType,
			"section_title":        doc.SectionTitle,
			"content":              doc.Content,
			"content_hash":         doc.ContentHash,
			"enabled":              doc.Enabled,
			"embedding_model":      doc.EmbeddingModel,
			"embedding_dimensions": doc.EmbeddingDimensions,
			"page_no":              doc.PageNo,
			"token_count":          doc.TokenCount,
			"source_metadata":      doc.Metadata,
		}
		vectors = append(vectors, vectorstore.VectorDocument{ID: strconv.FormatInt(doc.ChunkID, 10), Vector: doc.EmbeddingVector, Metadata: metadata})
	}
	if len(vectors) == 0 {
		return nil
	}
	if s.dimensions <= 0 {
		s.dimensions = len(vectors[0].Vector)
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
	return s.deleteByFilter(ctx, map[string]any{"owner_id": ownerID, "kb_id": kbID})
}

func (s *Store) SetDocumentEnabled(context.Context, int64, int64, bool) error {
	return nil
}

func (s *Store) Search(ctx context.Context, req retrieval.RetrievalRequest) (*retrieval.RetrievalResponse, error) {
	if s == nil || s.store == nil {
		return nil, fmt.Errorf("milvus vector retriever is not configured")
	}
	if len(req.QueryVector) == 0 {
		return nil, fmt.Errorf("query vector is required for milvus retrieval")
	}
	if req.TopK <= 0 {
		req.TopK = 8
	}
	filter := map[string]any{"owner_id": req.OwnerID}
	if len(req.KBIDs) == 1 {
		filter["kb_id"] = req.KBIDs[0]
	} else if len(req.KBIDs) > 1 {
		filter["kb_id"] = append([]int64(nil), req.KBIDs...)
	}
	filter["enabled"] = true
	for key, value := range req.Filters {
		filter[key] = value
	}
	items, err := s.store.Search(ctx, vectorstore.SearchRequest{Collection: s.collection, Vector: req.QueryVector, TopK: req.TopK, Filter: filter, HNSW: s.hnsw})
	if err != nil {
		return nil, err
	}
	results := make([]retrieval.RetrievalResult, 0, len(items))
	for _, item := range items {
		results = append(results, resultFromMetadata(item))
	}
	return &retrieval.RetrievalResponse{Results: results}, nil
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
	result.KBID = int64Value(item.Metadata["kb_id"])
	result.Content = stringValue(item.Metadata["content"])
	result.DocumentName = stringValue(item.Metadata["document_name"])
	if pageNo := int64Value(item.Metadata["page_no"]); pageNo > 0 {
		page := int(pageNo)
		result.PageNo = &page
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
