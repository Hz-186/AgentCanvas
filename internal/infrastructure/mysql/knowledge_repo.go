package mysql

import (
	"context"
	"time"

	"agentcanvas/internal/domain/knowledge"

	"gorm.io/gorm"
)

type KnowledgeBaseRepository struct {
	db *gorm.DB
}

func NewKnowledgeBaseRepository(db *gorm.DB) *KnowledgeBaseRepository {
	return &KnowledgeBaseRepository{db: db}
}

func (r *KnowledgeBaseRepository) Create(ctx context.Context, kb *knowledge.KnowledgeBase) error {
	now := time.Now().UTC()
	kb.CreatedAt = now
	kb.UpdatedAt = now
	return r.db.WithContext(ctx).Create(kb).Error
}

func (r *KnowledgeBaseRepository) ListByOwner(ctx context.Context, ownerID int64) ([]knowledge.KnowledgeBase, error) {
	var items []knowledge.KnowledgeBase
	err := r.db.WithContext(ctx).
		Where("owner_id = ? AND deleted_at IS NULL", ownerID).
		Order("id DESC").
		Find(&items).Error
	return items, err
}

func (r *KnowledgeBaseRepository) FindByID(ctx context.Context, ownerID, id int64) (*knowledge.KnowledgeBase, error) {
	var kb knowledge.KnowledgeBase
	err := r.db.WithContext(ctx).
		Where("id = ? AND owner_id = ? AND deleted_at IS NULL", id, ownerID).
		First(&kb).Error
	if err != nil {
		return nil, err
	}
	return &kb, nil
}

func (r *KnowledgeBaseRepository) Update(ctx context.Context, kb *knowledge.KnowledgeBase) error {
	kb.UpdatedAt = time.Now().UTC()
	return r.db.WithContext(ctx).Save(kb).Error
}

func (r *KnowledgeBaseRepository) SoftDelete(ctx context.Context, ownerID, id int64) error {
	now := time.Now().UTC()
	return r.db.WithContext(ctx).Model(&knowledge.KnowledgeBase{}).
		Where("id = ? AND owner_id = ? AND deleted_at IS NULL", id, ownerID).
		Updates(map[string]any{"deleted_at": now, "updated_at": now}).Error
}

func (r *KnowledgeBaseRepository) AdjustCounts(ctx context.Context, ownerID, id int64, documentDelta, chunkDelta int) error {
	return r.db.WithContext(ctx).Model(&knowledge.KnowledgeBase{}).
		Where("id = ? AND owner_id = ? AND deleted_at IS NULL", id, ownerID).
		Updates(map[string]any{
			"document_count": gorm.Expr("GREATEST(document_count + ?, 0)", documentDelta),
			"chunk_count":    gorm.Expr("GREATEST(chunk_count + ?, 0)", chunkDelta),
			"updated_at":     time.Now().UTC(),
		}).Error
}
