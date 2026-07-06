package retrieval_usecase

import (
	"strings"
	"unicode"

	"agentcanvas/internal/domain/retrieval"
)

const lowRecallMaxScoreThreshold = 0.000001

func analyzeRecall(req retrieval.RetrievalRequest, results []retrieval.RetrievalResult) *retrieval.RecallDiagnostics {
	diag := &retrieval.RecallDiagnostics{ResultCount: len(results), RequestedTopK: req.TopK, CandidateK: req.CandidateK}
	if len(results) == 0 {
		diag.LowRecall = true
		diag.Reason = "empty_results"
		return diag
	}
	sum := 0.0
	for i, item := range results {
		if item.KeywordScore > 0 {
			diag.KeywordCount++
		}
		if item.VectorScore > 0 {
			diag.VectorCount++
		}
		score := item.FinalScore
		if score == 0 {
			score = item.Score
		}
		if i == 0 || score > diag.MaxScore {
			diag.MaxScore = score
		}
		sum += score
	}
	diag.AverageScore = sum / float64(len(results))
	if len(results) > 1 {
		first := effectiveScore(results[0])
		second := effectiveScore(results[1])
		diag.ScoreGap = first - second
	}
	if len(results) < req.TopK {
		diag.LowRecall = true
		diag.Reason = "result_count_below_top_k"
		return diag
	}
	if req.Mode == retrieval.ModeHybrid && (diag.KeywordCount == 0 || diag.VectorCount == 0) {
		diag.LowRecall = true
		diag.Reason = "hybrid_coverage_incomplete"
		return diag
	}
	if diag.MaxScore <= lowRecallMaxScoreThreshold {
		diag.LowRecall = true
		diag.Reason = "max_score_too_low"
	}
	return diag
}

func effectiveScore(item retrieval.RetrievalResult) float64 {
	if item.FinalScore != 0 {
		return item.FinalScore
	}
	return item.Score
}

func rewriteQueryVariants(query string) []string {
	base := strings.TrimSpace(query)
	if base == "" {
		return nil
	}
	seen := map[string]bool{base: true}
	variants := make([]string, 0, 3)
	add := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			return
		}
		seen[value] = true
		variants = append(variants, value)
	}
	add(strings.TrimFunc(base, func(r rune) bool { return unicode.IsPunct(r) || unicode.IsSymbol(r) }))
	add(strings.Join(strings.Fields(base), " "))
	lower := strings.ToLower(base)
	for _, prefix := range []string{"如何", "怎么", "请问", "what is", "how to"} {
		if strings.HasPrefix(lower, prefix) {
			add(strings.TrimSpace(base[len(prefix):]))
		}
	}
	return variants
}

func expandedRecallRequest(req retrieval.RetrievalRequest) retrieval.RetrievalRequest {
	expanded := req
	expanded.CandidateK = max(max(req.CandidateK*2, req.TopK*8), defaultCandidateK*2)
	if expanded.CandidateK > 100 {
		expanded.CandidateK = 100
	}
	if expanded.TopK < expanded.CandidateK {
		expanded.TopK = expanded.CandidateK
	}
	return expanded
}

func truncateResults(results []retrieval.RetrievalResult, topK int) []retrieval.RetrievalResult {
	if topK > 0 && len(results) > topK {
		return results[:topK]
	}
	return results
}
