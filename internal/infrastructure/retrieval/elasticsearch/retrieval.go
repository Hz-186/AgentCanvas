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
	"agentcanvas/internal/domain/retrieval/fusion"
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
		return s.ensureVectorMapping(ctx)
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
		payload := map[string]any{
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
			"metadata":              doc.Metadata,
			"created_at":            doc.CreatedAt,
			"updated_at":            doc.UpdatedAt,
		}
		if len(doc.EmbeddingVector) > 0 {
			payload["embedding_vector"] = doc.EmbeddingVector
		}
		body, err := json.Marshal(payload)
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

func (s *Store) SetDocumentEnabled(ctx context.Context, ownerID, documentID int64, enabled bool) error {
	body, err := json.Marshal(map[string]any{
		"query": map[string]any{
			"bool": map[string]any{
				"filter": []map[string]any{
					{"term": map[string]any{"owner_id": ownerID}},
					{"term": map[string]any{"document_id": documentID}},
				},
			},
		},
		"script": map[string]any{
			"source": "ctx._source.enabled = params.enabled",
			"lang":   "painless",
			"params": map[string]any{"enabled": enabled},
		},
	})
	if err != nil {
		return err
	}
	res, err := s.client.UpdateByQuery(
		[]string{s.index},
		s.client.UpdateByQuery.WithContext(ctx),
		s.client.UpdateByQuery.WithBody(bytes.NewReader(body)),
		s.client.UpdateByQuery.WithRefresh(true),
		s.client.UpdateByQuery.WithConflicts("proceed"),
	)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.IsError() {
		return responseError("update document enabled", res)
	}
	return nil
}

func (s *Store) DeleteByKnowledgeBase(ctx context.Context, ownerID, kbID int64) error {
	return s.deleteByQuery(ctx, []map[string]any{
		{"term": map[string]any{"owner_id": ownerID}},
		{"term": map[string]any{"knowledge_base_id": kbID}},
	})
}

func (s *Store) DeleteInactiveGenerations(ctx context.Context, ownerID, documentID int64, activeGeneration string) error {
	return s.deleteQuery(ctx, map[string]any{
		"bool": map[string]any{
			"filter": []map[string]any{
				{"term": map[string]any{"owner_id": ownerID}},
				{"term": map[string]any{"document_id": documentID}},
			},
			"must_not": map[string]any{"term": map[string]any{"generation_id": activeGeneration}},
		},
	})
}

func (s *Store) Search(ctx context.Context, req retrieval.RetrievalRequest) (*retrieval.RetrievalResponse, error) {
	start := time.Now()
	if req.TopK <= 0 {
		req.TopK = 8
	}
	if req.CandidateK <= 0 {
		req.CandidateK = max(req.TopK*4, 20)
	}
	if req.Mode == "" {
		req.Mode = retrieval.ModeKeyword
	}
	var results []retrieval.RetrievalResult
	var err error
	switch req.Mode {
	case retrieval.ModeKeyword:
		results, err = s.keywordSearch(ctx, req, req.TopK)
	case retrieval.ModeVector:
		results, err = s.vectorSearch(ctx, req, req.TopK)
	case retrieval.ModeHybrid:
		if len(req.QueryVector) == 0 {
			err = fmt.Errorf("query vector is required for %s retrieval", req.Mode)
			break
		}
		keywordResults, keywordErr := s.keywordSearch(ctx, req, req.CandidateK)
		if keywordErr != nil {
			err = keywordErr
			break
		}
		vectorResults, vectorErr := s.vectorSearch(ctx, req, req.CandidateK)
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
	return &retrieval.RetrievalResponse{Results: results, LatencyMS: int(time.Since(start).Milliseconds())}, nil
}

func (s *Store) keywordSearch(ctx context.Context, req retrieval.RetrievalRequest, size int) ([]retrieval.RetrievalResult, error) {
	body := map[string]any{
		"size": size,
		"query": map[string]any{
			"bool": map[string]any{
				"filter": s.filters(req),
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
	}
	if req.EnableHighlight {
		body["highlight"] = map[string]any{
			"pre_tags":  []string{"<em>"},
			"post_tags": []string{"</em>"},
			"fields": map[string]any{
				"content": map[string]any{},
			},
		}
	}
	results, err := s.searchBody(ctx, body)
	if err != nil {
		return nil, err
	}
	for i := range results {
		results[i].KeywordScore = results[i].Score
		results[i].FinalScore = results[i].Score
	}
	return results, nil
}

func (s *Store) vectorSearch(ctx context.Context, req retrieval.RetrievalRequest, size int) ([]retrieval.RetrievalResult, error) {
	if len(req.QueryVector) == 0 {
		return nil, fmt.Errorf("query vector is required for %s retrieval", req.Mode)
	}
	body := map[string]any{
		"size": size,
		"knn": map[string]any{
			"field":          "embedding_vector",
			"query_vector":   req.QueryVector,
			"k":              size,
			"num_candidates": max(size*4, size),
			"filter":         s.filters(req),
		},
	}
	results, err := s.searchBody(ctx, body)
	if err != nil {
		return nil, err
	}
	for i := range results {
		results[i].VectorScore = results[i].Score
		results[i].FinalScore = results[i].Score
	}
	return results, nil
}

func (s *Store) searchBody(ctx context.Context, body map[string]any) ([]retrieval.RetrievalResult, error) {
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
			ChunkID:         hit.Source.ChunkID,
			DocumentID:      hit.Source.DocumentID,
			GenerationID:    hit.Source.GenerationID,
			KnowledgeBaseID: hit.Source.KnowledgeBaseID,
			Score:           hit.Score,
			Content:         hit.Source.Content,
			Highlight:       highlight,
			DocumentName:    hit.Source.DocumentName,
			PageNumber:      hit.Source.PageNumber,
			Metadata:        hit.Source.Metadata,
		})
	}
	return results, nil
}

func (s *Store) filters(req retrieval.RetrievalRequest) []map[string]any {
	filters := []map[string]any{{"term": map[string]any{"owner_id": req.OwnerID}}}
	if len(req.KnowledgeBaseIDs) > 0 {
		filters = append(filters, map[string]any{"terms": map[string]any{"knowledge_base_id": req.KnowledgeBaseIDs}})
	}
	if strings.TrimSpace(req.GenerationID) != "" {
		filters = append(filters, map[string]any{"term": map[string]any{"generation_id": req.GenerationID}})
	}
	if strings.TrimSpace(req.EmbeddingProfile) != "" {
		filters = append(filters, map[string]any{"term": map[string]any{"embedding_profile": req.EmbeddingProfile}})
	}
	if len(req.ActiveGenerationIDs) > 0 {
		should := make([]map[string]any, 0, len(req.ActiveGenerationIDs))
		for documentID, generationID := range req.ActiveGenerationIDs {
			if strings.TrimSpace(generationID) == "" {
				continue
			}
			should = append(should, map[string]any{"bool": map[string]any{"filter": []map[string]any{
				{"term": map[string]any{"document_id": documentID}},
				{"term": map[string]any{"generation_id": generationID}},
			}}})
		}
		if len(should) > 0 {
			filters = append(filters, map[string]any{"bool": map[string]any{"should": should, "minimum_should_match": 1}})
		}
	}
	for k, v := range req.Filters {
		if strings.TrimSpace(k) == "" || v == nil {
			continue
		}
		filters = append(filters, map[string]any{"term": map[string]any{k: v}})
	}
	// 排除被显式禁用的文档；旧文档(无 enabled 字段)与 enabled=true 均保留，保证向后兼容。
	filters = append(filters, map[string]any{
		"bool": map[string]any{
			"must_not": map[string]any{"term": map[string]any{"enabled": false}},
		},
	})
	return filters
}

func (s *Store) deleteByQuery(ctx context.Context, filters []map[string]any) error {
	return s.deleteQuery(ctx, map[string]any{"bool": map[string]any{"filter": filters}})
}

func (s *Store) deleteQuery(ctx context.Context, query map[string]any) error {
	body, err := json.Marshal(map[string]any{"query": query})
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

func (s *Store) ensureVectorMapping(ctx context.Context) error {
	mapping := chunkVectorMapping
	// 检查当前映射中是否已经存在 embedding_vector
	res, err := s.client.Indices.GetMapping(
		s.client.Indices.GetMapping.WithIndex(s.index),
		s.client.Indices.GetMapping.WithContext(ctx),
	)
	if err == nil {
		defer res.Body.Close()
		if !res.IsError() {
			if data, err := io.ReadAll(res.Body); err == nil {
				if strings.Contains(string(data), `"embedding_vector"`) {
					mapping = chunkVectorMappingWithoutVector
				}
			}
		}
	}

	res, err = s.client.Indices.PutMapping(
		[]string{s.index},
		strings.NewReader(mapping),
		s.client.Indices.PutMapping.WithContext(ctx),
	)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.IsError() {
		return responseError("update vector mapping", res)
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
	OwnerID             int64          `json:"owner_id"`
	KnowledgeBaseID     int64          `json:"knowledge_base_id"`
	DocumentID          int64          `json:"document_id"`
	GenerationID        string         `json:"generation_id"`
	ChunkID             int64          `json:"chunk_id"`
	ChunkIndex          int            `json:"chunk_index"`
	DocumentName        string         `json:"document_name"`
	FileType            string         `json:"file_type"`
	SectionTitle        string         `json:"section_title"`
	Content             string         `json:"content"`
	ContentHash         string         `json:"content_hash"`
	EmbeddingModel      string         `json:"embedding_model"`
	EmbeddingDimensions int            `json:"embedding_dimensions"`
	EmbeddingProviderID int64          `json:"embedding_provider_id"`
	EmbeddingMetric     string         `json:"embedding_metric"`
	EmbeddingProfile    string         `json:"embedding_profile"`
	PageNumber          *int           `json:"page_number"`
	TokenCount          int            `json:"token_count"`
	Metadata            map[string]any `json:"metadata"`
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
