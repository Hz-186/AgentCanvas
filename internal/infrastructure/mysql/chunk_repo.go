package mysql

import (
	"context"
	"time"

	"agentcanvas/internal/domain/knowledge"

	"gorm.io/gorm"
)

type ChunkRepository struct {
	db *gorm.DB
}

func NewChunkRepository(db *gorm.DB) *ChunkRepository {
	return &ChunkRepository{db: db}
}

func (r *ChunkRepository) CreateBatch(ctx context.Context, chunks []knowledge.DocumentChunk) error {
	if len(chunks) == 0 {
		return nil
	}
	now := time.Now().UTC()
	for i := range chunks {
		chunks[i].CreatedAt = now
		chunks[i].UpdatedAt = now
	}
	return r.db.WithContext(ctx).CreateInBatches(&chunks, 100).Error
}

func (r *ChunkRepository) ListByDocument(ctx context.Context, ownerID, documentID int64) ([]knowledge.DocumentChunk, error) {
	var chunks []knowledge.DocumentChunk
	err := r.db.WithContext(ctx).
		Where("owner_id = ? AND document_id = ?", ownerID, documentID).
		Order("chunk_index ASC").
		Find(&chunks).Error
	return chunks, err
}

func (r *ChunkRepository) UpdateIndexRefs(ctx context.Context, chunks []knowledge.DocumentChunk) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now().UTC()
		for _, chunk := range chunks {
			if err := tx.Model(&knowledge.DocumentChunk{}).
				Where("id = ? AND owner_id = ?", chunk.ID, chunk.OwnerID).
				Updates(map[string]any{
					"es_index":   chunk.ESIndex,
					"es_doc_id":  chunk.ESDocID,
					"updated_at": now,
				}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func (r *ChunkRepository) DeleteByDocument(ctx context.Context, ownerID, documentID int64) error {
	return r.db.WithContext(ctx).
		Where("owner_id = ? AND document_id = ?", ownerID, documentID).
		Delete(&knowledge.DocumentChunk{}).Error
}

func (r *ChunkRepository) DeleteByKnowledgeBase(ctx context.Context, ownerID, kbID int64) error {
	return r.db.WithContext(ctx).
		Where("owner_id = ? AND kb_id = ?", ownerID, kbID).
		Delete(&knowledge.DocumentChunk{}).Error
}

func (r *ChunkRepository) DeleteInactiveGenerations(ctx context.Context, ownerID, documentID int64, activeGeneration string) error {
	return r.db.WithContext(ctx).
		Where("owner_id = ? AND document_id = ? AND (generation <> ? OR generation IS NULL)", ownerID, documentID, activeGeneration).
		Delete(&knowledge.DocumentChunk{}).Error
}
