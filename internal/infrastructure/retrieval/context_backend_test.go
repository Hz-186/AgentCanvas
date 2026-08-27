package retrieval

import (
	"context"
	"errors"
	"testing"

	"agentcanvas/internal/domain/contextresource"
)

type countingContextIndex struct {
	calls int
	hits  []contextresource.SearchResult
	err   error
}

func (c *countingContextIndex) Upsert(context.Context, contextresource.Document, contextresource.EmbeddingProfile) (contextresource.EmbeddingProfile, error) {
	return contextresource.EmbeddingProfile{}, nil
}
func (c *countingContextIndex) Delete(context.Context, contextresource.OutboxItem) error { return nil }
func (c *countingContextIndex) Search(context.Context, contextresource.SearchRequest) ([]contextresource.SearchResult, error) {
	c.calls++
	return c.hits, c.err
}

// TestContextBackendKeywordModeNeverInvokesVectorBranch locks the permanent
// keyword-only routing contract: memory detail reads ask for keyword mode and
// the backend must never touch the semantic/vector index in that mode.
func TestContextBackendKeywordModeNeverInvokesVectorBranch(t *testing.T) {
	keyword := &countingContextIndex{hits: []contextresource.SearchResult{{ResourceType: contextresource.TypeLongTermMemory, ResourceID: "7", Score: 2}}}
	vector := &countingContextIndex{err: errors.New("vector branch must not run")}
	backend := ContextBackendIndex{Keyword: keyword, Semantic: vector}
	results, err := backend.Search(context.Background(), contextresource.SearchRequest{OwnerID: 1, Query: "fact", Mode: "keyword", TopK: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || keyword.calls != 1 || vector.calls != 0 {
		t.Fatalf("keyword-only routing violated: results=%+v keywordCalls=%d vectorCalls=%d", results, keyword.calls, vector.calls)
	}
}

// TestContextBackendEmptyModeDefaultsToKeywordOnly locks the empty-mode default
// onto the keyword index so an unset mode can never route into the vector leg.
func TestContextBackendEmptyModeDefaultsToKeywordOnly(t *testing.T) {
	keyword := &countingContextIndex{hits: []contextresource.SearchResult{{ResourceType: contextresource.TypeLongTermMemory, ResourceID: "3", Score: 1}}}
	vector := &countingContextIndex{err: errors.New("vector branch must not run")}
	backend := ContextBackendIndex{Keyword: keyword, Semantic: vector}
	results, err := backend.Search(context.Background(), contextresource.SearchRequest{OwnerID: 1, Query: "fact", TopK: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || keyword.calls != 1 || vector.calls != 0 {
		t.Fatalf("empty mode must default to keyword-only: results=%+v keywordCalls=%d vectorCalls=%d", results, keyword.calls, vector.calls)
	}
}
