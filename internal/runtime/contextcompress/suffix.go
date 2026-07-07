package contextcompress

import (
	"sort"
	"strings"
)

type suffixIndex struct {
	text []byte
	sa   []int
	rank []int
	lcp  []int
}

func newSuffixIndex(text string) suffixIndex {
	data := []byte(text)
	sa := buildSuffixArray(data)
	rank := make([]int, len(sa))
	for i, pos := range sa {
		rank[pos] = i
	}
	return suffixIndex{
		text: data,
		sa:   sa,
		rank: rank,
		lcp:  buildLCP(data, sa, rank),
	}
}

func buildSuffixArray(text []byte) []int {
	n := len(text)
	sa := make([]int, n)
	if n == 0 {
		return sa
	}
	rank := make([]int, n)
	tmp := make([]int, n)
	for i := 0; i < n; i++ {
		sa[i] = i
		rank[i] = int(text[i])
	}
	for width := 1; width < n; width <<= 1 {
		sort.Slice(sa, func(i, j int) bool {
			a, b := sa[i], sa[j]
			if rank[a] != rank[b] {
				return rank[a] < rank[b]
			}
			ar, br := -1, -1
			if a+width < n {
				ar = rank[a+width]
			}
			if b+width < n {
				br = rank[b+width]
			}
			if ar != br {
				return ar < br
			}
			return a < b
		})
		tmp[sa[0]] = 0
		for i := 1; i < n; i++ {
			prev, cur := sa[i-1], sa[i]
			tmp[cur] = tmp[prev]
			prevNext, curNext := -1, -1
			if prev+width < n {
				prevNext = rank[prev+width]
			}
			if cur+width < n {
				curNext = rank[cur+width]
			}
			if rank[prev] != rank[cur] || prevNext != curNext {
				tmp[cur]++
			}
		}
		copy(rank, tmp)
		if rank[sa[n-1]] == n-1 {
			break
		}
	}
	return sa
}

func buildLCP(text []byte, sa []int, rank []int) []int {
	n := len(text)
	lcp := make([]int, n)
	height := 0
	for i := 0; i < n; i++ {
		r := rank[i]
		if r == 0 {
			continue
		}
		j := sa[r-1]
		for i+height < n && j+height < n && text[i+height] == text[j+height] {
			height++
		}
		lcp[r] = height
		if height > 0 {
			height--
		}
	}
	return lcp
}

func (idx suffixIndex) longestPreviousPrefix(pos int, maxLen int, maxNeighborScan int) int {
	if pos < 0 || pos >= len(idx.rank) || maxLen <= 0 {
		return 0
	}
	r := idx.rank[pos]
	best := 0
	shared := maxLen
	for steps, k := 0, r-1; k >= 0 && shared > best; k-- {
		if maxNeighborScan > 0 && steps >= maxNeighborScan {
			break
		}
		steps++
		shared = min(shared, idx.lcp[k+1])
		if idx.sa[k] < pos && shared > best {
			best = min(shared, maxLen)
		}
	}
	shared = maxLen
	for steps, k := 0, r+1; k < len(idx.sa) && shared > best; k++ {
		if maxNeighborScan > 0 && steps >= maxNeighborScan {
			break
		}
		steps++
		shared = min(shared, idx.lcp[k])
		if idx.sa[k] < pos && shared > best {
			best = min(shared, maxLen)
		}
	}
	return best
}

func longestCommonSubstringSimilarity(a string, b string) float64 {
	a = normalizeComparableText(a)
	b = normalizeComparableText(b)
	if a == "" || b == "" {
		return 0
	}
	shorter := min(len(a), len(b))
	if a == b {
		return 1
	}
	sep := byte(0)
	text := a + string(sep) + b
	idx := newSuffixIndex(text)
	owner := func(pos int) int {
		if pos < len(a) {
			return 1
		}
		if pos > len(a) {
			return 2
		}
		return 0
	}
	best := 0
	for i := 1; i < len(idx.sa); i++ {
		leftOwner := owner(idx.sa[i-1])
		rightOwner := owner(idx.sa[i])
		if leftOwner == 0 || rightOwner == 0 || leftOwner == rightOwner {
			continue
		}
		if idx.lcp[i] > best {
			best = idx.lcp[i]
		}
	}
	if best <= 0 {
		return 0
	}
	if best > shorter {
		best = shorter
	}
	return float64(best) / float64(shorter)
}

func normalizeComparableText(text string) string {
	return strings.ToLower(strings.Join(strings.Fields(text), " "))
}
