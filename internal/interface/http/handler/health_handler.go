package handler

import (
	"context"
	"net/http"
	"time"

	esinfra "agentcanvas/internal/infrastructure/elasticsearch"
	minioinfra "agentcanvas/internal/infrastructure/minio"
	mysqlinfra "agentcanvas/internal/infrastructure/mysql"
	redisinfra "agentcanvas/internal/infrastructure/redis"
	"agentcanvas/internal/observability"
	"agentcanvas/internal/pkg/errors"
	"agentcanvas/internal/pkg/response"

	es "github.com/elastic/go-elasticsearch/v8"
	"github.com/gin-gonic/gin"
	"github.com/minio/minio-go/v7"
	redis "github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

type HealthHandler struct {
	db          *gorm.DB
	redis       *redis.Client
	minio       *minio.Client
	es          *es.Client
	minioBucket string
}

func NewHealthHandler(db *gorm.DB, redisClient *redis.Client, minioClient *minio.Client, esClient *es.Client, minioBucket string) *HealthHandler {
	return &HealthHandler{
		db:          db,
		redis:       redisClient,
		minio:       minioClient,
		es:          esClient,
		minioBucket: minioBucket,
	}
}

func (h *HealthHandler) Health(c *gin.Context) {
	response.OK(c, gin.H{
		"status": "healthy",
	})
}

func (h *HealthHandler) MySQL(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
	defer cancel()

	if err := mysqlinfra.Ping(ctx, h.db); err != nil {
		response.Error(c, http.StatusServiceUnavailable, errors.CodeDependencyUnavailable, err.Error())
		return
	}

	response.OK(c, gin.H{"component": "mysql", "status": "healthy"})
}

func (h *HealthHandler) Redis(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
	defer cancel()

	if err := redisinfra.Ping(ctx, h.redis); err != nil {
		response.Error(c, http.StatusServiceUnavailable, errors.CodeDependencyUnavailable, err.Error())
		return
	}

	response.OK(c, gin.H{"component": "redis", "status": "healthy"})
}

func (h *HealthHandler) MinIO(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
	defer cancel()

	if err := minioinfra.Ping(ctx, h.minio, h.minioBucket); err != nil {
		response.Error(c, http.StatusServiceUnavailable, errors.CodeDependencyUnavailable, err.Error())
		return
	}

	response.OK(c, gin.H{"component": "minio", "status": "healthy"})
}

func (h *HealthHandler) Elasticsearch(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
	defer cancel()

	if err := esinfra.Ping(ctx, h.es); err != nil {
		response.Error(c, http.StatusServiceUnavailable, errors.CodeDependencyUnavailable, err.Error())
		return
	}

	response.OK(c, gin.H{"component": "elasticsearch", "status": "healthy"})
}

func (h *HealthHandler) RuleSystem(c *gin.Context) {
	const (
		compileBacklogAlertThreshold = int64(100)
		compileAgeAlertSeconds       = int64(300)
	)
	type databaseMetrics struct {
		QueuedJobs             int64   `json:"queued_jobs"`
		CompilingJobs          int64   `json:"compiling_jobs"`
		FailedJobs             int64   `json:"failed_jobs"`
		RetryAttempts          int64   `json:"retry_attempts"`
		PromptTokens           int64   `json:"prompt_tokens"`
		CompletionTokens       int64   `json:"completion_tokens"`
		AverageCompileMS       float64 `json:"average_compile_ms"`
		OldestQueuedSeconds    int64   `json:"oldest_queued_seconds"`
		ReviewRequiredRuleSets int64   `json:"review_required_rule_sets"`
		OldestReviewSeconds    int64   `json:"oldest_review_seconds"`
		PublishedRuleSets      int64   `json:"published_rule_sets"`
		RollbackRuleSets       int64   `json:"rollback_rule_sets"`
	}
	var metrics databaseMetrics
	jobQuery := `SELECT
		COALESCE(SUM(status = 'queued'), 0) AS queued_jobs,
		COALESCE(SUM(status = 'compiling'), 0) AS compiling_jobs,
		COALESCE(SUM(status = 'failed'), 0) AS failed_jobs,
		COALESCE(SUM(GREATEST(attempts - 1, 0)), 0) AS retry_attempts,
		COALESCE(SUM(prompt_tokens), 0) AS prompt_tokens,
		COALESCE(SUM(completion_tokens), 0) AS completion_tokens,
		COALESCE(AVG(CASE WHEN finished_at IS NOT NULL AND started_at IS NOT NULL THEN TIMESTAMPDIFF(MICROSECOND, started_at, finished_at) / 1000 END), 0) AS average_compile_ms,
		COALESCE(TIMESTAMPDIFF(SECOND, MIN(CASE WHEN status = 'queued' THEN available_at END), UTC_TIMESTAMP()), 0) AS oldest_queued_seconds
		FROM workflow_rule_compile_jobs`
	if err := h.db.WithContext(c.Request.Context()).Raw(jobQuery).Scan(&metrics).Error; err != nil {
		response.Error(c, http.StatusServiceUnavailable, errors.CodeDependencyUnavailable, err.Error())
		return
	}
	ruleSetQuery := `SELECT
		COALESCE(SUM(status = 'review_required'), 0) AS review_required_rule_sets,
		COALESCE(TIMESTAMPDIFF(SECOND, MIN(CASE WHEN status = 'review_required' THEN updated_at END), UTC_TIMESTAMP()), 0) AS oldest_review_seconds,
		COALESCE(SUM(status = 'published'), 0) AS published_rule_sets,
		COALESCE(SUM(rollback_of_rule_set_id IS NOT NULL), 0) AS rollback_rule_sets
		FROM workflow_rule_sets`
	if err := h.db.WithContext(c.Request.Context()).Raw(ruleSetQuery).Scan(&metrics).Error; err != nil {
		response.Error(c, http.StatusServiceUnavailable, errors.CodeDependencyUnavailable, err.Error())
		return
	}
	process := observability.RuleSystemMetrics.Snapshot()
	alerts := gin.H{
		"compile_backlog":       metrics.QueuedJobs >= compileBacklogAlertThreshold,
		"compile_queue_stalled": metrics.OldestQueuedSeconds >= compileAgeAlertSeconds,
		"mandatory_overflow":    process.MandatoryOverflow > 0,
		"snapshot_integrity":    process.SnapshotIntegrityFail > 0,
	}
	response.OK(c, gin.H{"component": "rule_system", "database": metrics, "process": process, "alerts": alerts})
}
