package deepdoc

import (
	"testing"
)

func TestKMeans1DClustering(t *testing.T) {
	values := []float64{10, 11, 12, 50, 51, 52, 90, 91, 92}
	km := KMeans1DClustering(values, 3)
	if km == nil {
		t.Fatal("KMeans1DClustering returned nil")
	}
	if km.K != 3 {
		t.Fatalf("K = %d, want 3", km.K)
	}
	if len(km.Labels) != len(values) {
		t.Fatalf("labels len = %d, want %d", len(km.Labels), len(values))
	}
}

func TestKMeans1DSingleCluster(t *testing.T) {
	values := []float64{10, 11, 12}
	km := KMeans1DClustering(values, 1)
	if km != nil {
		t.Fatal("KMeans1DClustering with k=1 should return nil")
	}
}

func TestBestKMeans1D(t *testing.T) {
	values := []float64{10, 11, 12, 13, 14, 50, 51, 52, 53, 54}
	km := BestKMeans1D(values, 3)
	if km == nil {
		t.Fatal("BestKMeans1D returned nil")
	}
	if km.K < 2 {
		t.Fatalf("best K = %d, want >= 2", km.K)
	}
}

func TestSilhouetteScore(t *testing.T) {
	values := []float64{1, 2, 3, 10, 11, 12}
	labels := []int{0, 0, 0, 1, 1, 1}
	score := SilhouetteScore(values, labels, 2)
	if score <= 0 || score > 1 {
		t.Fatalf("Silhouette score = %f, want between 0 and 1", score)
	}
}
