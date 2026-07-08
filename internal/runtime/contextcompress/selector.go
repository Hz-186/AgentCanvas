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
	profile := buildCorpusProfile(items, opts)
	selected := make([]bool, len(items))
	used := 0
	version := 0
	h := make(candidateHeap, 0, len(items))
	for i := range items {
		gain := marginalGain(i, scores, selected, profile, opts.DiversityLambda)
		cost := itemCost(items[i])
		h = append(h, candidate{index: i, gain: gain, ratio: gain / float64(cost), version: version})
	}
	heap.Init(&h)
	for h.Len() > 0 {
		top := heap.Pop(&h).(candidate)
		if selected[top.index] {
			continue
		}
		cost := itemCost(items[top.index])
		if used+cost > budget && !items[top.index].Pinned {
			continue
		}
		gain := marginalGain(top.index, scores, selected, profile, opts.DiversityLambda)
		ratio := gain / float64(cost)
		if top.version != version || ratio+relativeEpsilon(ratio) < top.ratio {
			heap.Push(&h, candidate{index: top.index, gain: gain, ratio: ratio, version: version})
			continue
		}
		if gain <= 0 && !items[top.index].Pinned {
			break
		}
		selected[top.index] = true
		used += cost
		version++
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

func marginalGain(index int, scores []ScoredItem, selected []bool, profile corpusProfile, lambda float64) float64 {
	if selected[index] {
		return 0
	}
	gain := scores[index].Weight
	if scores[index].Item.Pinned {
		return gain + 1
	}
	if lambda <= 0 {
		return gain
	}
	redundancy := 0.0
	for i, ok := range selected {
		if !ok {
			continue
		}
		sim := approximateSimilarity(profile.items[index].feature, profile.items[i].feature, profile.idf)
		if sim > redundancy {
			redundancy = sim
		}
	}
	return gain * (1 - lambda*redundancy)
}

func relativeEpsilon(value float64) float64 {
	if value < 0 {
		value = -value
	}
	if value < 1 {
		value = 1
	}
	return value * 1e-9
}

func buildSimilarityMatrix(items []Item) [][]float64 {
	opts := DefaultOptions()
	profile := buildCorpusProfile(items, opts)
	sim := make([][]float64, len(items))
	for i := range sim {
		sim[i] = make([]float64, len(items))
		sim[i][i] = 1
	}
	for i := 0; i < len(items); i++ {
		for j := i + 1; j < len(items); j++ {
			value := approximateSimilarity(profile.items[i].feature, profile.items[j].feature, profile.idf)
			sim[i][j] = value
			sim[j][i] = value
		}
	}
	return sim
}
