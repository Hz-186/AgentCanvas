package mysql

import (
	"context"
	"time"

	"agentcanvas/internal/domain/knowledge"

	"gorm.io/gorm"
)

type RetrievalLogRepository struct {
	db *gorm.DB
}

func NewRetrievalLogRepository(db *gorm.DB) *RetrievalLogRepository {
	return &RetrievalLogRepository{db: db}
}

func (r *RetrievalLogRepository) Create(ctx context.Context, log *knowledge.RetrievalLog) error {
	log.CreatedAt = time.Now().UTC()
	return r.db.WithContext(ctx).Create(log).Error
}
