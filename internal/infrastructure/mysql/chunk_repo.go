package mysql

import (
	"agentcanvas/internal/domain/knowledge"
	"context"
	"time"

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
	for i := range chunks {
		if chunks[i].CreatedAt.IsZero() {
			chunks[i].CreatedAt = time.Now().UTC()
		}
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

func (r *ChunkRepository) DeleteByDocument(ctx context.Context, ownerID, documentID int64) error {
	return r.db.WithContext(ctx).
		Where("owner_id = ? AND document_id = ?", ownerID, documentID).
		Delete(&knowledge.DocumentChunk{}).Error
}

func (r *ChunkRepository) DeleteByKnowledgeBase(ctx context.Context, ownerID, knowledgeBaseID int64) error {
	return r.db.WithContext(ctx).
		Where("owner_id = ? AND knowledge_base_id = ?", ownerID, knowledgeBaseID).
		Delete(&knowledge.DocumentChunk{}).Error
}

func (r *ChunkRepository) DeleteInactiveGenerations(ctx context.Context, ownerID, documentID int64, activeGeneration string) error {
	return r.db.WithContext(ctx).
		Where("owner_id = ? AND document_id = ? AND (generation_id <> ? OR generation_id IS NULL)", ownerID, documentID, activeGeneration).
		Delete(&knowledge.DocumentChunk{}).Error
}
