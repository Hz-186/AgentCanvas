package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	ingestionusecase "agentcanvas/internal/application/ingestion_usecase"
	memoryusecase "agentcanvas/internal/application/memory_usecase"
	reflectionusecase "agentcanvas/internal/application/reflection_usecase"
	"agentcanvas/internal/domain/resource"
	"agentcanvas/internal/infrastructure"
	cacheinfra "agentcanvas/internal/infrastructure/cache"
	chunkerinfra "agentcanvas/internal/infrastructure/chunker"
	"agentcanvas/internal/infrastructure/llm"
	mysqlinfra "agentcanvas/internal/infrastructure/mysql"
	parserinfra "agentcanvas/internal/infrastructure/parser"
	"agentcanvas/internal/infrastructure/queue"
	redisinfra "agentcanvas/internal/infrastructure/redis"
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

	infraDeps, err := infrastructure.InitInfrastructure(ctx, cfg, infrastructure.InitOptions{InitializeQueue: true, PingRedisQueue: true})
	if err != nil {
		appLogger.Error("init infrastructure failed", "error", err)
		os.Exit(1)
	}
	db := infraDeps.DB
	indexer := infraDeps.RetrievalStore

	var resourceInvalidator resource.Invalidator
	if cfg.ResourceCache.Enabled {
		resourceCache := redisinfra.NewResourceSummaryCache(
			infraDeps.Redis,
			mysqlinfra.NewResourceSummaryQuery(db),
			cfg.ResourceCache.KeyPrefix+":"+cfg.App.Env,
			time.Duration(cfg.ResourceCache.TTLSeconds)*time.Second,
			appLogger,
		)
		retryingInvalidator := cacheinfra.NewRetryingInvalidator(
			resourceCache,
			mysqlinfra.NewResourceInvalidationStore(db),
			appLogger,
		)
		retryingInvalidator.Start(ctx)
		resourceInvalidator = retryingInvalidator
	}
	memoryCache := redisinfra.NewMemoryCache(infraDeps.Redis)
	knowledgeRepo := cacheinfra.NewKnowledgeRepository(mysqlinfra.NewKnowledgeBaseRepository(db), resourceInvalidator)
	documentRepo := mysqlinfra.NewDocumentRepository(db)
	chunkRepo := mysqlinfra.NewChunkRepository(db)
	ingestionJobRepo := mysqlinfra.NewIngestionJobRepository(db)
	providerRepo := mysqlinfra.NewProviderRepository(db)
	memoryRepo := cacheinfra.NewMemoryRepository(mysqlinfra.NewMemoryRepository(db), resourceInvalidator, memoryCache)
	memoryLogRepo := mysqlinfra.NewMemoryWriteLogRepository(db)
	messageRepo := mysqlinfra.NewMessageRepository(db)
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
	var archivalVecStore vectorstore.Store
	if cfg.Milvus.Enabled {
		archivalVecStore = vectorstore.NewMilvusStore(cfg.Milvus.Address, cfg.Milvus.Token, vectorstore.HNSWConfig{M: cfg.Milvus.M, EFConstruction: cfg.Milvus.EFConstruction, EFSearch: cfg.Milvus.EFSearch, MetricType: cfg.Milvus.MetricType})
	}

	hostname, _ := os.Hostname()
	workerID := fmt.Sprintf("%s-%d", hostname, os.Getpid())
	extractionJobRepo := mysqlinfra.NewExtractionJobRepository(db)
	dreamWorker := memoryusecase.NewDreamWorker(baseChatClient(), llm.NewOpenAICompatibleEmbeddingClient(), memoryRepo, memoryLogRepo, messageRepo, archivalVecStore, infraDeps.Redis, memoryusecase.NewDreamConfig(cfg.MemoryDream), workerID, extractionJobRepo)
	extractionCompatibility := memoryusecase.NewExtractionService(memoryRepo, extractionJobRepo, mysqlinfra.NewMergeLogRepository(db), messageRepo)
	reflectionRepo := mysqlinfra.NewReflectionRepository(db)
	reflectionJobRepo := mysqlinfra.NewReflectionJobRepository(db)
	reflectionRecallRepo := mysqlinfra.NewReflectionRecallLogRepository(db)
	reflectionEventSink := mysqlinfra.NewReflectionEventSink(mysqlinfra.NewRunEventRepository(db))
	dispatchReflection := cfg.ReflectionQueue.Backend == "nats"
	reflectionService := reflectionusecase.Service{Reflections: reflectionRepo, Jobs: reflectionJobRepo, RecallLogs: reflectionRecallRepo, Events: reflectionEventSink,
		DispatchEnabled: dispatchReflection}
	reflectionWorker := reflectionusecase.Worker{Service: reflectionService, Jobs: reflectionJobRepo, Providers: providerRepo, Secrets: secretBox,
		LLM: baseChatClient(), DispatchEnabled: dispatchReflection}
	var reflectionWG sync.WaitGroup
	reflectionWG.Add(1)
	if dispatchReflection {
		transport, transportErr := queue.NewReflectionNATSTransport(cfg.NATS, cfg.ReflectionQueue, appLogger)
		if transportErr != nil {
			appLogger.Error("init reflection nats transport failed", "error", transportErr)
			os.Exit(1)
		}
		runtime := &reflectionusecase.QueueRuntime{Worker: reflectionWorker, Jobs: reflectionJobRepo, Outbox: reflectionJobRepo,
			Transport: transport, Config: cfg.ReflectionQueue, WorkerID: workerID, Logger: appLogger}
		go func() {
			defer reflectionWG.Done()
			if runErr := runtime.Run(ctx); runErr != nil && ctx.Err() == nil {
				appLogger.Error("reflection queue runtime stopped", "error", runErr)
			}
		}()
	} else {
		go func() {
			defer reflectionWG.Done()
			runMySQLReflectionWorker(ctx, reflectionWorker, workerID, appLogger)
		}()
	}
	defer reflectionWG.Wait()
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
			processed, err = processNextJob(ctx, jobQueue, workerID, service, dreamWorker)
		} else {
			processed, err = service.ProcessNext(ctx, workerID)
		}
		if err != nil {
			appLogger.Error("process ingestion job failed", "error", err)
		}
		if processed {
			continue
		}
		if drained, drainErr := extractionCompatibility.ProcessNextDream(ctx, dreamWorker); drainErr != nil {
			appLogger.Error("drain legacy memory extraction job failed", "error", drainErr)
		} else if drained {
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

func runMySQLReflectionWorker(ctx context.Context, worker reflectionusecase.Worker, workerID string, appLogger *slog.Logger) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for ctx.Err() == nil {
		processed, err := worker.ProcessNext(ctx, workerID)
		if err != nil {
			appLogger.Error("process reflection job failed", "worker_id", workerID, "error", err)
		}
		if processed {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func baseChatClient() llm.ChatClient {
	return llm.NewOpenAICompatibleChatClient()
}

func processNextJob(ctx context.Context, jobQueue queue.JobQueue, workerID string, ingestion *ingestionusecase.Service, dreamWorker *memoryusecase.DreamWorker) (bool, error) {
	claimed, err := jobQueue.Claim(ctx, queue.ClaimOptions{WorkerID: workerID, Limit: 1})
	if err != nil || len(claimed) == 0 {
		return false, err
	}
	job := claimed[0]
	switch job.Type {
	case memoryusecase.DreamJobType:
		payload := memoryusecase.DreamPayload{JobID: payloadInt64(job.Payload, "job_id"), OwnerID: payloadInt64(job.Payload, "owner_id"), ConversationID: payloadInt64(job.Payload, "conversation_id")}
		if err := dreamWorker.HandleDreamJob(ctx, payload); err != nil {
			_ = jobQueue.Nack(ctx, job.ID, time.Now().Add(time.Minute))
			return true, err
		}
		return true, jobQueue.Ack(ctx, job.ID)
	default:
		processed, err := ingestion.ProcessNextFromQueue(ctx, singleJobQueue{jobQueue: jobQueue, claimed: job}, workerID)
		return processed, err
	}
}

type singleJobQueue struct {
	jobQueue queue.JobQueue
	claimed  queue.Job
}

func (q singleJobQueue) Publish(ctx context.Context, job queue.Job) error {
	return q.jobQueue.Publish(ctx, job)
}
func (q singleJobQueue) Claim(context.Context, queue.ClaimOptions) ([]queue.Job, error) {
	return []queue.Job{q.claimed}, nil
}
func (q singleJobQueue) Ack(ctx context.Context, jobID string) error {
	return q.jobQueue.Ack(ctx, jobID)
}
func (q singleJobQueue) Nack(ctx context.Context, jobID string, retryAt time.Time) error {
	return q.jobQueue.Nack(ctx, jobID, retryAt)
}

func payloadInt64(payload map[string]any, key string) int64 {
	if payload == nil {
		return 0
	}
	switch value := payload[key].(type) {
	case int64:
		return value
	case int:
		return int64(value)
	case float64:
		return int64(value)
	case json.Number:
		parsed, _ := value.Int64()
		return parsed
	case string:
		parsed, _ := strconv.ParseInt(value, 10, 64)
		return parsed
	default:
		parsed, _ := strconv.ParseInt(fmt.Sprint(value), 10, 64)
		return parsed
	}
}
