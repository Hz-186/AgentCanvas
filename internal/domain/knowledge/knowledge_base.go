package knowledge

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"

	"agentcanvas/internal/domain"
)

const (
	RetrievalBackendElasticsearch = "elasticsearch"
	RetrievalBackendMilvus        = "milvus"
	RetrievalModeKeyword          = "keyword"
	RetrievalModeVector           = "vector"
	RetrievalModeHybrid           = "hybrid"
	ChunkMethodFixedToken         = "fixed_token"
	ChunkMethodRecursive          = "recursive"
	EmbeddingMetricCosine         = "COSINE"
	EmbeddingMetricIP             = "IP"
	EmbeddingMetricL2             = "L2"
)

const (
	KnowledgeBaseStatusDisabled = domain.StatusDisabled
	KnowledgeBaseStatusActive   = domain.StatusActive
)

type KnowledgeBase struct {
	ID                  int64      `json:"id" gorm:"primaryKey;column:id"`
	OwnerID             int64      `json:"owner_id" gorm:"column:owner_id"`
	Name                string     `json:"name" gorm:"column:name"`
	Description         string     `json:"description" gorm:"column:description"`
	RetrievalBackend    string     `json:"retrieval_backend" gorm:"column:retrieval_backend"`
	RetrievalMode       string     `json:"retrieval_mode" gorm:"column:retrieval_mode"`
	EmbeddingProviderID *int64     `json:"embedding_provider_id" gorm:"column:embedding_provider_id"`
	EmbeddingModel      string     `json:"embedding_model" gorm:"column:embedding_model"`
	EmbeddingDimensions int        `json:"embedding_dimensions" gorm:"column:embedding_dimensions"`
	EmbeddingMetric     string     `json:"embedding_metric" gorm:"column:embedding_metric"`
	HybridWeight        float64    `json:"hybrid_weight" gorm:"column:hybrid_weight"`
	RerankEnabled       bool       `json:"rerank_enabled" gorm:"column:rerank_enabled"`
	RerankProviderID    *int64     `json:"rerank_provider_id" gorm:"column:rerank_provider_id"`
	RerankModel         string     `json:"rerank_model" gorm:"column:rerank_model"`
	ChunkMethod         string     `json:"chunk_method" gorm:"column:chunk_method"`
	ChunkSize           int        `json:"chunk_size" gorm:"column:chunk_size"`
	ChunkOverlap        int        `json:"chunk_overlap" gorm:"column:chunk_overlap"`
	Status              int        `json:"status" gorm:"column:status"`
	DocumentCount       int        `json:"document_count" gorm:"column:document_count"`
	ChunkCount          int        `json:"chunk_count" gorm:"column:chunk_count"`
	CreatedAt           time.Time  `json:"created_at" gorm:"column:created_at"`
	UpdatedAt           time.Time  `json:"updated_at" gorm:"column:updated_at"`
	DeletedAt           *time.Time `json:"-" gorm:"column:deleted_at"`
}

func (KnowledgeBase) TableName() string { return "knowledge_bases" }

type EmbeddingProfile struct {
	ProviderID int64  `json:"provider_id"`
	Model      string `json:"model"`
	Dimensions int    `json:"dimensions"`
	Metric     string `json:"metric"`
}

func (k KnowledgeBase) EmbeddingProfile() EmbeddingProfile {
	providerID := int64(0)
	if k.EmbeddingProviderID != nil {
		providerID = *k.EmbeddingProviderID
	}
	return EmbeddingProfile{
		ProviderID: providerID,
		Model:      strings.TrimSpace(k.EmbeddingModel),
		Dimensions: k.EmbeddingDimensions,
		Metric:     NormalizeEmbeddingMetric(k.EmbeddingMetric),
	}
}

func (p EmbeddingProfile) Key() string {
	p.Model = strings.TrimSpace(p.Model)
	p.Metric = NormalizeEmbeddingMetric(p.Metric)
	data, _ := json.Marshal(p)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func NormalizeEmbeddingMetric(metric string) string {
	metric = strings.ToUpper(strings.TrimSpace(metric))
	if metric == "" {
		return EmbeddingMetricCosine
	}
	return metric
}

func ValidEmbeddingMetric(metric string) bool {
	switch NormalizeEmbeddingMetric(metric) {
	case EmbeddingMetricCosine, EmbeddingMetricIP, EmbeddingMetricL2:
		return true
	default:
		return false
	}
}
