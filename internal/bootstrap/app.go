package bootstrap

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	independentagentusecase "agentcanvas/internal/application/agent_usecase"
	auditusecase "agentcanvas/internal/application/audit_usecase"
	authusecase "agentcanvas/internal/application/auth_usecase"
	knowledgeusecase "agentcanvas/internal/application/knowledge_usecase"
	memoryusecase "agentcanvas/internal/application/memory_usecase"
	providerusecase "agentcanvas/internal/application/provider_usecase"
	reflectionusecase "agentcanvas/internal/application/reflection_usecase"
	resourceusecase "agentcanvas/internal/application/resource_usecase"
	retrievalusecase "agentcanvas/internal/application/retrieval_usecase"
	skillusecase "agentcanvas/internal/application/skill_usecase"
	toolusecase "agentcanvas/internal/application/tool_usecase"
	workspaceusecase "agentcanvas/internal/application/workspace_usecase"
	"agentcanvas/internal/domain/contextresource"
	"agentcanvas/internal/domain/memory"
	"agentcanvas/internal/domain/resource"
	"agentcanvas/internal/infrastructure"
	cacheinfra "agentcanvas/internal/infrastructure/cache"
	cataloginfra "agentcanvas/internal/infrastructure/catalog"
	cryptoinfra "agentcanvas/internal/infrastructure/crypto"
	gitinfra "agentcanvas/internal/infrastructure/git"
	jobinfra "agentcanvas/internal/infrastructure/job"
	"agentcanvas/internal/infrastructure/llm"
	mysqlinfra "agentcanvas/internal/infrastructure/mysql"
	oauthinfra "agentcanvas/internal/infrastructure/oauth"
	pythonbridgeinfra "agentcanvas/internal/infrastructure/pythonbridge"
	redisinfra "agentcanvas/internal/infrastructure/redis"
	contextretrieval "agentcanvas/internal/infrastructure/retrieval"
	esretrieval "agentcanvas/internal/infrastructure/retrieval/elasticsearch"
	"agentcanvas/internal/infrastructure/vectorstore"
	httpserver "agentcanvas/internal/interface/http"
	"agentcanvas/internal/interface/http/handler"
	"agentcanvas/internal/pkg/config"
	agentruntime "agentcanvas/internal/runtime/agentruntime"
	"agentcanvas/internal/runtime/eventhub"
	"agentcanvas/internal/runtime/sandbox"
	"agentcanvas/internal/runtime/toolruntime"

	"github.com/gin-gonic/gin"
)

type App struct {
	Config *config.Config
	Logger *slog.Logger
	Router *gin.Engine
}

func NewApp(ctx context.Context, cfg *config.Config, log *slog.Logger) (*App, error) {
	infraDeps, err := infrastructure.InitInfrastructure(
		ctx, cfg, infrastructure.InitOptions{
			IncludeMemoryRetrieval: true, InitializeQueue: true,
		})
	if err != nil {
		return nil, err
	}
	if infraDeps.MemoryRetrievalIndexErr != nil {
		log.Warn("memory elasticsearch index initialization failed", "error", infraDeps.MemoryRetrievalIndexErr)
	}
	db := infraDeps.DB
	redisClient := infraDeps.Redis
	minioClient := infraDeps.MinIOClient
	esClient := infraDeps.ElasticsearchClient
	sessionSearch := esretrieval.NewSessionSearchStore(esClient, cfg.Elasticsearch.MessageIndex)
	indexContext, indexCancel := context.WithTimeout(ctx, 10*time.Second)
	if err := sessionSearch.EnsureIndex(indexContext); err != nil {
		log.Warn("ensure session message index failed", "error", err)
	}
	indexCancel()
	retrievalStore := infraDeps.RetrievalStore
	memoryRetrievalStore := infraDeps.MemoryRetrievalStore
	secretBox := infraDeps.SecretBox
	fileStorage := infraDeps.FileStorage

	providerCatalog, err := cataloginfra.NewLoader()
	if err != nil {
		return nil, fmt.Errorf("load provider catalog: %w", err)
	}

	var resourceQuery resource.Query = mysqlinfra.NewResourceSummaryQuery(db)
	var resourceInvalidator resource.Invalidator
	if cfg.ResourceCache.Enabled {
		resourceCache := redisinfra.NewResourceSummaryCache(
			redisClient,
			resourceQuery,
			cfg.ResourceCache.KeyPrefix+":"+cfg.App.Env,
			time.Duration(cfg.ResourceCache.TTLSeconds)*time.Second,
			log,
		)
		resourceQuery = resourceCache
		retryingInvalidator := cacheinfra.NewRetryingInvalidator(
			resourceCache,
			mysqlinfra.NewResourceInvalidationStore(db),
			log,
		)
		retryingInvalidator.Start(ctx)
		resourceInvalidator = retryingInvalidator
	}
	memoryCache := redisinfra.NewMemoryCache(redisClient)

	userRepo := mysqlinfra.NewUserRepository(db)
	oauthRepo := mysqlinfra.NewOAuthRepository(db)
	sessionRepo := mysqlinfra.NewSessionRepository(db)
	apiTokenRepo := mysqlinfra.NewAPITokenRepository(db)
	providerRepo := mysqlinfra.NewProviderRepository(db)
	auditRepo := mysqlinfra.NewAuditRepository(db)
	knowledgeRepo := cacheinfra.NewKnowledgeRepository(mysqlinfra.NewKnowledgeBaseRepository(db), resourceInvalidator)
	documentRepo := mysqlinfra.NewDocumentRepository(db)
	chunkRepo := mysqlinfra.NewChunkRepository(db)
	ingestionJobRepo := mysqlinfra.NewIngestionJobRepository(db)
	retrievalLogRepo := mysqlinfra.NewRetrievalLogRepository(db)
	agentRepo := mysqlinfra.NewAgentRepository(db)
	agentTurnRepo := mysqlinfra.NewAgentTurnRepository(db)
	agentImprovementRepo := mysqlinfra.NewAgentImprovementRepository(db)
	conversationRepo := mysqlinfra.NewConversationRepository(db)
	messageRepo := mysqlinfra.NewMessageRepository(db)
	compactionRepo := mysqlinfra.NewConversationCompactionRepository(db)
	reflectionRepo := mysqlinfra.NewReflectionRepository(db)
	reflectionJobRepo := mysqlinfra.NewReflectionJobRepository(db)
	reflectionRecallLogRepo := mysqlinfra.NewReflectionRecallLogRepository(db)
	runRepo := mysqlinfra.NewRunRepository(db)
	runEventRepo := mysqlinfra.NewRunEventRepository(db)
	reflectionEventSink := mysqlinfra.NewReflectionEventSink(runEventRepo)
	runStepRepo := mysqlinfra.NewRunStepRepository(db)
	approvalRepo := mysqlinfra.NewApprovalRepository(db)
	projectRepo := mysqlinfra.NewProjectRepository(db)
	workspaceRepo := mysqlinfra.NewWorkspaceRepository(db)
	gitService := gitinfra.NewService(gitinfra.Config{
		CommandTimeout:  time.Duration(cfg.GitWorkspace.GitCommandTimeoutSeconds) * time.Second,
		FetchTimeout:    time.Duration(cfg.GitWorkspace.FetchTimeoutSeconds) * time.Second,
		FetchFreshness:  time.Duration(cfg.GitWorkspace.FetchFreshnessSeconds) * time.Second,
		MaxOutputBytes:  cfg.GitWorkspace.MaxOutputBytes,
		WorktreeDirName: cfg.GitWorkspace.WorktreeDirName,
		GitUserName:     cfg.GitWorkspace.GitUserName,
		GitUserEmail:    cfg.GitWorkspace.GitUserEmail,
	})
	workspaceService := workspaceusecase.NewService(projectRepo, workspaceRepo, gitService, workspaceusecase.Config{
		Enabled:                 cfg.GitWorkspace.Enabled,
		AllowedRoots:            cfg.GitWorkspace.AllowedRoots,
		WorktreeDirName:         cfg.GitWorkspace.WorktreeDirName,
		MaxWorkspacesPerProject: cfg.GitWorkspace.MaxWorkspacesPerProject,
		PruneTTL:                time.Duration(cfg.GitWorkspace.PruneTTLHours) * time.Hour,
		PreserveDirty:           cfg.GitWorkspace.PreserveDirty,
		PreserveUnpushed:        cfg.GitWorkspace.PreserveUnpushed,
		AutoInitRepository:      cfg.GitWorkspace.AutoInitRepository,
	})
	workspaceService.ConfigureAudits(auditRepo)
	if cfg.GitWorkspace.Enabled {
		if recoverErr := workspaceService.RecoverAfterRestart(ctx, 0, 0); recoverErr != nil {
			log.Warn("recover persisted Git workspaces failed", "error", recoverErr)
		}
	}
	memoryRepo := cacheinfra.NewMemoryRepository(mysqlinfra.NewMemoryRepository(db), resourceInvalidator, memoryCache)
	memoryWriteLogRepo := mysqlinfra.NewMemoryWriteLogRepository(db)
	memoryRecallLogRepo := mysqlinfra.NewMemoryRecallLogRepository(db)
	workingMemoryRepo := redisinfra.NewWorkingMemoryRepository(redisClient, redisinfra.WorkingMemoryOptions{
		TTL:      time.Duration(cfg.WorkingMemory.TTLSeconds) * time.Second,
		LockTTL:  time.Duration(cfg.WorkingMemory.LockTTLMS) * time.Millisecond,
		LockWait: time.Duration(cfg.WorkingMemory.LockWaitMS) * time.Millisecond,
	})
	toolDefinitionRepo := cacheinfra.NewToolDefinitionRepository(mysqlinfra.NewToolDefinitionRepository(db), resourceInvalidator)
	toolInvocationRepo := mysqlinfra.NewToolInvocationRepository(db)
	toolPolicyRepo := mysqlinfra.NewToolPolicyRepository(db)
	toolPackRepo := mysqlinfra.NewToolPackRepository(db)
	mcpRepo := mysqlinfra.NewMCPRepository(db)
	skillRepo := cacheinfra.NewSkillRepository(mysqlinfra.NewSkillRepository(db), resourceInvalidator)

	jwtService := cryptoinfra.NewJWTService(cfg.Security.JWTSecret, cfg.Security.AccessTokenTTL())
	tokenHasher := cryptoinfra.NewTokenHasher(cfg.Security.RefreshTokenPepper)
	passwordHasher := cryptoinfra.NewPasswordHasher(0)
	githubClient := oauthinfra.NewGitHubClient(
		cfg.OAuth.GitHub.ClientID,
		cfg.OAuth.GitHub.ClientSecret,
		cfg.OAuth.GitHub.RedirectURL,
		cfg.OAuth.GitHub.Scopes,
	)
	authService := authusecase.NewService(userRepo,
		oauthRepo,
		sessionRepo,
		apiTokenRepo,
		auditRepo,
		passwordHasher,
		jwtService,
		tokenHasher,
		redisClient,
		githubClient,
		cfg.Security.RefreshTokenTTL(),
	)
	baseChatClient := llm.NewOpenAICompatibleChatClient()
	var chatClient llm.ChatClient = baseChatClient
	embeddingClient := llm.NewOpenAICompatibleEmbeddingClient()
	var archivalVecStore vectorstore.Store
	if cfg.Milvus.Enabled {
		archivalVecStore = vectorstore.NewMilvusStore(
			cfg.Milvus.Address,
			cfg.Milvus.Token,
			vectorstore.HNSWConfig{
				M:              cfg.Milvus.M,
				EFConstruction: cfg.Milvus.EFConstruction,
				EFSearch:       cfg.Milvus.EFSearch,
				MetricType:     cfg.Milvus.MetricType,
			},
		)
	}
	var contextIndex contextresource.Index
	if cfg.ContextIndex.Enabled && archivalVecStore != nil {
		providerID := cfg.ContextIndex.EmbeddingProviderID
		model := strings.TrimSpace(cfg.ContextIndex.EmbeddingModel)
		semanticIndex := contextretrieval.NewContextSemanticIndex(archivalVecStore, embeddingClient, providerRepo, secretBox, providerID, model, vectorstore.HNSWConfig{
			M: cfg.Milvus.M, EFConstruction: cfg.Milvus.EFConstruction, EFSearch: cfg.Milvus.EFSearch, MetricType: cfg.Milvus.MetricType,
		})
		keywordIndex := esretrieval.NewContextKeywordIndex(esClient, cfg.Elasticsearch.ContextIndex)
		ensureContext, cancelContext := context.WithTimeout(ctx, 10*time.Second)
		if err := keywordIndex.EnsureIndex(ensureContext); err != nil {
			log.Warn("ensure context keyword index failed; outbox worker will retry", "error", err)
		}
		cancelContext()
		contextIndex = contextretrieval.ContextHybridIndex{Keyword: keywordIndex, Semantic: semanticIndex, RRFK: 60}
		if cfg.ContextIndex.WorkerEnabled {
			contextWorker := &contextresource.Worker{Repository: mysqlinfra.NewContextResourceRepository(db), Index: contextIndex,
				WorkerID: fmt.Sprintf("context-api-%d", os.Getpid()), BatchSize: cfg.ContextIndex.BatchSize,
				Lease: time.Duration(cfg.ContextIndex.LeaseSeconds) * time.Second, PollInterval: time.Duration(cfg.ContextIndex.PollMilliseconds) * time.Millisecond, Logger: log}
			go contextWorker.Run(ctx)
		}
	}
	if cfg.LLMCache.Enabled {
		cacheTTL := time.Duration(cfg.LLMCache.TTLSeconds) * time.Second
		chatClient = llm.NewCachedChatClient(baseChatClient, baseChatClient, llm.CachedChatClientOptions{
			Redis: redisClient,
			TTL:   cacheTTL,
		})
	}
	reranker := llm.NewChatReranker(chatClient)
	retrievalService := retrievalusecase.NewService(
		knowledgeRepo, providerRepo, retrievalStore, embeddingClient, reranker, secretBox,
	).WithQueryRewriter(retrievalusecase.ProviderQueryRewriter{Providers: providerRepo, Client: chatClient, Secrets: secretBox})
	providerService := providerusecase.NewService(providerRepo, auditRepo, secretBox, llm.NewHTTPProviderTester())
	auditService := auditusecase.NewService(auditRepo)
	memoryService := memoryusecase.NewServiceWithCacheAndRetriever(memoryRepo, memoryCache, memoryRetrievalStore)
	memoryCandidateService := memoryusecase.NewCandidateService(agentImprovementRepo)
	memoryCommandService := memoryusecase.NewMemoryCommandService(memoryRepo, memoryWriteLogRepo)
	memoryService.ConfigureCommands(memoryCommandService)
	memoryService.ConfigureRecallLogs(memoryRecallLogRepo)
	toolService := toolusecase.NewService(toolDefinitionRepo)
	workspaceRoot, _ := os.Getwd()
	skillService := skillusecase.NewService(skillRepo, workspaceRoot)
	knowledgeService := knowledgeusecase.NewService(
		knowledgeRepo,
		documentRepo,
		chunkRepo,
		ingestionJobRepo,
		retrievalLogRepo,
		auditRepo,
		fileStorage,
		retrievalService,
		retrievalStore,
	)
	knowledgeService.ConfigurePythonChunking(cfg.PythonBridge.AllowExperimentalChunking)
	jobQueue := infraDeps.JobQueue
	dreamCfg := memoryusecase.NewDreamConfig(cfg.MemoryDream)
	if jobQueue != nil {
		knowledgeService.WithJobQueue(jobQueue)
	}
	reflectionService := reflectionusecase.Service{Reflections: reflectionRepo, Jobs: reflectionJobRepo, RecallLogs: reflectionRecallLogRepo, Events: reflectionEventSink,
		DispatchEnabled: cfg.ReflectionQueue.Backend == "nats"}
	if archivalVecStore != nil {
		reflectionService.Index = contextretrieval.NewReflectionSemanticIndex(archivalVecStore, embeddingClient, providerRepo, secretBox, reflectionRepo, vectorstore.HNSWConfig{
			M: cfg.Milvus.M, EFConstruction: cfg.Milvus.EFConstruction, EFSearch: cfg.Milvus.EFSearch, MetricType: cfg.Milvus.MetricType,
		})
	}
	toolCallingClient, ok := chatClient.(llm.ToolCallingClient)
	if !ok {
		return nil, fmt.Errorf("init agent runtime: tool calling client is required")
	}
	providerLoader := agentruntime.ProviderLoader{Providers: providerRepo, Secrets: secretBox}
	toolRegistry := toolruntime.BasicRegistry{Tools: toolDefinitionRepo, Invocations: toolInvocationRepo}
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
			return nil, fmt.Errorf("init Python bridge: %w", err)
		}
		if _, err := pythonBridge.GetCapabilities(ctx); err != nil {
			_ = pythonBridge.Close()
			return nil, fmt.Errorf("handshake with Python bridge: %w", err)
		}
		go func() {
			<-ctx.Done()
			_ = pythonBridge.Close()
		}()
	}
	agentRuntime, err := agentruntime.New(agentruntime.Deps{
		Repositories: agentruntime.Repositories{
			Retriever: retrievalService, Providers: providerLoader, MessageHistory: messageRepo, Compactions: compactionRepo,
			SessionSearch: sessionSearch, Memories: memoryRepo, MemoryReader: memoryService, MemoryWriteLogs: memoryWriteLogRepo,
			MemoryRecallLogs: memoryRecallLogRepo, MemoryCandidates: memoryCandidateService, MemoryRetriever: memoryRetrievalStore,
			WorkingMemory: workingMemoryRepo, ToolPacks: toolPackRepo, Skills: skillRepo, MCPServers: mcpRepo,
			ToolInvocations: toolInvocationRepo, ContextIndex: contextIndex,
		},
		RuntimeClients: agentruntime.RuntimeClients{
			LLM: chatClient, ToolCalling: toolCallingClient, Embedder: embeddingClient, PythonBridge: pythonBridge,
			Archival: agentruntime.ArchivalIndexFactoryFunc(func(provider agentruntime.LoadedProvider) memory.ArchivalIndex {
				if archivalVecStore == nil || strings.TrimSpace(provider.EmbeddingModel) == "" {
					return nil
				}
				return contextretrieval.ArchivalMemoryIndex{Store: archivalVecStore, Embedder: embeddingClient, Provider: provider.EmbeddingConfig, Model: provider.EmbeddingModel}
			}),
		},
		Tooling:       agentruntime.Tooling{ToolRegistry: toolRegistry, PythonToolAllowlist: cfg.PythonBridge.AllowedTools},
		Workspace:     agentruntime.Workspace{Sandbox: sandbox.NewDockerRunner(), Git: gitService},
		Observability: agentruntime.Observability{Audits: auditRepo, Reflections: reflectionService},
		Policies: agentruntime.Policies{
			MemoryExtractionTrigger: memoryusecase.NewDreamTrigger(jobQueue, redisClient, dreamCfg),
			FileReadMaxChars:        cfg.GitWorkspace.FileReadMaxChars, MaxOutputBytes: cfg.GitWorkspace.MaxOutputBytes,
			WorkspaceTimeout: time.Duration(cfg.GitWorkspace.GitCommandTimeoutSeconds) * time.Second,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("init agent runtime: %w", err)
	}
	agentService := independentagentusecase.NewService(agentRepo, agentTurnRepo, conversationRepo, messageRepo,
		runRepo, runEventRepo, runStepRepo, approvalRepo, agentRuntime)
	runStreamHub := eventhub.NewMemoryHub()
	agentService.ConfigureEventHub(runStreamHub)
	go func() {
		<-ctx.Done()
		runStreamHub.Close()
	}()
	agentService.ConfigureEditableResources(providerRepo, knowledgeRepo)
	agentService.ConfigureSessionSearch(sessionSearch)
	agentService.ConfigureWorkspace(workspaceService)
	agentRuntime.ConfigureSubagentDispatcher(agentService)
	agentRuntime.ConfigureMemoryReader(memoryService)
	agentRuntime.ConfigureMemoryCandidates(memoryCandidateService)
	agentRuntime.ConfigureSessionSearch(sessionSearch)
	improvementService := independentagentusecase.NewImprovementService(agentImprovementRepo, agentRepo, agentTurnRepo, messageRepo,
		runStepRepo, memoryRepo, reflectionRepo, skillRepo, providerLoader, toolCallingClient, cfg.AgentRuntime.MemoryReviewMode)
	improvementService.ConfigureMemoryCommands(memoryCommandService)
	improvementService.ConfigureReviewModel(cfg.AgentRuntime.ReviewProviderID, cfg.AgentRuntime.ReviewModel)
	if cfg.AgentRuntime.SelfImprovementEnabled {
		agentService.ConfigureImprovement(improvementService)
	}
	agentService.ConfigureWorker(time.Duration(cfg.AgentRuntime.LeaseSeconds) * time.Second)
	if cfg.AgentRuntime.WorkerEnabled {
		hostname, _ := os.Hostname()
		agentService.RunWorker(ctx, fmt.Sprintf("agent-api-%s-%d", hostname, os.Getpid()), cfg.AgentRuntime.WorkerConcurrency)
		if cfg.AgentRuntime.SelfImprovementEnabled {
			improvementService.RunWorker(ctx, fmt.Sprintf("review-api-%s-%d", hostname, os.Getpid()), cfg.AgentRuntime.ReviewWorkerConcurrency)
		}
	}

	healthHandler := handler.NewHealthHandler(db, redisClient, minioClient, esClient, cfg.MinIO.Bucket)
	authHandler := handler.NewAuthHandler(authService)
	oauthHandler := handler.NewOAuthHandler(authService)
	providerHandler := handler.NewProviderHandler(providerService, providerCatalog)
	auditHandler := handler.NewAuditHandler(auditService)
	memoryHandler := handler.NewMemoryHandler(memoryService)
	memoryHandler.ConfigureCandidates(memoryCandidateService, improvementService)
	toolHandler := handler.NewToolHandler(toolService, toolPolicyRepo, toolPackRepo, mcpRepo)
	skillHandler := handler.NewSkillHandler(skillService, auditRepo)
	knowledgeHandler := handler.NewKnowledgeHandler(knowledgeService)
	documentHandler := handler.NewDocumentHandler(knowledgeService)
	agentHandler := handler.NewAgentHandler(agentService, improvementService)
	agentHandler.ConfigureWorkspace(workspaceService)
	projectHandler := handler.NewProjectHandler(workspaceService)
	reflectionHandler := handler.NewReflectionHandler(reflectionService)
	resourceHandler := handler.NewResourceHandler(resourceusecase.NewService(resourceQuery))

	memoryScheduler := jobinfra.NewMemoryScheduler(memoryRepo, jobinfra.MemorySchedulerConfig{
		ConsolidationInterval: 1 * time.Hour,
		Logger:                log,
	})
	memoryScheduler.Start(ctx)

	router := httpserver.NewRouter(httpserver.RouterDeps{
		Logger:            log,
		HealthHandler:     healthHandler,
		AuthHandler:       authHandler,
		OAuthHandler:      oauthHandler,
		ProviderHandler:   providerHandler,
		MemoryHandler:     memoryHandler,
		ToolHandler:       toolHandler,
		SkillHandler:      skillHandler,
		AuditHandler:      auditHandler,
		KnowledgeHandler:  knowledgeHandler,
		DocumentHandler:   documentHandler,
		AgentHandler:      agentHandler,
		ProjectHandler:    projectHandler,
		ReflectionHandler: reflectionHandler,
		ResourceHandler:   resourceHandler,
		AuthService:       authService,
		APITokens:         apiTokenRepo,
	})

	return &App{Config: cfg, Logger: log, Router: router}, nil
}
