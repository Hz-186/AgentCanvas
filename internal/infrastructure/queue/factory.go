package queue

import (
	"context"
	"fmt"
	"time"

	"agentcanvas/internal/pkg/config"

	goredis "github.com/redis/go-redis/v9"
)

func NewConfiguredJobQueue(ctx context.Context, cfg *config.Config, redisClient *goredis.Client) (JobQueue, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config is required")
	}
	switch cfg.Queue.Backend {
	case "", "mysql":
		return nil, nil
	case "redis_stream":
		if redisClient == nil {
			return nil, fmt.Errorf("redis client is required for redis_stream queue")
		}
		return NewRedisStreamQueue(redisClient, cfg.Queue.RedisStream, cfg.Queue.RedisGroup, cfg.Queue.RedisConsumer), nil
	case "nats":
		queue, err := NewNATSJetStreamQueueFromConfig(cfg.NATS)
		if err != nil {
			return nil, err
		}
		if err := queue.ensure(ctx); err != nil {
			_ = queue.Close()
			return nil, err
		}
		if queue.AckWait <= 0 {
			queue.AckWait = time.Minute
		}
		return queue, nil
	default:
		return nil, fmt.Errorf("unsupported queue backend: %s", cfg.Queue.Backend)
	}
}
