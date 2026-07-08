package contextcompress

import (
	"math"
	"strings"
)

type itemProfile struct {
	feature textFeatures
	key     string
}

type corpusProfile struct {
	idf   map[string]float64
	items []itemProfile
}

func scoreItems(items []Item, opts Options) []ScoredItem {
	opts = normalizeOptions(opts)
	scores := make([]ScoredItem, len(items))
	if len(items) == 0 {
		return scores
	}
	profile := buildCorpusProfile(items, opts)
	lastTurn := 0
	for _, item := range items {
		if item.Turn > lastTurn {
			lastTurn = item.Turn
		}
	}
	seenTerms := make(map[string]float64)
	seenExact := make(map[string]bool)
	for i, item := range items {
		feature := profile.items[i].feature
		total := 0.0
		repeated := 0.0
		for token, count := range feature.counts {
			weight := count * profile.idf[token]
			total += weight
			if previous := seenTerms[token]; previous > 0 {
				repeated += math.Min(count, previous) * profile.idf[token]
			}
		}
		novelty := 1.0
		if total > 0 {
			novelty = 1 - repeated/total
			if novelty < 0 {
				novelty = 0
			}
		}
		if profile.items[i].key != "" && seenExact[profile.items[i].key] && novelty > 0.08 {
			novelty = 0.08
		}
		if item.Pinned && novelty < 0.45 {
			novelty = 0.45
		}
		age := lastTurn - item.Turn
		if age < 0 {
			age = 0
		}
		decay := expDecay(opts.Alpha, age)
		importance := itemImportance(item)
		weight := (novelty + feature.keySignal) * decay * importance
		if item.Pinned {
			weight += importance
		}
		scores[i] = ScoredItem{Item: item, Novelty: novelty, TimeDecay: decay, Weight: weight}
		for token, count := range feature.counts {
			if count > seenTerms[token] {
				seenTerms[token] = count
			}
		}
		if profile.items[i].key != "" {
			seenExact[profile.items[i].key] = true
		}
	}
	return scores
}

func buildCorpusProfile(items []Item, opts Options) corpusProfile {
	df := make(map[string]int)
	docTokens := make([][]string, len(items))
	for i, item := range items {
		tokens := tokenizeText(item.Content)
		docTokens[i] = tokens
		seen := make(map[string]bool, len(tokens))
		for _, token := range tokens {
			if !seen[token] {
				df[token]++
				seen[token] = true
			}
		}
	}
	idf := make(map[string]float64, len(df))
	n := float64(len(items))
	for token, count := range df {
		idf[token] = math.Log((n+1)/(float64(count)+0.5)) + 1
	}
	profiles := make([]itemProfile, len(items))
	for i, item := range items {
		feature := buildFeatures(item.Content, idf, opts.MinHashSize)
		if len(feature.tokens) == 0 && len(docTokens[i]) > 0 {
			feature.tokens = docTokens[i]
		}
		profiles[i] = itemProfile{feature: feature, key: normalizedExactKey(item.Content)}
	}
	return corpusProfile{idf: idf, items: profiles}
}

func normalizedExactKey(content string) string {
	return strings.ToLower(strings.Join(strings.Fields(content), " "))
}

func expDecay(alpha float64, age int) float64 {
	if age <= 0 || alpha <= 0 {
		return 1
	}
	return math.Exp(-alpha * float64(age))
}
