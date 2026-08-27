package skill_usecase

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"agentcanvas/internal/domain/skill"
)

// Retriever is the skill subsystem's query reader. Skill queries are routed
// here, never through the memory subsystem: skills are not memory artifacts.
type Retriever struct {
	repo    skill.Repository
	maxScan int
}

func NewRetriever(repo skill.Repository) *Retriever {
	return &Retriever{repo: repo, maxScan: 100}
}

// Search returns enabled, non-deleted owner skills scored against the query,
// ordered by score descending with an ascending ID tie-break, capped at limit
// (default 3, maximum 5).
func (r *Retriever) Search(ctx context.Context, ownerID int64, query string, limit int) ([]skill.Skill, error) {
	if r == nil || r.repo == nil {
		return nil, fmt.Errorf("skill subsystem retriever is not configured")
	}
	if ownerID <= 0 {
		return nil, fmt.Errorf("skill search owner is required")
	}
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 3
	}
	if limit > 5 {
		limit = 5
	}
	items, err := r.repo.List(ctx, ownerID, r.maxScan, 0)
	if err != nil {
		return nil, err
	}
	tokens := strings.Fields(query)
	type match struct {
		item  skill.Skill
		score float64
	}
	matches := make([]match, 0, len(items))
	for _, item := range items {
		if !item.Enabled || item.DeletedAt != nil {
			continue
		}
		score := scoreSkillForQuery(item, tokens)
		if score <= 0 {
			continue
		}
		matches = append(matches, match{item: item, score: score})
	}
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].score == matches[j].score {
			return matches[i].item.ID < matches[j].item.ID
		}
		return matches[i].score > matches[j].score
	})
	if len(matches) > limit {
		matches = matches[:limit]
	}
	skills := make([]skill.Skill, 0, len(matches))
	for _, item := range matches {
		skills = append(skills, item.item)
	}
	return skills, nil
}

// scoreSkillForQuery mirrors the runtime's skill metadata scoring: name beats
// description beats tags for each query token.
func scoreSkillForQuery(item skill.Skill, tokens []string) float64 {
	name := strings.ToLower(item.Name)
	description := strings.ToLower(item.Description)
	tags := strings.ToLower(strings.Join(item.Tags(), " "))
	var score float64
	for _, token := range tokens {
		if token == "" {
			continue
		}
		if strings.Contains(name, token) {
			score += 1
		}
		if strings.Contains(description, token) {
			score += 0.7
		}
		if strings.Contains(tags, token) {
			score += 0.4
		}
	}
	return score
}

var _ skill.Retriever = (*Retriever)(nil)
