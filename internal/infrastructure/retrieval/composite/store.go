package composite

import (
	"context"
	"fmt"
	"sort"
	"time"

	"agentcanvas/internal/domain/retrieval"
)

// Store coordinates primary and vector retrieval backends.
type Store struct {
	// Primary handles keyword indexing and retrieval.
	Primary retrieval.Backend
	// Vector handles vector indexing and retrieval.
	Vector retrieval.Backend
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
	if s.Vector != nil {
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
	if s.Vector != nil {
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
	if s.Vector != nil {
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
	if s.Vector != nil {
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
	if s.Vector != nil {
		return s.Vector.DeleteByKnowledgeBase(ctx, ownerID, kbID)
	}
	return nil
}

func (s *Store) Search(ctx context.Context, req retrieval.RetrievalRequest) (*retrieval.RetrievalResponse, error) {
	start := time.Now()
	if s.Primary == nil {
		return nil, nil
	}
	if s.Vector == nil || req.Mode == retrieval.ModeKeyword {
		return s.Primary.Search(ctx, req)
	}
	if req.Mode == retrieval.ModeVector {
		return s.Vector.Search(ctx, req)
	}
	if req.Mode != retrieval.ModeHybrid {
		return s.Primary.Search(ctx, req)
	}
	keywordReq := req
	keywordReq.Mode = retrieval.ModeKeyword
	keywordResp, err := s.Primary.Search(ctx, keywordReq)
	if err != nil {
		return nil, err
	}
	if keywordResp == nil {
		keywordResp = &retrieval.RetrievalResponse{}
	}
	vectorReq := req
	vectorReq.Mode = retrieval.ModeVector
	vectorResp, err := s.Vector.Search(ctx, vectorReq)
	if err != nil {
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
	if weight <= 0 || weight > 1 {
		weight = 0.5
	}
	results := fuse(keywordResp.Results, vectorResp.Results, weight, req.TopK)
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

func fuse(keywordResults, vectorResults []retrieval.RetrievalResult, vectorWeight float64, topK int) []retrieval.RetrievalResult {
	merged := make(map[string]retrieval.RetrievalResult, len(keywordResults)+len(vectorResults))
	maxKeyword := maxScore(keywordResults)
	maxVector := maxScore(vectorResults)
	for _, item := range keywordResults {
		item.KeywordScore = effectiveScore(item)
		item.FinalScore = normalize(item.KeywordScore, maxKeyword) * (1 - vectorWeight)
		item.Score = item.FinalScore
		merged[resultKey(item)] = item
	}
	for _, item := range vectorResults {
		key := resultKey(item)
		existing, ok := merged[key]
		if !ok {
			existing = item
		}
		existing.VectorScore = effectiveScore(item)
		existing.FinalScore += normalize(existing.VectorScore, maxVector) * vectorWeight
		existing.Score = existing.FinalScore
		if existing.Content == "" {
			existing.Content = item.Content
		}
		if existing.DocumentName == "" {
			existing.DocumentName = item.DocumentName
		}
		if existing.PageNo == nil {
			existing.PageNo = item.PageNo
		}
		if existing.Metadata == nil {
			existing.Metadata = item.Metadata
		}
		merged[key] = existing
	}
	results := make([]retrieval.RetrievalResult, 0, len(merged))
	for _, item := range merged {
		results = append(results, item)
	}
	sort.SliceStable(results, func(i, j int) bool { return results[i].FinalScore > results[j].FinalScore })
	if topK > 0 && len(results) > topK {
		results = results[:topK]
	}
	return results
}

func resultKey(item retrieval.RetrievalResult) string {
	if item.ChunkID != 0 {
		return fmt.Sprintf("chunk:%d", item.ChunkID)
	}
	return fmt.Sprintf("doc:%d:kb:%d:page:%v:content:%s", item.DocumentID, item.KBID, item.PageNo, item.Content)
}

func maxScore(items []retrieval.RetrievalResult) float64 {
	max := 0.0
	for _, item := range items {
		if score := effectiveScore(item); score > max {
			max = score
		}
	}
	return max
}

func effectiveScore(item retrieval.RetrievalResult) float64 {
	if item.FinalScore != 0 {
		return item.FinalScore
	}
	return item.Score
}

func normalize(score, max float64) float64 {
	if max <= 0 {
		return score
	}
	return score / max
}
