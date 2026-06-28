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
			authGroup.POST("/oauth/exchange", deps.OAuthHandler.ExchangeCode)
		}

		protected := v1.Group("")
		protected.Use(middleware.Auth(deps.AuthService, deps.APITokens))
		{
			protected.GET("/auth/me", deps.AuthHandler.Me)

			protected.GET("/provider-catalog", deps.ProviderHandler.ListCatalog)
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
			protected.POST("/tool-policies", deps.ToolHandler.CreatePolicy)
			protected.GET("/tool-policies", deps.ToolHandler.ListPolicies)
			protected.GET("/tool-policies/:id", deps.ToolHandler.GetPolicy)
			protected.PATCH("/tool-policies/:id", deps.ToolHandler.UpdatePolicy)
			protected.DELETE("/tool-policies/:id", deps.ToolHandler.DeletePolicy)
			protected.POST("/tool-packs", deps.ToolHandler.CreatePack)
			protected.GET("/tool-packs", deps.ToolHandler.ListPacks)
			protected.GET("/tool-packs/:id", deps.ToolHandler.GetPack)
			protected.PATCH("/tool-packs/:id", deps.ToolHandler.UpdatePack)
			protected.DELETE("/tool-packs/:id", deps.ToolHandler.DeletePack)
			protected.POST("/tool-packs/:pack_id/items", deps.ToolHandler.AddPackItem)
			protected.DELETE("/tool-packs/:pack_id/items", deps.ToolHandler.RemovePackItem)
			protected.GET("/tool-packs/:pack_id/items", deps.ToolHandler.ListPackItems)
			protected.POST("/mcp-servers", deps.ToolHandler.CreateMCPServer)
			protected.GET("/mcp-servers", deps.ToolHandler.ListMCPServers)
			protected.GET("/mcp-servers/:id", deps.ToolHandler.GetMCPServer)
			protected.PATCH("/mcp-servers/:id", deps.ToolHandler.UpdateMCPServer)
			protected.DELETE("/mcp-servers/:id", deps.ToolHandler.DeleteMCPServer)
			protected.POST("/mcp-servers/:id/refresh", deps.ToolHandler.RefreshMCPServer)
			protected.GET("/mcp-servers/:id/tools", deps.ToolHandler.ListMCPTools)

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
			protected.PATCH("/documents/:id", deps.DocumentHandler.SetEnabled)
			protected.DELETE("/documents/:id", deps.DocumentHandler.Delete)
			protected.GET("/documents/:id/chunks", deps.DocumentHandler.ListChunks)

			protected.GET("/ingestion-jobs/:id", deps.KnowledgeHandler.GetIngestionJob)

			protected.POST("/dialogs", deps.DialogHandler.Create)
			protected.GET("/dialogs", deps.DialogHandler.List)
			protected.GET("/dialogs/:dialog_id", deps.DialogHandler.Get)
			protected.PATCH("/dialogs/:dialog_id", deps.DialogHandler.Update)
			protected.DELETE("/dialogs/:dialog_id", deps.DialogHandler.Delete)
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
			protected.GET("/agents/:id/profile", deps.AgentHandler.GetProfile)
			protected.PATCH("/agents/:id/profile", deps.AgentHandler.UpdateProfile)
			protected.POST("/agents/:id/eval-datasets", deps.AgentHandler.CreateEvalDataset)
			protected.GET("/agents/:id/eval-datasets", deps.AgentHandler.ListEvalDatasets)
			protected.POST("/eval-datasets/:id/cases", deps.AgentHandler.CreateEvalCase)
			protected.GET("/eval-datasets/:id/cases", deps.AgentHandler.ListEvalCases)
			protected.POST("/eval-datasets/:id/runs", deps.AgentHandler.RunEvalDataset)
			protected.GET("/eval-datasets/:id/runs", deps.AgentHandler.ListEvalRuns)
			protected.GET("/eval-runs/:id/results", deps.AgentHandler.ListEvalResults)
			protected.POST("/agent-teams", deps.AgentHandler.CreateTeam)
			protected.GET("/agent-teams", deps.AgentHandler.ListTeams)
			protected.DELETE("/agent-teams/:id", deps.AgentHandler.DeleteTeam)
			protected.POST("/agent-teams/:id/members", deps.AgentHandler.AddTeamMember)
			protected.GET("/agent-teams/:id/members", deps.AgentHandler.ListTeamMembers)
			protected.DELETE("/agent-teams/:id/members/:agent_id", deps.AgentHandler.RemoveTeamMember)
			protected.POST("/agents/:id/flow-versions", deps.AgentHandler.CreateFlowVersion)
			protected.GET("/agents/:id/flow-versions", deps.AgentHandler.ListFlowVersions)
			protected.GET("/flow-versions/:id", deps.AgentHandler.GetFlowVersion)
			protected.POST("/flow-versions/:id/publish", deps.AgentHandler.PublishFlowVersion)
			protected.POST("/flow-versions/:id/validate", deps.AgentHandler.ValidateFlowVersion)
			protected.POST("/agents/:id/runs", deps.AgentHandler.Run)
			protected.POST("/agents/:id/runs/stream", deps.AgentHandler.StreamRun)
			protected.GET("/runs/:id", deps.AgentHandler.GetRun)
			protected.GET("/runs/:id/events", deps.AgentHandler.ListRunEvents)
			protected.GET("/runs/:id/children", deps.AgentHandler.ListChildRuns)
			protected.GET("/runs/:id/node-logs", deps.AgentHandler.ListNodeLogs)
			protected.GET("/runs/:id/steps", deps.AgentHandler.ListRunSteps)
			protected.GET("/runs/:id/memory-write-logs", deps.AgentHandler.ListMemoryWriteLogs)
			protected.GET("/runs/:id/tool-invocations", deps.AgentHandler.ListToolInvocations)
			protected.POST("/runs/:id/cancel", deps.AgentHandler.CancelRun)
			protected.POST("/runs/:id/pause", deps.AgentHandler.PauseRun)
			protected.POST("/runs/:id/resume", deps.AgentHandler.ResumeRun)
			protected.GET("/approval-requests", deps.AgentHandler.ListApprovalRequests)
			protected.POST("/approval-requests/:id/approve", deps.AgentHandler.ApproveRequest)
			protected.POST("/approval-requests/:id/reject", deps.AgentHandler.RejectRequest)
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
	index, err := fs.ReadFile(dist, "index.html")
	if err != nil {
		return
	}

	r.GET("/", gin.WrapH(fileServer))
	r.GET("/assets/*filepath", gin.WrapH(fileServer))
	r.NoRoute(func(c *gin.Context) {
		if strings.HasPrefix(c.Request.URL.Path, "/api/") {
			c.JSON(http.StatusNotFound, gin.H{"code": 404, "message": "not found", "data": nil})
			return
		}
		c.Data(http.StatusOK, "text/html; charset=utf-8", index)
	})
}
