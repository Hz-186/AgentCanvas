package bootstrap

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	auditusecase "agentcanvas/internal/application/audit_usecase"
	authusecase "agentcanvas/internal/application/auth_usecase"
	chatusecase "agentcanvas/internal/application/chat_usecase"
	dialogusecase "agentcanvas/internal/application/dialog_usecase"
	knowledgeusecase "agentcanvas/internal/application/knowledge_usecase"
	memoryusecase "agentcanvas/internal/application/memory_usecase"
	providerusecase "agentcanvas/internal/application/provider_usecase"
	retrievalusecase "agentcanvas/internal/application/retrieval_usecase"
	toolusecase "agentcanvas/internal/application/tool_usecase"
	agentusecase "agentcanvas/internal/application/workflow_usecase"
	"agentcanvas/internal/infrastructure"
	cataloginfra "agentcanvas/internal/infrastructure/catalog"
	cryptoinfra "agentcanvas/internal/infrastructure/crypto"
	jobinfra "agentcanvas/internal/infrastructure/job"
	"agentcanvas/internal/infrastructure/llm"
	mysqlinfra "agentcanvas/internal/infrastructure/mysql"
	oauthinfra "agentcanvas/internal/infrastructure/oauth"
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
	infraDeps, err := infrastructure.InitInfrastructure(ctx, cfg, infrastructure.InitOptions{IncludeMemoryRetrieval: true, InitializeQueue: true})
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

	userRepo := mysqlinfra.NewUserRepository(db)
	oauthRepo := mysqlinfra.NewOAuthRepository(db)
	sessionRepo := mysqlinfra.NewSessionRepository(db)
	apiTokenRepo := mysqlinfra.NewAPITokenRepository(db)
	providerRepo := mysqlinfra.NewProviderRepository(db)
	auditRepo := mysqlinfra.NewAuditRepository(db)
	knowledgeRepo := mysqlinfra.NewKnowledgeBaseRepository(db)
	documentRepo := mysqlinfra.NewDocumentRepository(db)
	chunkRepo := mysqlinfra.NewChunkRepository(db)
	ingestionJobRepo := mysqlinfra.NewIngestionJobRepository(db)
	retrievalLogRepo := mysqlinfra.NewRetrievalLogRepository(db)
	dialogRepo := mysqlinfra.NewDialogRepository(db)
	conversationRepo := mysqlinfra.NewConversationRepository(db)
	messageRepo := mysqlinfra.NewMessageRepository(db)
	usageRepo := mysqlinfra.NewUsageRepository(db)
	workflowRepo := mysqlinfra.NewWorkflowRepository(db)
	workflowProfileRepo := mysqlinfra.NewWorkflowProfileRepository(db)
	flowVersionRepo := mysqlinfra.NewFlowVersionRepository(db)
	runRepo := mysqlinfra.NewRunRepository(db)
	runEventRepo := mysqlinfra.NewRunEventRepository(db)
	nodeLogRepo := mysqlinfra.NewNodeLogRepository(db)
	runStepRepo := mysqlinfra.NewRunStepRepository(db)
	workflowEvalRepo := mysqlinfra.NewWorkflowEvalRepository(db)
	approvalRepo := mysqlinfra.NewApprovalRepository(db)
	workflowTeamRepo := mysqlinfra.NewWorkflowTeamRepository(db)
	memoryRepo := mysqlinfra.NewMemoryRepository(db)
	memoryWriteLogRepo := mysqlinfra.NewMemoryWriteLogRepository(db)
	extractionJobRepo := mysqlinfra.NewExtractionJobRepository(db)
	mergeLogRepo := mysqlinfra.NewMergeLogRepository(db)
	memoryCache := redisinfra.NewMemoryCache(redisClient)
	workingMemoryRepo := redisinfra.NewWorkingMemoryRepository(redisClient)
	toolDefinitionRepo := mysqlinfra.NewToolDefinitionRepository(db)
	toolInvocationRepo := mysqlinfra.NewToolInvocationRepository(db)
	toolPolicyRepo := mysqlinfra.NewToolPolicyRepository(db)
	toolPackRepo := mysqlinfra.NewToolPackRepository(db)
	mcpRepo := mysqlinfra.NewMCPRepository(db)

	jwtService := cryptoinfra.NewJWTService(cfg.Security.JWTSecret, cfg.Security.AccessTokenTTL())
	tokenHasher := cryptoinfra.NewTokenHasher(cfg.Security.RefreshTokenPepper)
	passwordHasher := cryptoinfra.NewPasswordHasher(0)
	githubClient := oauthinfra.NewGitHubClient(cfg.OAuth.GitHub.ClientID, cfg.OAuth.GitHub.ClientSecret, cfg.OAuth.GitHub.RedirectURL, cfg.OAuth.GitHub.Scopes)

	authService := authusecase.NewService(userRepo, oauthRepo, sessionRepo, apiTokenRepo, auditRepo, passwordHasher, jwtService, tokenHasher, redisClient, githubClient, cfg.Security.RefreshTokenTTL())
	chatClient := llm.NewOpenAICompatibleChatClient()
	embeddingClient := llm.NewOpenAICompatibleEmbeddingClient()
	reranker := llm.NewChatReranker(chatClient)
	retrievalService := retrievalusecase.NewService(knowledgeRepo, providerRepo, retrievalStore, embeddingClient, reranker, secretBox)

	providerService := providerusecase.NewService(providerRepo, auditRepo, secretBox, llm.NewHTTPProviderTester())
	auditService := auditusecase.NewService(auditRepo)
	memoryService := memoryusecase.NewServiceWithCacheAndRetriever(memoryRepo, memoryCache, memoryRetrievalStore)
	toolService := toolusecase.NewService(toolDefinitionRepo)
	knowledgeService := knowledgeusecase.NewService(knowledgeRepo, documentRepo, chunkRepo, ingestionJobRepo, retrievalLogRepo, auditRepo, fileStorage, retrievalService, retrievalStore)
	jobQueue := infraDeps.JobQueue
	if jobQueue != nil {
		knowledgeService.WithJobQueue(jobQueue)
	}
	dialogService := dialogusecase.NewService(dialogRepo)
	chatService := chatusecase.NewService(providerRepo, dialogRepo, knowledgeRepo, conversationRepo, messageRepo, usageRepo, retrievalService, chatClient, secretBox)
	workflowService, err := agentusecase.NewService(workflowRepo, workflowProfileRepo, flowVersionRepo, runRepo, runEventRepo, nodeLogRepo, runStepRepo, workflowEvalRepo, approvalRepo, workflowTeamRepo, memoryRepo, memoryWriteLogRepo, memoryRetrievalStore, workingMemoryRepo, extractionJobRepo, mergeLogRepo, toolDefinitionRepo, toolPackRepo, mcpRepo, toolInvocationRepo, providerRepo, messageRepo, retrievalService, chatClient, secretBox)
	if err != nil {
		return nil, fmt.Errorf("init workflow service: %w", err)
	}

	healthHandler := handler.NewHealthHandler(db, redisClient, minioClient, esClient, cfg.MinIO.Bucket)
	authHandler := handler.NewAuthHandler(authService)
	oauthHandler := handler.NewOAuthHandler(authService)
	providerHandler := handler.NewProviderHandler(providerService, providerCatalog)
	auditHandler := handler.NewAuditHandler(auditService)
	memoryHandler := handler.NewMemoryHandler(memoryService)
	toolHandler := handler.NewToolHandler(toolService, toolPolicyRepo, toolPackRepo, mcpRepo)
	knowledgeHandler := handler.NewKnowledgeHandler(knowledgeService)
	documentHandler := handler.NewDocumentHandler(knowledgeService)
	dialogHandler := handler.NewDialogHandler(dialogService)
	chatHandler := handler.NewChatHandler(chatService)
	workflowHandler := handler.NewWorkflowHandler(workflowService)

	memoryScheduler := jobinfra.NewMemoryScheduler(memoryRepo, jobinfra.MemorySchedulerConfig{
		ConsolidationInterval: 1 * time.Hour,
		Logger:                log,
	})
	memoryScheduler.Start(ctx)

	router := httpserver.NewRouter(httpserver.RouterDeps{
		Logger:           log,
		HealthHandler:    healthHandler,
		AuthHandler:      authHandler,
		OAuthHandler:     oauthHandler,
		ProviderHandler:  providerHandler,
		MemoryHandler:    memoryHandler,
		ToolHandler:      toolHandler,
		AuditHandler:     auditHandler,
		KnowledgeHandler: knowledgeHandler,
		DocumentHandler:  documentHandler,
		DialogHandler:    dialogHandler,
		ChatHandler:      chatHandler,
		WorkflowHandler:  workflowHandler,
		AuthService:      authService,
		APITokens:        apiTokenRepo,
	})

	return &App{Config: cfg, Logger: log, Router: router}, nil
}
