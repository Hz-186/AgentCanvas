package contextcompress

import "unicode/utf8"

type Item struct {
	ID         int
	Content    string
	Tokens     int
	Turn       int
	Role       string
	Pinned     bool
	Importance float64
}

type Options struct {
	Budget              int
	Alpha               float64
	DiversityLambda     float64
	MinReferenceLength  int
	MaxNeighborScan     int
	SummaryBudget       int
	KeepRecent          int
	MinShingleRunes     int
	SimHashBits         int
	MinHashSize         int
	SimilarityThreshold float64
}

type ScoredItem struct {
	Item      Item
	Novelty   float64
	TimeDecay float64
	Weight    float64
}

type Selection struct {
	Selected []ScoredItem
	Omitted  []ScoredItem
	Scores   []ScoredItem
}

type Fragment struct {
	ItemID  int
	Index   int
	Content string
	Tokens  int
	Turn    int
	Score   float64
}

type Compression struct {
	Kept      []ScoredItem
	Omitted   []ScoredItem
	Scores    []ScoredItem
	Fragments []Fragment
	Summary   string
}

func DefaultOptions() Options {
	return Options{
		Alpha:               0.08,
		DiversityLambda:     0.35,
		MinReferenceLength:  4,
		MaxNeighborScan:     96,
		MinShingleRunes:     3,
		SimHashBits:         64,
		MinHashSize:         64,
		SimilarityThreshold: 0.72,
	}
}

func normalizeOptions(opts Options) Options {
	if opts.Alpha < 0 {
		opts.Alpha = 0
	}
	if opts.DiversityLambda < 0 {
		opts.DiversityLambda = 0
	}
	if opts.MinReferenceLength <= 0 {
		opts.MinReferenceLength = DefaultOptions().MinReferenceLength
	}
	if opts.MaxNeighborScan < 0 {
		opts.MaxNeighborScan = 0
	}
	if opts.MinShingleRunes <= 0 {
		opts.MinShingleRunes = DefaultOptions().MinShingleRunes
	}
	if opts.SimHashBits <= 0 || opts.SimHashBits > 64 {
		opts.SimHashBits = DefaultOptions().SimHashBits
	}
	if opts.MinHashSize <= 0 {
		opts.MinHashSize = DefaultOptions().MinHashSize
	}
	if opts.SimilarityThreshold <= 0 || opts.SimilarityThreshold > 1 {
		opts.SimilarityThreshold = DefaultOptions().SimilarityThreshold
	}
	return opts
}

func itemCost(item Item) int {
	if item.Tokens > 0 {
		return item.Tokens
	}
	if len(item.Content) == 0 {
		return 1
	}
	return estimateTokens(item.Content)
}

func estimateTokens(content string) int {
	if content == "" {
		return 0
	}
	ascii := 0
	nonASCII := 0
	for _, r := range content {
		if r < utf8.RuneSelf {
			ascii++
		} else {
			nonASCII++
		}
	}
	estimate := nonASCII + ascii/4
	if estimate <= 0 {
		return 1
	}
	return estimate
}

func itemImportance(item Item) float64 {
	if item.Importance > 0 {
		return item.Importance
	}
	return 1
}
