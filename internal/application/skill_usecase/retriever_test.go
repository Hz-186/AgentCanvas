package skill_usecase

import (
	"context"
	"testing"
	"time"

	"agentcanvas/internal/domain"
	"agentcanvas/internal/domain/skill"

	"gorm.io/gorm"
)

type retrieverFakeRepo struct {
	items []skill.Skill
}

func (r *retrieverFakeRepo) Create(context.Context, *skill.Skill) error { return nil }
func (r *retrieverFakeRepo) Update(context.Context, *skill.Skill) error { return nil }
func (r *retrieverFakeRepo) FindByID(context.Context, int64, int64) (*skill.Skill, error) {
	return nil, gorm.ErrRecordNotFound
}
func (r *retrieverFakeRepo) List(_ context.Context, ownerID int64, limit, offset int) ([]skill.Skill, error) {
	items := make([]skill.Skill, 0, len(r.items))
	for _, item := range r.items {
		if item.OwnerID == ownerID {
			items = append(items, item)
		}
	}
	return items, nil
}
func (r *retrieverFakeRepo) ListByIDs(context.Context, int64, []int64) ([]skill.Skill, error) {
	return nil, nil
}
func (r *retrieverFakeRepo) SoftDelete(context.Context, int64, int64) error { return nil }

func TestRetrieverSearchRanksAndScopesOwnerSkills(t *testing.T) {
	deletedAt := time.Now()
	repo := &retrieverFakeRepo{items: []skill.Skill{
		{SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 1, OwnerID: 1}}, Name: "repo-review", Description: "review repository changes", Enabled: skill.Enabled},
		{SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 2, OwnerID: 1}}, Name: "doc-writer", Description: "write documentation", Enabled: skill.Enabled},
		{SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 3, OwnerID: 1}, DeletedAt: &deletedAt}, Name: "deploy-workflow", Description: "deploy release workflow", Enabled: skill.Enabled},
		{SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 4, OwnerID: 1}}, Name: "disabled-skill", Description: "review disabled", Enabled: skill.Disabled},
		{SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 5, OwnerID: 2}}, Name: "foreign-skill", Description: "review foreign owner", Enabled: skill.Enabled},
	}}
	retriever := NewRetriever(repo)

	items, err := retriever.Search(context.Background(), 1, "review repo changes", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ID != 1 {
		t.Fatalf("search results = %+v, want the owner-1 repo-review skill only", items)
	}
}

func TestRetrieverSearchLimitsAndRequiresConfiguration(t *testing.T) {
	repo := &retrieverFakeRepo{items: []skill.Skill{
		{SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 1, OwnerID: 1}}, Name: "alpha", Description: "common topic", Enabled: skill.Enabled},
		{SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 2, OwnerID: 1}}, Name: "beta", Description: "common topic", Enabled: skill.Enabled},
		{SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 3, OwnerID: 1}}, Name: "gamma", Description: "common topic", Enabled: skill.Enabled},
	}}
	retriever := NewRetriever(repo)

	items, err := retriever.Search(context.Background(), 1, "common topic", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].ID != 1 || items[1].ID != 2 {
		t.Fatalf("search results = %+v, want the first two skills by ID", items)
	}
	if _, err := (&Retriever{}).Search(context.Background(), 1, "anything", 3); err == nil {
		t.Fatal("expected a configuration error for a retriever without a repository")
	}
}
