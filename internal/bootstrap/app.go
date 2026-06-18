package bootstrap

import (
	"context"
	"fmt"
	"log/slog"

	auditusecase "agentcanvas/internal/application/audit_usecase"
	authusecase "agentcanvas/internal/application/auth_usecase"
	knowledgeusecase "agentcanvas/internal/application/knowledge_usecase"
	providerusecase "agentcanvas/internal/application/provider_usecase"
	cryptoinfra "agentcanvas/internal/infrastructure/crypto"
	esinfra "agentcanvas/internal/infrastructure/elasticsearch"
	"agentcanvas/internal/infrastructure/llm"
	minioinfra "agentcanvas/internal/infrastructure/minio"
	mysqlinfra "agentcanvas/internal/infrastructure/mysql"
	oauthinfra "agentcanvas/internal/infrastructure/oauth"
	redisinfra "agentcanvas/internal/infrastructure/redis"
	esretrieval "agentcanvas/internal/infrastructure/retrieval/elasticsearch"
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
	if err := esStore.EnsureIndex(ctx); err != nil {
		return nil, fmt.Errorf("ensure elasticsearch chunk index: %w", err)
	}

	secretBox, err := cryptoinfra.NewSecretBox(cfg.Security.SecretEncryptKey)
	if err != nil {
		return nil, fmt.Errorf("init secret box: %w", err)
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
	fileStorage := minioinfra.NewFileStorage(minioClient, cfg.MinIO.Bucket)

	jwtService := cryptoinfra.NewJWTService(cfg.Security.JWTSecret, cfg.Security.AccessTokenTTL())
	tokenHasher := cryptoinfra.NewTokenHasher(cfg.Security.RefreshTokenPepper)
	passwordHasher := cryptoinfra.NewPasswordHasher(0)
	githubClient := oauthinfra.NewGitHubClient(cfg.OAuth.GitHub.ClientID, cfg.OAuth.GitHub.ClientSecret, cfg.OAuth.GitHub.RedirectURL, cfg.OAuth.GitHub.Scopes)

	authService := authusecase.NewService(userRepo, oauthRepo, sessionRepo, apiTokenRepo, auditRepo, passwordHasher, jwtService, tokenHasher, redisClient, githubClient, cfg.Security.RefreshTokenTTL())
	providerService := providerusecase.NewService(providerRepo, auditRepo, secretBox, llm.NewHTTPProviderTester())
	auditService := auditusecase.NewService(auditRepo)
	knowledgeService := knowledgeusecase.NewService(knowledgeRepo, documentRepo, chunkRepo, ingestionJobRepo, retrievalLogRepo, auditRepo, fileStorage, esStore, esStore)

	healthHandler := handler.NewHealthHandler(db, redisClient, minioClient, esClient, cfg.MinIO.Bucket)
	authHandler := handler.NewAuthHandler(authService)
	oauthHandler := handler.NewOAuthHandler(authService)
	providerHandler := handler.NewProviderHandler(providerService)
	auditHandler := handler.NewAuditHandler(auditService)
	knowledgeHandler := handler.NewKnowledgeHandler(knowledgeService)
	documentHandler := handler.NewDocumentHandler(knowledgeService)

	router := httpserver.NewRouter(httpserver.RouterDeps{
		Logger:           log,
		HealthHandler:    healthHandler,
		AuthHandler:      authHandler,
		OAuthHandler:     oauthHandler,
		ProviderHandler:  providerHandler,
		AuditHandler:     auditHandler,
		KnowledgeHandler: knowledgeHandler,
		DocumentHandler:  documentHandler,
		AuthService:      authService,
		APITokens:        apiTokenRepo,
	})

	return &App{Config: cfg, Logger: log, Router: router}, nil
}
