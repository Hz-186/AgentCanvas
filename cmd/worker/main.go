package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	ingestionusecase "agentcanvas/internal/application/ingestion_usecase"
	"agentcanvas/internal/domain/retrieval"
	chunkerinfra "agentcanvas/internal/infrastructure/chunker"
	cryptoinfra "agentcanvas/internal/infrastructure/crypto"
	esinfra "agentcanvas/internal/infrastructure/elasticsearch"
	"agentcanvas/internal/infrastructure/llm"
	minioinfra "agentcanvas/internal/infrastructure/minio"
	mysqlinfra "agentcanvas/internal/infrastructure/mysql"
	parserinfra "agentcanvas/internal/infrastructure/parser"
	queueinfra "agentcanvas/internal/infrastructure/queue"
	redisinfra "agentcanvas/internal/infrastructure/redis"
	compositeretrieval "agentcanvas/internal/infrastructure/retrieval/composite"
	esretrieval "agentcanvas/internal/infrastructure/retrieval/elasticsearch"
	milvusretrieval "agentcanvas/internal/infrastructure/retrieval/milvus"
	"agentcanvas/internal/infrastructure/vectorstore"
	"agentcanvas/internal/pkg/config"
	"agentcanvas/internal/pkg/logger"
)

func main() {
	configPath := os.Getenv("AGENTCANVAS_CONFIG_PATH")
	if configPath == "" {
		configPath = "configs/config.local.yaml"
		if _, err := os.Stat(configPath); os.IsNotExist(err) {
			configPath = "configs/config.yaml"
		}
	}

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		log.Fatalf("load config error: %v", err)
	}
	appLogger := logger.New(cfg.App.Env)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, err := mysqlinfra.New(cfg.MySQL)
	if err != nil {
		appLogger.Error("init mysql failed", "error", err)
		os.Exit(1)
	}
	minioClient, err := minioinfra.New(cfg.MinIO)
	if err != nil {
		appLogger.Error("init minio failed", "error", err)
		os.Exit(1)
	}
	if err := minioinfra.EnsureBucket(ctx, minioClient, cfg.MinIO.Bucket); err != nil {
		appLogger.Error("ensure minio bucket failed", "error", err)
		os.Exit(1)
	}
	esClient, err := esinfra.New(cfg.Elasticsearch)
	if err != nil {
		appLogger.Error("init elasticsearch failed", "error", err)
		os.Exit(1)
	}
	esStore := esretrieval.NewStore(esClient, cfg.Elasticsearch)
	var indexer retrieval.Indexer = esStore
	if cfg.Milvus.Enabled {
		milvusVector := vectorstore.NewMilvusStore(cfg.Milvus.Address, cfg.Milvus.Token, vectorstore.HNSWConfig{M: cfg.Milvus.M, EFConstruction: cfg.Milvus.EFConstruction, EFSearch: cfg.Milvus.EFSearch, MetricType: cfg.Milvus.MetricType})
		milvusStore := milvusretrieval.NewStore(milvusVector, cfg.Milvus.Collection, cfg.Milvus.Dimensions, vectorstore.HNSWConfig{M: cfg.Milvus.M, EFConstruction: cfg.Milvus.EFConstruction, EFSearch: cfg.Milvus.EFSearch, MetricType: cfg.Milvus.MetricType})
		indexer = compositeretrieval.New(esStore, milvusStore)
	}
	if err := indexer.EnsureIndex(ctx); err != nil {
		appLogger.Error("ensure elasticsearch chunk index failed", "error", err)
		os.Exit(1)
	}

	knowledgeRepo := mysqlinfra.NewKnowledgeBaseRepository(db)
	documentRepo := mysqlinfra.NewDocumentRepository(db)
	chunkRepo := mysqlinfra.NewChunkRepository(db)
	ingestionJobRepo := mysqlinfra.NewIngestionJobRepository(db)
	providerRepo := mysqlinfra.NewProviderRepository(db)
	fileStorage := minioinfra.NewFileStorage(minioClient, cfg.MinIO.Bucket)
	secretBox, err := cryptoinfra.NewSecretBox(cfg.Security.SecretEncryptKey)
	if err != nil {
		appLogger.Error("init secret box failed", "error", err)
		os.Exit(1)
	}
	parserRegistry := parserinfra.NewDefaultRegistry()
	if cfg.OCR.Enabled {
		parserRegistry = parserinfra.NewDefaultRegistryWithOCR(parserinfra.NewHTTPOCRClient(cfg.OCR.Endpoint, cfg.OCR.Token, time.Duration(cfg.OCR.TimeoutSeconds)*time.Second))
	}

	service := ingestionusecase.NewService(
		knowledgeRepo,
		documentRepo,
		chunkRepo,
		ingestionJobRepo,
		providerRepo,
		fileStorage,
		parserRegistry,
		chunkerinfra.NewDefaultRegistry(),
		indexer,
		llm.NewOpenAICompatibleEmbeddingClient(),
		secretBox,
		cfg.Elasticsearch.ChunkIndex,
	)
	var jobQueue queueinfra.JobQueue
	if cfg.Queue.Backend == "redis_stream" {
		redisClient := redisinfra.New(cfg.Redis)
		if err := redisinfra.Ping(ctx, redisClient); err != nil {
			appLogger.Error("init redis stream queue failed", "error", err)
			os.Exit(1)
		}
		jobQueue = queueinfra.NewRedisStreamQueue(redisClient, cfg.Queue.RedisStream, cfg.Queue.RedisGroup, cfg.Queue.RedisConsumer)
	}

	hostname, _ := os.Hostname()
	workerID := fmt.Sprintf("%s-%d", hostname, os.Getpid())
	appLogger.Info("worker started", "worker_id", workerID)

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			appLogger.Info("worker stopped")
			return
		default:
		}

		var processed bool
		var err error
		if jobQueue != nil {
			processed, err = service.ProcessNextFromQueue(ctx, jobQueue, workerID)
		} else {
			processed, err = service.ProcessNext(ctx, workerID)
		}
		if err != nil {
			appLogger.Error("process ingestion job failed", "error", err)
		}
		if processed {
			continue
		}

		select {
		case <-ctx.Done():
			appLogger.Info("worker stopped")
			return
		case <-ticker.C:
		}
	}
}
