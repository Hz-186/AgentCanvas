package mysql

import (
	"agentcanvas/internal/domain/auth"
	"context"
	"time"

	"gorm.io/gorm"
)

type APITokenRepository struct {
	db *gorm.DB
}

func NewAPITokenRepository(db *gorm.DB) *APITokenRepository {
	return &APITokenRepository{db: db}
}

func (r *APITokenRepository) Create(ctx context.Context, token *auth.APIToken) error {
	now := time.Now().UTC()
	token.CreatedAt = now
	token.UpdatedAt = now
	return r.db.WithContext(ctx).Create(token).Error
}

func (r *APITokenRepository) ListByOwner(ctx context.Context, ownerID int64) ([]auth.APIToken, error) {
	var tokens []auth.APIToken
	err := r.db.WithContext(ctx).
		Where("owner_id = ?", ownerID).
		Order("id DESC").
		Find(&tokens).Error
	return tokens, err
}

func (r *APITokenRepository) FindActiveByHash(ctx context.Context, tokenHash string, now time.Time) (*auth.APIToken, error) {
	var token auth.APIToken
	err := r.db.WithContext(ctx).
		Where("token_hash = ? AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at > ?)", tokenHash, now).
		First(&token).Error
	if err != nil {
		return nil, err
	}
	return &token, nil
}

func (r *APITokenRepository) RevokeByID(ctx context.Context, ownerID, id int64, revokedAt time.Time) error {
	return r.db.WithContext(ctx).Model(&auth.APIToken{}).
		Where("id = ? AND owner_id = ? AND revoked_at IS NULL", id, ownerID).
		Updates(map[string]any{"revoked_at": revokedAt, "updated_at": time.Now().UTC()}).Error
}
