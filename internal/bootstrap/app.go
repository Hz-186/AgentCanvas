package bootstrap

import (
	"context"
	"fmt"
	"log/slog"

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
	"agentcanvas/internal/domain/retrieval"
	cataloginfra "agentcanvas/internal/infrastructure/catalog"
	cryptoinfra "agentcanvas/internal/infrastructure/crypto"
	esinfra "agentcanvas/internal/infrastructure/elasticsearch"
	"agentcanvas/internal/infrastructure/llm"
	minioinfra "agentcanvas/internal/infrastructure/minio"
	mysqlinfra "agentcanvas/internal/infrastructure/mysql"
	oauthinfra "agentcanvas/internal/infrastructure/oauth"
	queueinfra "agentcanvas/internal/infrastructure/queue"
	redisinfra "agentcanvas/internal/infrastructure/redis"
	compositeretrieval "agentcanvas/internal/infrastructure/retrieval/composite"
	esretrieval "agentcanvas/internal/infrastructure/retrieval/elasticsearch"
	milvusretrieval "agentcanvas/internal/infrastructure/retrieval/milvus"
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
	esStore := esretrieval.NewStore(esClient, cfg.Elasticsearch)
	var retrievalStore interface {
		EnsureIndex(context.Context) error
		IndexChunks(context.Context, []retrieval.ChunkIndexDocument) error
		SetDocumentEnabled(context.Context, int64, int64, bool) error
		DeleteByDocument(context.Context, int64, int64) error
		DeleteByKnowledgeBase(context.Context, int64, int64) error
		Search(context.Context, retrieval.RetrievalRequest) (*retrieval.RetrievalResponse, error)
	} = esStore
	if cfg.Milvus.Enabled {
		milvusVector := vectorstore.NewMilvusStore(cfg.Milvus.Address, cfg.Milvus.Token, milvusHNSW(cfg))
		milvusStore := milvusretrieval.NewStore(milvusVector, cfg.Milvus.Collection, cfg.Milvus.Dimensions, milvusHNSW(cfg))
		retrievalStore = compositeretrieval.New(esStore, milvusStore)
	}
	if err := retrievalStore.EnsureIndex(ctx); err != nil {
		return nil, fmt.Errorf("ensure elasticsearch chunk index: %w", err)
	}

	secretBox, err := cryptoinfra.NewSecretBox(cfg.Security.SecretEncryptKey)
	if err != nil {
		return nil, fmt.Errorf("init secret box: %w", err)
	}

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
	toolDefinitionRepo := mysqlinfra.NewToolDefinitionRepository(db)
	toolInvocationRepo := mysqlinfra.NewToolInvocationRepository(db)
	toolPolicyRepo := mysqlinfra.NewToolPolicyRepository(db)
	toolPackRepo := mysqlinfra.NewToolPackRepository(db)
	mcpRepo := mysqlinfra.NewMCPRepository(db)
	fileStorage := minioinfra.NewFileStorage(minioClient, cfg.MinIO.Bucket)

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
	memoryService := memoryusecase.NewService(memoryRepo)
	toolService := toolusecase.NewService(toolDefinitionRepo)
	knowledgeService := knowledgeusecase.NewService(knowledgeRepo, documentRepo, chunkRepo, ingestionJobRepo, retrievalLogRepo, auditRepo, fileStorage, retrievalService, retrievalStore)
	if cfg.Queue.Backend == "redis_stream" {
		knowledgeService.WithJobQueue(queueinfra.NewRedisStreamQueue(redisClient, cfg.Queue.RedisStream, cfg.Queue.RedisGroup, cfg.Queue.RedisConsumer))
	}
	dialogService := dialogusecase.NewService(dialogRepo)
	chatService := chatusecase.NewService(providerRepo, dialogRepo, knowledgeRepo, conversationRepo, messageRepo, usageRepo, retrievalService, chatClient, secretBox)
	workflowService := agentusecase.NewService(workflowRepo, workflowProfileRepo, flowVersionRepo, runRepo, runEventRepo, nodeLogRepo, runStepRepo, workflowEvalRepo, approvalRepo, workflowTeamRepo, memoryRepo, memoryWriteLogRepo, toolDefinitionRepo, toolPackRepo, mcpRepo, toolInvocationRepo, providerRepo, messageRepo, retrievalService, chatClient, secretBox)

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

func milvusHNSW(cfg *config.Config) vectorstore.HNSWConfig {
	return vectorstore.HNSWConfig{M: cfg.Milvus.M, EFConstruction: cfg.Milvus.EFConstruction, EFSearch: cfg.Milvus.EFSearch, MetricType: cfg.Milvus.MetricType}
}
