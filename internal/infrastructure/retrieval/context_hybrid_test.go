package retrieval

import (
	"context"
	"errors"
	"testing"

	"agentcanvas/internal/domain/contextresource"
)

type contextIndexFake struct {
	results []contextresource.SearchResult
	err     error
}

func (f contextIndexFake) Upsert(context.Context, contextresource.Document, contextresource.EmbeddingProfile) (contextresource.EmbeddingProfile, error) {
	return contextresource.EmbeddingProfile{}, f.err
}
func (f contextIndexFake) Delete(context.Context, contextresource.OutboxItem) error { return f.err }
func (f contextIndexFake) Search(context.Context, contextresource.SearchRequest) ([]contextresource.SearchResult, error) {
	return f.results, f.err
}

func TestContextHybridSearchFallsBackWhenVectorUnavailable(t *testing.T) {
	keyword := contextIndexFake{results: []contextresource.SearchResult{{ResourceType: contextresource.TypeConversationMessage, ResourceID: "7", Score: 2}}}
	semantic := contextIndexFake{err: errors.New("milvus timeout")}
	results, err := (ContextHybridIndex{Keyword: keyword, Semantic: semantic}).Search(context.Background(), contextresource.SearchRequest{OwnerID: 1, Query: "401", TopK: 5})
	if err != nil || len(results) != 1 || results[0].ResourceID != "7" {
		t.Fatalf("expected keyword fallback, results=%+v err=%v", results, err)
	}
}

func TestContextHybridSearchFusesAndDeduplicates(t *testing.T) {
	keyword := contextIndexFake{results: []contextresource.SearchResult{{ResourceType: contextresource.TypeSkill, ResourceID: "1"}, {ResourceType: contextresource.TypeSkill, ResourceID: "2"}}}
	semantic := contextIndexFake{results: []contextresource.SearchResult{{ResourceType: contextresource.TypeSkill, ResourceID: "2"}, {ResourceType: contextresource.TypeSkill, ResourceID: "3"}}}
	results, err := (ContextHybridIndex{Keyword: keyword, Semantic: semantic, RRFK: 60}).Search(context.Background(), contextresource.SearchRequest{OwnerID: 1, Query: "auth", TopK: 5})
	if err != nil || len(results) != 3 || results[0].ResourceID != "2" {
		t.Fatalf("unexpected fusion: results=%+v err=%v", results, err)
	}
}
