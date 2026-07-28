package fusion

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"agentcanvas/internal/domain/retrieval"
)

const DefaultRankConstant = 60

type RankedList[T any] struct {
	Items  []T
	Weight float64
}

type RankedResult[T any] struct {
	Item  T
	Score float64
}

// NormalizeScore converts a backend-specific score to the range [0, 1].
func NormalizeScore(score, maxScore float64) float64 {
	if maxScore <= 0 || math.IsNaN(maxScore) || math.IsInf(maxScore, 0) || math.IsNaN(score) || math.IsInf(score, 0) {
		return 0
	}
	return score / maxScore
}

// WeightedRetrievalResults fuses keyword and vector results with max-score normalization.
func WeightedRetrievalResults(keywordResults, vectorResults []retrieval.RetrievalResult, vectorWeight float64, topK int) []retrieval.RetrievalResult {
	if vectorWeight <= 0 || vectorWeight > 1 || math.IsNaN(vectorWeight) || math.IsInf(vectorWeight, 0) {
		vectorWeight = 0.5
	}
	type fusedItem struct {
		result retrieval.RetrievalResult
		order  int
	}
	items := make([]fusedItem, 0, len(keywordResults)+len(vectorResults))
	positions := make(map[string]int, len(keywordResults)+len(vectorResults))
	maxKeyword := maxRetrievalScore(keywordResults)
	maxVector := maxRetrievalScore(vectorResults)
	seenKeyword := make(map[string]struct{}, len(keywordResults))

	for _, candidate := range keywordResults {
		key := RetrievalResultKey(candidate)
		if _, duplicate := seenKeyword[key]; duplicate {
			continue
		}
		seenKeyword[key] = struct{}{}
		score := effectiveRetrievalScore(candidate)
		candidate.KeywordScore = score
		candidate.VectorScore = 0
		candidate.FinalScore = NormalizeScore(score, maxKeyword) * (1 - vectorWeight)
		candidate.Score = candidate.FinalScore
		positions[key] = len(items)
		items = append(items, fusedItem{result: candidate, order: len(items)})
	}

	seenVector := make(map[string]struct{}, len(vectorResults))
	for _, candidate := range vectorResults {
		score := effectiveRetrievalScore(candidate)
		key := RetrievalResultKey(candidate)
		if _, duplicate := seenVector[key]; duplicate {
			continue
		}
		seenVector[key] = struct{}{}
		if position, exists := positions[key]; exists {
			existing := items[position].result
			existing.VectorScore = score
			existing.FinalScore += NormalizeScore(score, maxVector) * vectorWeight
			existing.Score = existing.FinalScore
			items[position].result = mergeRetrievalFields(existing, candidate)
			continue
		}
		candidate.KeywordScore = 0
		candidate.VectorScore = score
		candidate.FinalScore = NormalizeScore(score, maxVector) * vectorWeight
		candidate.Score = candidate.FinalScore
		positions[key] = len(items)
		items = append(items, fusedItem{result: candidate, order: len(items)})
	}

	sort.SliceStable(items, func(i, j int) bool {
		if items[i].result.FinalScore == items[j].result.FinalScore {
			return items[i].order < items[j].order
		}
		return items[i].result.FinalScore > items[j].result.FinalScore
	})
	if topK > 0 && len(items) > topK {
		items = items[:topK]
	}
	results := make([]retrieval.RetrievalResult, 0, len(items))
	for _, item := range items {
		results = append(results, item.result)
	}
	return results
}

// ReciprocalRank fuses independent ranked lists. Rank positions start at one.
func ReciprocalRank[T any](lists []RankedList[T], key func(T) string, rankConstant, rankWindow, topK int) []RankedResult[T] {
	if rankConstant <= 0 {
		rankConstant = DefaultRankConstant
	}
	type fusedItem struct {
		item  T
		score float64
		order int
	}
	items := make([]fusedItem, 0)
	positions := make(map[string]int)
	for _, list := range lists {
		weight := list.Weight
		if weight <= 0 || math.IsNaN(weight) || math.IsInf(weight, 0) {
			weight = 1
		}
		limit := len(list.Items)
		if rankWindow > 0 && limit > rankWindow {
			limit = rankWindow
		}
		seen := make(map[string]struct{}, limit)
		for index := 0; index < limit; index++ {
			candidate := list.Items[index]
			identity := key(candidate)
			if _, duplicate := seen[identity]; duplicate {
				continue
			}
			seen[identity] = struct{}{}
			score := weight / float64(rankConstant+index+1)
			if position, exists := positions[identity]; exists {
				items[position].score += score
				continue
			}
			positions[identity] = len(items)
			items = append(items, fusedItem{item: candidate, score: score, order: len(items)})
		}
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].score == items[j].score {
			return items[i].order < items[j].order
		}
		return items[i].score > items[j].score
	})
	if topK > 0 && len(items) > topK {
		items = items[:topK]
	}
	results := make([]RankedResult[T], 0, len(items))
	for _, item := range items {
		results = append(results, RankedResult[T]{Item: item.item, Score: item.score})
	}
	return results
}

func RRFRetrievalResults(groups [][]retrieval.RetrievalResult, rankConstant, rankWindow, topK int) []retrieval.RetrievalResult {
	if rankWindow > 0 && topK > rankWindow {
		rankWindow = topK
	}
	lists := make([]RankedList[retrieval.RetrievalResult], 0, len(groups))
	for _, group := range groups {
		lists = append(lists, RankedList[retrieval.RetrievalResult]{Items: group, Weight: 1})
	}
	ranked := ReciprocalRank(lists, rrfRetrievalResultKey, rankConstant, rankWindow, topK)
	results := make([]retrieval.RetrievalResult, 0, len(ranked))
	for _, item := range ranked {
		item.Item.Score = item.Score
		item.Item.FinalScore = item.Score
		results = append(results, item.Item)
	}
	return results
}

func RetrievalResultKey(item retrieval.RetrievalResult) string {
	if item.ChunkID != 0 {
		return fmt.Sprintf("chunk:%d", item.ChunkID)
	}
	page := ""
	if item.PageNo != nil {
		page = fmt.Sprint(*item.PageNo)
	}
	return fmt.Sprintf("doc:%d:kb:%d:page:%s:content:%s", item.DocumentID, item.KBID, page, strings.TrimSpace(item.Content))
}

func rrfRetrievalResultKey(item retrieval.RetrievalResult) string {
	if item.ChunkID > 0 {
		return fmt.Sprintf("chunk:%d", item.ChunkID)
	}
	if item.DocumentID > 0 {
		return fmt.Sprintf("document:%d:page:%v", item.DocumentID, item.PageNo)
	}
	return "content:" + strings.TrimSpace(item.Content)
}

func maxRetrievalScore(items []retrieval.RetrievalResult) float64 {
	maxScore := 0.0
	for _, item := range items {
		if score := effectiveRetrievalScore(item); score > maxScore && !math.IsNaN(score) && !math.IsInf(score, 0) {
			maxScore = score
		}
	}
	return maxScore
}

func effectiveRetrievalScore(item retrieval.RetrievalResult) float64 {
	if item.FinalScore != 0 {
		return item.FinalScore
	}
	return item.Score
}

func mergeRetrievalFields(existing, candidate retrieval.RetrievalResult) retrieval.RetrievalResult {
	if existing.Content == "" {
		existing.Content = candidate.Content
	}
	if existing.Highlight == "" {
		existing.Highlight = candidate.Highlight
	}
	if existing.DocumentName == "" {
		existing.DocumentName = candidate.DocumentName
	}
	if existing.PageNo == nil {
		existing.PageNo = candidate.PageNo
	}
	if existing.Metadata == nil {
		existing.Metadata = candidate.Metadata
	}
	return existing
}
