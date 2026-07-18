package retrieval

import (
	"strings"
	"testing"

	"agentcanvas/internal/domain/contextresource"
)

func TestContextCollectionSeparatesEmbeddingProfiles(t *testing.T) {
	a := contextResourceCollection(contextresource.EmbeddingProfile{ProviderID: 1, Model: "a", Dimensions: 1536})
	b := contextResourceCollection(contextresource.EmbeddingProfile{ProviderID: 1, Model: "a", Dimensions: 3072})
	if a == b || !strings.Contains(a, "1536") || !strings.Contains(b, "3072") {
		t.Fatalf("unexpected collections: %q %q", a, b)
	}
}

func TestContextVectorIDIncludesTenantAndType(t *testing.T) {
	base := contextresource.DocumentID(1, contextresource.TypeSkill, "7")
	if base == contextresource.DocumentID(2, contextresource.TypeSkill, "7") || base == contextresource.DocumentID(1, contextresource.TypeTool, "7") {
		t.Fatal("vector id must isolate tenant and resource type")
	}
}
