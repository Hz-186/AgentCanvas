package redis

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"agentcanvas/internal/domain/memory"

	"github.com/redis/go-redis/v9"
)

const (
	memoryCachePrefix = "mem"
	defaultTTL        = 5 * time.Minute
	itemTTL           = 30 * time.Minute
	ownerKeySetTTL    = 1 * time.Hour
)

type MemoryCache struct {
	client *redis.Client
}

func NewMemoryCache(client *redis.Client) *MemoryCache {
	return &MemoryCache{client: client}
}

func (c *MemoryCache) itemKey(ownerID int64, key string) string {
	return fmt.Sprintf("%s:i:%d:%s", memoryCachePrefix, ownerID, key)
}

func (c *MemoryCache) ownerKeySet(ownerID int64) string {
	return fmt.Sprintf("%s:owner:%d:keys", memoryCachePrefix, ownerID)
}

func (c *MemoryCache) Get(ctx context.Context, ownerID int64, key string) ([]memory.Memory, bool, error) {
	raw, err := c.client.Get(ctx, c.itemKey(ownerID, key)).Bytes()
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
	cacheKey := c.itemKey(ownerID, key)
	pipe := c.client.Pipeline()
	pipe.Set(ctx, cacheKey, data, ttl)
	pipe.SAdd(ctx, c.ownerKeySet(ownerID), cacheKey)
	pipe.Expire(ctx, c.ownerKeySet(ownerID), ownerKeySetTTL)
	_, pipeErr := pipe.Exec(ctx)
	return pipeErr
}

func (c *MemoryCache) InvalidateOwner(ctx context.Context, ownerID int64) error {
	setKey := c.ownerKeySet(ownerID)
	keys, err := c.client.SMembers(ctx, setKey).Result()
	if err != nil {
		return err
	}
	if len(keys) == 0 {
		return nil
	}
	allKeys := append(keys, setKey)
	return c.client.Del(ctx, allKeys...).Err()
}

func (c *MemoryCache) InvalidateItem(ctx context.Context, ownerID, id int64) error {
	return c.InvalidateOwner(ctx, ownerID)
}

func (c *MemoryCache) Close() error {
	return nil
}
