package mysql

import (
	"agentcanvas/internal/domain/provider"
	"context"
	"time"

	"gorm.io/gorm"
)

type ProviderRepository struct {
	db *gorm.DB
}

func NewProviderRepository(db *gorm.DB) *ProviderRepository {
	return &ProviderRepository{db: db}
}

func (r *ProviderRepository) Create(ctx context.Context, p *provider.ModelProvider) error {
	now := time.Now().UTC()
	p.CreatedAt = now
	p.UpdatedAt = now
	return r.db.WithContext(ctx).Create(p).Error
}

func (r *ProviderRepository) ListByOwner(ctx context.Context, ownerID int64) ([]provider.ModelProvider, error) {
	var providers []provider.ModelProvider
	err := r.db.WithContext(ctx).
		Where("owner_id = ? AND deleted_at IS NULL", ownerID).
		Order("id DESC").
		Find(&providers).Error
	return providers, err
}

func (r *ProviderRepository) FindByID(ctx context.Context, ownerID, id int64) (*provider.ModelProvider, error) {
	var p provider.ModelProvider
	err := r.db.WithContext(ctx).
		Where("id = ? AND owner_id = ? AND deleted_at IS NULL", id, ownerID).
		First(&p).Error
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func (r *ProviderRepository) Update(ctx context.Context, p *provider.ModelProvider) error {
	p.UpdatedAt = time.Now().UTC()
	return r.db.WithContext(ctx).Save(p).Error
}

func (r *ProviderRepository) SoftDelete(ctx context.Context, ownerID, id int64) error {
	now := time.Now().UTC()
	return r.db.WithContext(ctx).Model(&provider.ModelProvider{}).
		Where("id = ? AND owner_id = ? AND deleted_at IS NULL", id, ownerID).
		Updates(map[string]any{"deleted_at": now, "updated_at": now}).Error
}
