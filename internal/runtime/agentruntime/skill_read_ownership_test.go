package agentruntime

import (
	"context"
	"encoding/json"
	"testing"

	"agentcanvas/internal/domain"
	"agentcanvas/internal/domain/skill"
	"agentcanvas/internal/runtime/toolruntime"

	"gorm.io/gorm"
)

// fakeSkillQueryRetriever records calls and returns canned workflow skills. It
// is the skill subsystem retriever the runtime must route skill queries to.
type fakeSkillQueryRetriever struct {
	skills  []skill.Skill
	calls   int
	ownerID int64
	query   string
}

func (r *fakeSkillQueryRetriever) Search(_ context.Context, ownerID int64, query string, limit int) ([]skill.Skill, error) {
	r.calls++
	r.ownerID = ownerID
	r.query = query
	if limit <= 0 || limit > len(r.skills) {
		return r.skills, nil
	}
	return r.skills[:limit], nil
}

// fakeAttachedSkillRepository serves loadSkillDefinitions for the ownership
// test. Its attached-skill metadata deliberately differs from the retriever's
// workflow so the test proves the result came from the skill subsystem.
type fakeAttachedSkillRepository struct {
	items map[int64]*skill.Skill
}

func (r *fakeAttachedSkillRepository) Create(context.Context, *skill.Skill) error { return nil }
func (r *fakeAttachedSkillRepository) Update(context.Context, *skill.Skill) error { return nil }
func (r *fakeAttachedSkillRepository) FindByID(_ context.Context, ownerID, id int64) (*skill.Skill, error) {
	item := r.items[id]
	if item == nil || item.OwnerID != ownerID {
		return nil, gorm.ErrRecordNotFound
	}
	return item, nil
}
func (r *fakeAttachedSkillRepository) List(context.Context, int64, int, int) ([]skill.Skill, error) {
	return nil, nil
}
func (r *fakeAttachedSkillRepository) ListByIDs(_ context.Context, ownerID int64, ids []int64) ([]skill.Skill, error) {
	items := make([]skill.Skill, 0, len(ids))
	for _, id := range ids {
		if item := r.items[id]; item != nil && item.OwnerID == ownerID {
			items = append(items, *item)
		}
	}
	return items, nil
}
func (r *fakeAttachedSkillRepository) SoftDelete(context.Context, int64, int64) error { return nil }

// SkillReadOwnershipTest#shouldRouteSkillQueryToSkillSubsystem
func TestSkillReadOwnershipShouldRouteSkillQueryToSkillSubsystem(t *testing.T) {
	retriever := &fakeSkillQueryRetriever{skills: []skill.Skill{
		{SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 7, OwnerID: 1}}, Name: "deploy-workflow", Description: "deploy release workflow", Enabled: skill.Enabled},
	}}
	artifacts := &fakeMemoryArtifactRepository{} // memory artifacts must stay untouched
	attached := &fakeAttachedSkillRepository{items: map[int64]*skill.Skill{
		7: {SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 7, OwnerID: 1}}, Name: "attached-def", Description: "attached metadata only", Enabled: skill.Enabled},
	}}
	n := runtimeCore{coreRepositories: coreRepositories{
		Skills:          attached,
		SkillRetriever:  retriever,
		MemoryArtifacts: artifacts,
	}}
	cfg := agentRuntimeConfig{RuntimeResourceRefs: RuntimeResourceRefs{SkillIDs: []int64{7}, SkillLoadingMode: "search"}}

	tools, err := n.loadTools(context.Background(), 1, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	var searchTool *toolruntime.SkillSearchTool
	for _, item := range tools {
		if tool, ok := item.(toolruntime.SkillSearchTool); ok {
			searchTool = &tool
			break
		}
	}
	if searchTool == nil {
		t.Fatal("skill_search tool was not loaded for search mode")
	}
	result, err := searchTool.Execute(context.Background(), toolruntime.ToolRunContext{OwnerID: 1}, json.RawMessage(`{"goal":"deploy workflow"}`))
	if err != nil {
		t.Fatal(err)
	}
	var output struct {
		Matches []struct {
			SkillID float64 `json:"skill_id"`
			Name    string  `json:"name"`
		} `json:"matches"`
	}
	if err := json.Unmarshal(result.ContentJSON, &output); err != nil {
		t.Fatal(err)
	}
	if retriever.calls != 1 {
		t.Fatalf("skill retriever calls = %d, want exactly one routed skill query", retriever.calls)
	}
	if retriever.ownerID != 1 || retriever.query != "deploy workflow" {
		t.Fatalf("skill retriever received owner %d query %q", retriever.ownerID, retriever.query)
	}
	if len(output.Matches) != 1 || output.Matches[0].Name != "deploy-workflow" {
		t.Fatalf("skill query did not route to the skill subsystem retriever: %+v", output.Matches)
	}
	if artifacts.latestCalls != 0 {
		t.Fatalf("memory artifact reads = %d, want zero (skills are not memory artifacts)", artifacts.latestCalls)
	}
}
