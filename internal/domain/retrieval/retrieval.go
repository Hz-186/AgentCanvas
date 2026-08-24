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
	KnowledgeBaseID     int64
	DocumentID          int64
	GenerationID        string
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
	EmbeddingProviderID int64
	EmbeddingMetric     string
	EmbeddingProfile    string
	Enabled             bool
	PageNumber          *int
	TokenCount          int
	Metadata            map[string]any
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

type RetrievalRequest struct {
	OwnerID             int64
	KnowledgeBaseIDs    []int64
	GenerationID        string
	ActiveGenerationIDs map[int64]string
	EmbeddingProfile    string
	Query               string
	Conversation        []QueryTurn
	RewriteProviderID   int64
	RewriteModel        string
	TopK                int
	CandidateK          int
	Mode                Mode
	QueryVector         []float32
	VectorWeight        float64
	Filters             map[string]any
	EnableHighlight     bool
}

type QueryTurn struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type HardConstraint struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

type QueryPlan struct {
	OriginalQuery string `json:"original_query"`
	// Standardized questions after removing spaces and special punctuation marks
	NormalizedQuery string `json:"normalized_query"`
	// after replace "it"
	ResolvedQuery         string           `json:"resolved_query,omitempty"`
	PreciseQuery          string           `json:"precise_query"`
	HardConstraints       []HardConstraint `json:"hard_constraints,omitempty"`
	Paraphrases           []string         `json:"paraphrases,omitempty"`
	SynonymQueries        []string         `json:"synonym_queries,omitempty"`
	Subqueries            []string         `json:"subqueries,omitempty"`
	UnresolvedReferences  []string         `json:"unresolved_references,omitempty"`
	NeedsClarification    bool             `json:"needs_clarification"`
	ClarificationQuestion string           `json:"clarification_question,omitempty"`
	RewriteInvoked        bool             `json:"rewrite_invoked"`
	Confidence            float64          `json:"confidence"`
}

type Clarification struct {
	Required   bool     `json:"required"`
	Question   string   `json:"question,omitempty"`
	References []string `json:"unresolved_references,omitempty"`
}

type RetrievalResult struct {
	ChunkID         int64          `json:"chunk_id"`
	DocumentID      int64          `json:"document_id"`
	GenerationID    string         `json:"generation_id,omitempty"`
	KnowledgeBaseID int64          `json:"knowledge_base_id"`
	Score           float64        `json:"score"`
	KeywordScore    float64        `json:"keyword_score"`
	VectorScore     float64        `json:"vector_score"`
	FinalScore      float64        `json:"final_score"` // after Rerank
	Content         string         `json:"content"`
	Highlight       string         `json:"highlight"`
	DocumentName    string         `json:"document_name"`
	PageNumber      *int           `json:"page_number"`
	Metadata        map[string]any `json:"metadata"`
}

type RetrievalResponse struct {
	Results       []RetrievalResult      `json:"results"` // List of all chunks results found
	LatencyMS     int                    `json:"latency_ms"`
	Diagnostics   *RecallDiagnostics     `json:"diagnostics,omitempty"`
	Trace         []RetrievalTraceRecord `json:"trace,omitempty"`
	QueryPlan     *QueryPlan             `json:"query_plan,omitempty"`
	Clarification *Clarification         `json:"clarification,omitempty"`
}

// RecallDiagnostics Diagnostics
type RecallDiagnostics struct {
	LowRecall bool `json:"low_recall"`
	// such like "vector similarity lower than threshold"
	Reason            string  `json:"reason,omitempty"`
	ResultCount       int     `json:"result_count"`
	RequestedTopK     int     `json:"requested_top_k"`
	CandidateK        int     `json:"candidate_k"`
	KeywordCount      int     `json:"keyword_count"`
	VectorCount       int     `json:"vector_count"`
	MaxScore          float64 `json:"max_score"`
	AverageScore      float64 `json:"average_score"`
	ScoreGap          float64 `json:"score_gap"`
	Expanded          bool    `json:"expanded"`
	ExpandedCandidate int     `json:"expanded_candidate_k,omitempty"`
	FallbackMode      Mode    `json:"fallback_mode,omitempty"`
	Reranked          bool    `json:"reranked"`
}

type RetrievalTraceRecord struct {
	Stage    string         `json:"stage"`
	Mode     Mode           `json:"mode,omitempty"`
	Message  string         `json:"message,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

// Indexer maintains searchable document indexes.
type Indexer interface {
	// EnsureIndex creates the backing index or collection when needed.
	EnsureIndex(ctx context.Context) error
	// IndexChunks writes document chunks to the backing index.
	IndexChunks(ctx context.Context, docs []ChunkIndexDocument) error
	// SetDocumentEnabled toggles all indexed chunks for a document.
	SetDocumentEnabled(ctx context.Context, ownerID, documentID int64, enabled bool) error
	// DeleteByDocument removes all indexed chunks for a document.
	DeleteByDocument(ctx context.Context, ownerID, documentID int64) error
	// DeleteByKnowledgeBase removes all indexed chunks for a knowledge base.
	DeleteByKnowledgeBase(ctx context.Context, ownerID, kbID int64) error
}

// Retriever searches indexed document chunks.
type Retriever interface {
	Search(ctx context.Context, req RetrievalRequest) (*RetrievalResponse, error)
}

// Backend combines indexing and retrieval capabilities.
type Backend interface {
	Indexer
	Retriever
}
