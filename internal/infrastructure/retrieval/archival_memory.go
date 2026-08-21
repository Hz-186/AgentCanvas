package retrieval

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"agentcanvas/internal/domain/contextresource"
	"agentcanvas/internal/domain/memory"
	"agentcanvas/internal/infrastructure/llm"
	"agentcanvas/internal/infrastructure/vectorstore"
)

const ArchivalMemoryCollection = "agent_archival_memories"

type ArchivalMemoryIndex struct {
	Store      vectorstore.Store
	Embedder   llm.EmbeddingClient
	Provider   llm.EmbeddingProviderConfig
	ProviderID int64
	Model      string
}

func (s ArchivalMemoryIndex) Index(ctx context.Context, item memory.Memory) error {
	vector, err := s.embed(ctx, item.Content)
	if err != nil {
		return err
	}
	collection := s.collection()
	if err := s.Store.EnsureCollection(ctx, collection, len(vector), vectorstore.DefaultHNSWConfig()); err != nil {
		return err
	}
	conversationID := int64(0)
	if item.ConversationID != nil {
		conversationID = *item.ConversationID
	}
	return s.Store.Upsert(ctx, collection, []vectorstore.VectorDocument{{
		ID: strconv.FormatInt(item.ID, 10), Vector: vector,
		Metadata: map[string]any{"owner_id": item.OwnerID, "conversation_id": conversationID, "memory_id": item.ID, "memory_type": item.MemoryType},
	}})
}

func (s ArchivalMemoryIndex) Search(ctx context.Context, ownerID int64, query string, limit int) ([]int64, error) {
	vector, err := s.embed(ctx, query)
	if err != nil {
		return nil, err
	}
	results, err := s.Store.Search(ctx, vectorstore.SearchRequest{Collection: s.collection(), Vector: vector, TopK: limit, Filter: map[string]any{"owner_id": ownerID}})
	if err != nil {
		return nil, err
	}
	ids := make([]int64, 0, len(results))
	for _, result := range results {
		id, err := strconv.ParseInt(result.ID, 10, 64)
		if err != nil {
			id, err = metadataInt64(result.Metadata["memory_id"])
		}
		if err == nil && id > 0 {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

func (s ArchivalMemoryIndex) Delete(ctx context.Context, memoryID int64) error {
	if s.Store == nil || memoryID <= 0 {
		return nil
	}
	if store, ok := s.Store.(interface {
		DeleteByFilter(context.Context, string, map[string]any) error
	}); ok {
		return store.DeleteByFilter(ctx, s.collection(), map[string]any{"memory_id": memoryID})
	}
	return s.Store.Delete(ctx, s.collection(), []string{strconv.FormatInt(memoryID, 10)})
}

func metadataInt64(value any) (int64, error) {
	switch typed := value.(type) {
	case float64:
		return int64(typed), nil
	case int64:
		return typed, nil
	case json.Number:
		return typed.Int64()
	case string:
		return strconv.ParseInt(typed, 10, 64)
	default:
		return 0, fmt.Errorf("memory_id metadata is invalid")
	}
}

func (s ArchivalMemoryIndex) embed(ctx context.Context, text string) ([]float32, error) {
	if s.Store == nil || s.Embedder == nil || strings.TrimSpace(s.Model) == "" {
		return nil, fmt.Errorf("archival memory index is not configured")
	}
	resp, err := s.Embedder.Embed(ctx, s.Provider, llm.EmbeddingRequest{Model: s.Model, Input: []string{text}})
	if err != nil {
		return nil, err
	}
	if resp == nil || len(resp.Embeddings) == 0 || len(resp.Embeddings[0]) == 0 {
		return nil, fmt.Errorf("embedding response is empty")
	}
	return resp.Embeddings[0], nil
}

func (s ArchivalMemoryIndex) collection() string {
	// The backing vector field fixes its dimension. A changed dimension is
	// rejected by Elasticsearch/Milvus and requires an explicit rebuild.
	hash := contextresource.HashContent(fmt.Sprintf("%d\x1f%s\x1fCOSINE", s.ProviderID, strings.TrimSpace(s.Model)))
	if len(hash) > 12 {
		hash = hash[:12]
	}
	return fmt.Sprintf("%s_%s", ArchivalMemoryCollection, hash)
}

var _ memory.ArchivalIndex = ArchivalMemoryIndex{}
