package catalog

import "testing"

func TestLoaderIncludesBGEPresets(t *testing.T) {
	loader, err := NewLoader()
	if err != nil {
		t.Fatalf("NewLoader() error = %v", err)
	}

	var hasBGEM3, hasBGEReranker bool
	for _, provider := range loader.List() {
		for _, model := range provider.Models {
			switch model.Name {
			case "BAAI/bge-m3":
				hasBGEM3 = model.ModelType == "embedding" && model.MaxTokens == 8192
			case "BAAI/bge-reranker-v2-m3":
				hasBGEReranker = model.ModelType == "rerank" && model.MaxTokens == 8192
			}
		}
	}
	if !hasBGEM3 {
		t.Fatal("catalog missing BAAI/bge-m3 embedding preset")
	}
	if !hasBGEReranker {
		t.Fatal("catalog missing BAAI/bge-reranker-v2-m3 rerank preset")
	}
}
