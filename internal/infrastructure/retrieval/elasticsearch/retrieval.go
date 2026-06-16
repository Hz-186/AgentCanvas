package elasticsearch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"agentcanvas/internal/domain/retrieval"
	"agentcanvas/internal/pkg/config"

	esclient "github.com/elastic/go-elasticsearch/v8"
	"github.com/elastic/go-elasticsearch/v8/esapi"
)

type Store struct {
	client *esclient.Client
	index  string
}

func NewStore(client *esclient.Client, cfg config.ElasticsearchConfig) *Store {
	index := strings.TrimSpace(cfg.ChunkIndex)
	if index == "" {
		index = "agentcanvas_chunks_v1"
	}
	return &Store{client: client, index: index}
}

func (s *Store) EnsureIndex(ctx context.Context) error {
	res, err := s.client.Indices.Exists([]string{s.index}, s.client.Indices.Exists.WithContext(ctx))
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusOK {
		return nil
	}
	if res.StatusCode != http.StatusNotFound {
		return responseError("check index", res)
	}

	res, err = s.client.Indices.Create(
		s.index,
		s.client.Indices.Create.WithContext(ctx),
		s.client.Indices.Create.WithBody(strings.NewReader(chunkIndexMapping)),
	)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.IsError() {
		return responseError("create index", res)
	}
	return nil
}

func (s *Store) IndexChunks(ctx context.Context, docs []retrieval.ChunkIndexDocument) error {
	for _, doc := range docs {
		body, err := json.Marshal(map[string]any{
			"owner_id":      doc.OwnerID,
			"kb_id":         doc.KBID,
			"document_id":   doc.DocumentID,
			"chunk_id":      doc.ChunkID,
			"chunk_index":   doc.ChunkIndex,
			"document_name": doc.DocumentName,
			"file_type":     doc.FileType,
			"section_title": doc.SectionTitle,
			"content":       doc.Content,
			"content_hash":  doc.ContentHash,
			"page_no":       doc.PageNo,
			"token_count":   doc.TokenCount,
			"metadata":      doc.Metadata,
			"created_at":    doc.CreatedAt,
			"updated_at":    doc.UpdatedAt,
		})
		if err != nil {
			return err
		}

		res, err := s.client.Index(
			s.index,
			bytes.NewReader(body),
			s.client.Index.WithContext(ctx),
			s.client.Index.WithDocumentID(strconv.FormatInt(doc.ChunkID, 10)),
			s.client.Index.WithRefresh("true"),
		)
		if err != nil {
			return err
		}
		if res.IsError() {
			err := responseError("index chunk", res)
			res.Body.Close()
			return err
		}
		res.Body.Close()
	}
	return nil
}

func (s *Store) DeleteByDocument(ctx context.Context, ownerID, documentID int64) error {
	return s.deleteByQuery(ctx, []map[string]any{
		{"term": map[string]any{"owner_id": ownerID}},
		{"term": map[string]any{"document_id": documentID}},
	})
}

func (s *Store) DeleteByKnowledgeBase(ctx context.Context, ownerID, kbID int64) error {
	return s.deleteByQuery(ctx, []map[string]any{
		{"term": map[string]any{"owner_id": ownerID}},
		{"term": map[string]any{"kb_id": kbID}},
	})
}

func (s *Store) Search(ctx context.Context, req retrieval.RetrievalRequest) (*retrieval.RetrievalResponse, error) {
	start := time.Now()
	if req.TopK <= 0 {
		req.TopK = 8
	}
	if req.Mode == "" {
		req.Mode = retrieval.ModeKeyword
	}
	if req.Mode != retrieval.ModeKeyword {
		return nil, fmt.Errorf("unsupported retrieval mode: %s", req.Mode)
	}

	filters := []map[string]any{{"term": map[string]any{"owner_id": req.OwnerID}}}
	if len(req.KBIDs) > 0 {
		filters = append(filters, map[string]any{"terms": map[string]any{"kb_id": req.KBIDs}})
	}
	for k, v := range req.Filters {
		if strings.TrimSpace(k) == "" || v == nil {
			continue
		}
		filters = append(filters, map[string]any{"term": map[string]any{k: v}})
	}

	body := map[string]any{
		"size": req.TopK,
		"query": map[string]any{
			"bool": map[string]any{
				"filter": filters,
				"must": []map[string]any{
					{
						"multi_match": map[string]any{
							"query":  req.Query,
							"fields": []string{"content^3", "section_title^2", "document_name"},
							"type":   "best_fields",
						},
					},
				},
			},
		},
		"highlight": map[string]any{
			"pre_tags":  []string{"<em>"},
			"post_tags": []string{"</em>"},
			"fields": map[string]any{
				"content": map[string]any{},
			},
		},
	}

	data, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	res, err := s.client.Search(
		s.client.Search.WithContext(ctx),
		s.client.Search.WithIndex(s.index),
		s.client.Search.WithBody(bytes.NewReader(data)),
	)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.IsError() {
		return nil, responseError("search chunks", res)
	}

	var parsed searchResponse
	if err := json.NewDecoder(res.Body).Decode(&parsed); err != nil {
		return nil, err
	}
	results := make([]retrieval.RetrievalResult, 0, len(parsed.Hits.Hits))
	for _, hit := range parsed.Hits.Hits {
		highlight := ""
		if values := hit.Highlight["content"]; len(values) > 0 {
			highlight = values[0]
		}
		results = append(results, retrieval.RetrievalResult{
			ChunkID:      hit.Source.ChunkID,
			DocumentID:   hit.Source.DocumentID,
			KBID:         hit.Source.KBID,
			Score:        hit.Score,
			Content:      hit.Source.Content,
			Highlight:    highlight,
			DocumentName: hit.Source.DocumentName,
			PageNo:       hit.Source.PageNo,
			Metadata:     hit.Source.Metadata,
		})
	}

	return &retrieval.RetrievalResponse{
		Results:   results,
		LatencyMS: int(time.Since(start).Milliseconds()),
	}, nil
}

func (s *Store) deleteByQuery(ctx context.Context, filters []map[string]any) error {
	body, err := json.Marshal(map[string]any{
		"query": map[string]any{
			"bool": map[string]any{
				"filter": filters,
			},
		},
	})
	if err != nil {
		return err
	}
	res, err := s.client.DeleteByQuery(
		[]string{s.index},
		bytes.NewReader(body),
		s.client.DeleteByQuery.WithContext(ctx),
		s.client.DeleteByQuery.WithRefresh(true),
		s.client.DeleteByQuery.WithConflicts("proceed"),
	)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.IsError() {
		return responseError("delete chunks", res)
	}
	return nil
}

func responseError(action string, res *esapi.Response) error {
	data, _ := io.ReadAll(res.Body)
	return fmt.Errorf("%s failed: status=%s body=%s", action, res.Status(), strings.TrimSpace(string(data)))
}

type searchResponse struct {
	Hits struct {
		Hits []struct {
			Score     float64             `json:"_score"`
			Source    chunkSearchDocument `json:"_source"`
			Highlight map[string][]string `json:"highlight"`
		} `json:"hits"`
	} `json:"hits"`
}

type chunkSearchDocument struct {
	OwnerID      int64          `json:"owner_id"`
	KBID         int64          `json:"kb_id"`
	DocumentID   int64          `json:"document_id"`
	ChunkID      int64          `json:"chunk_id"`
	ChunkIndex   int            `json:"chunk_index"`
	DocumentName string         `json:"document_name"`
	FileType     string         `json:"file_type"`
	SectionTitle string         `json:"section_title"`
	Content      string         `json:"content"`
	ContentHash  string         `json:"content_hash"`
	PageNo       *int           `json:"page_no"`
	TokenCount   int            `json:"token_count"`
	Metadata     map[string]any `json:"metadata"`
}
