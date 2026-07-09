package redis

import (
	"context"
	"fmt"

	"github.com/redis/go-redis/v9"
)

func ProbeRediSearch(ctx context.Context, client *redis.Client) error {
	if client == nil {
		return fmt.Errorf("redis client is not configured")
	}
	if err := client.Do(ctx, "FT._LIST").Err(); err != nil {
		return fmt.Errorf("redisearch unavailable: %w", err)
	}
	return nil
}
