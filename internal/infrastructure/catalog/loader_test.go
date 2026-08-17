package catalog

import (
	"testing"

	"agentcanvas/internal/domain/provider"
)

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

func TestCatalogCapabilitiesMatchConfiguredModelsAndAdapters(t *testing.T) {
	loader, err := NewLoader()
	if err != nil {
		t.Fatalf("NewLoader() error = %v", err)
	}
	for _, item := range loader.List() {
		var hasChat, hasEmbedding bool
		for _, model := range item.Models {
			hasChat = hasChat || model.ModelType == "chat"
			hasEmbedding = hasEmbedding || model.ModelType == "embedding"
		}
		if item.Capabilities.Chat != hasChat || item.Capabilities.Embedding != hasEmbedding {
			t.Fatalf("provider %s capabilities=%+v models=%+v", item.Key, item.Capabilities, item.Models)
		}
		if item.Capabilities.ToolCalling || item.Capabilities.Streaming {
			if !item.Capabilities.Chat {
				t.Fatalf("provider %s advertises chat extensions without chat", item.Key)
			}
			switch item.ProviderType {
			case provider.TypeOpenAICompatible, provider.TypeDeepSeek, provider.TypeQwen, provider.TypeAzureOpenAI:
			default:
				t.Fatalf("provider %s advertises unsupported chat extensions for %s", item.Key, item.ProviderType)
			}
		}
		if item.ProviderType == provider.TypeOllama || item.ProviderType == provider.TypeLocal {
			t.Fatalf("unimplemented provider %s must not be published in catalog", item.ProviderType)
		}
	}
}
