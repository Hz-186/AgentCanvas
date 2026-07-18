package retrieval

import (
	"context"
	"errors"
	"sort"
	"strings"

	"agentcanvas/internal/domain/contextresource"
)

type ContextHybridIndex struct {
	Keyword  contextresource.Index
	Semantic contextresource.Index
	RRFK     int
}

func (s ContextHybridIndex) Upsert(ctx context.Context, document contextresource.Document, profile contextresource.EmbeddingProfile) (contextresource.EmbeddingProfile, error) {
	var err error
	if s.Keyword != nil {
		if _, keywordErr := s.Keyword.Upsert(ctx, document, profile); keywordErr != nil {
			err = errors.Join(err, keywordErr)
		}
	}
	if s.Semantic != nil {
		resolved, semanticErr := s.Semantic.Upsert(ctx, document, profile)
		if semanticErr != nil {
			err = errors.Join(err, semanticErr)
		} else {
			profile = resolved
		}
	}
	return profile, err
}

func (s ContextHybridIndex) Delete(ctx context.Context, item contextresource.OutboxItem) error {
	var err error
	if s.Keyword != nil {
		err = errors.Join(err, s.Keyword.Delete(ctx, item))
	}
	if s.Semantic != nil {
		err = errors.Join(err, s.Semantic.Delete(ctx, item))
	}
	return err
}

func (s ContextHybridIndex) Search(ctx context.Context, request contextresource.SearchRequest) ([]contextresource.SearchResult, error) {
	groups := make([][]contextresource.SearchResult, 0, 2)
	var joined error
	mode := strings.ToLower(strings.TrimSpace(request.Mode))
	if s.Keyword != nil && mode != "vector" {
		items, err := s.Keyword.Search(ctx, request)
		if err != nil {
			joined = errors.Join(joined, err)
		} else if len(items) > 0 {
			groups = append(groups, items)
		}
	}
	if s.Semantic != nil && mode != "keyword" {
		items, err := s.Semantic.Search(ctx, request)
		if err != nil {
			joined = errors.Join(joined, err)
		} else if len(items) > 0 {
			groups = append(groups, items)
		}
	}
	if len(groups) == 0 {
		return nil, joined
	}
	rrfK := s.RRFK
	if rrfK <= 0 {
		rrfK = 60
	}
	type fused struct {
		result contextresource.SearchResult
		score  float64
	}
	merged := map[string]fused{}
	for _, group := range groups {
		seen := map[string]bool{}
		for rank, item := range group {
			key := item.ResourceType + "\x1f" + item.ResourceID
			if seen[key] {
				continue
			}
			seen[key] = true
			current := merged[key]
			if current.result.ResourceID == "" || item.Score > current.result.Score {
				current.result = item
			}
			current.score += 1 / float64(rrfK+rank+1)
			merged[key] = current
		}
	}
	results := make([]contextresource.SearchResult, 0, len(merged))
	for _, item := range merged {
		item.result.Score = item.score
		results = append(results, item.result)
	}
	sort.SliceStable(results, func(i, j int) bool { return results[i].Score > results[j].Score })
	limit := request.TopK
	if limit <= 0 {
		limit = 12
	}
	if len(results) > limit {
		results = results[:limit]
	}
	return results, nil
}

var _ contextresource.Index = ContextHybridIndex{}
