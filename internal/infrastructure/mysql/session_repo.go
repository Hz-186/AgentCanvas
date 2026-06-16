package mysql

import (
	"agentcanvas/internal/domain/auth"
	"context"
	"time"

	"gorm.io/gorm"
)

type SessionRepository struct {
	db *gorm.DB
}

func NewSessionRepository(db *gorm.DB) *SessionRepository {
	return &SessionRepository{db: db}
}

func (r *SessionRepository) Create(ctx context.Context, session *auth.Session) error {
	now := time.Now().UTC()
	session.CreatedAt = now
	session.UpdatedAt = now
	return r.db.WithContext(ctx).Create(session).Error
}

func (r *SessionRepository) FindActiveByRefreshHash(ctx context.Context, refreshHash string, now time.Time) (*auth.Session, error) {
	var session auth.Session
	err := r.db.WithContext(ctx).
		Where("refresh_token_hash = ? AND revoked_at IS NULL AND expires_at > ?", refreshHash, now).
		First(&session).Error
	if err != nil {
		return nil, err
	}
	return &session, nil
}

func (r *SessionRepository) RevokeByRefreshHash(ctx context.Context, refreshHash string, revokedAt time.Time) error {
	return r.db.WithContext(ctx).Model(&auth.Session{}).
		Where("refresh_token_hash = ? AND revoked_at IS NULL", refreshHash).
		Updates(map[string]any{"revoked_at": revokedAt, "updated_at": time.Now().UTC()}).Error
}
