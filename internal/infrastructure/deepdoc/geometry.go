package deepdoc

import "math"

type Point struct {
	X float64
	Y float64
}

type Rect struct {
	Left   float64
	Top    float64
	Right  float64
	Bottom float64
}

func NewRect(x, y, w, h float64) Rect {
	return Rect{Left: x, Top: y, Right: x + w, Bottom: y + h}
}

func (r Rect) Width() float64   { return r.Right - r.Left }
func (r Rect) Height() float64  { return r.Bottom - r.Top }
func (r Rect) CenterX() float64 { return (r.Left + r.Right) / 2 }
func (r Rect) CenterY() float64 { return (r.Top + r.Bottom) / 2 }

func (r Rect) OverlapY(other Rect) float64 {
	return math.Max(0, math.Min(r.Bottom, other.Bottom)-math.Max(r.Top, other.Top))
}

func (r Rect) OverlapX(other Rect) float64 {
	return math.Max(0, math.Min(r.Right, other.Right)-math.Max(r.Left, other.Left))
}

func (r Rect) Union(other Rect) Rect {
	return Rect{
		Left:   math.Min(r.Left, other.Left),
		Top:    math.Min(r.Top, other.Top),
		Right:  math.Max(r.Right, other.Right),
		Bottom: math.Max(r.Bottom, other.Bottom),
	}
}

func (r Rect) Area() float64 {
	w := r.Width()
	h := r.Height()
	if w <= 0 || h <= 0 {
		return 0
	}
	return w * h
}

func (r Rect) IoU(other Rect) float64 {
	intersection := Rect{
		Left:   math.Max(r.Left, other.Left),
		Top:    math.Max(r.Top, other.Top),
		Right:  math.Min(r.Right, other.Right),
		Bottom: math.Min(r.Bottom, other.Bottom),
	}
	intersectArea := intersection.Area()
	if intersectArea <= 0 {
		return 0
	}
	unionArea := r.Area() + other.Area() - intersectArea
	if unionArea <= 0 {
		return 0
	}
	return intersectArea / unionArea
}

func (r Rect) OverlapRatioY(other Rect) float64 {
	overlap := r.OverlapY(other)
	if overlap <= 0 {
		return 0
	}
	minHeight := math.Min(r.Height(), other.Height())
	if minHeight <= 0 {
		return 0
	}
	return overlap / minHeight
}

func (r Rect) OverlapRatioX(other Rect) float64 {
	overlap := r.OverlapX(other)
	if overlap <= 0 {
		return 0
	}
	minWidth := math.Min(r.Width(), other.Width())
	if minWidth <= 0 {
		return 0
	}
	return overlap / minWidth
}

func (r Rect) YGap(other Rect) float64 {
	if r.Bottom < other.Top {
		return other.Top - r.Bottom
	}
	if other.Bottom < r.Top {
		return r.Top - other.Bottom
	}
	return 0
}

func (r Rect) XGap(other Rect) float64 {
	if r.Right < other.Left {
		return other.Left - r.Right
	}
	if other.Right < r.Left {
		return r.Left - other.Right
	}
	return 0
}

func MedianFloat64(values []float64) float64 {
	n := len(values)
	if n == 0 {
		return 0
	}
	sorted := make([]float64, n)
	copy(sorted, values)
	float64Slice(sorted).sort()
	if n%2 == 0 {
		return (sorted[n/2-1] + sorted[n/2]) / 2
	}
	return sorted[n/2]
}

type float64Slice []float64

func (s float64Slice) sort() {
	for i := 0; i < len(s); i++ {
		for j := i + 1; j < len(s); j++ {
			if s[i] > s[j] {
				s[i], s[j] = s[j], s[i]
			}
		}
	}
}
