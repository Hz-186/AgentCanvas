package contextcompress

import (
	"hash/fnv"
	"math/bits"
	"sort"
	"strings"
)

type textFeatures struct {
	tokens    []string
	counts    map[string]float64
	simhash   uint64
	minhash   []uint64
	norm      float64
	salience  float64
	keySignal float64
}

func buildFeatures(text string, idf map[string]float64, minHashSize int) textFeatures {
	tokens := tokenizeText(text)
	counts := tokenCounts(tokens)
	f := textFeatures{
		tokens:    tokens,
		counts:    counts,
		simhash:   simHash(counts, idf),
		minhash:   minHash(tokens, minHashSize),
		keySignal: keySignal(text),
	}
	for token, count := range counts {
		weight := count * idf[token]
		f.salience += weight
		f.norm += weight * weight
	}
	return f
}

func simHash(counts map[string]float64, idf map[string]float64) uint64 {
	var weights [64]float64
	for token, count := range counts {
		h := hashString64(token)
		weight := count * idf[token]
		if weight == 0 {
			weight = count
		}
		for bit := 0; bit < 64; bit++ {
			if h&(uint64(1)<<bit) != 0 {
				weights[bit] += weight
			} else {
				weights[bit] -= weight
			}
		}
	}
	var result uint64
	for bit, weight := range weights {
		if weight > 0 {
			result |= uint64(1) << bit
		}
	}
	return result
}

func minHash(tokens []string, size int) []uint64 {
	if size <= 0 {
		return nil
	}
	seen := make(map[uint64]bool, len(tokens))
	values := make([]uint64, 0, len(tokens))
	for _, token := range tokens {
		base := hashString64(token)
		for i := 0; i < 2; i++ {
			value := mixHash(base + uint64(i)*0x9e3779b97f4a7c15)
			if !seen[value] {
				seen[value] = true
				values = append(values, value)
			}
		}
	}
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	if len(values) > size {
		values = values[:size]
	}
	return values
}

func approximateSimilarity(a, b textFeatures, idf map[string]float64) float64 {
	if len(a.tokens) == 0 || len(b.tokens) == 0 {
		return 0
	}
	cos := cosineSimilarity(a, b, idf)
	jac := minHashSimilarity(a.minhash, b.minhash)
	ham := bits.OnesCount64(a.simhash ^ b.simhash)
	sim := 1 - float64(ham)/64.0
	return 0.55*cos + 0.30*jac + 0.15*sim
}

func cosineSimilarity(a, b textFeatures, idf map[string]float64) float64 {
	if a.norm == 0 || b.norm == 0 {
		return 0
	}
	if len(a.counts) > len(b.counts) {
		a, b = b, a
	}
	dot := 0.0
	for token, ac := range a.counts {
		bc := b.counts[token]
		if bc == 0 {
			continue
		}
		weight := idf[token]
		dot += ac * weight * bc * weight
	}
	return dot / sqrt(a.norm*b.norm)
}

func minHashSimilarity(a, b []uint64) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	i, j, same := 0, 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] == b[j]:
			same++
			i++
			j++
		case a[i] < b[j]:
			i++
		default:
			j++
		}
	}
	return float64(same) / float64(max(len(a), len(b)))
}

func keySignal(text string) float64 {
	lower := strings.ToLower(text)
	signal := 0.0
	for _, marker := range []string{"must", "never", "always", "required", "error", "failed", "failure", "todo", "fix", "decision", "constraint", "必须", "不要", "始终", "永远", "错误", "失败", "修复", "决定", "结论", "约束", "重要"} {
		if strings.Contains(lower, marker) {
			signal += 0.18
		}
	}
	if signal > 0.9 {
		return 0.9
	}
	return signal
}

func hashString64(value string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(value))
	return h.Sum64()
}

func mixHash(x uint64) uint64 {
	x ^= x >> 30
	x *= 0xbf58476d1ce4e5b9
	x ^= x >> 27
	x *= 0x94d049bb133111eb
	x ^= x >> 31
	return x
}

func sqrt(v float64) float64 {
	if v <= 0 {
		return 0
	}
	x := v
	for i := 0; i < 8; i++ {
		x = 0.5 * (x + v/x)
	}
	return x
}
