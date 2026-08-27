package health

import (
	"context"
	"fmt"

	esinfra "agentcanvas/internal/infrastructure/elasticsearch"
	minioinfra "agentcanvas/internal/infrastructure/minio"
	mysqlinfra "agentcanvas/internal/infrastructure/mysql"
	redisinfra "agentcanvas/internal/infrastructure/redis"
	"agentcanvas/internal/observability"

	es "github.com/elastic/go-elasticsearch/v8"
	"github.com/minio/minio-go/v7"
	redis "github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

type Checker struct {
	db          *gorm.DB
	redis       *redis.Client
	minio       *minio.Client
	es          *es.Client
	minioBucket string
}

func NewChecker(db *gorm.DB, redisClient *redis.Client, minioClient *minio.Client, esClient *es.Client, minioBucket string) *Checker {
	return &Checker{db: db, redis: redisClient, minio: minioClient, es: esClient, minioBucket: minioBucket}
}

func (c *Checker) Check(ctx context.Context, component string) error {
	switch component {
	case "mysql":
		return mysqlinfra.Ping(ctx, c.db)
	case "redis":
		return redisinfra.Ping(ctx, c.redis)
	case "minio":
		return minioinfra.Ping(ctx, c.minio, c.minioBucket)
	case "elasticsearch":
		return esinfra.Ping(ctx, c.es)
	default:
		return fmt.Errorf("unknown health component %q", component)
	}
}

func (c *Checker) ContextSystem(ctx context.Context) (map[string]any, error) {
	type databaseMetrics struct {
		PendingOutbox        int64 `json:"pending_outbox"`
		ProcessingOutbox     int64 `json:"processing_outbox"`
		DeadLetterOutbox     int64 `json:"dead_letter_outbox"`
		RetryAttempts        int64 `json:"retry_attempts"`
		OldestPendingSeconds int64 `json:"oldest_pending_seconds"`
		StaleLeases          int64 `json:"stale_leases"`
		Compactions          int64 `json:"compactions"`
		FallbackCompactions  int64 `json:"fallback_compactions"`
		FailedCompactions    int64 `json:"failed_compactions"`
	}
	var metrics databaseMetrics
	queries := []string{
		`SELECT COALESCE(SUM(status = 'pending'), 0) AS pending_outbox, COALESCE(SUM(status = 'processing'), 0) AS processing_outbox,
		COALESCE(SUM(status = 'dead_letter'), 0) AS dead_letter_outbox, COALESCE(SUM(attempt_count), 0) AS retry_attempts,
		COALESCE(TIMESTAMPDIFF(SECOND, MIN(CASE WHEN status = 'pending' THEN created_at END), UTC_TIMESTAMP()), 0) AS oldest_pending_seconds,
		COALESCE(SUM(status = 'processing' AND lease_expires_at < UTC_TIMESTAMP()), 0) AS stale_leases FROM context_resource_index_outbox`,
		`SELECT COUNT(*) AS compactions, COALESCE(SUM(status = 'fallback'), 0) AS fallback_compactions,
		COALESCE(SUM(status = 'failed'), 0) AS failed_compactions FROM conversation_compactions`,
	}
	for _, query := range queries {
		if err := c.db.WithContext(ctx).Raw(query).Scan(&metrics).Error; err != nil {
			return nil, err
		}
	}
	process := observability.ContextSystemMetrics.Snapshot()
	return map[string]any{
		"component": "context_system", "database": metrics, "process": process,
		"alerts": map[string]any{
			"outbox_backlog": metrics.PendingOutbox >= 100, "outbox_stalled": metrics.OldestPendingSeconds >= 300,
			"stale_leases": metrics.StaleLeases > 0, "dead_letter": metrics.DeadLetterOutbox > 0,
			"context_overflow": process.ContextOverflow > 0,
		},
	}, nil
}

func (c *Checker) MemorySystem(context.Context) (map[string]any, error) {
	process := observability.MemoryRuntimeMetrics.Snapshot()
	return map[string]any{
		"component": "memory_system", "process": process,
		"alerts": map[string]any{
			"dream_failures":          process["dream_failures"] > 0,
			"scheduler_failures":      process["scheduler_failures"] > 0,
			"scheduler_lock_failures": process["scheduler_lock_failures"] > 0,
		},
	}, nil
}
