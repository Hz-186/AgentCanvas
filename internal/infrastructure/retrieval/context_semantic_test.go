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
}
