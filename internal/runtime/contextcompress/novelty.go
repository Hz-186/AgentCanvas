package contextcompress

import "math"

type messageSpan struct {
	itemIndex int
	start     int
	end       int
}

func scoreItems(items []Item, opts Options) []ScoredItem {
	opts = normalizeOptions(opts)
	scores := make([]ScoredItem, len(items))
	if len(items) == 0 {
		return scores
	}
	text, spans, posToSpan := buildCorpus(items)
	idx := newSuffixIndex(text)
	literalCounts := make([]int, len(items))
	lengths := make([]int, len(items))
	for _, span := range spans {
		lengths[span.itemIndex] = span.end - span.start
		for pos := span.start; pos < span.end; {
			match := idx.longestPreviousPrefix(pos, span.end-pos, opts.MaxNeighborScan)
			if match >= opts.MinReferenceLength {
				pos += match
				continue
			}
			if posToSpan[pos] >= 0 {
				literalCounts[span.itemIndex]++
			}
			pos++
		}
	}
	lastTurn := 0
	for _, item := range items {
		if item.Turn > lastTurn {
			lastTurn = item.Turn
		}
	}
	for i, item := range items {
		length := lengths[i]
		novelty := 1.0
		if length > 0 {
			novelty = float64(literalCounts[i]) / float64(length)
		}
		age := lastTurn - item.Turn
		if age < 0 {
			age = 0
		}
		decay := expDecay(opts.Alpha, age)
		weight := novelty * decay
		scores[i] = ScoredItem{Item: item, Novelty: novelty, TimeDecay: decay, Weight: weight}
	}
	return scores
}

func buildCorpus(items []Item) (string, []messageSpan, []int) {
	total := 0
	for _, item := range items {
		total += len(item.Content) + 1
	}
	data := make([]byte, 0, total)
	spans := make([]messageSpan, 0, len(items))
	posToSpan := make([]int, 0, total)
	for i, item := range items {
		start := len(data)
		data = append(data, item.Content...)
		end := len(data)
		spans = append(spans, messageSpan{itemIndex: i, start: start, end: end})
		for p := start; p < end; p++ {
			posToSpan = append(posToSpan, i)
		}
		data = append(data, 0)
		posToSpan = append(posToSpan, -1)
	}
	return string(data), spans, posToSpan
}

func expDecay(alpha float64, age int) float64 {
	if age <= 0 {
		return 1
	}
	return math.Exp(-alpha * float64(age))
}
