package retrieval_usecase

import "math"

type RankingMetrics struct {
	RecallAtK float64 `json:"recall_at_k"`
	MRR       float64 `json:"mrr"`
	NDCG      float64 `json:"ndcg"`
}

func EvaluateRanking(relevant map[string]bool, ranked []string, k int) RankingMetrics {
	if k <= 0 || k > len(ranked) {
		k = len(ranked)
	}
	if len(relevant) == 0 || k == 0 {
		return RankingMetrics{}
	}
	hits, reciprocalRank, dcg := 0, 0.0, 0.0
	seen := map[string]bool{}
	for index := 0; index < k; index++ {
		id := ranked[index]
		if seen[id] || !relevant[id] {
			continue
		}
		seen[id] = true
		hits++
		if reciprocalRank == 0 {
			reciprocalRank = 1 / float64(index+1)
		}
		dcg += 1 / math.Log2(float64(index+2))
	}
	idealHits := min(k, len(relevant))
	idcg := 0.0
	for index := 0; index < idealHits; index++ {
		idcg += 1 / math.Log2(float64(index+2))
	}
	ndcg := 0.0
	if idcg > 0 {
		ndcg = dcg / idcg
	}
	return RankingMetrics{RecallAtK: float64(hits) / float64(len(relevant)), MRR: reciprocalRank, NDCG: ndcg}
}
