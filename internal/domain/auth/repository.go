package auth

import (
	"context"
	"time"
)

type OAuthRepository interface {
	Create(ctx context.Context, account *OAuthAccount) error
	FindByProviderUserID(ctx context.Context, provider, providerUserID string) (*OAuthAccount, error)
}

type SessionRepository interface {
	Create(ctx context.Context, session *Session) error
	FindActiveByRefreshHash(ctx context.Context, refreshHash string, now time.Time) (*Session, error)
	RevokeByRefreshHash(ctx context.Context, refreshHash string, revokedAt time.Time) error
}

type APITokenRepository interface {
	Create(ctx context.Context, token *APIToken) error
	ListByOwner(ctx context.Context, ownerID int64) ([]APIToken, error)
	FindActiveByHash(ctx context.Context, tokenHash string, now time.Time) (*APIToken, error)
	RevokeByID(ctx context.Context, ownerID, id int64, revokedAt time.Time) error
}
