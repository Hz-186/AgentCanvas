package elasticsearch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"agentcanvas/internal/domain/contextresource"

	esclient "github.com/elastic/go-elasticsearch/v8"
)

const defaultContextResourceIndex = "agentcanvas_context_resources_v1"

const contextResourceMapping = `{
  "mappings": {
    "dynamic": "strict",
    "properties": {
      "owner_id": {"type": "long"},
      "workflow_id": {"type": "long"},
      "conversation_id": {"type": "long"},
      "resource_type": {"type": "keyword"},
      "resource_id": {"type": "keyword"},
      "content_hash": {"type": "keyword"},
      "content": {"type": "text"}
    }
  }
}`

type ContextKeywordIndex struct {
	client   *esclient.Client
	index    string
	mu       sync.RWMutex
	ensureMu sync.Mutex
	ensured  bool
}

func NewContextKeywordIndex(client *esclient.Client, index string) *ContextKeywordIndex {
	if strings.TrimSpace(index) == "" {
		index = defaultContextResourceIndex
	}
	return &ContextKeywordIndex{client: client, index: strings.TrimSpace(index)}
}

func (s *ContextKeywordIndex) EnsureIndex(ctx context.Context) error {
	if s == nil || s.client == nil {
		return fmt.Errorf("context keyword index client is not configured")
	}
	s.ensureMu.Lock()
	defer s.ensureMu.Unlock()
	s.mu.RLock()
	ensured := s.ensured
	s.mu.RUnlock()
	if ensured {
		return nil
	}
	response, err := s.client.Indices.Exists([]string{s.index}, s.client.Indices.Exists.WithContext(ctx))
	if err != nil {
		return err
	}
	response.Body.Close()
	if response.StatusCode == http.StatusOK {
		s.mu.Lock()
		s.ensured = true
		s.mu.Unlock()
		return nil
	}
	if response.StatusCode != http.StatusNotFound {
		return fmt.Errorf("check context resource index failed: %s", response.Status())
	}
	response, err = s.client.Indices.Create(s.index, s.client.Indices.Create.WithContext(ctx), s.client.Indices.Create.WithBody(strings.NewReader(contextResourceMapping)))
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.IsError() {
		return responseError("create context resource index", response)
	}
	s.mu.Lock()
	s.ensured = true
	s.mu.Unlock()
	return nil
}

func (s *ContextKeywordIndex) ensureIndex(ctx context.Context) error {
	s.mu.RLock()
	ensured := s.ensured
	s.mu.RUnlock()
	if ensured {
		return nil
	}
	return s.EnsureIndex(ctx)
}

func (s *ContextKeywordIndex) Upsert(ctx context.Context, document contextresource.Document, profile contextresource.EmbeddingProfile) (contextresource.EmbeddingProfile, error) {
	if err := s.ensureIndex(ctx); err != nil {
		return profile, err
	}
	payload, err := json.Marshal(map[string]any{"owner_id": document.OwnerID, "workflow_id": document.WorkflowID, "conversation_id": document.ConversationID,
		"resource_type": document.ResourceType, "resource_id": document.ResourceID, "content_hash": document.ContentHash, "content": document.Content})
	if err != nil {
		return profile, err
	}
	response, err := s.client.Index(s.index, bytes.NewReader(payload), s.client.Index.WithContext(ctx),
		s.client.Index.WithDocumentID(contextresource.DocumentID(document.OwnerID, document.ResourceType, document.ResourceID)), s.client.Index.WithRefresh("false"))
	if err != nil {
		return profile, err
	}
	defer response.Body.Close()
	if response.IsError() {
		return profile, responseError("index context resource", response)
	}
	return profile, nil
}

func (s *ContextKeywordIndex) Delete(ctx context.Context, item contextresource.OutboxItem) error {
	if err := s.ensureIndex(ctx); err != nil {
		return err
	}
	response, err := s.client.Delete(s.index, contextresource.DocumentID(item.OwnerID, item.ResourceType, item.ResourceID), s.client.Delete.WithContext(ctx), s.client.Delete.WithRefresh("false"))
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return nil
	}
	if response.IsError() {
		return responseError("delete context resource", response)
	}
	return nil
}

func (s *ContextKeywordIndex) Search(ctx context.Context, request contextresource.SearchRequest) ([]contextresource.SearchResult, error) {
	if request.OwnerID <= 0 || strings.TrimSpace(request.Query) == "" {
		return nil, nil
	}
	if err := s.ensureIndex(ctx); err != nil {
		return nil, err
	}
	limit := request.TopK
	if limit <= 0 {
		limit = 12
	}
	filters := []map[string]any{{"term": map[string]any{"owner_id": request.OwnerID}}}
	if len(request.ResourceTypes) == 1 {
		filters = append(filters, map[string]any{"term": map[string]any{"resource_type": request.ResourceTypes[0]}})
	} else if len(request.ResourceTypes) > 1 {
		filters = append(filters, map[string]any{"terms": map[string]any{"resource_type": request.ResourceTypes}})
	}
	if request.ConversationID > 0 {
		filters = append(filters, map[string]any{"term": map[string]any{"conversation_id": request.ConversationID}})
	}
	body, _ := json.Marshal(map[string]any{"size": limit * 2, "query": map[string]any{"bool": map[string]any{
		"filter": filters, "must": []map[string]any{{"simple_query_string": map[string]any{"query": request.Query, "fields": []string{"content"}, "default_operator": "or"}}},
	}}})
	response, err := s.client.Search(s.client.Search.WithContext(ctx), s.client.Search.WithIndex(s.index), s.client.Search.WithBody(bytes.NewReader(body)))
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.IsError() {
		return nil, responseError("search context resources", response)
	}
	var decoded struct {
		Hits struct {
			Hits []struct {
				Score  float64 `json:"_score"`
				Source struct {
					WorkflowID     int64  `json:"workflow_id"`
					ConversationID int64  `json:"conversation_id"`
					ResourceType   string `json:"resource_type"`
					ResourceID     string `json:"resource_id"`
					ContentHash    string `json:"content_hash"`
				} `json:"_source"`
			} `json:"hits"`
		} `json:"hits"`
	}
	if err := json.NewDecoder(response.Body).Decode(&decoded); err != nil {
		return nil, err
	}
	results := make([]contextresource.SearchResult, 0, limit)
	for _, hit := range decoded.Hits.Hits {
		if request.WorkflowID > 0 && hit.Source.WorkflowID != 0 && hit.Source.WorkflowID != request.WorkflowID {
			continue
		}
		results = append(results, contextresource.SearchResult{ResourceType: hit.Source.ResourceType, ResourceID: hit.Source.ResourceID, Score: hit.Score,
			Metadata: map[string]any{"workflow_id": hit.Source.WorkflowID, "conversation_id": hit.Source.ConversationID, "content_hash": hit.Source.ContentHash}})
		if len(results) >= limit {
			break
		}
	}
	return results, nil
}

var _ contextresource.Index = (*ContextKeywordIndex)(nil)
