package mysql

import (
	"agentcanvas/internal/domain/audit"
	"context"
	"time"

	"gorm.io/gorm"
)

type AuditRepository struct {
	db *gorm.DB
}

func NewAuditRepository(db *gorm.DB) *AuditRepository {
	return &AuditRepository{db: db}
}

func (r *AuditRepository) Create(ctx context.Context, log *audit.Log) error {
	if log.CreatedAt.IsZero() {
		log.CreatedAt = time.Now().UTC()
	}
	return r.db.WithContext(ctx).Create(log).Error
}

func (r *AuditRepository) ListByOwner(ctx context.Context, ownerID int64, limit, offset int) ([]audit.Log, error) {
	var logs []audit.Log
	err := r.db.WithContext(ctx).
		Where("owner_id = ?", ownerID).
		Order("id DESC").
		Limit(limit).
		Offset(offset).
		Find(&logs).Error
	return logs, err
}
