package vectorstore

import "context"

type HNSWConfig struct {
	M              int    `json:"m"`
	EFConstruction int    `json:"ef_construction"`
	EFSearch       int    `json:"ef_search"`
	MetricType     string `json:"metric_type"`
}

func DefaultHNSWConfig() HNSWConfig {
	return HNSWConfig{M: 16, EFConstruction: 200, EFSearch: 64, MetricType: "COSINE"}
}

func NormalizeHNSWConfig(config HNSWConfig) HNSWConfig {
	defaults := DefaultHNSWConfig()
	if config.M <= 0 {
		config.M = defaults.M
	}
	if config.EFConstruction <= 0 {
		config.EFConstruction = defaults.EFConstruction
	}
	if config.EFSearch <= 0 {
		config.EFSearch = defaults.EFSearch
	}
	if config.MetricType == "" {
		config.MetricType = defaults.MetricType
	}
	return config
}

type VectorDocument struct {
	ID       string         `json:"id"`
	Vector   []float32      `json:"vector"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

type SearchRequest struct {
	Collection string         `json:"collection"`
	Vector     []float32      `json:"vector"`
	TopK       int            `json:"top_k"`
	Filter     map[string]any `json:"filter,omitempty"`
	HNSW       HNSWConfig     `json:"hnsw,omitempty"`
}

type SearchResult struct {
	ID       string         `json:"id"`
	Score    float64        `json:"score"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

type Store interface {
	EnsureCollection(ctx context.Context, name string, dimensions int, hnsw HNSWConfig) error
	Upsert(ctx context.Context, collection string, docs []VectorDocument) error
	Delete(ctx context.Context, collection string, ids []string) error
	Search(ctx context.Context, req SearchRequest) ([]SearchResult, error)
}
