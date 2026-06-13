package redis

import (
	"context"

	"agentcanvas/internal/pkg/config"

	"github.com/redis/go-redis/v9"
)

func New(cfg config.RedisConfig) *redis.Client {
	return redis.NewClient(&redis.Options{
		Addr:     cfg.Addr,
		Password: cfg.Password,
		DB:       cfg.DB,
	})
}

func Ping(ctx context.Context, client *redis.Client) error {
	return client.Ping(ctx).Err()
}
