package mysql

import (
	"agentcanvas/internal/domain/auth"
	"context"
	"time"

	"gorm.io/gorm"
)

type OAuthRepository struct {
	db *gorm.DB
}

func NewOAuthRepository(db *gorm.DB) *OAuthRepository {
	return &OAuthRepository{db: db}
}

func (r *OAuthRepository) Create(ctx context.Context, account *auth.OAuthAccount) error {
	now := time.Now().UTC()
	account.CreatedAt = now
	account.UpdatedAt = now
	return r.db.WithContext(ctx).Create(account).Error
}

func (r *OAuthRepository) FindByProviderUserID(ctx context.Context, provider, providerUserID string) (*auth.OAuthAccount, error) {
	var account auth.OAuthAccount
	err := r.db.WithContext(ctx).Where("provider = ? AND provider_user_id = ?", provider, providerUserID).First(&account).Error
	if err != nil {
		return nil, err
	}
	return &account, nil
}

func (r *OAuthRepository) DeleteByProviderUserID(ctx context.Context, provider, providerUserID string) error {
	return r.db.WithContext(ctx).
		Where("provider = ? AND provider_user_id = ?", provider, providerUserID).
		Delete(&auth.OAuthAccount{}).Error
}
