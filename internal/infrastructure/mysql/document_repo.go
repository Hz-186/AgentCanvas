package mysql

import (
	"context"
	"fmt"
	"time"

	"agentcanvas/internal/domain/knowledge"

	"gorm.io/gorm"
)

type DocumentRepository struct {
	db *gorm.DB
}

type GenerationCommitter struct {
	db *gorm.DB
}

func NewGenerationCommitter(db *gorm.DB) *GenerationCommitter {
	return &GenerationCommitter{db: db}
}

func (c *GenerationCommitter) Activate(ctx context.Context, doc *knowledge.Document, cleanup *knowledge.IngestionJob, chunkDelta int) error {
	if c == nil || c.db == nil || doc == nil || cleanup == nil {
		return fmt.Errorf("generation commit is not configured")
	}
	now := time.Now().UTC()
	doc.UpdatedAt = now
	cleanup.Status = knowledge.IngestionJobStatusPending
	cleanup.AttemptCount = 0
	if cleanup.MaxAttempts <= 0 {
		cleanup.MaxAttempts = 5
	}
	cleanup.CreatedAt = now
	cleanup.UpdatedAt = now
	return c.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&knowledge.Document{}).
			Where("id = ? AND owner_id = ? AND deleted_at IS NULL", doc.ID, doc.OwnerID).
			Select("*").Updates(doc)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return gorm.ErrRecordNotFound
		}
		result = tx.Model(&knowledge.KnowledgeBase{}).
			Where("id = ? AND owner_id = ? AND deleted_at IS NULL", doc.KnowledgeBaseID, doc.OwnerID).
			UpdateColumn("chunk_count", gorm.Expr("GREATEST(chunk_count + ?, 0)", chunkDelta))
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return gorm.ErrRecordNotFound
		}
		return tx.Create(cleanup).Error
	})
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

func (r *DocumentRepository) ListByKnowledgeBase(ctx context.Context, ownerID, knowledgeBaseID int64) ([]knowledge.Document, error) {
	var docs []knowledge.Document
	err := r.db.WithContext(ctx).
		Where("owner_id = ? AND knowledge_base_id = ? AND deleted_at IS NULL", ownerID, knowledgeBaseID).
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

func (r *DocumentRepository) SetEnabled(ctx context.Context, ownerID, id int64, enabled bool) error {
	return r.db.WithContext(ctx).Model(&knowledge.Document{}).
		Where("id = ? AND owner_id = ? AND deleted_at IS NULL", id, ownerID).
		Updates(map[string]any{"enabled": enabled, "updated_at": time.Now().UTC()}).Error
}

func (r *DocumentRepository) SoftDelete(ctx context.Context, ownerID, id int64) error {
	now := time.Now().UTC()
	return r.db.WithContext(ctx).Model(&knowledge.Document{}).
		Where("id = ? AND owner_id = ? AND deleted_at IS NULL", id, ownerID).
		Updates(map[string]any{"deleted_at": now, "updated_at": now}).Error
}

func (r *DocumentRepository) SoftDeleteByKnowledgeBase(ctx context.Context, ownerID, knowledgeBaseID int64) error {
	now := time.Now().UTC()
	return r.db.WithContext(ctx).Model(&knowledge.Document{}).
		Where("owner_id = ? AND knowledge_base_id = ? AND deleted_at IS NULL", ownerID, knowledgeBaseID).
		Updates(map[string]any{"deleted_at": now, "updated_at": now}).Error
}
