package retrieval

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"agentcanvas/internal/domain/memory"

	"github.com/elastic/go-elasticsearch/v8"
	"github.com/elastic/go-elasticsearch/v8/esapi"
)

const memoryIndexName = "agentcanvas_memories_v1"

type MemoryStore struct {
	client *elasticsearch.Client
}

func NewMemoryStore(client *elasticsearch.Client) *MemoryStore {
	return &MemoryStore{client: client}
}

const memoryIndexMapping = `{
  "settings": {
    "number_of_shards": 1,
    "number_of_replicas": 0
  },
  "mappings": {
    "properties": {
      "owner_id": { "type": "long" },
      "memory_id": { "type": "long" },
      "memory_type": { "type": "keyword" },
      "memory_level": { "type": "keyword" },
      "content": {
        "type": "text",
        "fields": {
          "keyword": { "type": "keyword", "ignore_above": 512 }
        }
      },
      "title": {
        "type": "text",
        "fields": {
          "keyword": { "type": "keyword", "ignore_above": 256 }
        }
      },
      "importance": { "type": "double" },
      "created_at": { "type": "date" },
      "updated_at": { "type": "date" }
    }
  }
}`

func (s *MemoryStore) EnsureIndex(ctx context.Context) error {
	res, err := s.client.Indices.Exists([]string{memoryIndexName}, s.client.Indices.Exists.WithContext(ctx))
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusOK {
		return nil
	}
	if res.StatusCode != http.StatusNotFound {
		return readErrorResponse("check index", res)
	}
	res, err = s.client.Indices.Create(
		memoryIndexName,
		s.client.Indices.Create.WithContext(ctx),
		s.client.Indices.Create.WithBody(strings.NewReader(memoryIndexMapping)),
	)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.IsError() {
		return readErrorResponse("create index", res)
	}
	return nil
}

func (s *MemoryStore) Index(ctx context.Context, item memory.Memory) error {
	doc := map[string]any{
		"owner_id":     item.OwnerID,
		"memory_id":    item.ID,
		"memory_type":  item.MemoryType,
		"memory_level": item.MemoryLevel,
		"title":        item.Title,
		"content":      item.Content,
		"importance":   item.Importance,
		"created_at":   item.CreatedAt,
		"updated_at":   item.UpdatedAt,
	}
	body, _ := json.Marshal(doc)
	res, err := s.client.Index(
		memoryIndexName,
		bytes.NewReader(body),
		s.client.Index.WithContext(ctx),
		s.client.Index.WithDocumentID(strconv.FormatInt(item.ID, 10)),
		s.client.Index.WithRefresh("true"),
	)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.IsError() {
		return readErrorResponse("index memory", res)
	}
	return nil
}

type memorySearchQuery struct {
	Size  int              `json:"size"`
	Query map[string]any   `json:"query"`
	Sort  []map[string]any `json:"sort,omitempty"`
}

func (s *MemoryStore) Search(ctx context.Context, ownerID int64, query string, memoryTypes []string, limit int) ([]int64, error) {
	if limit <= 0 {
		limit = 5
	}
	must := []map[string]any{
		{"term": map[string]any{"owner_id": ownerID}},
	}
	if query != "" {
		must = append(must, map[string]any{
			"match": map[string]any{
				"content": map[string]any{
					"query":    query,
					"operator": "or",
				},
			},
		})
	}
	if len(memoryTypes) > 0 {
		must = append(must, map[string]any{
			"terms": map[string]any{"memory_type": memoryTypes},
		})
	}
	searchQuery := memorySearchQuery{
		Size: limit,
		Query: map[string]any{
			"bool": map[string]any{
				"must": must,
			},
		},
	}
	body, _ := json.Marshal(searchQuery)
	res, err := s.client.Search(
		s.client.Search.WithContext(ctx),
		s.client.Search.WithIndex(memoryIndexName),
		s.client.Search.WithBody(bytes.NewReader(body)),
	)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.IsError() {
		return nil, readErrorResponse("search memories", res)
	}
	var result memorySearchResponse
	if err := json.NewDecoder(res.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode memory search response: %w", err)
	}
	ids := make([]int64, 0, len(result.Hits.Hits))
	for _, hit := range result.Hits.Hits {
		ids = append(ids, hit.Source.MemoryID)
	}
	return ids, nil
}

func (s *MemoryStore) Delete(ctx context.Context, memoryID int64) error {
	res, err := s.client.Delete(
		memoryIndexName,
		strconv.FormatInt(memoryID, 10),
		s.client.Delete.WithContext(ctx),
		s.client.Delete.WithRefresh("true"),
	)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.IsError() {
		if res.StatusCode == http.StatusNotFound {
			return nil
		}
		return readErrorResponse("delete memory", res)
	}
	return nil
}

type memorySearchResponse struct {
	Hits struct {
		Hits []struct {
			Source struct {
				MemoryID int64 `json:"memory_id"`
			} `json:"_source"`
		} `json:"hits"`
	} `json:"hits"`
}

func readErrorResponse(action string, res *esapi.Response) error {
	body, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
	return fmt.Errorf("es %s failed [%d]: %s", action, res.StatusCode, strings.TrimSpace(string(body)))
}
