package retrieval

import (
	"context"
	"strings"
	"testing"

	"agentcanvas/internal/domain/contextresource"
	"agentcanvas/internal/infrastructure/vectorstore"
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

func TestContextScopeFilterSeparatesMessagesAndSharedResources(t *testing.T) {
	message := contextScopeFilter(contextresource.SearchRequest{OwnerID: 9, AgentID: 3, ConversationID: 7, ResourceTypes: []string{contextresource.TypeConversationMessage}})
	if message["agent_id"] != int64(3) || message["conversation_id"] != int64(7) {
		t.Fatalf("conversation messages must match exact agent and conversation: %#v", message)
	}
	shared := contextScopeFilter(contextresource.SearchRequest{OwnerID: 9, AgentID: 3, ConversationID: 7, ResourceTypes: []string{contextresource.TypeLongTermMemory}})
	agents, agentsOK := shared["agent_id"].([]int64)
	conversations, conversationsOK := shared["conversation_id"].([]int64)
	if !agentsOK || !conversationsOK || len(agents) != 2 || agents[0] != 0 || agents[1] != 3 || len(conversations) != 2 || conversations[0] != 0 || conversations[1] != 7 {
		t.Fatalf("shared resources must allow global and current scopes: %#v", shared)
	}
	projectScoped := contextScopeFilter(contextresource.SearchRequest{OwnerID: 9, ProjectID: 42, ResourceTypes: []string{contextresource.TypeLongTermMemory}})
	projects, projectsOK := projectScoped["project_id"].([]int64)
	if !projectsOK || len(projects) != 2 || projects[0] != 0 || projects[1] != 42 {
		t.Fatalf("project resources must allow global and current project only: %#v", projectScoped)
	}
}

func TestContextKeywordSearchUsesMilvusTextIndexWithoutEmbedding(t *testing.T) {
	store := &fakeContextTextStore{results: []vectorstore.SearchResult{{ID: "memory-1", Score: 0.8, Metadata: map[string]any{
		"owner_id": int64(9), "resource_type": contextresource.TypeLongTermMemory, "resource_id": "1",
	}}}}
	index := &ContextSemanticIndex{Store: store}

	results, err := index.Search(context.Background(), contextresource.SearchRequest{OwnerID: 9, Query: "hello", Mode: "keyword", TopK: 2})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(results) != 1 || store.request.Collection != contextResourceKeywordCollection || store.request.QueryText != "hello" {
		t.Fatalf("keyword results/request = %+v / %+v", results, store.request)
	}
}

func TestContextKeywordSearchExcludesOtherProjectsBeforeRecall(t *testing.T) {
	store := &fakeContextTextStore{results: []vectorstore.SearchResult{
		{ID: "other", Score: 1, Metadata: map[string]any{"owner_id": int64(9), "project_id": int64(99), "resource_type": contextresource.TypeLongTermMemory, "resource_id": "99"}},
		{ID: "current", Score: .9, Metadata: map[string]any{"owner_id": int64(9), "project_id": int64(42), "resource_type": contextresource.TypeLongTermMemory, "resource_id": "42"}},
	}}
	index := &ContextSemanticIndex{Store: store}
	results, err := index.Search(context.Background(), contextresource.SearchRequest{OwnerID: 9, ProjectID: 42, ResourceTypes: []string{contextresource.TypeLongTermMemory}, Query: "fact", Mode: "keyword", TopK: 2})
	if err != nil || len(results) != 1 || results[0].ResourceID != "42" {
		t.Fatalf("cross-project context leaked: results=%+v err=%v", results, err)
	}
}

func TestFuseContextResultsAppliesRRFOnce(t *testing.T) {
	keyword := []contextresource.SearchResult{{ResourceType: "memory", ResourceID: "1", Score: 10}}
	vector := []contextresource.SearchResult{{ResourceType: "memory", ResourceID: "1", Score: 20}, {ResourceType: "memory", ResourceID: "2", Score: 5}}
	results := fuseContextResults(keyword, vector, 2)
	if len(results) != 2 || results[0].ResourceID != "1" || results[0].Score <= results[1].Score {
		t.Fatalf("fused results = %+v", results)
	}
}

type fakeContextTextStore struct {
	request vectorstore.SearchRequest
	results []vectorstore.SearchResult
}

func (*fakeContextTextStore) EnsureCollection(context.Context, string, int, vectorstore.HNSWConfig) error {
	return nil
}
func (*fakeContextTextStore) Upsert(context.Context, string, []vectorstore.VectorDocument) error {
	return nil
}
func (*fakeContextTextStore) Delete(context.Context, string, []string) error { return nil }
func (*fakeContextTextStore) Search(context.Context, vectorstore.SearchRequest) ([]vectorstore.SearchResult, error) {
	return nil, nil
}
func (f *fakeContextTextStore) SearchText(_ context.Context, request vectorstore.SearchRequest) ([]vectorstore.SearchResult, error) {
	f.request = request
	return f.results, nil
}
