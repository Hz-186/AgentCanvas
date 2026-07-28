package retrieval

import (
	"context"
	"errors"
	"strings"

	"agentcanvas/internal/domain/contextresource"
	"agentcanvas/internal/domain/retrieval/fusion"
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
	lists := make([]fusion.RankedList[contextresource.SearchResult], 0, len(groups))
	bestByKey := make(map[string]contextresource.SearchResult)
	for _, group := range groups {
		lists = append(lists, fusion.RankedList[contextresource.SearchResult]{Items: group, Weight: 1})
		for _, item := range group {
			key := contextResultKey(item)
			if current, exists := bestByKey[key]; !exists || item.Score > current.Score {
				bestByKey[key] = item
			}
		}
	}
	limit := request.TopK
	if limit <= 0 {
		limit = 12
	}
	ranked := fusion.ReciprocalRank(lists, contextResultKey, s.RRFK, 0, limit)
	results := make([]contextresource.SearchResult, 0, len(ranked))
	for _, item := range ranked {
		result := bestByKey[contextResultKey(item.Item)]
		result.Score = item.Score
		results = append(results, result)
	}
	return results, nil
}

func contextResultKey(item contextresource.SearchResult) string {
	return item.ResourceType + "\x1f" + item.ResourceID
}

var _ contextresource.Index = ContextHybridIndex{}
