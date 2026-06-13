package bootstrap

import (
	"context"
	"fmt"
	"log/slog"

	esinfra "agentcanvas/internal/infrastructure/elasticsearch"
	minioinfra "agentcanvas/internal/infrastructure/minio"
	mysqlinfra "agentcanvas/internal/infrastructure/mysql"
	redisinfra "agentcanvas/internal/infrastructure/redis"
	httpserver "agentcanvas/internal/interface/http"
	"agentcanvas/internal/interface/http/handler"
	"agentcanvas/internal/pkg/config"

	"github.com/gin-gonic/gin"
)

type App struct {
	Config *config.Config
	Logger *slog.Logger
	Router *gin.Engine
}

func NewApp(ctx context.Context, cfg *config.Config, log *slog.Logger) (*App, error) {
	db, err := mysqlinfra.New(cfg.MySQL)
	if err != nil {
		return nil, fmt.Errorf("init mysql: %w", err)
	}

	redisClient := redisinfra.New(cfg.Redis)

	minioClient, err := minioinfra.New(cfg.MinIO)
	if err != nil {
		return nil, fmt.Errorf("init minio: %w", err)
	}
	if err := minioinfra.EnsureBucket(ctx, minioClient, cfg.MinIO.Bucket); err != nil {
		return nil, fmt.Errorf("ensure minio bucket: %w", err)
	}

	esClient, err := esinfra.New(cfg.Elasticsearch)
	if err != nil {
		return nil, fmt.Errorf("init elasticsearch: %w", err)
	}

	healthHandler := handler.NewHealthHandler(
		db,
		redisClient,
		minioClient,
		esClient,
		cfg.MinIO.Bucket,
	)

	router := httpserver.NewRouter(log, healthHandler)

	return &App{
		Config: cfg,
		Logger: log,
		Router: router,
	}, nil
}
