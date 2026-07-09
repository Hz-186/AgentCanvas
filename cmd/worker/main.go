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
	"agentcanvas/internal/infrastructure"
	chunkerinfra "agentcanvas/internal/infrastructure/chunker"
	"agentcanvas/internal/infrastructure/llm"
	mysqlinfra "agentcanvas/internal/infrastructure/mysql"
	parserinfra "agentcanvas/internal/infrastructure/parser"
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

	infraDeps, err := infrastructure.InitInfrastructure(ctx, cfg, infrastructure.InitOptions{InitializeQueue: true, PingRedisQueue: true})
	if err != nil {
		appLogger.Error("init infrastructure failed", "error", err)
		os.Exit(1)
	}
	db := infraDeps.DB
	indexer := infraDeps.RetrievalStore

	knowledgeRepo := mysqlinfra.NewKnowledgeBaseRepository(db)
	documentRepo := mysqlinfra.NewDocumentRepository(db)
	chunkRepo := mysqlinfra.NewChunkRepository(db)
	ingestionJobRepo := mysqlinfra.NewIngestionJobRepository(db)
	providerRepo := mysqlinfra.NewProviderRepository(db)
	fileStorage := infraDeps.FileStorage
	secretBox := infraDeps.SecretBox
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
	jobQueue := infraDeps.JobQueue

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
