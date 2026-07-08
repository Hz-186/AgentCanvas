package contextcompress

import (
	"sort"
	"strings"
)

func Compress(items []Item, opts Options) Compression {
	opts = normalizeOptions(opts)
	selection := Select(items, opts)
	result := Compression{Kept: selection.Selected, Omitted: selection.Omitted, Scores: selection.Scores}
	if len(selection.Omitted) == 0 {
		return result
	}
	budget := opts.SummaryBudget
	if budget <= 0 {
		budget = opts.Budget / 2
	}
	if budget <= 0 {
		for _, item := range selection.Omitted {
			budget += itemCost(item.Item)
		}
		budget /= 3
	}
	if budget <= 0 {
		budget = 1
	}
	fragments := rankFragments(selection.Omitted, opts)
	chosen := selectFragments(fragments, budget, opts)
	result.Fragments = chosen
	result.Summary = renderSummary(chosen)
	return result
}

type rankedFragment struct {
	fragment Fragment
	feature  textFeatures
	score    float64
}

func rankFragments(items []ScoredItem, opts Options) []rankedFragment {
	fragmentItems := make([]Item, 0, len(items)*2)
	itemWeight := make(map[int]float64, len(items))
	for _, scored := range items {
		itemWeight[scored.Item.ID] = scored.Weight + itemImportance(scored.Item)
		for _, fragment := range splitFragments(scored.Item) {
			fragmentItems = append(fragmentItems, Item{ID: len(fragmentItems), Content: fragment.Content, Tokens: fragment.Tokens, Turn: fragment.Turn})
		}
	}
	if len(fragmentItems) == 0 {
		return nil
	}
	profile := buildCorpusProfile(fragmentItems, opts)
	ranked := make([]rankedFragment, 0, len(fragmentItems))
	pos := 0
	for _, scored := range items {
		for _, fragment := range splitFragments(scored.Item) {
			feature := profile.items[pos].feature
			lengthPenalty := 1.0
			if fragment.Tokens <= 2 {
				lengthPenalty = 0.45
			}
			score := (feature.salience + 1 + feature.keySignal*4) * itemWeight[fragment.ItemID] * lengthPenalty
			fragment.Score = score
			ranked = append(ranked, rankedFragment{fragment: fragment, feature: feature, score: score})
			pos++
		}
	}
	return ranked
}

func selectFragments(fragments []rankedFragment, budget int, opts Options) []Fragment {
	selected := make([]bool, len(fragments))
	chosen := make([]Fragment, 0)
	used := 0
	idf := buildIDFForRanked(fragments)
	for {
		best := -1
		bestGain := 0.0
		fallback := -1
		fallbackGain := 0.0
		for i, fragment := range fragments {
			if selected[i] || fragment.fragment.Tokens <= 0 {
				continue
			}
			redundancy := 0.0
			if opts.DiversityLambda > 0 {
				for j, ok := range selected {
					if !ok {
						continue
					}
					sim := approximateSimilarity(fragment.feature, fragments[j].feature, idf)
					if sim > redundancy {
						redundancy = sim
					}
				}
			}
			gain := fragment.score * (1 - opts.DiversityLambda*redundancy)
			ratio := gain / float64(fragment.fragment.Tokens)
			if fallback < 0 || ratio > fallbackGain {
				fallback = i
				fallbackGain = ratio
			}
			if used+fragment.fragment.Tokens > budget {
				continue
			}
			if best < 0 || ratio > bestGain {
				best = i
				bestGain = ratio
			}
		}
		if best < 0 {
			if len(chosen) == 0 && fallback >= 0 && budget-used > 0 {
				selected[fallback] = true
				chosen = append(chosen, trimFragmentToBudget(fragments[fallback].fragment, budget-used))
			}
			break
		}
		selected[best] = true
		chosen = append(chosen, fragments[best].fragment)
		used += fragments[best].fragment.Tokens
	}
	sort.SliceStable(chosen, func(i, j int) bool {
		if chosen[i].Turn != chosen[j].Turn {
			return chosen[i].Turn < chosen[j].Turn
		}
		if chosen[i].ItemID != chosen[j].ItemID {
			return chosen[i].ItemID < chosen[j].ItemID
		}
		return chosen[i].Index < chosen[j].Index
	})
	return chosen
}

func trimFragmentToBudget(fragment Fragment, budget int) Fragment {
	if budget <= 0 || fragment.Tokens <= budget {
		return fragment
	}
	limit := budget * 4
	if limit < 8 {
		limit = 8
	}
	content := []rune(fragment.Content)
	if len(content) > limit {
		fragment.Content = strings.TrimSpace(string(content[:limit])) + "..."
		fragment.Tokens = estimateTokens(fragment.Content)
	}
	return fragment
}

func buildIDFForRanked(fragments []rankedFragment) map[string]float64 {
	idf := make(map[string]float64)
	for _, fragment := range fragments {
		for token := range fragment.feature.counts {
			idf[token] = 1
		}
	}
	return idf
}

func renderSummary(fragments []Fragment) string {
	if len(fragments) == 0 {
		return ""
	}
	parts := make([]string, 0, len(fragments)+1)
	parts = append(parts, "Earlier conversation summary:")
	for _, fragment := range fragments {
		line := strings.Join(strings.Fields(fragment.Content), " ")
		if line == "" {
			continue
		}
		parts = append(parts, "- "+line)
	}
	if len(parts) == 1 {
		return ""
	}
	return strings.Join(parts, "\n")
}
