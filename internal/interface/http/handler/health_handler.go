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
	type databaseMetrics struct {
		PublishedRuleSets  int64 `json:"published_rule_sets"`
		RollbackRuleSets   int64 `json:"rollback_rule_sets"`
		LegacyRuleProfiles int64 `json:"legacy_rule_profiles_remaining"`
	}
	var metrics databaseMetrics
	ruleSetQuery := `SELECT
		COALESCE(SUM(status = 'published'), 0) AS published_rule_sets,
		COALESCE(SUM(rollback_of_rule_set_id IS NOT NULL), 0) AS rollback_rule_sets
		FROM workflow_rule_sets`
	if err := h.db.WithContext(c.Request.Context()).Raw(ruleSetQuery).Scan(&metrics).Error; err != nil {
		response.Error(c, http.StatusServiceUnavailable, errors.CodeDependencyUnavailable, err.Error())
		return
	}
	legacyProfileQuery := `SELECT COUNT(*) AS legacy_rule_profiles
		FROM workflow_profiles
		WHERE active_rule_set_id IS NULL AND deleted_at IS NULL
		AND JSON_LENGTH(JSON_EXTRACT(context_policy_json, '$.rules')) > 0`
	if err := h.db.WithContext(c.Request.Context()).Raw(legacyProfileQuery).Scan(&metrics).Error; err != nil {
		response.Error(c, http.StatusServiceUnavailable, errors.CodeDependencyUnavailable, err.Error())
		return
	}
	process := observability.RuleSystemMetrics.Snapshot()
	alerts := gin.H{
		"mandatory_overflow":   process.MandatoryOverflow > 0,
		"snapshot_integrity":   process.SnapshotIntegrityFail > 0,
		"legacy_rule_profiles": metrics.LegacyRuleProfiles > 0,
	}
	response.OK(c, gin.H{"component": "rule_system", "database": metrics, "process": process, "alerts": alerts})
}

func (h *HealthHandler) ReflectionSystem(c *gin.Context) {
	const (
		jobBacklogAlertThreshold = int64(100)
		jobAgeAlertSeconds       = int64(300)
	)
	type databaseMetrics struct {
		PendingJobs          int64 `json:"pending_jobs"`
		RunningJobs          int64 `json:"running_jobs"`
		FailedJobs           int64 `json:"failed_jobs"`
		RetryAttempts        int64 `json:"retry_attempts"`
		OldestPendingSeconds int64 `json:"oldest_pending_seconds"`
		ActiveReflections    int64 `json:"active_reflections"`
		ValidatedReflections int64 `json:"validated_reflections"`
		DisputedReflections  int64 `json:"disputed_reflections"`
		HelpfulFeedback      int64 `json:"helpful_feedback"`
		HarmfulFeedback      int64 `json:"harmful_feedback"`
		OutboxPending        int64 `json:"outbox_pending"`
		OldestOutboxSeconds  int64 `json:"oldest_outbox_seconds"`
		StaleRunningJobs     int64 `json:"stale_running_jobs"`
		DLQJobs              int64 `json:"dlq_jobs"`
	}
	var metrics databaseMetrics
	jobQuery := `SELECT
		COALESCE(SUM(status = 'pending'), 0) AS pending_jobs,
		COALESCE(SUM(status = 'running'), 0) AS running_jobs,
		COALESCE(SUM(status = 'failed'), 0) AS failed_jobs,
		COALESCE(SUM(GREATEST(attempt_count - 1, 0)), 0) AS retry_attempts,
		COALESCE(TIMESTAMPDIFF(SECOND, MIN(CASE WHEN status = 'pending' THEN created_at END), UTC_TIMESTAMP()), 0) AS oldest_pending_seconds,
		COALESCE(SUM(status = 'running' AND lease_expires_at IS NOT NULL AND lease_expires_at < UTC_TIMESTAMP()), 0) AS stale_running_jobs,
		COALESCE(SUM(status = 'failed' AND failure_type IN ('permanent','exhausted')), 0) AS dlq_jobs
		FROM agent_reflection_jobs`
	if err := h.db.WithContext(c.Request.Context()).Raw(jobQuery).Scan(&metrics).Error; err != nil {
		response.Error(c, http.StatusServiceUnavailable, errors.CodeDependencyUnavailable, err.Error())
		return
	}
	outboxQuery := `SELECT
		COALESCE(SUM(status IN ('pending','publishing')), 0) AS outbox_pending,
		COALESCE(TIMESTAMPDIFF(SECOND, MIN(CASE WHEN status IN ('pending','publishing') THEN created_at END), UTC_TIMESTAMP()), 0) AS oldest_outbox_seconds
		FROM agent_reflection_job_outbox`
	if err := h.db.WithContext(c.Request.Context()).Raw(outboxQuery).Scan(&metrics).Error; err != nil {
		response.Error(c, http.StatusServiceUnavailable, errors.CodeDependencyUnavailable, err.Error())
		return
	}
	reflectionQuery := `SELECT
		COALESCE(SUM(status = 'active' AND deleted_at IS NULL), 0) AS active_reflections,
		COALESCE(SUM(status = 'validated' AND deleted_at IS NULL), 0) AS validated_reflections,
		COALESCE(SUM(status = 'disputed' AND deleted_at IS NULL), 0) AS disputed_reflections
		FROM agent_reflections`
	if err := h.db.WithContext(c.Request.Context()).Raw(reflectionQuery).Scan(&metrics).Error; err != nil {
		response.Error(c, http.StatusServiceUnavailable, errors.CodeDependencyUnavailable, err.Error())
		return
	}
	feedbackQuery := `SELECT
		COALESCE(SUM(verdict = 'helpful'), 0) AS helpful_feedback,
		COALESCE(SUM(verdict = 'harmful'), 0) AS harmful_feedback
		FROM agent_reflection_recall_logs`
	if err := h.db.WithContext(c.Request.Context()).Raw(feedbackQuery).Scan(&metrics).Error; err != nil {
		response.Error(c, http.StatusServiceUnavailable, errors.CodeDependencyUnavailable, err.Error())
		return
	}
	process := observability.ReflectionSystemMetrics.Snapshot()
	alerts := gin.H{
		"job_backlog":        metrics.PendingJobs >= jobBacklogAlertThreshold,
		"job_queue_stalled":  metrics.OldestPendingSeconds >= jobAgeAlertSeconds,
		"failed_jobs":        metrics.FailedJobs > 0,
		"outbox_stalled":     metrics.OldestOutboxSeconds >= 60,
		"stale_running_jobs": metrics.StaleRunningJobs > 0,
		"dlq_jobs":           metrics.DLQJobs > 0,
	}
	response.OK(c, gin.H{"component": "reflection_system", "database": metrics, "process": process, "alerts": alerts})
}

func (h *HealthHandler) ContextSystem(c *gin.Context) {
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
	outboxQuery := `SELECT
		COALESCE(SUM(status = 'pending'), 0) AS pending_outbox,
		COALESCE(SUM(status = 'processing'), 0) AS processing_outbox,
		COALESCE(SUM(status = 'dead_letter'), 0) AS dead_letter_outbox,
		COALESCE(SUM(attempt_count), 0) AS retry_attempts,
		COALESCE(TIMESTAMPDIFF(SECOND, MIN(CASE WHEN status = 'pending' THEN created_at END), UTC_TIMESTAMP()), 0) AS oldest_pending_seconds,
		COALESCE(SUM(status = 'processing' AND lease_expires_at < UTC_TIMESTAMP()), 0) AS stale_leases
		FROM context_resource_index_outbox`
	if err := h.db.WithContext(c.Request.Context()).Raw(outboxQuery).Scan(&metrics).Error; err != nil {
		response.Error(c, http.StatusServiceUnavailable, errors.CodeDependencyUnavailable, err.Error())
		return
	}
	compactionQuery := `SELECT
		COUNT(*) AS compactions,
		COALESCE(SUM(status = 'fallback'), 0) AS fallback_compactions,
		COALESCE(SUM(status = 'failed'), 0) AS failed_compactions
		FROM conversation_compactions`
	if err := h.db.WithContext(c.Request.Context()).Raw(compactionQuery).Scan(&metrics).Error; err != nil {
		response.Error(c, http.StatusServiceUnavailable, errors.CodeDependencyUnavailable, err.Error())
		return
	}
	process := observability.ContextSystemMetrics.Snapshot()
	alerts := gin.H{
		"outbox_backlog":   metrics.PendingOutbox >= 100,
		"outbox_stalled":   metrics.OldestPendingSeconds >= 300,
		"stale_leases":     metrics.StaleLeases > 0,
		"dead_letter":      metrics.DeadLetterOutbox > 0,
		"context_overflow": process.ContextOverflow > 0,
	}
	response.OK(c, gin.H{"component": "context_system", "database": metrics, "process": process, "alerts": alerts})
}
