package redis

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"agentcanvas/internal/domain/memory"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

const (
	memoryCachePrefix = "mem"
	defaultTTL        = 5 * time.Minute
)

type MemoryCache struct {
	client *redis.Client
}

func NewMemoryCache(client *redis.Client) *MemoryCache {
	return &MemoryCache{client: client}
}

func (c *MemoryCache) itemKey(ownerID int64, epoch, key string) string {
	return fmt.Sprintf("%s:v2:i:%d:%s:%s", memoryCachePrefix, ownerID, epoch, key)
}

func (c *MemoryCache) epochKey(ownerID int64) string {
	return fmt.Sprintf("%s:v2:epoch:%d", memoryCachePrefix, ownerID)
}

func (c *MemoryCache) epoch(ctx context.Context, ownerID int64) (string, error) {
	key := c.epochKey(ownerID)
	epoch, err := c.client.Get(ctx, key).Result()
	if err == nil {
		return epoch, nil
	}
	if err != redis.Nil {
		return "", err
	}
	epoch = uuid.NewString()
	created, err := c.client.SetNX(ctx, key, epoch, 0).Result()
	if err != nil {
		return "", err
	}
	if created {
		return epoch, nil
	}
	return c.client.Get(ctx, key).Result()
}

func (c *MemoryCache) Get(ctx context.Context, ownerID int64, key string) ([]memory.Memory, bool, error) {
	epoch, err := c.epoch(ctx, ownerID)
	if err != nil {
		return nil, false, err
	}
	raw, err := c.client.Get(ctx, c.itemKey(ownerID, epoch, key)).Bytes()
	if err != nil {
		if err == redis.Nil {
			return nil, false, nil
		}
		return nil, false, err
	}
	var items []memory.Memory
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, false, fmt.Errorf("memory cache deserialize: %w", err)
	}
	return items, true, nil
}

func (c *MemoryCache) Set(ctx context.Context, ownerID int64, key string, items []memory.Memory, ttl time.Duration) error {
	if ttl <= 0 {
		ttl = defaultTTL
	}
	data, err := json.Marshal(items)
	if err != nil {
		return fmt.Errorf("memory cache serialize: %w", err)
	}
	epoch, err := c.epoch(ctx, ownerID)
	if err != nil {
		return err
	}
	return c.client.Set(ctx, c.itemKey(ownerID, epoch, key), data, ttl).Err()
}

func (c *MemoryCache) InvalidateOwner(ctx context.Context, ownerID int64) error {
	return c.client.Set(ctx, c.epochKey(ownerID), uuid.NewString(), 0).Err()
}

func (c *MemoryCache) InvalidateItem(ctx context.Context, ownerID, id int64) error {
	return c.InvalidateOwner(ctx, ownerID)
}

func (c *MemoryCache) Close() error {
	return nil
}
