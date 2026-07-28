package fusion

import (
	"math"
	"testing"

	"agentcanvas/internal/domain/retrieval"
)

func TestNormalizeScore(t *testing.T) {
	if got := NormalizeScore(4, 8); got != 0.5 {
		t.Fatalf("NormalizeScore() = %v, want 0.5", got)
	}
	for _, maxScore := range []float64{0, -1, math.NaN(), math.Inf(1)} {
		if got := NormalizeScore(4, maxScore); got != 0 {
			t.Fatalf("NormalizeScore(4, %v) = %v, want 0", maxScore, got)
		}
	}
}

func TestWeightedRetrievalResultsMergesAndStablySorts(t *testing.T) {
	keyword := []retrieval.RetrievalResult{
		{ChunkID: 1, Score: 10, Highlight: "keyword highlight"},
		{ChunkID: 2, Score: 5, Content: "keyword only"},
	}
	vector := []retrieval.RetrievalResult{
		{ChunkID: 1, Score: 20, Content: "filled from vector"},
		{ChunkID: 3, Score: 10, Content: "vector only"},
	}

	results := WeightedRetrievalResults(keyword, vector, 0.5, 3)
	if len(results) != 3 || results[0].ChunkID != 1 || results[1].ChunkID != 2 || results[2].ChunkID != 3 {
		t.Fatalf("unexpected stable fusion order: %+v", results)
	}
	if results[0].KeywordScore != 10 || results[0].VectorScore != 20 || results[0].FinalScore != 1 {
		t.Fatalf("unexpected merged scores: %+v", results[0])
	}
	if results[0].Content != "filled from vector" || results[0].Highlight != "keyword highlight" {
		t.Fatalf("unexpected merged fields: %+v", results[0])
	}
}

func TestWeightedRetrievalResultsDefaultsInvalidWeightAndTruncates(t *testing.T) {
	keyword := []retrieval.RetrievalResult{{ChunkID: 1, Score: 2}, {ChunkID: 1, Score: 1}}
	vector := []retrieval.RetrievalResult{{ChunkID: 2, Score: 4}, {ChunkID: 2, Score: 3}}
	results := WeightedRetrievalResults(keyword, vector, -1, 1)
	if len(results) != 1 || results[0].ChunkID != 1 {
		t.Fatalf("unexpected default-weight result: %+v", results)
	}
}

func TestReciprocalRankSupportsWeightsDeduplicationAndWindow(t *testing.T) {
	lists := []RankedList[string]{
		{Items: []string{"a", "a", "b"}, Weight: 2},
		{Items: []string{"b", "c"}, Weight: 1},
	}
	results := ReciprocalRank(lists, func(item string) string { return item }, 0, 2, 2)
	if len(results) != 2 || results[0].Item != "a" || results[1].Item != "b" {
		t.Fatalf("unexpected RRF results: %+v", results)
	}
	wantA := 2.0 / float64(DefaultRankConstant+1)
	if results[0].Score != wantA {
		t.Fatalf("RRF score = %v, want %v", results[0].Score, wantA)
	}
}

func TestRRFRetrievalResultsKeepsFirstSeenOnEqualScore(t *testing.T) {
	groups := [][]retrieval.RetrievalResult{
		{{ChunkID: 1}, {ChunkID: 2}},
		{{ChunkID: 3}, {ChunkID: 4}},
	}
	results := RRFRetrievalResults(groups, 60, 0, 4)
	if len(results) != 4 || results[0].ChunkID != 1 || results[1].ChunkID != 3 || results[2].ChunkID != 2 || results[3].ChunkID != 4 {
		t.Fatalf("unexpected stable RRF order: %+v", results)
	}
}

func TestRRFRetrievalResultsRaisesWindowToTopK(t *testing.T) {
	groups := [][]retrieval.RetrievalResult{{
		{ChunkID: 1}, {ChunkID: 2}, {ChunkID: 3},
	}}
	results := RRFRetrievalResults(groups, 60, 1, 3)
	if len(results) != 3 {
		t.Fatalf("RRFRetrievalResults() returned %d results, want 3", len(results))
	}
}
