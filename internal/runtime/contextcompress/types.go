package contextcompress

import "math"

type Item struct {
	ID      int
	Content string
	Tokens  int
	Turn    int
}

type Options struct {
	Budget             int
	Alpha              float64
	DiversityLambda    float64
	MinReferenceLength int
	MaxNeighborScan    int
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

func normalizeOptions(opts Options) Options {
	if opts.Alpha <= 0 {
		opts.Alpha = 0.08
	}
	if opts.DiversityLambda < 0 {
		opts.DiversityLambda = 0
	}
	if opts.DiversityLambda == 0 {
		opts.DiversityLambda = 0.35
	}
	if opts.MinReferenceLength <= 0 {
		opts.MinReferenceLength = 4
	}
	if opts.MaxNeighborScan < 0 {
		opts.MaxNeighborScan = 0
	}
	if opts.MaxNeighborScan == 0 {
		opts.MaxNeighborScan = 96
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
	return int(math.Ceil(float64(len(item.Content)) / 4.0))
}
