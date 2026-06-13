package httpsever

import (
	"log/slog"

	"agentcanvas/internal/interface/http/handler"
	"agentcanvas/internal/interface/http/middleware"

	"github.com/gin-gonic/gin"
)

func NewRouter(log *slog.Logger, healthHandler *handler.HealthHandler) *gin.Engine {
	r := gin.New()

	// middleware
	r.Use(middleware.RequestID())
	r.Use(middleware.Recovery(log))
	r.Use(middleware.CORS())

	v1 := r.Group("/api/v1")
	{
		v1.GET("/health", healthHandler.Health)
		v1.GET("/health/mysql", healthHandler.MySQL)
		v1.GET("/health/redis", healthHandler.Redis)
		v1.GET("/health/minio", healthHandler.MinIO)
		v1.GET("/health/es", healthHandler.Elasticsearch)
	}

	return r
}
