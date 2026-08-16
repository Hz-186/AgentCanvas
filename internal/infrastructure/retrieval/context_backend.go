package retrieval

import (
	"context"
	"fmt"
	"strings"

	"agentcanvas/internal/domain/contextresource"
	"agentcanvas/internal/domain/retrieval/fusion"
)

// ContextBackendIndex keeps keyword and semantic indexes inside one selected
// backend. It never combines indexes from different infrastructure systems.
type ContextBackendIndex struct {
	Keyword  contextresource.Index
	Semantic contextresource.Index
}

func (s ContextBackendIndex) Upsert(ctx context.Context, document contextresource.Document, profile contextresource.EmbeddingProfile) (contextresource.EmbeddingProfile, error) {
	resolved := profile
	if s.Keyword != nil {
		var err error
		resolved, err = s.Keyword.Upsert(ctx, document, resolved)
		if err != nil {
			return resolved, err
		}
	}
	if s.Semantic != nil {
		var err error
		resolved, err = s.Semantic.Upsert(ctx, document, resolved)
		if err != nil {
			return resolved, err
		}
	}
	return resolved, nil
}

func (s ContextBackendIndex) Delete(ctx context.Context, item contextresource.OutboxItem) error {
	if s.Keyword != nil {
		if err := s.Keyword.Delete(ctx, item); err != nil {
			return err
		}
	}
	if s.Semantic != nil {
		return s.Semantic.Delete(ctx, item)
	}
	return nil
}

func (s ContextBackendIndex) Search(ctx context.Context, request contextresource.SearchRequest) ([]contextresource.SearchResult, error) {
	mode := strings.ToLower(strings.TrimSpace(request.Mode))
	switch mode {
	case "", "keyword":
		if s.Keyword == nil {
			return nil, fmt.Errorf("context keyword backend is not configured")
		}
		return s.Keyword.Search(ctx, request)
	case "vector":
		if s.Semantic == nil {
			return nil, fmt.Errorf("context vector backend is not configured")
		}
		return s.Semantic.Search(ctx, request)
	case "hybrid":
		if s.Keyword == nil || s.Semantic == nil {
			return nil, fmt.Errorf("context hybrid backend is not configured")
		}
		keywordRequest := request
		keywordRequest.Mode = "keyword"
		keyword, err := s.Keyword.Search(ctx, keywordRequest)
		if err != nil {
			return nil, err
		}
		semanticRequest := request
		semanticRequest.Mode = "vector"
		semantic, err := s.Semantic.Search(ctx, semanticRequest)
		if err != nil {
			return nil, err
		}
		limit := request.TopK
		if limit <= 0 {
			limit = 12
		}
		return fuseContextResults(keyword, semantic, limit), nil
	default:
		return nil, fmt.Errorf("unsupported context retrieval mode: %s", request.Mode)
	}
}

var _ contextresource.Index = ContextBackendIndex{}

func contextResultKey(item contextresource.SearchResult) string {
	return item.ResourceType + "\x1f" + item.ResourceID
}

func fuseContextResults(keyword, vector []contextresource.SearchResult, limit int) []contextresource.SearchResult {
	ranked := fusion.ReciprocalRank([]fusion.RankedList[contextresource.SearchResult]{
		{Items: keyword, Weight: 1},
		{Items: vector, Weight: 1},
	}, contextResultKey, fusion.DefaultRankConstant, 0, limit)
	best := make(map[string]contextresource.SearchResult, len(keyword)+len(vector))
	for _, item := range append(keyword, vector...) {
		key := contextResultKey(item)
		if current, ok := best[key]; !ok || item.Score > current.Score {
			best[key] = item
		}
	}
	results := make([]contextresource.SearchResult, 0, len(ranked))
	for _, item := range ranked {
		result := best[contextResultKey(item.Item)]
		result.Score = item.Score
		results = append(results, result)
	}
	return results
}
