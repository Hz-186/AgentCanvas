package mysql

import (
	"context"
	"time"

	"gorm.io/gorm"
)

type baseRepository[T any] struct {
	db *gorm.DB
}

func newBaseRepository[T any](db *gorm.DB) baseRepository[T] {
	return baseRepository[T]{db: db}
}

func (r baseRepository[T]) create(ctx context.Context, item *T) error {
	return r.db.WithContext(ctx).Create(item).Error
}

func (r baseRepository[T]) save(ctx context.Context, item *T) error {
	return r.db.WithContext(ctx).Save(item).Error
}

func (r baseRepository[T]) findActiveByID(ctx context.Context, ownerID, id int64) (*T, error) {
	var item T
	err := r.db.WithContext(ctx).Where("id = ? AND owner_id = ? AND deleted_at IS NULL", id, ownerID).First(&item).Error
	if err != nil {
		return nil, err
	}
	return &item, nil
}

func (r baseRepository[T]) listActiveByOwner(ctx context.Context, ownerID int64, order string) ([]T, error) {
	var items []T
	err := r.db.WithContext(ctx).Where("owner_id = ? AND deleted_at IS NULL", ownerID).Order(order).Find(&items).Error
	return items, err
}

func (r baseRepository[T]) softDelete(ctx context.Context, model *T, ownerID, id int64) error {
	now := time.Now().UTC()
	return r.db.WithContext(ctx).Model(model).
		Where("id = ? AND owner_id = ? AND deleted_at IS NULL", id, ownerID).
		Updates(map[string]any{"deleted_at": now, "updated_at": now}).Error
}
