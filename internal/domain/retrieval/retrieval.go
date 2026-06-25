package retrieval

import (
	"context"
	"time"
)

type Mode string

const (
	ModeKeyword Mode = "keyword"
	ModeVector  Mode = "vector"
	ModeHybrid  Mode = "hybrid"
)

type ChunkIndexDocument struct {
	OwnerID             int64
	KBID                int64
	DocumentID          int64
	ChunkID             int64
	ChunkIndex          int
	DocumentName        string
	FileType            string
	SectionTitle        string
	Content             string
	ContentHash         string
	EmbeddingVector     []float32
	EmbeddingModel      string
	EmbeddingDimensions int
	Enabled             bool
	PageNo              *int
	TokenCount          int
	Metadata            map[string]any
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

type RetrievalRequest struct {
	OwnerID         int64
	KBIDs           []int64
	Query           string
	TopK            int
	CandidateK      int
	Mode            Mode
	QueryVector     []float32
	HybridWeight    float64
	Filters         map[string]any
	EnableHighlight bool
}

type RetrievalResult struct {
	ChunkID      int64          `json:"chunk_id"`
	DocumentID   int64          `json:"document_id"`
	KBID         int64          `json:"kb_id"`
	Score        float64        `json:"score"`
	KeywordScore float64        `json:"keyword_score"`
	VectorScore  float64        `json:"vector_score"`
	FinalScore   float64        `json:"final_score"`
	Content      string         `json:"content"`
	Highlight    string         `json:"highlight"`
	DocumentName string         `json:"document_name"`
	PageNo       *int           `json:"page_no"`
	Metadata     map[string]any `json:"metadata"`
}

type RetrievalResponse struct {
	Results   []RetrievalResult `json:"results"`
	LatencyMS int               `json:"latency_ms"`
}

type Indexer interface {
	EnsureIndex(ctx context.Context) error
	IndexChunks(ctx context.Context, docs []ChunkIndexDocument) error
	SetDocumentEnabled(ctx context.Context, ownerID, documentID int64, enabled bool) error
	DeleteByDocument(ctx context.Context, ownerID, documentID int64) error
	DeleteByKnowledgeBase(ctx context.Context, ownerID, kbID int64) error
}

type Retriever interface {
	Search(ctx context.Context, req RetrievalRequest) (*RetrievalResponse, error)
}
