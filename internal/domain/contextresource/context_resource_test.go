package contextresource

import "testing"

func TestEmbeddingProfileHashSeparatesVectorSpaces(t *testing.T) {
	a := (EmbeddingProfile{ProviderID: 1, Model: "embed-a", Dimensions: 1536}).Normalized()
	b := (EmbeddingProfile{ProviderID: 1, Model: "embed-a", Dimensions: 3072}).Normalized()
	if a.Hash == "" || a.Hash == b.Hash {
		t.Fatalf("expected distinct non-empty hashes: a=%q b=%q", a.Hash, b.Hash)
	}
}

func TestEmbeddingProfileHashIsRecomputedAfterProviderResolution(t *testing.T) {
	withoutProvider := (EmbeddingProfile{Model: "embed-a"}).Normalized()
	resolved := withoutProvider
	resolved.ProviderID = 9
	resolved = resolved.Normalized()
	if withoutProvider.Hash == resolved.Hash {
		t.Fatal("provider resolution must create a distinct profile hash")
	}
}

func TestHashContentIsDeterministicAndTrimsBoundaryWhitespace(t *testing.T) {
	if HashContent(" value ") != HashContent("value") {
		t.Fatal("expected boundary whitespace normalization")
	}
}
