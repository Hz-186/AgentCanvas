package mysql

import (
	"context"
	"time"

	"agentcanvas/internal/domain/resource"

	"gorm.io/gorm"
)

type ResourceInvalidationStore struct {
	db *gorm.DB
}

func NewResourceInvalidationStore(db *gorm.DB) *ResourceInvalidationStore {
	return &ResourceInvalidationStore{db: db}
}

func (s *ResourceInvalidationStore) Enqueue(ctx context.Context, ownerID int64, kind resource.Kind, cause error) error {
	now := time.Now().UTC()
	message := ""
	if cause != nil {
		message = cause.Error()
	}
	return s.db.WithContext(ctx).Create(&resource.InvalidationEvent{
		OwnerID: ownerID, Kind: kind, NextRetryAt: now, LastError: message, CreatedAt: now, UpdatedAt: now,
	}).Error
}

func (s *ResourceInvalidationStore) ListPending(ctx context.Context, limit int) ([]resource.InvalidationEvent, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	var events []resource.InvalidationEvent
	err := s.db.WithContext(ctx).
		Where("processed_at IS NULL AND next_retry_at <= ?", time.Now().UTC()).
		Order("id ASC").Limit(limit).Find(&events).Error
	return events, err
}

func (s *ResourceInvalidationStore) MarkProcessed(ctx context.Context, id int64) error {
	now := time.Now().UTC()
	return s.db.WithContext(ctx).Model(&resource.InvalidationEvent{}).Where("id = ? AND processed_at IS NULL", id).
		Updates(map[string]any{"processed_at": now, "updated_at": now, "last_error": ""}).Error
}

func (s *ResourceInvalidationStore) MarkFailed(ctx context.Context, id int64, attempts int, nextRetryAt time.Time, cause error) error {
	message := ""
	if cause != nil {
		message = cause.Error()
	}
	return s.db.WithContext(ctx).Model(&resource.InvalidationEvent{}).Where("id = ? AND processed_at IS NULL", id).
		Updates(map[string]any{"attempts": attempts, "next_retry_at": nextRetryAt, "last_error": message, "updated_at": time.Now().UTC()}).Error
}

func (s *ResourceInvalidationStore) DeleteProcessedBefore(ctx context.Context, cutoff time.Time, limit int) (int64, error) {
	if limit <= 0 || limit > 5000 {
		limit = 1000
	}
	var ids []int64
	if err := s.db.WithContext(ctx).Model(&resource.InvalidationEvent{}).
		Where("processed_at IS NOT NULL AND processed_at < ?", cutoff).
		Order("id ASC").Limit(limit).Pluck("id", &ids).Error; err != nil || len(ids) == 0 {
		return 0, err
	}
	result := s.db.WithContext(ctx).Where("id IN ?", ids).Delete(&resource.InvalidationEvent{})
	return result.RowsAffected, result.Error
}
