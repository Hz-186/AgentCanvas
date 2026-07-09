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
	return newBaseRepository[knowledge.KnowledgeBase](r.db).create(ctx, kb)
}

func (r *KnowledgeBaseRepository) ListByOwner(ctx context.Context, ownerID int64) ([]knowledge.KnowledgeBase, error) {
	return newBaseRepository[knowledge.KnowledgeBase](r.db).listActiveByOwner(ctx, ownerID, "id DESC")
}

func (r *KnowledgeBaseRepository) FindByID(ctx context.Context, ownerID, id int64) (*knowledge.KnowledgeBase, error) {
	return newBaseRepository[knowledge.KnowledgeBase](r.db).findActiveByID(ctx, ownerID, id)
}

func (r *KnowledgeBaseRepository) Update(ctx context.Context, kb *knowledge.KnowledgeBase) error {
	kb.UpdatedAt = time.Now().UTC()
	return newBaseRepository[knowledge.KnowledgeBase](r.db).save(ctx, kb)
}

func (r *KnowledgeBaseRepository) SoftDelete(ctx context.Context, ownerID, id int64) error {
	return newBaseRepository[knowledge.KnowledgeBase](r.db).softDelete(ctx, &knowledge.KnowledgeBase{}, ownerID, id)
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
