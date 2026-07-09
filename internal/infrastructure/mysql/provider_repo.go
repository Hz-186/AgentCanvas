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
	return newBaseRepository[provider.ModelProvider](r.db).create(ctx, p)
}

func (r *ProviderRepository) ListByOwner(ctx context.Context, ownerID int64) ([]provider.ModelProvider, error) {
	return newBaseRepository[provider.ModelProvider](r.db).listActiveByOwner(ctx, ownerID, "id DESC")
}

func (r *ProviderRepository) FindByID(ctx context.Context, ownerID, id int64) (*provider.ModelProvider, error) {
	return newBaseRepository[provider.ModelProvider](r.db).findActiveByID(ctx, ownerID, id)
}

func (r *ProviderRepository) Update(ctx context.Context, p *provider.ModelProvider) error {
	p.UpdatedAt = time.Now().UTC()
	return newBaseRepository[provider.ModelProvider](r.db).save(ctx, p)
}

func (r *ProviderRepository) SoftDelete(ctx context.Context, ownerID, id int64) error {
	return newBaseRepository[provider.ModelProvider](r.db).softDelete(ctx, &provider.ModelProvider{}, ownerID, id)
}
