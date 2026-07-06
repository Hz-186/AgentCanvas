package composite

import (
	"context"
	"sort"
	"time"

	"agentcanvas/internal/domain/retrieval"
)

type Store struct {
	Primary retrieval.Indexer
	Keyword retrieval.Retriever
	Vector  interface {
		retrieval.Indexer
		retrieval.Retriever
	}
}

func New(primary interface {
	retrieval.Indexer
	retrieval.Retriever
}, vector interface {
	retrieval.Indexer
	retrieval.Retriever
}) *Store {
	return &Store{Primary: primary, Keyword: primary, Vector: vector}
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
	if s.Keyword == nil {
		return nil, nil
	}
	if s.Vector == nil || req.Mode == retrieval.ModeKeyword {
		return s.Keyword.Search(ctx, req)
	}
	if req.Mode == retrieval.ModeVector {
		return s.Vector.Search(ctx, req)
	}
	if req.Mode != retrieval.ModeHybrid {
		return s.Keyword.Search(ctx, req)
	}
	keywordReq := req
	keywordReq.Mode = retrieval.ModeKeyword
	keywordResp, err := s.Keyword.Search(ctx, keywordReq)
	if err != nil {
		return nil, err
	}
	vectorReq := req
	vectorReq.Mode = retrieval.ModeVector
	vectorResp, err := s.Vector.Search(ctx, vectorReq)
	if err != nil {
		return keywordResp, nil
	}
	weight := req.HybridWeight
	if weight <= 0 || weight > 1 {
		weight = 0.5
	}
	results := fuse(keywordResp.Results, vectorResp.Results, weight, req.TopK)
	return &retrieval.RetrievalResponse{Results: results, LatencyMS: int(time.Since(start).Milliseconds())}, nil
}

func fuse(keywordResults, vectorResults []retrieval.RetrievalResult, vectorWeight float64, topK int) []retrieval.RetrievalResult {
	merged := make(map[int64]retrieval.RetrievalResult, len(keywordResults)+len(vectorResults))
	maxKeyword := maxScore(keywordResults)
	maxVector := maxScore(vectorResults)
	for _, item := range keywordResults {
		item.KeywordScore = effectiveScore(item)
		item.FinalScore = normalize(item.KeywordScore, maxKeyword) * (1 - vectorWeight)
		item.Score = item.FinalScore
		merged[item.ChunkID] = item
	}
	for _, item := range vectorResults {
		existing, ok := merged[item.ChunkID]
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
		merged[existing.ChunkID] = existing
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
