package contextcompress

import "container/heap"

type candidate struct {
	index   int
	ratio   float64
	gain    float64
	version int
}

type candidateHeap []candidate

func (h candidateHeap) Len() int { return len(h) }
func (h candidateHeap) Less(i, j int) bool {
	if h[i].ratio != h[j].ratio {
		return h[i].ratio > h[j].ratio
	}
	if h[i].gain != h[j].gain {
		return h[i].gain > h[j].gain
	}
	return h[i].index < h[j].index
}
func (h candidateHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *candidateHeap) Push(x any)   { *h = append(*h, x.(candidate)) }
func (h *candidateHeap) Pop() any {
	old := *h
	item := old[len(old)-1]
	*h = old[:len(old)-1]
	return item
}

func Select(items []Item, opts Options) Selection {
	opts = normalizeOptions(opts)
	scores := scoreItems(items, opts)
	if len(scores) == 0 {
		return Selection{}
	}
	budget := opts.Budget
	if budget <= 0 {
		for _, item := range items {
			budget += itemCost(item)
		}
	}
	sim := buildSimilarityMatrix(items)
	selected := make([]bool, len(items))
	coverage := make([]float64, len(items))
	used := 0
	version := 0
	h := make(candidateHeap, 0, len(items))
	for i := range items {
		gain := marginalGain(i, scores, selected, coverage, sim, opts.DiversityLambda)
		heap.Push(&h, candidate{index: i, gain: gain, ratio: gain / float64(itemCost(items[i])), version: version})
	}
	heap.Init(&h)
	for h.Len() > 0 {
		top := heap.Pop(&h).(candidate)
		if selected[top.index] {
			continue
		}
		cost := itemCost(items[top.index])
		if used+cost > budget {
			continue
		}
		gain := marginalGain(top.index, scores, selected, coverage, sim, opts.DiversityLambda)
		ratio := gain / float64(cost)
		if top.version != version || ratio+1e-12 < top.ratio {
			heap.Push(&h, candidate{index: top.index, gain: gain, ratio: ratio, version: version})
			continue
		}
		if gain <= 0 {
			break
		}
		selected[top.index] = true
		used += cost
		version++
		for j := range coverage {
			if sim[top.index][j] > coverage[j] {
				coverage[j] = sim[top.index][j]
			}
		}
	}
	result := Selection{Scores: scores}
	for i, score := range scores {
		if selected[i] {
			result.Selected = append(result.Selected, score)
		} else {
			result.Omitted = append(result.Omitted, score)
		}
	}
	return result
}

func marginalGain(index int, scores []ScoredItem, selected []bool, coverage []float64, sim [][]float64, lambda float64) float64 {
	if selected[index] {
		return 0
	}
	gain := scores[index].Weight
	if lambda <= 0 {
		return gain
	}
	for j := range scores {
		if sim[index][j] > coverage[j] {
			gain += lambda * scores[j].Weight * (sim[index][j] - coverage[j])
		}
	}
	return gain
}

func buildSimilarityMatrix(items []Item) [][]float64 {
	sim := make([][]float64, len(items))
	for i := range sim {
		sim[i] = make([]float64, len(items))
		sim[i][i] = 1
	}
	for i := 0; i < len(items); i++ {
		for j := i + 1; j < len(items); j++ {
			value := longestCommonSubstringSimilarity(items[i].Content, items[j].Content)
			sim[i][j] = value
			sim[j][i] = value
		}
	}
	return sim
}
