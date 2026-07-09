package mysql

import (
	"context"
	"time"

	"agentcanvas/internal/domain/skill"

	"gorm.io/gorm"
)

type SkillRepository struct{ db *gorm.DB }

func NewSkillRepository(db *gorm.DB) *SkillRepository {
	return &SkillRepository{db: db}
}

func (r *SkillRepository) Create(ctx context.Context, item *skill.Skill) error {
	now := time.Now().UTC()
	if item.CreatedAt.IsZero() {
		item.CreatedAt = now
	}
	if item.UpdatedAt.IsZero() {
		item.UpdatedAt = now
	}
	return r.db.WithContext(ctx).Create(item).Error
}

func (r *SkillRepository) Update(ctx context.Context, item *skill.Skill) error {
	item.UpdatedAt = time.Now().UTC()
	return r.db.WithContext(ctx).Save(item).Error
}

func (r *SkillRepository) FindByID(ctx context.Context, ownerID, id int64) (*skill.Skill, error) {
	var item skill.Skill
	err := r.db.WithContext(ctx).Where("owner_id = ? AND id = ? AND deleted_at IS NULL", ownerID, id).First(&item).Error
	if err != nil {
		return nil, err
	}
	return &item, nil
}

func (r *SkillRepository) List(ctx context.Context, ownerID int64, limit, offset int) ([]skill.Skill, error) {
	var items []skill.Skill
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	err := r.db.WithContext(ctx).Where("owner_id = ? AND deleted_at IS NULL", ownerID).
		Order("updated_at DESC, id DESC").Limit(limit).Offset(offset).Find(&items).Error
	return items, err
}

func (r *SkillRepository) ListByIDs(ctx context.Context, ownerID int64, ids []int64) ([]skill.Skill, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var items []skill.Skill
	err := r.db.WithContext(ctx).Where("owner_id = ? AND id IN ? AND deleted_at IS NULL", ownerID, ids).
		Order("updated_at DESC, id DESC").Find(&items).Error
	return items, err
}

func (r *SkillRepository) SoftDelete(ctx context.Context, ownerID, id int64) error {
	now := time.Now().UTC()
	return r.db.WithContext(ctx).Model(&skill.Skill{}).
		Where("owner_id = ? AND id = ? AND deleted_at IS NULL", ownerID, id).
		Updates(map[string]any{"deleted_at": now, "updated_at": now}).Error
}
