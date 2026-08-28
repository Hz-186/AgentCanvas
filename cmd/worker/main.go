package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	ingestionusecase "agentcanvas/internal/application/ingestion_usecase"
	memoryusecase "agentcanvas/internal/application/memory_usecase"
	bootstrap "agentcanvas/internal/bootstrap"
	"agentcanvas/internal/domain/resource"
	"agentcanvas/internal/domain/retrieval"
	"agentcanvas/internal/infrastructure"
	cacheinfra "agentcanvas/internal/infrastructure/cache"
	chunkerinfra "agentcanvas/internal/infrastructure/chunker"
	"agentcanvas/internal/infrastructure/llm"
	mysqlinfra "agentcanvas/internal/infrastructure/mysql"
	parserinfra "agentcanvas/internal/infrastructure/parser"
	pythonbridgeinfra "agentcanvas/internal/infrastructure/pythonbridge"
	"agentcanvas/internal/infrastructure/queue"
	redisinfra "agentcanvas/internal/infrastructure/redis"
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
	if os.Getenv("AGENTCANVAS_WORKER_ROLE") == "agent" {
		// Build the same application service graph as the API, but start only
		// the durable Agent Turn worker in this process. HTTP requests enqueue
		// turns; this process owns their lease and execution.
		cfg.AgentRuntime.WorkerEnabled = false
		app, appErr := bootstrap.NewApp(ctx, cfg, appLogger)
		if appErr != nil {
			appLogger.Error("init agent worker failed", "error", appErr)
			os.Exit(1)
		}
		hostname, _ := os.Hostname()
		workerID := fmt.Sprintf("agent-worker-%s-%d", hostname, os.Getpid())
		app.AgentService.RunWorker(ctx, workerID, cfg.AgentRuntime.WorkerConcurrency)
		if cfg.AgentRuntime.SelfImprovementEnabled && app.ImprovementService != nil {
			app.ImprovementService.RunWorker(ctx, workerID+"-review", cfg.AgentRuntime.ReviewWorkerConcurrency)
		}
		<-ctx.Done()
		return
	}

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
	knowledgeRepo := cacheinfra.NewKnowledgeRepository(mysqlinfra.NewKnowledgeBaseRepository(db), resourceInvalidator)
	documentRepo := mysqlinfra.NewDocumentRepository(db)
	chunkRepo := mysqlinfra.NewChunkRepository(db)
	ingestionJobRepo := mysqlinfra.NewIngestionJobRepository(db)
	providerRepo := mysqlinfra.NewProviderRepository(db)
	messageRepo := mysqlinfra.NewMessageRepository(db)
	fileStorage := infraDeps.FileStorage
	secretBox := infraDeps.SecretBox
	var ocrClient parserinfra.OCRClient
	if cfg.OCR.Enabled {
		ocrClient = parserinfra.NewHTTPOCRClient(cfg.OCR.Endpoint, cfg.OCR.Token, time.Duration(cfg.OCR.TimeoutSeconds)*time.Second)
	}
	parserRegistry := parserinfra.NewDefaultRegistryWithOCR(ocrClient)
	goPDFParser := parserinfra.NewPDFParser(ocrClient)
	chunkers := chunkerinfra.NewDefaultRegistry()
	var pythonBridge *pythonbridgeinfra.Client
	if cfg.PythonBridge.Enabled {
		pythonBridge, err = pythonbridgeinfra.NewClient(pythonbridgeinfra.Config{
			Enabled:         true,
			Target:          cfg.PythonBridge.Target,
			AuthToken:       os.Getenv(cfg.PythonBridge.AuthTokenEnv),
			ConnectTimeout:  time.Duration(cfg.PythonBridge.ConnectTimeoutSeconds) * time.Second,
			RequestTimeout:  time.Duration(cfg.PythonBridge.RequestTimeoutSeconds) * time.Second,
			MaxSendBytes:    cfg.PythonBridge.MaxSendBytes,
			MaxReceiveBytes: cfg.PythonBridge.MaxReceiveBytes,
			MaxConcurrency:  cfg.PythonBridge.MaxConcurrency,
		})
		if err != nil {
			appLogger.Error("init Python bridge failed", "error", err)
			os.Exit(1)
		}
		capabilities, err := pythonBridge.GetCapabilities(ctx)
		if err != nil {
			appLogger.Error("handshake with Python bridge failed", "error", err)
			_ = pythonBridge.Close()
			os.Exit(1)
		}
		defer pythonBridge.Close()
		parserMethods := cfg.PythonBridge.AllowedParserMethods
		if len(parserMethods) == 0 {
			parserMethods = []string{pythonbridgeinfra.LangChainPDFParser}
		}
		if cfg.PythonBridge.DocumentParser != "go" {
			if !cfg.PythonBridge.AllowExperimentalParsing || !containsString(parserMethods, cfg.PythonBridge.DocumentParser) || !containsString(capabilities.ParserMethods, cfg.PythonBridge.DocumentParser) {
				appLogger.Error("configured Python document parser is not available", "method", cfg.PythonBridge.DocumentParser)
				_ = pythonBridge.Close()
				os.Exit(1)
			}
			pythonParser := pythonbridgeinfra.NewPDFParser(pythonBridge, goPDFParser, cfg.PythonBridge.FallbackToGoOCR, cfg.PythonBridge.MaxDocumentBytes)
			if cfg.PythonBridge.ShadowDocumentParser {
				parserRegistry.Register("pdf", pythonbridgeinfra.NewShadowParser(goPDFParser, pythonParser, appLogger, cfg.PythonBridge.MaxDocumentBytes))
			} else {
				parserRegistry.Register("pdf", pythonParser)
			}
		} else if cfg.PythonBridge.ShadowDocumentParser {
			if !cfg.PythonBridge.AllowExperimentalParsing || !containsString(parserMethods, pythonbridgeinfra.LangChainPDFParser) || !containsString(capabilities.ParserMethods, pythonbridgeinfra.LangChainPDFParser) {
				appLogger.Error("Python document parser shadow is not available")
				_ = pythonBridge.Close()
				os.Exit(1)
			}
			candidate := pythonbridgeinfra.NewPDFParser(pythonBridge, nil, false, cfg.PythonBridge.MaxDocumentBytes)
			parserRegistry.Register("pdf", pythonbridgeinfra.NewShadowParser(goPDFParser, candidate, appLogger, cfg.PythonBridge.MaxDocumentBytes))
		}
		methods := cfg.PythonBridge.AllowedChunkMethods
		if len(methods) == 0 {
			methods = []string{"python:fixed_token", "python:recursive"}
		}
		if cfg.PythonBridge.AllowExperimentalChunking || cfg.PythonBridge.ShadowEnabled {
			for _, method := range methods {
				if !containsString(capabilities.ChunkMethods, method) {
					appLogger.Error("configured Python chunk method is not available", "method", method)
					_ = pythonBridge.Close()
					os.Exit(1)
				}
			}
			for _, method := range methods {
				if method == "python:fixed_token" || method == "python:recursive" || method == "python:langchain_recursive" {
					chunkers.Register(pythonbridgeinfra.NewPythonChunker(pythonBridge, method))
				}
			}
		}
		if cfg.PythonBridge.ShadowEnabled {
			for _, method := range []string{"fixed_token", "recursive"} {
				if !containsString(methods, "python:"+method) {
					continue
				}
				primary, primaryErr := chunkers.Select(method)
				shadow, shadowErr := chunkers.Select("python:" + method)
				if primaryErr == nil && shadowErr == nil {
					chunkers.Register(chunkerinfra.NewShadowChunker(primary, shadow, appLogger))
				}
			}
			if containsString(methods, "python:langchain_recursive") {
				primary, primaryErr := chunkers.Select("recursive")
				shadow, shadowErr := chunkers.Select("python:langchain_recursive")
				if primaryErr == nil && shadowErr == nil {
					chunkers.Register(chunkerinfra.NewShadowChunker(primary, shadow, appLogger))
				}
			}
		}
	}

	service := ingestionusecase.NewService(
		knowledgeRepo,
		documentRepo,
		chunkRepo,
		ingestionJobRepo,
		providerRepo,
		fileStorage,
		parserRegistry,
		chunkers,
		indexer,
		llm.NewOpenAICompatibleEmbeddingClient(),
		secretBox,
		cfg.Elasticsearch.ChunkIndex,
	)
	indexers := make(map[string]retrieval.Indexer, len(infraDeps.RetrievalStores))
	for name, backend := range infraDeps.RetrievalStores {
		indexers[name] = backend
	}
	service.ConfigureIndexers(indexers)
	service.ConfigureGenerationCommitter(mysqlinfra.NewGenerationCommitter(db))
	jobQueue := infraDeps.JobQueue
	if cfg.Queue.Backend == "mysql" {
		// MySQL is already the business queue. Claim it through the ingestion
		// repository once instead of wrapping the same row as a transport job.
		jobQueue = nil
	}
	hostname, _ := os.Hostname()
	workerID := fmt.Sprintf("%s-%d", hostname, os.Getpid())
	extractionJobRepo := mysqlinfra.NewExtractionJobRepository(db)
	memoryRepo := mysqlinfra.NewMemoryRepository(db)
	durableMemoryCfg := memoryusecase.NewDurableMemoryConfig(cfg.EffectiveDurableMemory())
	writeJobPipeline := memoryusecase.NewMemoryWritePipeline(
		workerID,
		mysqlinfra.NewMemoryWriteJobRepository(db),
		memoryusecase.NewSQLMemoryWriter(memoryRepo),
		memoryusecase.SlogWriteWarnings{Logger: appLogger},
	)
	durableMemoryWorker := memoryusecase.NewDurableMemoryWorker(
		baseChatClient(),
		messageRepo,
		extractionJobRepo,
		infraDeps.Redis,
		durableMemoryCfg,
		workerID,
		memoryusecase.WithConsolidationProjection(memoryusecase.NewConsolidationProjection(mysqlinfra.NewMemoryArtifactRepository(db))),
		memoryusecase.WithConsolidationSources(memoryRepo),
		memoryusecase.WithExtractionWrites(writeJobPipeline),
	)
	writeJobWorker := memoryusecase.NewWriteJobWorker(writeJobPipeline)
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
			processed, err = processNextJob(ctx, jobQueue, workerID, service, durableMemoryWorker)
			if !processed && err == nil {
				processed, err = service.ProcessNext(ctx, workerID)
			}
		} else {
			processed, err = service.ProcessNext(ctx, workerID)
		}
		if err != nil {
			appLogger.Error("process ingestion job failed", "error", err)
		}
		if processed {
			continue
		}
		if drained, drainErr := durableMemoryWorker.ProcessNext(ctx); drainErr != nil {
			appLogger.Error("process durable memory job failed", "error", drainErr)
		} else if drained {
			continue
		}
		if drained, drainErr := writeJobWorker.ProcessNext(ctx); drainErr != nil {
			appLogger.Error("process memory write job failed", "error", drainErr)
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

func baseChatClient() llm.ChatClient {
	return llm.NewOpenAICompatibleChatClient()
}

func processNextJob(ctx context.Context, jobQueue queue.JobQueue, workerID string, ingestion *ingestionusecase.Service, durableMemoryWorker *memoryusecase.DurableMemoryWorker) (bool, error) {
	claimed, err := jobQueue.Claim(ctx, queue.ClaimOptions{WorkerID: workerID, Limit: 1})
	if err != nil || len(claimed) == 0 {
		return false, err
	}
	job := claimed[0]
	switch job.Type {
	// "memory:codex" is the retired job type; accept it so transport jobs
	// published before the rename are still processed after an upgrade.
	case memoryusecase.DurableMemoryJobType, "memory:codex":
		payload := memoryusecase.DurableMemoryPayload{JobID: payloadInt64(job.Payload, "job_id"), OwnerID: payloadInt64(job.Payload, "owner_id"), ConversationID: payloadInt64(job.Payload, "conversation_id")}
		if err := durableMemoryWorker.Handle(ctx, payload); err != nil {
			attemptCount := job.AttemptCount
			if attemptCount < 1 {
				attemptCount = 1
			}
			if nackErr := jobQueue.Nack(ctx, job.ID, time.Now().Add(time.Duration(attemptCount)*time.Minute)); nackErr != nil {
				return true, fmt.Errorf("durable memory job failed: %v; nack: %w", err, nackErr)
			}
			return true, err
		}
		return true, jobQueue.Ack(ctx, job.ID)
	case memoryusecase.DreamJobType:
		// Drain legacy transport jobs without routing them through the retired
		// Dream writer. New jobs are emitted only as memory:durable.
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

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
