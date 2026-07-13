package bootstrap

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	auditusecase "agentcanvas/internal/application/audit_usecase"
	authusecase "agentcanvas/internal/application/auth_usecase"
	chatusecase "agentcanvas/internal/application/chat_usecase"
	dialogusecase "agentcanvas/internal/application/dialog_usecase"
	knowledgeusecase "agentcanvas/internal/application/knowledge_usecase"
	memoryusecase "agentcanvas/internal/application/memory_usecase"
	providerusecase "agentcanvas/internal/application/provider_usecase"
	reflectionusecase "agentcanvas/internal/application/reflection_usecase"
	resourceusecase "agentcanvas/internal/application/resource_usecase"
	retrievalusecase "agentcanvas/internal/application/retrieval_usecase"
	skillusecase "agentcanvas/internal/application/skill_usecase"
	toolusecase "agentcanvas/internal/application/tool_usecase"
	agentusecase "agentcanvas/internal/application/workflow_usecase"
	"agentcanvas/internal/domain/resource"
	"agentcanvas/internal/infrastructure"
	cacheinfra "agentcanvas/internal/infrastructure/cache"
	cataloginfra "agentcanvas/internal/infrastructure/catalog"
	cryptoinfra "agentcanvas/internal/infrastructure/crypto"
	jobinfra "agentcanvas/internal/infrastructure/job"
	"agentcanvas/internal/infrastructure/llm"
	mysqlinfra "agentcanvas/internal/infrastructure/mysql"
	oauthinfra "agentcanvas/internal/infrastructure/oauth"
	redisinfra "agentcanvas/internal/infrastructure/redis"
	"agentcanvas/internal/infrastructure/vectorstore"
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
	dialogRepo := cacheinfra.NewDialogRepository(mysqlinfra.NewDialogRepository(db), resourceInvalidator)
	conversationRepo := mysqlinfra.NewConversationRepository(db)
	messageRepo := mysqlinfra.NewMessageRepository(db)
	usageRepo := mysqlinfra.NewUsageRepository(db)
	workflowRepo := cacheinfra.NewWorkflowRepository(mysqlinfra.NewWorkflowRepository(db), resourceInvalidator)
	workflowProfileRepo := mysqlinfra.NewWorkflowProfileRepository(db)
	workflowRuleSetRepo := mysqlinfra.NewWorkflowRuleSetRepository(db)
	reflectionRepo := mysqlinfra.NewReflectionRepository(db)
	reflectionJobRepo := mysqlinfra.NewReflectionJobRepository(db)
	reflectionRecallLogRepo := mysqlinfra.NewReflectionRecallLogRepository(db)
	flowVersionRepo := mysqlinfra.NewFlowVersionRepository(db)
	runRepo := mysqlinfra.NewRunRepository(db)
	runEventRepo := mysqlinfra.NewRunEventRepository(db)
	reflectionEventSink := mysqlinfra.NewReflectionEventSink(runEventRepo)
	nodeLogRepo := mysqlinfra.NewNodeLogRepository(db)
	runStepRepo := mysqlinfra.NewRunStepRepository(db)
	workflowEvalRepo := mysqlinfra.NewWorkflowEvalRepository(db)
	approvalRepo := mysqlinfra.NewApprovalRepository(db)
	workflowTeamRepo := mysqlinfra.NewWorkflowTeamRepository(db)
	memoryRepo := cacheinfra.NewMemoryRepository(mysqlinfra.NewMemoryRepository(db), resourceInvalidator, memoryCache)
	memoryWriteLogRepo := mysqlinfra.NewMemoryWriteLogRepository(db)
	extractionJobRepo := mysqlinfra.NewExtractionJobRepository(db)
	mergeLogRepo := mysqlinfra.NewMergeLogRepository(db)
	workingMemoryRepo := redisinfra.NewWorkingMemoryRepository(redisClient)
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
	if cfg.LLMCache.Enabled {
		cacheTTL := time.Duration(cfg.LLMCache.TTLSeconds) * time.Second
		l2Enabled := cfg.LLMCache.L2Enabled
		var l2Store vectorstore.Store
		if l2Enabled {
			if err := redisinfra.ProbeRediSearch(ctx, redisClient); err != nil {
				log.Warn("redisearch unavailable, semantic llm cache disabled", "error", err)
				l2Enabled = false
			} else {
				l2Store = vectorstore.NewRedisStackStore(redisClient).WithDefaultTTL(cacheTTL)
			}
		}
		resolveEmbedding := func(ctx context.Context, ownerID int64) (llm.EmbeddingProviderConfig, string, error) {
			if ownerID <= 0 || cfg.LLMCache.EmbeddingProviderID <= 0 {
				return llm.EmbeddingProviderConfig{}, "", fmt.Errorf("embedding provider is not configured")
			}
			provider, err := providerRepo.FindByID(ctx, ownerID, cfg.LLMCache.EmbeddingProviderID)
			if err != nil {
				return llm.EmbeddingProviderConfig{}, "", err
			}
			apiKey, err := secretBox.Decrypt(provider.EncryptedAPIKey)
			if err != nil {
				return llm.EmbeddingProviderConfig{}, "", err
			}
			model := strings.TrimSpace(cfg.LLMCache.EmbeddingModel)
			if model == "" {
				model = strings.TrimSpace(provider.DefaultEmbeddingModel)
			}
			if model == "" {
				return llm.EmbeddingProviderConfig{}, "", fmt.Errorf("embedding model is not configured")
			}
			return llm.EmbeddingProviderConfig{
				ProviderType: provider.ProviderType, BaseURL: provider.BaseURL, APIKey: apiKey,
			}, model, nil
		}
		chatClient = llm.NewCachedChatClient(baseChatClient, baseChatClient, llm.CachedChatClientOptions{
			Redis:        redisClient,
			L2Store:      l2Store,
			Embedder:     embeddingClient,
			ResolveEmbed: resolveEmbedding,
			TTL:          cacheTTL,
			L1Enabled:    cfg.LLMCache.L1Enabled,
			L2Enabled:    l2Enabled,
			Similarity:   cfg.LLMCache.SimilarityThreshold,
		})
	}
	reranker := llm.NewChatReranker(chatClient)
	retrievalService := retrievalusecase.NewService(
		knowledgeRepo, providerRepo, retrievalStore, embeddingClient, reranker, secretBox,
	)
	providerService := providerusecase.NewService(providerRepo, auditRepo, secretBox, llm.NewHTTPProviderTester())
	auditService := auditusecase.NewService(auditRepo)
	memoryService := memoryusecase.NewServiceWithCacheAndRetriever(memoryRepo, memoryCache, memoryRetrievalStore)
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
	jobQueue := infraDeps.JobQueue
	dreamCfg := memoryusecase.NewDreamConfig(cfg.MemoryDream)
	if jobQueue != nil {
		knowledgeService.WithJobQueue(jobQueue)
	}
	dialogService := dialogusecase.NewService(dialogRepo)
	chatService := chatusecase.NewService(
		providerRepo,
		dialogRepo,
		knowledgeRepo,
		conversationRepo,
		messageRepo,
		usageRepo,
		retrievalService,
		chatClient,
		secretBox,
	)
	chatService.ConfigureDream(jobQueue, redisClient, dreamCfg)
	reflectionService := reflectionusecase.Service{Reflections: reflectionRepo, Jobs: reflectionJobRepo, RecallLogs: reflectionRecallLogRepo, Events: reflectionEventSink}
	workflowService, err := agentusecase.NewService(
		workflowRepo,
		workflowProfileRepo,
		flowVersionRepo,
		runRepo,
		runEventRepo,
		nodeLogRepo,
		runStepRepo,
		workflowEvalRepo,
		approvalRepo,
		auditRepo,
		workflowTeamRepo,
		memoryRepo,
		memoryWriteLogRepo,
		memoryRetrievalStore,
		workingMemoryRepo,
		extractionJobRepo,
		mergeLogRepo,
		toolDefinitionRepo,
		toolPackRepo,
		skillRepo,
		mcpRepo,
		toolInvocationRepo,
		providerRepo,
		conversationRepo,
		messageRepo,
		retrievalService,
		chatClient,
		embeddingClient,
		archivalVecStore,
		secretBox,
		reflectionService,
	)
	if err != nil {
		return nil, fmt.Errorf("init workflow service: %w", err)
	}
	workflowService.ConfigureDream(jobQueue, redisClient, dreamCfg)
	workflowService.ConfigureRuleSets(workflowRuleSetRepo, jobQueue)

	healthHandler := handler.NewHealthHandler(db, redisClient, minioClient, esClient, cfg.MinIO.Bucket)
	authHandler := handler.NewAuthHandler(authService)
	oauthHandler := handler.NewOAuthHandler(authService)
	providerHandler := handler.NewProviderHandler(providerService, providerCatalog)
	auditHandler := handler.NewAuditHandler(auditService)
	memoryHandler := handler.NewMemoryHandler(memoryService)
	toolHandler := handler.NewToolHandler(toolService, toolPolicyRepo, toolPackRepo, mcpRepo)
	skillHandler := handler.NewSkillHandler(skillService, auditRepo)
	knowledgeHandler := handler.NewKnowledgeHandler(knowledgeService)
	documentHandler := handler.NewDocumentHandler(knowledgeService)
	dialogHandler := handler.NewDialogHandler(dialogService)
	chatHandler := handler.NewChatHandler(chatService)
	workflowHandler := handler.NewWorkflowHandler(workflowService)
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
		DialogHandler:     dialogHandler,
		ChatHandler:       chatHandler,
		WorkflowHandler:   workflowHandler,
		ReflectionHandler: reflectionHandler,
		ResourceHandler:   resourceHandler,
		AuthService:       authService,
		APITokens:         apiTokenRepo,
	})

	return &App{Config: cfg, Logger: log, Router: router}, nil
}
