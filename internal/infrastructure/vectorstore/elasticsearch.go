package vectorstore

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	esclient "github.com/elastic/go-elasticsearch/v8"
)

// ElasticsearchStore adapts Elasticsearch's dense_vector index to the shared
// vectorstore contract used by context, reflection, and archival memory.
type ElasticsearchStore struct {
	client *esclient.Client
}

func NewElasticsearchStore(client *esclient.Client) *ElasticsearchStore {
	return &ElasticsearchStore{client: client}
}

func (s *ElasticsearchStore) EnsureCollection(ctx context.Context, name string, dimensions int, _ HNSWConfig) error {
	if s == nil || s.client == nil || strings.TrimSpace(name) == "" || dimensions <= 0 {
		return fmt.Errorf("elasticsearch vector index and dimensions are required")
	}
	res, err := s.client.Indices.Exists([]string{name}, s.client.Indices.Exists.WithContext(ctx))
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusOK {
		return nil
	}
	if res.StatusCode != http.StatusNotFound {
		return esResponseError("check vector index", res.Body, res.Status())
	}
	mapping := fmt.Sprintf(`{"mappings":{"properties":{"text":{"type":"text"},"vector":{"type":"dense_vector","dims":%d,"index":true,"similarity":"cosine"},"metadata":{"type":"object","enabled":true}}}}`, dimensions)
	res, err = s.client.Indices.Create(name, s.client.Indices.Create.WithContext(ctx), s.client.Indices.Create.WithBody(strings.NewReader(mapping)))
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.IsError() {
		return esResponseError("create vector index", res.Body, res.Status())
	}
	return nil
}

func (s *ElasticsearchStore) Upsert(ctx context.Context, collection string, docs []VectorDocument) error {
	if s == nil || s.client == nil || strings.TrimSpace(collection) == "" || len(docs) == 0 {
		return fmt.Errorf("elasticsearch vector index and docs are required")
	}
	for _, doc := range docs {
		if strings.TrimSpace(doc.ID) == "" || len(doc.Vector) == 0 {
			return fmt.Errorf("elasticsearch vector document id and vector are required")
		}
		body, err := json.Marshal(map[string]any{"text": doc.Text, "vector": doc.Vector, "metadata": doc.Metadata})
		if err != nil {
			return err
		}
		res, err := s.client.Index(collection, bytes.NewReader(body), s.client.Index.WithContext(ctx), s.client.Index.WithDocumentID(doc.ID), s.client.Index.WithRefresh("true"))
		if err != nil {
			return err
		}
		if res.IsError() {
			err = esResponseError("index vector document", res.Body, res.Status())
			res.Body.Close()
			return err
		}
		res.Body.Close()
	}
	return nil
}

func (s *ElasticsearchStore) Delete(ctx context.Context, collection string, ids []string) error {
	for _, id := range ids {
		res, err := s.client.Delete(collection, id, s.client.Delete.WithContext(ctx), s.client.Delete.WithRefresh("true"))
		if err != nil {
			return err
		}
		if res.StatusCode != http.StatusNotFound && res.IsError() {
			err = esResponseError("delete vector document", res.Body, res.Status())
			res.Body.Close()
			return err
		}
		res.Body.Close()
	}
	return nil
}

func (s *ElasticsearchStore) DeleteByFilter(ctx context.Context, collection string, filter map[string]any) error {
	return s.updateByQuery(ctx, collection, filter, nil)
}

func (s *ElasticsearchStore) UpdateMetadataByFilter(ctx context.Context, collection string, filter map[string]any, mutate func(map[string]any) map[string]any) error {
	if mutate == nil {
		return nil
	}
	docs, err := s.queryDocuments(ctx, collection, filter)
	if err != nil {
		return err
	}
	for _, doc := range docs {
		metadata := mutate(cloneMetadata(doc.Metadata))
		body, err := json.Marshal(map[string]any{"text": doc.Text, "vector": doc.Vector, "metadata": metadata})
		if err != nil {
			return err
		}
		res, err := s.client.Index(collection, bytes.NewReader(body), s.client.Index.WithContext(ctx), s.client.Index.WithDocumentID(doc.ID), s.client.Index.WithRefresh("true"))
		if err != nil {
			return err
		}
		if res.IsError() {
			err = esResponseError("update vector metadata", res.Body, res.Status())
			res.Body.Close()
			return err
		}
		res.Body.Close()
	}
	return nil
}

func (s *ElasticsearchStore) Search(ctx context.Context, req SearchRequest) ([]SearchResult, error) {
	if s == nil || s.client == nil || strings.TrimSpace(req.Collection) == "" || len(req.Vector) == 0 {
		return nil, fmt.Errorf("elasticsearch vector index and query vector are required")
	}
	if req.TopK <= 0 {
		req.TopK = 8
	}
	filters := make([]map[string]any, 0, len(req.Filter))
	for key, value := range req.Filter {
		filters = append(filters, metadataFilter(key, value))
	}
	body := map[string]any{"size": req.TopK, "knn": map[string]any{
		"field": "vector", "query_vector": req.Vector, "k": req.TopK, "num_candidates": req.TopK * 4,
	}}
	if len(filters) > 0 {
		body["knn"].(map[string]any)["filter"] = filters
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	res, err := s.client.Search(s.client.Search.WithContext(ctx), s.client.Search.WithIndex(req.Collection), s.client.Search.WithBody(bytes.NewReader(encoded)))
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.IsError() {
		return nil, esResponseError("search vector documents", res.Body, res.Status())
	}
	var decoded struct {
		Hits struct {
			Hits []struct {
				ID     string  `json:"_id"`
				Score  float64 `json:"_score"`
				Source struct {
					Text     string         `json:"text"`
					Vector   []float32      `json:"vector"`
					Metadata map[string]any `json:"metadata"`
				} `json:"_source"`
			} `json:"hits"`
		} `json:"hits"`
	}
	if err := json.NewDecoder(res.Body).Decode(&decoded); err != nil {
		return nil, err
	}
	results := make([]SearchResult, 0, len(decoded.Hits.Hits))
	for _, hit := range decoded.Hits.Hits {
		metadata := cloneMetadata(hit.Source.Metadata)
		if hit.Source.Text != "" {
			metadata["text"] = hit.Source.Text
		}
		results = append(results, SearchResult{ID: hit.ID, Score: hit.Score, Metadata: metadata})
	}
	return results, nil
}

func (s *ElasticsearchStore) queryDocuments(ctx context.Context, collection string, filter map[string]any) ([]VectorDocument, error) {
	filters := make([]map[string]any, 0, len(filter))
	for key, value := range filter {
		filters = append(filters, metadataFilter(key, value))
	}
	body, _ := json.Marshal(map[string]any{"size": 10000, "query": map[string]any{"bool": map[string]any{"filter": filters}}})
	res, err := s.client.Search(s.client.Search.WithContext(ctx), s.client.Search.WithIndex(collection), s.client.Search.WithBody(bytes.NewReader(body)))
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.IsError() {
		return nil, esResponseError("query vector documents", res.Body, res.Status())
	}
	var decoded struct {
		Hits struct {
			Hits []struct {
				ID     string `json:"_id"`
				Source struct {
					Text     string         `json:"text"`
					Vector   []float32      `json:"vector"`
					Metadata map[string]any `json:"metadata"`
				} `json:"_source"`
			} `json:"hits"`
		} `json:"hits"`
	}
	if err := json.NewDecoder(res.Body).Decode(&decoded); err != nil {
		return nil, err
	}
	result := make([]VectorDocument, 0, len(decoded.Hits.Hits))
	for _, hit := range decoded.Hits.Hits {
		result = append(result, VectorDocument{ID: hit.ID, Text: hit.Source.Text, Vector: hit.Source.Vector, Metadata: hit.Source.Metadata})
	}
	return result, nil
}

func (s *ElasticsearchStore) updateByQuery(ctx context.Context, collection string, filter map[string]any, _ func(map[string]any) map[string]any) error {
	filters := make([]map[string]any, 0, len(filter))
	for key, value := range filter {
		filters = append(filters, metadataFilter(key, value))
	}
	body, _ := json.Marshal(map[string]any{"query": map[string]any{"bool": map[string]any{"filter": filters}}})
	res, err := s.client.DeleteByQuery([]string{collection}, bytes.NewReader(body), s.client.DeleteByQuery.WithContext(ctx), s.client.DeleteByQuery.WithRefresh(true))
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.IsError() {
		return esResponseError("delete vector documents", res.Body, res.Status())
	}
	return nil
}

func esResponseError(action string, body io.Reader, status string) error {
	data, _ := io.ReadAll(body)
	return fmt.Errorf("%s failed: status=%s body=%s", action, status, strings.TrimSpace(string(data)))
}

func metadataFilter(key string, value any) map[string]any {
	field := "metadata." + key
	switch typed := value.(type) {
	case []int:
		return map[string]any{"terms": map[string]any{field: typed}}
	case []int64:
		return map[string]any{"terms": map[string]any{field: typed}}
	case []string:
		return map[string]any{"terms": map[string]any{field: typed}}
	case []any:
		return map[string]any{"terms": map[string]any{field: typed}}
	default:
		return map[string]any{"term": map[string]any{field: value}}
	}
}

var _ Store = (*ElasticsearchStore)(nil)
