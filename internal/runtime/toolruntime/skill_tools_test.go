package toolruntime

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"agentcanvas/internal/domain/skill"

	"gorm.io/gorm"
)

type fakeSkillRepo struct {
	items map[int64]*skill.Skill
}

func (r *fakeSkillRepo) Create(context.Context, *skill.Skill) error { return nil }
func (r *fakeSkillRepo) Update(context.Context, *skill.Skill) error { return nil }
func (r *fakeSkillRepo) FindByID(ctx context.Context, ownerID, id int64) (*skill.Skill, error) {
	item := r.items[id]
	if item == nil || item.OwnerID != ownerID {
		return nil, gorm.ErrRecordNotFound
	}
	clone := *item
	return &clone, nil
}
func (r *fakeSkillRepo) List(context.Context, int64, int, int) ([]skill.Skill, error) {
	return nil, nil
}
func (r *fakeSkillRepo) ListByIDs(ctx context.Context, ownerID int64, ids []int64) ([]skill.Skill, error) {
	items := make([]skill.Skill, 0, len(ids))
	for _, id := range ids {
		if item := r.items[id]; item != nil && item.OwnerID == ownerID {
			items = append(items, *item)
		}
	}
	return items, nil
}
func (r *fakeSkillRepo) SoftDelete(context.Context, int64, int64) error { return nil }

func TestSkillLoadToolReadsInlineSkill(t *testing.T) {
	repo := &fakeSkillRepo{items: map[int64]*skill.Skill{
		1: {ID: 1, OwnerID: 1, Name: "review", Description: "review repo", SourceType: skill.SourceInline, ContentMD: "# Skill", Status: skill.StatusActive},
	}}
	tool := SkillLoadTool{Repository: repo, AllowedSkillIDs: []int64{1}, MaxContentBytes: 1024}
	result, err := tool.Execute(context.Background(), ToolRunContext{OwnerID: 1}, json.RawMessage(`{"skill_id":1}`))
	if err != nil {
		t.Fatal(err)
	}
	var output map[string]any
	if err := json.Unmarshal(result.ContentJSON, &output); err != nil {
		t.Fatal(err)
	}
	if output["name"] != "review" || output["content_md"] != "# Skill" {
		t.Fatalf("unexpected output: %+v", output)
	}
}

func TestSkillLoadToolRejectsUnboundSkill(t *testing.T) {
	repo := &fakeSkillRepo{items: map[int64]*skill.Skill{
		1: {ID: 1, OwnerID: 1, Name: "review", Description: "review repo", SourceType: skill.SourceInline, ContentMD: "# Skill", Status: skill.StatusActive},
	}}
	tool := SkillLoadTool{Repository: repo, AllowedSkillIDs: []int64{2}}
	if _, err := tool.Execute(context.Background(), ToolRunContext{OwnerID: 1}, json.RawMessage(`{"skill_id":1}`)); err == nil {
		t.Fatal("expected unbound skill to be rejected")
	}
}

func TestSkillLoadToolReadsLocalPathSkill(t *testing.T) {
	dir := t.TempDir()
	entry := filepath.Join(dir, "SKILL.md")
	if err := os.WriteFile(entry, []byte("local content"), 0o644); err != nil {
		t.Fatal(err)
	}
	repo := &fakeSkillRepo{items: map[int64]*skill.Skill{
		1: {ID: 1, OwnerID: 1, Name: "local", Description: "local skill", SourceType: skill.SourceLocalPath, BundlePath: dir, EntryFile: "SKILL.md", Status: skill.StatusActive},
	}}
	tool := SkillLoadTool{Repository: repo, AllowedSkillIDs: []int64{1}, SkillRoot: dir, MaxContentBytes: 1024}
	result, err := tool.Execute(context.Background(), ToolRunContext{OwnerID: 1}, json.RawMessage(`{"skill_id":1}`))
	if err != nil {
		t.Fatal(err)
	}
	var output map[string]any
	if err := json.Unmarshal(result.ContentJSON, &output); err != nil {
		t.Fatal(err)
	}
	if output["content_md"] != "local content" {
		t.Fatalf("unexpected output: %+v", output)
	}
}

func TestSkillSearchToolReturnsRankedMatches(t *testing.T) {
	tool := SkillSearchTool{Skills: []skill.Skill{
		{ID: 1, Name: "repo-review", Description: "review repository changes"},
		{ID: 2, Name: "doc-writer", Description: "write documentation"},
	}, Limit: 3}
	result, err := tool.Execute(context.Background(), ToolRunContext{OwnerID: 1}, json.RawMessage(`{"goal":"review repo changes"}`))
	if err != nil {
		t.Fatal(err)
	}
	var output struct {
		Matches []map[string]any `json:"matches"`
	}
	if err := json.Unmarshal(result.ContentJSON, &output); err != nil {
		t.Fatal(err)
	}
	if len(output.Matches) == 0 || int(output.Matches[0]["skill_id"].(float64)) != 1 {
		t.Fatalf("unexpected matches: %+v", output.Matches)
	}
}
