package httpserver

import (
	authusecase "agentcanvas/internal/application/auth_usecase"
	authdomain "agentcanvas/internal/domain/auth"
	"log/slog"

	"agentcanvas/internal/interface/http/handler"
	"agentcanvas/internal/interface/http/middleware"

	"github.com/gin-gonic/gin"
)

type RouterDeps struct {
	Logger          *slog.Logger
	HealthHandler   *handler.HealthHandler
	AuthHandler     *handler.AuthHandler
	OAuthHandler    *handler.OAuthHandler
	ProviderHandler *handler.ProviderHandler
	AuditHandler    *handler.AuditHandler
	AuthService     *authusecase.Service
	APITokens       authdomain.APITokenRepository
}

func NewRouter(deps RouterDeps) *gin.Engine {
	r := gin.New()

	// middleware
	r.Use(middleware.RequestID())
	r.Use(middleware.Recovery(deps.Logger))
	r.Use(middleware.CORS())

	v1 := r.Group("/api/v1")
	{
		v1.GET("/health", deps.HealthHandler.Health)
		v1.GET("/health/mysql", deps.HealthHandler.MySQL)
		v1.GET("/health/redis", deps.HealthHandler.Redis)
		v1.GET("/health/minio", deps.HealthHandler.MinIO)
		v1.GET("/health/es", deps.HealthHandler.Elasticsearch)

		authGroup := v1.Group("/auth")
		{
			authGroup.POST("/register", deps.AuthHandler.Register)
			authGroup.POST("/login", deps.AuthHandler.Login)
			authGroup.POST("/refresh", deps.AuthHandler.Refresh)
			authGroup.POST("/logout", deps.AuthHandler.Logout)
			authGroup.GET("/github/redirect", deps.OAuthHandler.GitHubRedirect)
			authGroup.GET("/github/callback", deps.OAuthHandler.GitHubCallback)
		}

		protected := v1.Group("")
		protected.Use(middleware.Auth(deps.AuthService, deps.APITokens))
		{
			protected.GET("/auth/me", deps.AuthHandler.Me)

			protected.GET("/model-providers", deps.ProviderHandler.List)
			protected.POST("/model-providers", deps.ProviderHandler.Create)
			protected.GET("/model-providers/:id", deps.ProviderHandler.Get)
			protected.PATCH("/model-providers/:id", deps.ProviderHandler.Update)
			protected.DELETE("/model-providers/:id", deps.ProviderHandler.Delete)
			protected.POST("/model-providers/:id/test", deps.ProviderHandler.Test)

			protected.GET("/api-tokens", deps.AuthHandler.ListAPITokens)
			protected.POST("/api-tokens", deps.AuthHandler.CreateAPIToken)
			protected.DELETE("/api-tokens/:id", deps.AuthHandler.DeleteAPIToken)

			protected.GET("/audit-logs", deps.AuditHandler.List)
		}
	}

	return r
}
