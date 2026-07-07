package deepdoc

import (
	"math"
	"testing"
)

func TestRectWidthHeight(t *testing.T) {
	r := NewRect(10, 20, 100, 200)
	if r.Width() != 100 {
		t.Fatalf("Width() = %f, want 100", r.Width())
	}
	if r.Height() != 200 {
		t.Fatalf("Height() = %f, want 200", r.Height())
	}
}

func TestRectCenter(t *testing.T) {
	r := NewRect(0, 0, 100, 100)
	if r.CenterX() != 50 || r.CenterY() != 50 {
		t.Fatalf("Center = (%f, %f), want (50, 50)", r.CenterX(), r.CenterY())
	}
}

func TestRectOverlapY(t *testing.T) {
	a := NewRect(0, 0, 10, 10)
	b := NewRect(0, 5, 10, 15)
	if overlap := a.OverlapY(b); overlap != 5 {
		t.Fatalf("OverlapY = %f, want 5", overlap)
	}
	b2 := NewRect(0, 20, 10, 30)
	if overlap := a.OverlapY(b2); overlap != 0 {
		t.Fatalf("OverlapY = %f, want 0 (no overlap)", overlap)
	}
}

func TestRectOverlapRatioY(t *testing.T) {
	a := NewRect(0, 0, 10, 20)
	b := NewRect(0, 10, 10, 30)
	if ratio := a.OverlapRatioY(b); math.Abs(ratio-0.5) > 0.001 {
		t.Fatalf("OverlapRatioY = %f, want 0.5", ratio)
	}
}

func TestRectYGap(t *testing.T) {
	a := NewRect(0, 0, 10, 10)
	b := NewRect(0, 15, 10, 25)
	if gap := a.YGap(b); gap != 5 {
		t.Fatalf("YGap = %f, want 5", gap)
	}
}

func TestRectIoU(t *testing.T) {
	a := NewRect(0, 0, 10, 10)
	b := NewRect(5, 5, 10, 10)
	iou := a.IoU(b)
	if iou <= 0 || iou >= 1 {
		t.Fatalf("IoU = %f, want between 0 and 1", iou)
	}
}

func TestRectUnion(t *testing.T) {
	a := NewRect(0, 0, 10, 10)
	b := NewRect(5, 5, 10, 10)
	u := a.Union(b)
	if u.Left != 0 || u.Top != 0 || u.Right != 15 || u.Bottom != 15 {
		t.Fatalf("Union = %+v, want (0,0,15,15)", u)
	}
}

func TestMedianFloat64(t *testing.T) {
	if m := MedianFloat64([]float64{1, 3, 2}); m != 2 {
		t.Fatalf("Median = %f, want 2", m)
	}
	if m := MedianFloat64([]float64{1, 2, 3, 4}); m != 2.5 {
		t.Fatalf("Median = %f, want 2.5", m)
	}
	if m := MedianFloat64(nil); m != 0 {
		t.Fatalf("Median = %f, want 0", m)
	}
}
