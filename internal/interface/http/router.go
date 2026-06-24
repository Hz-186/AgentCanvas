package httpserver

import (
	"io/fs"
	"net/http"
	"strings"

	authusecase "agentcanvas/internal/application/auth_usecase"
	authdomain "agentcanvas/internal/domain/auth"
	"log/slog"

	"agentcanvas/internal/interface/http/handler"
	"agentcanvas/internal/interface/http/middleware"
	webui "agentcanvas/web"

	"github.com/gin-gonic/gin"
)

type RouterDeps struct {
	Logger           *slog.Logger
	HealthHandler    *handler.HealthHandler
	AuthHandler      *handler.AuthHandler
	OAuthHandler     *handler.OAuthHandler
	ProviderHandler  *handler.ProviderHandler
	MemoryHandler    *handler.MemoryHandler
	ToolHandler      *handler.ToolHandler
	AuditHandler     *handler.AuditHandler
	KnowledgeHandler *handler.KnowledgeHandler
	DocumentHandler  *handler.DocumentHandler
	DialogHandler    *handler.DialogHandler
	ChatHandler      *handler.ChatHandler
	AgentHandler     *handler.AgentHandler
	AuthService      *authusecase.Service
	APITokens        authdomain.APITokenRepository
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

			protected.GET("/memories", deps.MemoryHandler.List)
			protected.POST("/memories", deps.MemoryHandler.Create)
			protected.GET("/memories/:id", deps.MemoryHandler.Get)
			protected.PATCH("/memories/:id", deps.MemoryHandler.Update)
			protected.DELETE("/memories/:id", deps.MemoryHandler.Delete)

			protected.GET("/tool-definitions", deps.ToolHandler.List)
			protected.POST("/tool-definitions", deps.ToolHandler.Create)
			protected.GET("/tool-definitions/:id", deps.ToolHandler.Get)
			protected.PATCH("/tool-definitions/:id", deps.ToolHandler.Update)
			protected.DELETE("/tool-definitions/:id", deps.ToolHandler.Delete)
			protected.POST("/tool-definitions/:id/test", deps.ToolHandler.Test)

			protected.POST("/knowledge-bases", deps.KnowledgeHandler.Create)
			protected.GET("/knowledge-bases", deps.KnowledgeHandler.List)
			protected.GET("/knowledge-bases/:id", deps.KnowledgeHandler.Get)
			protected.PATCH("/knowledge-bases/:id", deps.KnowledgeHandler.Update)
			protected.DELETE("/knowledge-bases/:id", deps.KnowledgeHandler.Delete)
			protected.POST("/knowledge-bases/:id/reindex", deps.KnowledgeHandler.Reindex)
			protected.POST("/knowledge-bases/:id/documents", deps.KnowledgeHandler.UploadDocument)
			protected.GET("/knowledge-bases/:id/documents", deps.KnowledgeHandler.ListDocuments)
			protected.POST("/knowledge-bases/:id/search", deps.KnowledgeHandler.Search)

			protected.GET("/documents/:id", deps.DocumentHandler.Get)
			protected.DELETE("/documents/:id", deps.DocumentHandler.Delete)
			protected.GET("/documents/:id/chunks", deps.DocumentHandler.ListChunks)

			protected.GET("/ingestion-jobs/:id", deps.KnowledgeHandler.GetIngestionJob)

			protected.POST("/dialogs", deps.DialogHandler.Create)
			protected.GET("/dialogs", deps.DialogHandler.List)
			protected.GET("/dialogs/:id", deps.DialogHandler.Get)
			protected.PATCH("/dialogs/:id", deps.DialogHandler.Update)
			protected.DELETE("/dialogs/:id", deps.DialogHandler.Delete)
			protected.POST("/dialogs/:dialog_id/rag/chat", deps.ChatHandler.Chat)
			protected.POST("/dialogs/:dialog_id/rag/chat/stream", deps.ChatHandler.StreamChat)
			protected.GET("/dialogs/:dialog_id/conversations", deps.ChatHandler.ListConversations)
			protected.GET("/dialogs/:dialog_id/conversations/:id", deps.ChatHandler.GetConversation)
			protected.GET("/dialogs/:dialog_id/conversations/:id/messages", deps.ChatHandler.ListMessages)
			protected.DELETE("/dialogs/:dialog_id/conversations/:id", deps.ChatHandler.DeleteConversation)

			protected.POST("/agents", deps.AgentHandler.Create)
			protected.GET("/agents", deps.AgentHandler.List)
			protected.GET("/agents/:id", deps.AgentHandler.Get)
			protected.PATCH("/agents/:id", deps.AgentHandler.Update)
			protected.DELETE("/agents/:id", deps.AgentHandler.Delete)
			protected.POST("/agents/:id/flow-versions", deps.AgentHandler.CreateFlowVersion)
			protected.GET("/agents/:id/flow-versions", deps.AgentHandler.ListFlowVersions)
			protected.GET("/flow-versions/:id", deps.AgentHandler.GetFlowVersion)
			protected.POST("/flow-versions/:id/publish", deps.AgentHandler.PublishFlowVersion)
			protected.POST("/flow-versions/:id/validate", deps.AgentHandler.ValidateFlowVersion)
			protected.POST("/agents/:id/runs", deps.AgentHandler.Run)
			protected.POST("/agents/:id/runs/stream", deps.AgentHandler.StreamRun)
			protected.GET("/runs/:id", deps.AgentHandler.GetRun)
			protected.GET("/runs/:id/events", deps.AgentHandler.ListRunEvents)
			protected.GET("/runs/:id/node-logs", deps.AgentHandler.ListNodeLogs)
			protected.GET("/runs/:id/memory-write-logs", deps.AgentHandler.ListMemoryWriteLogs)
			protected.GET("/runs/:id/tool-invocations", deps.AgentHandler.ListToolInvocations)
			protected.POST("/runs/:id/cancel", deps.AgentHandler.CancelRun)
		}
	}

	registerWebUI(r)

	return r
}

func registerWebUI(r *gin.Engine) {
	dist, err := fs.Sub(webui.Files, "dist")
	if err != nil {
		return
	}
	files := http.FS(dist)
	fileServer := http.FileServer(files)

	r.GET("/", gin.WrapH(fileServer))
	r.GET("/assets/*filepath", gin.WrapH(fileServer))
	r.NoRoute(func(c *gin.Context) {
		if strings.HasPrefix(c.Request.URL.Path, "/api/") {
			c.JSON(http.StatusNotFound, gin.H{"code": 404, "message": "not found", "data": nil})
			return
		}
		c.FileFromFS("index.html", files)
	})
}
