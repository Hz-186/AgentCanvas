package composite

import (
	"context"
	"fmt"
	"math"
	"time"

	"agentcanvas/internal/domain/retrieval"
	"agentcanvas/internal/domain/retrieval/fusion"
)

// Store coordinates primary and vector retrieval backends.
type Store struct {
	// Primary handles keyword indexing and retrieval.
	Primary retrieval.Backend
	// Vector handles vector indexing and retrieval.
	Vector retrieval.Backend
	shared bool
}

// NewShared creates a store where one backend provides both atomic search modes.
// Lifecycle operations are sent to the shared backend only once.
func NewShared(backend retrieval.Backend) *Store {
	return &Store{Primary: backend, Vector: backend, shared: true}
}

// New creates a store backed by primary and vector retrieval engines.
func New(primary, vector retrieval.Backend) *Store {
	return &Store{
		Primary: primary,
		Vector:  vector,
	}
}

func (s *Store) EnsureIndex(ctx context.Context) error {
	if s.Primary != nil {
		if err := s.Primary.EnsureIndex(ctx); err != nil {
			return err
		}
	}
	if s.Vector != nil && !s.shared {
		return s.Vector.EnsureIndex(ctx)
	}
	return nil
}

func (s *Store) IndexChunks(ctx context.Context, docs []retrieval.ChunkIndexDocument) error {
	if s.Primary != nil {
		if err := s.Primary.IndexChunks(ctx, docs); err != nil {
			return err
		}
	}
	if s.Vector != nil && !s.shared {
		return s.Vector.IndexChunks(ctx, docs)
	}
	return nil
}

func (s *Store) SetDocumentEnabled(ctx context.Context, ownerID, documentID int64, enabled bool) error {
	if s.Primary != nil {
		if err := s.Primary.SetDocumentEnabled(ctx, ownerID, documentID, enabled); err != nil {
			return err
		}
	}
	if s.Vector != nil && !s.shared {
		return s.Vector.SetDocumentEnabled(ctx, ownerID, documentID, enabled)
	}
	return nil
}

func (s *Store) DeleteByDocument(ctx context.Context, ownerID, documentID int64) error {
	if s.Primary != nil {
		if err := s.Primary.DeleteByDocument(ctx, ownerID, documentID); err != nil {
			return err
		}
	}
	if s.Vector != nil && !s.shared {
		return s.Vector.DeleteByDocument(ctx, ownerID, documentID)
	}
	return nil
}

func (s *Store) DeleteByKnowledgeBase(ctx context.Context, ownerID, kbID int64) error {
	if s.Primary != nil {
		if err := s.Primary.DeleteByKnowledgeBase(ctx, ownerID, kbID); err != nil {
			return err
		}
	}
	if s.Vector != nil && !s.shared {
		return s.Vector.DeleteByKnowledgeBase(ctx, ownerID, kbID)
	}
	return nil
}

func (s *Store) Search(ctx context.Context, req retrieval.RetrievalRequest) (*retrieval.RetrievalResponse, error) {
	switch req.Mode {
	case retrieval.ModeKeyword:
		if s.Primary == nil {
			return nil, fmt.Errorf("keyword retrieval backend is not configured")
		}
		return s.Primary.Search(ctx, req)
	case retrieval.ModeVector:
		if s.Vector == nil {
			return nil, fmt.Errorf("vector retrieval backend is not configured")
		}
		return s.Vector.Search(ctx, req)
	case retrieval.ModeHybrid:
		return s.hybridSearch(ctx, req)
	default:
		return nil, fmt.Errorf("unsupported retrieval mode: %s", req.Mode)
	}
}

func (s *Store) hybridSearch(ctx context.Context, req retrieval.RetrievalRequest) (*retrieval.RetrievalResponse, error) {
	if s.Primary == nil {
		return nil, fmt.Errorf("keyword retrieval backend is not configured")
	}
	if s.Vector == nil {
		return nil, fmt.Errorf("vector retrieval backend is not configured")
	}
	start := time.Now()
	topK := req.TopK
	if topK <= 0 {
		topK = 8
	}
	candidateK := req.CandidateK
	if candidateK <= 0 {
		candidateK = max(topK*4, 20)
	}
	candidateK = max(candidateK, topK)
	keywordReq := req
	keywordReq.Mode = retrieval.ModeKeyword
	keywordReq.TopK = candidateK
	keywordResp, err := s.Primary.Search(ctx, keywordReq)
	if err != nil {
		return nil, err
	}
	if keywordResp == nil {
		keywordResp = &retrieval.RetrievalResponse{}
	}
	vectorReq := req
	vectorReq.Mode = retrieval.ModeVector
	vectorReq.TopK = candidateK
	vectorResp, err := s.Vector.Search(ctx, vectorReq)
	if err != nil {
		keywordResp.Results = truncateResults(keywordResp.Results, topK)
		keywordResp.LatencyMS = int(time.Since(start).Milliseconds())
		keywordResp.Trace = append(keywordResp.Trace, retrieval.RetrievalTraceRecord{
			Stage:   "hybrid_vector_recall",
			Mode:    retrieval.ModeVector,
			Message: "vector_backend_failed",
			Metadata: map[string]any{
				"error":         err.Error(),
				"keyword_count": len(keywordResp.Results),
			},
		})
		return keywordResp, nil
	}
	if vectorResp == nil {
		vectorResp = &retrieval.RetrievalResponse{}
	}
	weight := req.HybridWeight
	if weight <= 0 || weight > 1 || math.IsNaN(weight) || math.IsInf(weight, 0) {
		weight = 0.5
	}
	results := fusion.WeightedRetrievalResults(keywordResp.Results, vectorResp.Results, weight, topK)
	trace := append([]retrieval.RetrievalTraceRecord{}, keywordResp.Trace...)
	trace = append(trace, vectorResp.Trace...)
	trace = append(trace, retrieval.RetrievalTraceRecord{
		Stage:   "hybrid_fusion",
		Mode:    retrieval.ModeHybrid,
		Message: "keyword_vector_fused",
		Metadata: map[string]any{
			"keyword_count": len(keywordResp.Results),
			"vector_count":  len(vectorResp.Results),
			"result_count":  len(results),
			"vector_weight": weight,
		},
	})
	return &retrieval.RetrievalResponse{
		Results:   results,
		LatencyMS: int(time.Since(start).Milliseconds()),
		Trace:     trace,
	}, nil
}

func truncateResults(results []retrieval.RetrievalResult, topK int) []retrieval.RetrievalResult {
	if topK > 0 && len(results) > topK {
		return results[:topK]
	}
	return results
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
