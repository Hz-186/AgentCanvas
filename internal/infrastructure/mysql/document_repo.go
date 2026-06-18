package mysql

import (
	"context"
	"time"

	"agentcanvas/internal/domain/knowledge"

	"gorm.io/gorm"
)

type DocumentRepository struct {
	db *gorm.DB
}

func NewDocumentRepository(db *gorm.DB) *DocumentRepository {
	return &DocumentRepository{db: db}
}

func (r *DocumentRepository) Create(ctx context.Context, doc *knowledge.Document) error {
	now := time.Now().UTC()
	doc.CreatedAt = now
	doc.UpdatedAt = now
	return r.db.WithContext(ctx).Create(doc).Error
}

func (r *DocumentRepository) ListByKnowledgeBase(ctx context.Context, ownerID, kbID int64) ([]knowledge.Document, error) {
	var docs []knowledge.Document
	err := r.db.WithContext(ctx).
		Where("owner_id = ? AND kb_id = ? AND deleted_at IS NULL", ownerID, kbID).
		Order("id DESC").
		Find(&docs).Error
	return docs, err
}

func (r *DocumentRepository) FindByID(ctx context.Context, ownerID, id int64) (*knowledge.Document, error) {
	var doc knowledge.Document
	err := r.db.WithContext(ctx).
		Where("id = ? AND owner_id = ? AND deleted_at IS NULL", id, ownerID).
		First(&doc).Error
	if err != nil {
		return nil, err
	}
	return &doc, nil
}

func (r *DocumentRepository) Update(ctx context.Context, doc *knowledge.Document) error {
	doc.UpdatedAt = time.Now().UTC()
	return r.db.WithContext(ctx).Save(doc).Error
}

func (r *DocumentRepository) SoftDelete(ctx context.Context, ownerID, id int64) error {
	now := time.Now().UTC()
	return r.db.WithContext(ctx).Model(&knowledge.Document{}).
		Where("id = ? AND owner_id = ? AND deleted_at IS NULL", id, ownerID).
		Updates(map[string]any{"deleted_at": now, "updated_at": now}).Error
}

func (r *DocumentRepository) SoftDeleteByKnowledgeBase(ctx context.Context, ownerID, kbID int64) error {
	now := time.Now().UTC()
	return r.db.WithContext(ctx).Model(&knowledge.Document{}).
		Where("owner_id = ? AND kb_id = ? AND deleted_at IS NULL", ownerID, kbID).
		Updates(map[string]any{"deleted_at": now, "updated_at": now}).Error
}
