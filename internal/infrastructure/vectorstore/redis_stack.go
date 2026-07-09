package vectorstore

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

type RedisStackStore struct {
	client     *redis.Client
	prefix     string
	indexName  string
	defaultTTL time.Duration
}

func NewRedisStackStore(client *redis.Client) *RedisStackStore {
	return &RedisStackStore{client: client, prefix: "vs", indexName: "idx"}
}

func (s *RedisStackStore) WithDefaultTTL(ttl time.Duration) *RedisStackStore {
	s.defaultTTL = ttl
	return s
}

func (s *RedisStackStore) EnsureCollection(ctx context.Context, name string, dimensions int, hnsw HNSWConfig) error {
	name = strings.TrimSpace(name)
	if s.client == nil {
		return fmt.Errorf("redis client is not configured")
	}
	if name == "" || dimensions <= 0 {
		return fmt.Errorf("redis stack collection name and dimensions are required")
	}
	config := NormalizeHNSWConfig(hnsw)
	args := []any{
		"FT.CREATE",
		s.index(name),
		"ON", "HASH",
		"PREFIX", "1", s.collectionPrefix(name),
		"SCHEMA",
		"owner_id", "TAG",
		"metadata_json", "TEXT",
		"vector", "VECTOR", "HNSW", "12",
		"TYPE", "FLOAT32",
		"DIM", strconv.Itoa(dimensions),
		"DISTANCE_METRIC", strings.ToUpper(config.MetricType),
		"M", strconv.Itoa(config.M),
		"EF_CONSTRUCTION", strconv.Itoa(config.EFConstruction),
		"EF_RUNTIME", strconv.Itoa(config.EFSearch),
	}
	if err := s.client.Do(ctx, args...).Err(); err != nil && !strings.Contains(strings.ToLower(err.Error()), "index already exists") {
		return err
	}
	return nil
}

func (s *RedisStackStore) Upsert(ctx context.Context, collection string, docs []VectorDocument) error {
	collection = strings.TrimSpace(collection)
	if s.client == nil {
		return fmt.Errorf("redis client is not configured")
	}
	if collection == "" || len(docs) == 0 {
		return fmt.Errorf("redis stack collection and docs are required")
	}
	pipe := s.client.Pipeline()
	for _, doc := range docs {
		if strings.TrimSpace(doc.ID) == "" || len(doc.Vector) == 0 {
			return fmt.Errorf("redis stack document id and vector are required")
		}
		metadataJSON, err := json.Marshal(doc.Metadata)
		if err != nil {
			return err
		}
		ownerID := metadataString(doc.Metadata, "owner_id")
		key := s.DocumentKey(collection, doc.ID)
		fields := map[string]any{
			"owner_id":      ownerID,
			"metadata_json": string(metadataJSON),
			"vector":        float32SliceToBytes(doc.Vector),
		}
		pipe.HSet(ctx, key, fields)
		if s.defaultTTL > 0 {
			pipe.Expire(ctx, key, s.defaultTTL)
		}
	}
	_, err := pipe.Exec(ctx)
	return err
}

func (s *RedisStackStore) Delete(ctx context.Context, collection string, ids []string) error {
	collection = strings.TrimSpace(collection)
	if s.client == nil {
		return fmt.Errorf("redis client is not configured")
	}
	if collection == "" || len(ids) == 0 {
		return fmt.Errorf("redis stack collection and ids are required")
	}
	keys := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		keys = append(keys, s.DocumentKey(collection, id))
	}
	if len(keys) == 0 {
		return nil
	}
	return s.client.Del(ctx, keys...).Err()
}

func (s *RedisStackStore) Search(ctx context.Context, req SearchRequest) ([]SearchResult, error) {
	if s.client == nil {
		return nil, fmt.Errorf("redis client is not configured")
	}
	req.Collection = strings.TrimSpace(req.Collection)
	if req.Collection == "" || len(req.Vector) == 0 {
		return nil, fmt.Errorf("redis stack collection and query vector are required")
	}
	if req.TopK <= 0 {
		req.TopK = 8
	}
	query := filterQuery(req.Filter) + "=>[KNN " + strconv.Itoa(req.TopK) + " @vector $query_vector AS score]"
	args := []any{
		"FT.SEARCH",
		s.index(req.Collection),
		query,
		"PARAMS", "2", "query_vector", float32SliceToBytes(req.Vector),
		"SORTBY", "score",
		"RETURN", "1", "metadata_json",
		"LIMIT", "0", strconv.Itoa(req.TopK),
		"DIALECT", "2",
	}
	result, err := s.client.Do(ctx, args...).Result()
	if err != nil {
		return nil, err
	}
	items, ok := result.([]any)
	if !ok || len(items) < 1 {
		return nil, nil
	}
	results := make([]SearchResult, 0, req.TopK)
	for i := 1; i+1 < len(items); i += 2 {
		key, _ := items[i].(string)
		fields, ok := items[i+1].([]any)
		if !ok {
			continue
		}
		parsed := parseSearchFields(fields)
		metadata := map[string]any{}
		if raw := strings.TrimSpace(parsed["metadata_json"]); raw != "" {
			_ = json.Unmarshal([]byte(raw), &metadata)
		}
		id := documentIDFromKey(key)
		results = append(results, SearchResult{
			ID:       id,
			Score:    parseFloat(parsed["score"]),
			Metadata: metadata,
		})
	}
	return results, nil
}

func (s *RedisStackStore) Expire(ctx context.Context, collection, id string, ttl time.Duration) error {
	if s.client == nil || ttl <= 0 {
		return nil
	}
	return s.client.Expire(ctx, s.DocumentKey(collection, id), ttl).Err()
}

func (s *RedisStackStore) DocumentKey(collection, id string) string {
	return s.collectionPrefix(collection) + strings.TrimSpace(id)
}

func (s *RedisStackStore) collectionPrefix(name string) string {
	return s.prefix + ":" + strings.TrimSpace(name) + ":"
}

func (s *RedisStackStore) index(name string) string {
	return s.indexName + ":" + strings.TrimSpace(name)
}

func filterQuery(filter map[string]any) string {
	if len(filter) == 0 {
		return "*"
	}
	parts := make([]string, 0, len(filter))
	if ownerID := metadataString(filter, "owner_id"); ownerID != "" {
		parts = append(parts, "@owner_id:{"+escapeTagValue(ownerID)+"}")
	}
	if len(parts) == 0 {
		return "*"
	}
	return strings.Join(parts, " ")
}

func parseSearchFields(fields []any) map[string]string {
	parsed := make(map[string]string, len(fields)/2)
	for i := 0; i+1 < len(fields); i += 2 {
		key, _ := fields[i].(string)
		switch value := fields[i+1].(type) {
		case string:
			parsed[key] = value
		case []byte:
			parsed[key] = string(value)
		default:
			parsed[key] = fmt.Sprintf("%v", value)
		}
	}
	return parsed
}

func documentIDFromKey(key string) string {
	if idx := strings.LastIndex(key, ":"); idx >= 0 && idx+1 < len(key) {
		return key[idx+1:]
	}
	return key
}

func escapeTagValue(value string) string {
	replacer := strings.NewReplacer(
		"-", `\-`,
		" ", `\ `,
		"{", `\{`,
		"}", `\}`,
		"|", `\|`,
	)
	return replacer.Replace(value)
}

func metadataString(metadata map[string]any, key string) string {
	if len(metadata) == 0 {
		return ""
	}
	value, ok := metadata[key]
	if !ok || value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case fmt.Stringer:
		return strings.TrimSpace(typed.String())
	case int:
		return strconv.Itoa(typed)
	case int64:
		return strconv.FormatInt(typed, 10)
	case float64:
		if math.Trunc(typed) == typed {
			return strconv.FormatInt(int64(typed), 10)
		}
		return strconv.FormatFloat(typed, 'f', -1, 64)
	default:
		return strings.TrimSpace(fmt.Sprintf("%v", typed))
	}
}

func parseFloat(value string) float64 {
	parsed, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil {
		return 0
	}
	return parsed
}

func float32SliceToBytes(vector []float32) []byte {
	buf := make([]byte, len(vector)*4)
	for i, value := range vector {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(value))
	}
	return buf
}
