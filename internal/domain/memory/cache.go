package memory

import (
	"context"
	"time"
)

type CacheKey struct {
	Prefix string
	OwnerID int64
	ID      string
}

type CacheEntry struct {
	Memories  []Memory
	ExpiresAt time.Time
}

type Cache interface {
	Get(ctx context.Context, ownerID int64, key string) ([]Memory, bool, error)
	Set(ctx context.Context, ownerID int64, key string, items []Memory, ttl time.Duration) error
	InvalidateOwner(ctx context.Context, ownerID int64) error
	InvalidateItem(ctx context.Context, ownerID, id int64) error
	Close() error
}
