package handler

import (
	"context"
	"net/http"
	"time"

	esinfra "agentcanvas/internal/infrastructure/elasticsearch"
	minioinfra "agentcanvas/internal/infrastructure/minio"
	mysqlinfra "agentcanvas/internal/infrastructure/mysql"
	redisinfra "agentcanvas/internal/infrastructure/redis"
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
