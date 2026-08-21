package vectorstore

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

type MilvusStore struct {
	Address string
	Token   string
	Default HNSWConfig
	Client  *http.Client
}

type milvusEntityRecord struct {
	ID       string         `json:"id"`
	Text     string         `json:"text"`
	Vector   []float32      `json:"vector"`
	Metadata milvusMetadata `json:"metadata"`
}

type milvusMetadata map[string]any

func (m *milvusMetadata) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || bytes.Equal(data, []byte("null")) {
		*m = nil
		return nil
	}
	if data[0] == '"' {
		var encoded string
		if err := json.Unmarshal(data, &encoded); err != nil {
			return err
		}
		data = []byte(encoded)
	}
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*m = decoded
	return nil
}

func NewMilvusStore(address, token string, hnsw HNSWConfig) *MilvusStore {
	return &MilvusStore{Address: strings.TrimSpace(address), Token: token, Default: NormalizeHNSWConfig(hnsw), Client: &http.Client{Timeout: 15 * time.Second}}
}

func (s *MilvusStore) EnsureCollection(ctx context.Context, name string, dimensions int, hnsw HNSWConfig) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("milvus collection name is required")
	}
	if err := s.validate(); err != nil {
		return err
	}
	config := s.hnsw(hnsw)
	var has struct {
		Data struct {
			Has bool `json:"has"`
		} `json:"data"`
	}
	if err := s.do(ctx, "/v2/vectordb/collections/has", map[string]any{"collectionName": name}, &has); err != nil {
		return err
	}
	if !has.Data.Has {
		create := map[string]any{
			"collectionName": name,
			"schema": map[string]any{
				"autoId":             false,
				"enableDynamicField": true,
				"functions": []map[string]any{{
					"name":             "text_bm25_emb",
					"type":             "BM25",
					"inputFieldNames":  []string{"text"},
					"outputFieldNames": []string{"sparse"},
				}},
				"fields": []map[string]any{
					{"fieldName": "id", "dataType": "VarChar", "isPrimary": true, "elementTypeParams": map[string]any{"max_length": "128"}},
					{"fieldName": "text", "dataType": "VarChar", "elementTypeParams": map[string]any{"max_length": "65535", "enable_analyzer": true}},
					{"fieldName": "sparse", "dataType": "SparseFloatVector"},
					{"fieldName": "metadata", "dataType": "JSON"},
				},
			},
		}
		if dimensions > 0 {
			schema := create["schema"].(map[string]any)
			fields := schema["fields"].([]map[string]any)
			fields = append(fields, map[string]any{"fieldName": "vector", "dataType": "FloatVector", "elementTypeParams": map[string]any{"dim": fmt.Sprintf("%d", dimensions)}})
			schema["fields"] = fields
		}
		if err := s.do(ctx, "/v2/vectordb/collections/create", create, nil); err != nil {
			return err
		}
	}
	indexParams := []map[string]any{{
		"fieldName":  "sparse",
		"indexName":  "sparse_bm25_idx",
		"indexType":  "SPARSE_INVERTED_INDEX",
		"metricType": "BM25",
		"params":     map[string]any{"inverted_index_algo": "DAAT_MAXSCORE", "bm25_k1": 1.2, "bm25_b": 0.75},
	}}
	if dimensions > 0 {
		indexParams = append(indexParams, map[string]any{
			"fieldName":  "vector",
			"indexName":  "vector_hnsw_idx",
			"indexType":  "HNSW",
			"metricType": strings.ToUpper(config.MetricType),
			"params":     map[string]any{"M": config.M, "efConstruction": config.EFConstruction},
		})
	}
	index := map[string]any{
		"collectionName": name,
		"indexParams":    indexParams,
	}
	if err := s.do(ctx, "/v2/vectordb/indexes/create", index, nil); err != nil && !isMilvusAlreadyExists(err) {
		return err
	}
	if err := s.do(ctx, "/v2/vectordb/collections/load", map[string]any{"collectionName": name}, nil); err != nil && !isMilvusAlreadyLoaded(err) {
		return err
	}
	return nil
}

func (s *MilvusStore) hnsw(config HNSWConfig) HNSWConfig {
	if config == (HNSWConfig{}) {
		return s.Default
	}
	return NormalizeHNSWConfig(config)
}

func (s *MilvusStore) validate() error {
	if strings.TrimSpace(s.Address) == "" {
		return fmt.Errorf("milvus address is required")
	}
	if _, err := s.baseURL(); err != nil {
		return err
	}
	return nil
}

func (s *MilvusStore) Upsert(ctx context.Context, collection string, docs []VectorDocument) error {
	collection = strings.TrimSpace(collection)
	if collection == "" || len(docs) == 0 {
		return fmt.Errorf("milvus collection and docs are required")
	}
	if err := s.validate(); err != nil {
		return err
	}
	data := make([]map[string]any, 0, len(docs))
	dimensions := 0
	for _, doc := range docs {
		if strings.TrimSpace(doc.ID) == "" || (len(doc.Vector) == 0 && strings.TrimSpace(doc.Text) == "") {
			return fmt.Errorf("milvus document id and text or vector are required")
		}
		if len(doc.Vector) > 0 && dimensions == 0 {
			dimensions = len(doc.Vector)
		}
		if len(doc.Vector) > 0 && len(doc.Vector) != dimensions {
			return fmt.Errorf("milvus vector dimensions mismatch")
		}
		item := map[string]any{"id": doc.ID, "text": doc.Text, "metadata": doc.Metadata}
		if len(doc.Vector) > 0 {
			item["vector"] = doc.Vector
		}
		data = append(data, item)
	}
	return s.do(ctx, "/v2/vectordb/entities/upsert", map[string]any{"collectionName": collection, "data": data}, nil)
}

func (s *MilvusStore) Delete(ctx context.Context, collection string, ids []string) error {
	collection = strings.TrimSpace(collection)
	if collection == "" || len(ids) == 0 {
		return fmt.Errorf("milvus collection and ids are required")
	}
	if err := s.validate(); err != nil {
		return err
	}
	quoted := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		quoted = append(quoted, fmt.Sprintf("id in ['%s']", strings.ReplaceAll(id, "'", "\\'")))
	}
	if len(quoted) == 0 {
		return fmt.Errorf("milvus ids are required")
	}
	return s.do(ctx, "/v2/vectordb/entities/delete", map[string]any{"collectionName": collection, "filter": strings.Join(quoted, " || ")}, nil)
}

func (s *MilvusStore) DeleteByFilter(ctx context.Context, collection string, filter map[string]any) error {
	collection = strings.TrimSpace(collection)
	if collection == "" || len(filter) == 0 {
		return fmt.Errorf("milvus collection and filter are required")
	}
	if err := s.validate(); err != nil {
		return err
	}
	expr, err := milvusFilter(filter)
	if err != nil {
		return err
	}
	if expr == "" {
		return fmt.Errorf("milvus filter is empty")
	}
	return s.do(ctx, "/v2/vectordb/entities/delete", map[string]any{"collectionName": collection, "filter": expr}, nil)
}

func (s *MilvusStore) DeleteByFilterExcept(ctx context.Context, collection string, filter map[string]any, field string, value any) error {
	collection = strings.TrimSpace(collection)
	field = strings.TrimSpace(field)
	if collection == "" || len(filter) == 0 || !milvusFilterFieldPattern.MatchString(field) {
		return fmt.Errorf("milvus collection, filter, and exclusion field are required")
	}
	if err := s.validate(); err != nil {
		return err
	}
	base, err := milvusFilter(filter)
	if err != nil {
		return err
	}
	literal, ok := milvusLiteral(value)
	if !ok {
		return fmt.Errorf("unsupported milvus exclusion value %T", value)
	}
	expr := base + fmt.Sprintf(" && metadata['%s'] != %s", field, literal)
	return s.do(ctx, "/v2/vectordb/entities/delete", map[string]any{"collectionName": collection, "filter": expr}, nil)
}

func (s *MilvusStore) QueryByFilter(ctx context.Context, collection string, filter map[string]any, limit int) ([]VectorDocument, error) {
	collection = strings.TrimSpace(collection)
	if collection == "" || len(filter) == 0 {
		return nil, fmt.Errorf("milvus collection and filter are required")
	}
	if err := s.validate(); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 10000
	}
	expr, err := milvusFilter(filter)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Data []milvusEntityRecord `json:"data"`
	}
	query := map[string]any{
		"collectionName": collection,
		"filter":         expr,
		"limit":          limit,
		"outputFields":   []string{"id", "text", "vector", "metadata"},
	}
	if err := s.do(ctx, "/v2/vectordb/entities/query", query, &resp); err != nil {
		query["outputFields"] = []string{"id", "text", "metadata"}
		if retryErr := s.do(ctx, "/v2/vectordb/entities/query", query, &resp); retryErr != nil {
			return nil, retryErr
		}
	}
	results := make([]VectorDocument, 0, len(resp.Data))
	for _, item := range resp.Data {
		results = append(results, VectorDocument{ID: item.ID, Text: item.Text, Vector: item.Vector, Metadata: map[string]any(item.Metadata)})
	}
	return results, nil
}

func (s *MilvusStore) UpdateMetadataByFilter(ctx context.Context, collection string, filter map[string]any, mutate func(map[string]any) map[string]any) error {
	if mutate == nil {
		return nil
	}
	docs, err := s.QueryByFilter(ctx, collection, filter, 10000)
	if err != nil {
		return err
	}
	if len(docs) == 0 {
		return nil
	}
	updated := make([]VectorDocument, 0, len(docs))
	for _, doc := range docs {
		metadata := cloneMetadata(doc.Metadata)
		metadata = mutate(metadata)
		updated = append(updated, VectorDocument{ID: doc.ID, Text: doc.Text, Vector: doc.Vector, Metadata: metadata})
	}
	return s.Upsert(ctx, collection, updated)
}

func (s *MilvusStore) Search(ctx context.Context, req SearchRequest) ([]SearchResult, error) {
	req.Collection = strings.TrimSpace(req.Collection)
	if req.Collection == "" || len(req.Vector) == 0 {
		return nil, fmt.Errorf("milvus collection and query vector are required")
	}
	if err := s.validate(); err != nil {
		return nil, err
	}
	if req.TopK <= 0 {
		req.TopK = 8
	}
	config := s.hnsw(req.HNSW)
	body := map[string]any{
		"collectionName": req.Collection,
		"data":           [][]float32{req.Vector},
		"limit":          req.TopK,
		"annsField":      "vector",
		"outputFields":   []string{"id", "metadata"},
		"searchParams":   map[string]any{"metricType": strings.ToUpper(config.MetricType), "params": map[string]any{"ef": config.EFSearch}},
	}
	if len(req.Filter) > 0 || len(req.AnyFilters) > 0 {
		expr, err := milvusSearchFilter(req.Filter, req.AnyFilters)
		if err != nil {
			return nil, err
		}
		if expr != "" {
			body["filter"] = expr
		}
	}
	var resp struct {
		Data []struct {
			ID       string         `json:"id"`
			Distance float64        `json:"distance"`
			Score    float64        `json:"score"`
			Metadata milvusMetadata `json:"metadata"`
		} `json:"data"`
	}
	if err := s.do(ctx, "/v2/vectordb/entities/search", body, &resp); err != nil {
		return nil, err
	}
	results := make([]SearchResult, 0, len(resp.Data))
	for _, item := range resp.Data {
		score := item.Score
		if score == 0 {
			score = item.Distance
		}
		results = append(results, SearchResult{ID: item.ID, Score: score, Metadata: map[string]any(item.Metadata)})
	}
	return results, nil
}

// SearchText performs Milvus BM25 full-text search using the collection's
// internally generated sparse vector field.
func (s *MilvusStore) SearchText(ctx context.Context, req SearchRequest) ([]SearchResult, error) {
	req.Collection = strings.TrimSpace(req.Collection)
	if req.Collection == "" || strings.TrimSpace(req.QueryText) == "" {
		return nil, fmt.Errorf("milvus collection and query text are required")
	}
	if err := s.validate(); err != nil {
		return nil, err
	}
	if req.TopK <= 0 {
		req.TopK = 8
	}
	body := map[string]any{
		"collectionName": req.Collection,
		"data":           []string{req.QueryText},
		"limit":          req.TopK,
		"annsField":      "sparse",
		"outputFields":   []string{"id", "text", "metadata"},
	}
	if len(req.Filter) > 0 || len(req.AnyFilters) > 0 {
		expr, err := milvusSearchFilter(req.Filter, req.AnyFilters)
		if err != nil {
			return nil, err
		}
		if expr != "" {
			body["filter"] = expr
		}
	}
	var resp struct {
		Data []struct {
			ID       string         `json:"id"`
			Distance float64        `json:"distance"`
			Score    float64        `json:"score"`
			Metadata milvusMetadata `json:"metadata"`
		} `json:"data"`
	}
	if err := s.do(ctx, "/v2/vectordb/entities/search", body, &resp); err != nil {
		return nil, err
	}
	results := make([]SearchResult, 0, len(resp.Data))
	for _, item := range resp.Data {
		score := item.Score
		if score == 0 {
			score = item.Distance
		}
		results = append(results, SearchResult{ID: item.ID, Score: score, Metadata: map[string]any(item.Metadata)})
	}
	return results, nil
}

func (s *MilvusStore) do(ctx context.Context, path string, payload any, out any) error {
	base, err := s.baseURL()
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, base.ResolveReference(&url.URL{Path: path}).String(), bytes.NewReader(encoded))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	if strings.TrimSpace(s.Token) != "" {
		request.Header.Set("Authorization", "Bearer "+strings.TrimSpace(s.Token))
	}
	client := s.Client
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		return err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("milvus request %s failed: status=%d body=%s", path, response.StatusCode, strings.TrimSpace(string(data)))
	}
	var envelope struct {
		Code    int             `json:"code"`
		Message string          `json:"message"`
		Data    json.RawMessage `json:"data"`
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, &envelope); err == nil && (envelope.Code != 0 || len(envelope.Data) > 0 || envelope.Message != "") {
		if envelope.Code != 0 {
			return fmt.Errorf("milvus request %s failed: code=%d message=%s", path, envelope.Code, envelope.Message)
		}
		if out == nil {
			return nil
		}
		return json.Unmarshal(data, out)
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(data, out)
}

func (s *MilvusStore) baseURL() (*url.URL, error) {
	address := strings.TrimSpace(s.Address)
	if !strings.Contains(address, "://") {
		address = "http://" + address
	}
	parsed, err := url.Parse(address)
	if err != nil || parsed.Host == "" {
		return nil, fmt.Errorf("invalid milvus address: %s", s.Address)
	}
	return parsed, nil
}

func milvusFilter(filter map[string]any) (string, error) {
	keys := make([]string, 0, len(filter))
	for key := range filter {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		value := filter[key]
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if !milvusFilterFieldPattern.MatchString(key) {
			return "", fmt.Errorf("invalid milvus filter field: %s", key)
		}
		switch v := value.(type) {
		case string, int, int64, int32, float64, float32, bool:
			literal, _ := milvusLiteral(v)
			parts = append(parts, fmt.Sprintf("metadata['%s'] == %s", key, literal))
		case []int64:
			if len(v) > 0 {
				parts = append(parts, fmt.Sprintf("metadata['%s'] in [%s]", key, joinMilvusValues(v)))
			}
		case []int:
			if len(v) > 0 {
				parts = append(parts, fmt.Sprintf("metadata['%s'] in [%s]", key, joinMilvusValues(v)))
			}
		case []string:
			if len(v) > 0 {
				parts = append(parts, fmt.Sprintf("metadata['%s'] in [%s]", key, joinMilvusValues(v)))
			}
		case []any:
			if joined := joinMilvusValues(v); joined != "" {
				parts = append(parts, fmt.Sprintf("metadata['%s'] in [%s]", key, joined))
			}
		}
	}
	return strings.Join(parts, " && "), nil
}

func milvusLiteral(value any) (string, bool) {
	switch v := value.(type) {
	case string:
		return "'" + escapeMilvusString(v) + "'", true
	case int:
		return strconv.Itoa(v), true
	case int64:
		return strconv.FormatInt(v, 10), true
	case int32:
		return strconv.FormatInt(int64(v), 10), true
	case float64:
		return strconv.FormatFloat(v, 'g', -1, 64), true
	case float32:
		return strconv.FormatFloat(float64(v), 'g', -1, 32), true
	case bool:
		return strconv.FormatBool(v), true
	default:
		return "", false
	}
}

func milvusSearchFilter(filter map[string]any, anyFilters []map[string]any) (string, error) {
	base, err := milvusFilter(filter)
	if err != nil {
		return "", err
	}
	groups := make([]string, 0, len(anyFilters))
	for _, candidate := range anyFilters {
		expr, err := milvusFilter(candidate)
		if err != nil {
			return "", err
		}
		if expr != "" {
			groups = append(groups, "("+expr+")")
		}
	}
	if len(groups) == 0 {
		return base, nil
	}
	any := "(" + strings.Join(groups, " || ") + ")"
	if base == "" {
		return any, nil
	}
	return base + " && " + any, nil
}

var milvusFilterFieldPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func escapeMilvusString(value string) string {
	return strings.ReplaceAll(value, "'", "\\'")
}

func cloneMetadata(metadata map[string]any) map[string]any {
	if len(metadata) == 0 {
		return map[string]any{}
	}
	cloned := make(map[string]any, len(metadata))
	for key, value := range metadata {
		cloned[key] = value
	}
	return cloned
}

func joinMilvusValues[T any](values []T) string {
	parts := make([]string, 0, len(values))
	for _, value := range values {
		switch v := any(value).(type) {
		case string:
			parts = append(parts, fmt.Sprintf("'%s'", escapeMilvusString(v)))
		case int:
			parts = append(parts, fmt.Sprintf("%d", v))
		case int64:
			parts = append(parts, fmt.Sprintf("%d", v))
		case int32:
			parts = append(parts, fmt.Sprintf("%d", v))
		case float64:
			parts = append(parts, fmt.Sprintf("%v", v))
		case float32:
			parts = append(parts, fmt.Sprintf("%v", v))
		case bool:
			parts = append(parts, fmt.Sprintf("%v", v))
		}
	}
	return strings.Join(parts, ", ")
}

func isMilvusAlreadyExists(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "already exist") || strings.Contains(msg, "duplicated")
}

func isMilvusAlreadyLoaded(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "already loaded") || strings.Contains(msg, "loaded")
}
