package mysql

import (
	"context"
	"time"

	"agentcanvas/internal/domain/usage"

	"gorm.io/gorm"
)

type UsageRepository struct {
	db *gorm.DB
}

func NewUsageRepository(db *gorm.DB) *UsageRepository {
	return &UsageRepository{db: db}
}

func (r *UsageRepository) Create(ctx context.Context, log *usage.ModelUsageLog) error {
	log.CreatedAt = time.Now().UTC()
	if log.UsageType == "" {
		log.UsageType = usage.TypeChat
	}
	return r.db.WithContext(ctx).Create(log).Error
}
