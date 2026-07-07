package deepdoc

import (
	"math"
	"sort"
)

type KMeans1D struct {
	K        int
	Centers  []float64
	Labels   []int
	MaxIter  int
}

func KMeans1DClustering(values []float64, k int) *KMeans1D {
	if k <= 1 || len(values) <= k {
		return nil
	}
	km := &KMeans1D{K: k, MaxIter: 100}
	km.fit(values)
	return km
}

func (km *KMeans1D) fit(values []float64) {
	n := len(values)
	km.Centers = make([]float64, km.K)
	km.Labels = make([]int, n)

	sorted := make([]float64, n)
	copy(sorted, values)
	sort.Float64s(sorted)

	stride := float64(n) / float64(km.K)
	for i := 0; i < km.K; i++ {
		idx := int(float64(i) * stride)
		if idx >= n {
			idx = n - 1
		}
		km.Centers[i] = sorted[idx]
	}

	for iter := 0; iter < km.MaxIter; iter++ {
		changed := false
		for i, v := range values {
			nearest := 0
			minDist := math.Abs(v - km.Centers[0])
			for j := 1; j < km.K; j++ {
				if d := math.Abs(v - km.Centers[j]); d < minDist {
					minDist = d
					nearest = j
				}
			}
			if km.Labels[i] != nearest {
				km.Labels[i] = nearest
				changed = true
			}
		}
		if !changed {
			break
		}
		newCenters := make([]float64, km.K)
		counts := make([]int, km.K)
		for i, v := range values {
			label := km.Labels[i]
			newCenters[label] += v
			counts[label]++
		}
		for j := 0; j < km.K; j++ {
			if counts[j] > 0 {
				km.Centers[j] = newCenters[j] / float64(counts[j])
			}
		}
	}
}

func SilhouetteScore(values []float64, labels []int, k int) float64 {
	n := len(values)
	if n <= 1 || k <= 1 {
		return 0
	}
	totalScore := 0.0
	count := 0
	for i := 0; i < n; i++ {
		label := labels[i]
		a := meanDistanceToCluster(values, labels, i, label)
		b := math.MaxFloat64
		for j := 0; j < k; j++ {
			if j == label {
				continue
			}
			if d := meanDistanceToCluster(values, labels, i, j); d < b {
				b = d
			}
		}
		if a < b {
			totalScore += 1 - a/b
		} else if a > b {
			totalScore += b/a - 1
		}
		count++
	}
	if count == 0 {
		return 0
	}
	return totalScore / float64(count)
}

func meanDistanceToCluster(values []float64, labels []int, idx, cluster int) float64 {
	sum := 0.0
	n := 0
	for i, v := range values {
		if i != idx && labels[i] == cluster {
			sum += math.Abs(values[idx] - v)
			n++
		}
	}
	if n == 0 {
		return 0
	}
	return sum / float64(n)
}

func BestKMeans1D(values []float64, maxK int) *KMeans1D {
	if len(values) <= 1 {
		return nil
	}
	if maxK < 2 {
		maxK = 2
	}
	if maxK > len(values)-1 {
		maxK = len(values) - 1
	}
	var best *KMeans1D
	bestScore := -1.0
	for k := 2; k <= maxK; k++ {
		km := KMeans1DClustering(values, k)
		if km == nil {
			continue
		}
		score := SilhouetteScore(values, km.Labels, k)
		if score > bestScore {
			bestScore = score
			best = km
		}
	}
	return best
}
