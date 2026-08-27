package httpserver

import (
	"io/fs"
	"net/http"
	"strings"

	authusecase "agentcanvas/internal/application/auth_usecase"
	"agentcanvas/internal/domain/audit"
	authdomain "agentcanvas/internal/domain/auth"
	"log/slog"

	"agentcanvas/internal/interface/http/handler"
	"agentcanvas/internal/interface/http/middleware"
	webui "agentcanvas/web"

	"github.com/gin-gonic/gin"
)

type RouterDeps struct {
	Logger            *slog.Logger
	HealthHandler     *handler.HealthHandler
	AuthHandler       *handler.AuthHandler
	OAuthHandler      *handler.OAuthHandler
	ProviderHandler   *handler.ProviderHandler
	MemoryHandler     *handler.MemoryHandler
	ReflectionHandler *handler.ReflectionHandler
	ToolHandler       *handler.ToolHandler
	SkillHandler      *handler.SkillHandler
	AuditHandler      *handler.AuditHandler
	KnowledgeHandler  *handler.KnowledgeHandler
	DocumentHandler   *handler.DocumentHandler
	AgentHandler      *handler.AgentHandler
	ProjectHandler    *handler.ProjectHandler
	ResourceHandler   *handler.ResourceHandler
	AuthService       *authusecase.Service
	APITokens         authdomain.APITokenRepository
	Audits            audit.Repository
	CORSOrigins       []string
}

func NewRouter(deps RouterDeps) *gin.Engine {
	r := gin.New()

	// middleware
	r.Use(middleware.RequestID())
	r.Use(middleware.Recovery(deps.Logger))
	r.Use(middleware.CORS(deps.CORSOrigins))

	v1 := r.Group("/api/v1")
	{
		v1.GET("/health", deps.HealthHandler.Health)
		v1.GET("/health/mysql", deps.HealthHandler.MySQL)
		v1.GET("/health/redis", deps.HealthHandler.Redis)
		v1.GET("/health/minio", deps.HealthHandler.MinIO)
		v1.GET("/health/es", deps.HealthHandler.Elasticsearch)
		v1.GET("/health/reflection-system", deps.HealthHandler.ReflectionSystem)
		v1.GET("/health/context-system", deps.HealthHandler.ContextSystem)
		v1.GET("/health/memory-system", deps.HealthHandler.MemorySystem)

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
		protected.Use(middleware.Auth(deps.AuthService, deps.APITokens, deps.Audits))
		protected.Use(middleware.RequireRouteScope())
		{
			protected.GET("/auth/me", deps.AuthHandler.Me)
			protected.GET("/resource-summaries/:kind", deps.ResourceHandler.List)

			registerCRUD(protected, "/agents", ":id", deps.AgentHandler.Create, deps.AgentHandler.List, deps.AgentHandler.Get, deps.AgentHandler.Update, deps.AgentHandler.Delete)
			registerCRUD(protected, "/projects", ":id", deps.ProjectHandler.Create, deps.ProjectHandler.List, deps.ProjectHandler.Get, deps.ProjectHandler.Update, deps.ProjectHandler.Delete)
			protected.POST("/projects/:id/folders", deps.ProjectHandler.AddFolder)
			protected.GET("/projects/:id/folders", deps.ProjectHandler.ListFolders)
			protected.DELETE("/projects/:id/folders/:folder_id", deps.ProjectHandler.DeleteFolder)
			protected.GET("/projects/:id/git/status", deps.ProjectHandler.GitStatus)
			protected.GET("/projects/:id/git/branches", deps.ProjectHandler.GitBranches)
			protected.GET("/projects/:id/git/worktrees", deps.ProjectHandler.GitWorktrees)
			protected.PATCH("/agents/:id/settings", deps.AgentHandler.UpdateSettings)
			protected.POST("/agents/:id/conversations", deps.AgentHandler.CreateConversation)
			protected.GET("/agents/:id/conversations", deps.AgentHandler.ListConversations)
			protected.PATCH("/agents/:id/conversations/:conversation_id/mode", deps.AgentHandler.UpdateConversationMode)
			protected.GET("/agents/:id/conversations/:conversation_id/messages", deps.AgentHandler.ListMessages)
			protected.POST("/agents/:id/conversations/:conversation_id/turns", deps.AgentHandler.StartTurn)
			protected.POST("/agents/:id/conversations/:conversation_id/compact", deps.AgentHandler.CompactConversation)
			protected.GET("/agents/:id/conversations/:conversation_id/turns/latest", deps.AgentHandler.GetLatestTurn)
			protected.POST("/agents/:id/conversations/:conversation_id/fork", deps.AgentHandler.ForkConversation)
			protected.GET("/agents/:id/conversations/:conversation_id/goal", deps.AgentHandler.GetGoal)
			protected.PUT("/agents/:id/conversations/:conversation_id/goal", deps.AgentHandler.SetGoal)
			protected.DELETE("/agents/:id/conversations/:conversation_id/goal", deps.AgentHandler.ClearGoal)
			protected.GET("/agents/:id/conversations/:conversation_id/goal/events", deps.AgentHandler.StreamGoalEvents)
			protected.DELETE("/agents/:id/conversations/:conversation_id", deps.AgentHandler.DeleteConversation)
			protected.GET("/agent-turns/:id", deps.AgentHandler.GetTurn)
			protected.GET("/agents/:id/session-search", deps.AgentHandler.SearchSessions)
			protected.GET("/agents/:id/improvement-reviews", deps.AgentHandler.ListImprovementReviews)
			protected.GET("/agents/:id/change-proposals", deps.AgentHandler.ListChangeProposals)
			protected.GET("/agents/:id/reflections", deps.ReflectionHandler.List)
			protected.PATCH("/agents/:id/reflections/:reflection_id", deps.ReflectionHandler.SetStatus)
			protected.POST("/agent-change-proposals/:id/approve", deps.AgentHandler.ApproveChangeProposal)
			protected.POST("/agent-change-proposals/:id/reject", deps.AgentHandler.RejectChangeProposal)
			protected.GET("/runs/:id", deps.AgentHandler.GetRun)
			protected.GET("/runs/:id/events", deps.AgentHandler.ListRunEvents)
			protected.GET("/runs/:id/events/stream", deps.AgentHandler.StreamRunEvents)
			protected.GET("/runs/:id/events/stream/v1", deps.AgentHandler.StreamRunEventsV1)
			protected.GET("/runs/:id/children", deps.AgentHandler.ListChildRuns)
			protected.GET("/runs/:id/steps", deps.AgentHandler.ListRunSteps)
			protected.GET("/runs/:id/trace", deps.AgentHandler.GetRunTrace)
			protected.GET("/runs/:id/workspace", deps.AgentHandler.GetRunWorkspace)
			protected.GET("/runs/:id/git/status", deps.AgentHandler.RunGitStatus)
			protected.GET("/runs/:id/git/diff", deps.AgentHandler.RunGitDiff)
			protected.GET("/runs/:id/git/log", deps.AgentHandler.RunGitLog)
			protected.POST("/runs/:id/git/commit", deps.AgentHandler.RunGitCommit)
			protected.POST("/workspaces/:id/cleanup", deps.AgentHandler.CleanupWorkspace)
			protected.POST("/workspaces/:id/refresh", deps.AgentHandler.RefreshWorkspace)
			protected.POST("/runs/:id/cancel", deps.AgentHandler.CancelRun)
			protected.POST("/runs/:id/resume", deps.AgentHandler.ResumeRun)
			protected.POST("/runs/:id/reflections/:reflection_id/feedback", deps.ReflectionHandler.Feedback)
			protected.GET("/approval-requests", deps.AgentHandler.ListApprovalRequests)
			protected.POST("/approval-requests/:id/approve", deps.AgentHandler.ApproveRequest)
			protected.POST("/approval-requests/:id/reject", deps.AgentHandler.RejectRequest)

			protected.GET("/provider-catalog", deps.ProviderHandler.ListCatalog)
			registerCRUD(protected, "/model-providers", ":id", deps.ProviderHandler.Create, deps.ProviderHandler.List, deps.ProviderHandler.Get, deps.ProviderHandler.Update, deps.ProviderHandler.Delete)
			protected.POST("/model-providers/:id/test", deps.ProviderHandler.Test)

			protected.GET("/api-tokens", deps.AuthHandler.ListAPITokens)
			protected.POST("/api-tokens", deps.AuthHandler.CreateAPIToken)
			protected.DELETE("/api-tokens/:id", deps.AuthHandler.DeleteAPIToken)

			protected.GET("/audit-logs", deps.AuditHandler.List)

			// Durable memory is owned by the asynchronous Codex consolidation
			// pipeline. Keep legacy SQL memory endpoints read-only for migration
			// and audit visibility; no HTTP route may mutate durable memory or
			// approve a memory proposal.
			protected.GET("/memories", deps.MemoryHandler.List)
			protected.GET("/memories/:id", deps.MemoryHandler.Get)
			protected.GET("/memory-recall-logs", deps.MemoryHandler.ListRecallLogs)
			protected.POST("/memory-recall-logs/:id/feedback", deps.MemoryHandler.SetRecallFeedback)

			registerCRUD(protected, "/tool-definitions", ":id", deps.ToolHandler.Create, deps.ToolHandler.List, deps.ToolHandler.Get, deps.ToolHandler.Update, deps.ToolHandler.Delete)
			protected.POST("/tool-definitions/:id/test", deps.ToolHandler.Test)
			registerCRUD(protected, "/skills", ":id", deps.SkillHandler.Create, deps.SkillHandler.List, deps.SkillHandler.Get, deps.SkillHandler.Update, deps.SkillHandler.Delete)
			protected.POST("/skills/:id/validate", deps.SkillHandler.Validate)
			registerCRUD(protected, "/tool-packs", ":id", deps.ToolHandler.CreatePack, deps.ToolHandler.ListPacks, deps.ToolHandler.GetPack, deps.ToolHandler.UpdatePack, deps.ToolHandler.DeletePack)
			protected.POST("/tool-packs/:id/items", deps.ToolHandler.AddPackItem)
			protected.DELETE("/tool-packs/:id/items", deps.ToolHandler.RemovePackItem)
			protected.GET("/tool-packs/:id/items", deps.ToolHandler.ListPackItems)
			registerCRUD(protected, "/mcp-servers", ":id", deps.ToolHandler.CreateMCPServer, deps.ToolHandler.ListMCPServers, deps.ToolHandler.GetMCPServer, deps.ToolHandler.UpdateMCPServer, deps.ToolHandler.DeleteMCPServer)
			protected.POST("/mcp-servers/:id/refresh", deps.ToolHandler.RefreshMCPServer)
			protected.GET("/mcp-servers/:id/tools", deps.ToolHandler.ListMCPTools)

			registerCRUD(protected, "/knowledge-bases", ":id", deps.KnowledgeHandler.Create, deps.KnowledgeHandler.List, deps.KnowledgeHandler.Get, deps.KnowledgeHandler.Update, deps.KnowledgeHandler.Delete)
			protected.POST("/knowledge-bases/:id/reindex", deps.KnowledgeHandler.Reindex)
			protected.POST("/knowledge-bases/:id/documents", deps.KnowledgeHandler.UploadDocument)
			protected.GET("/knowledge-bases/:id/documents", deps.KnowledgeHandler.ListDocuments)
			protected.POST("/knowledge-bases/:id/search", deps.KnowledgeHandler.Search)

			protected.GET("/documents/:id", deps.DocumentHandler.Get)
			protected.PATCH("/documents/:id", deps.DocumentHandler.SetEnabled)
			protected.DELETE("/documents/:id", deps.DocumentHandler.Delete)
			protected.GET("/documents/:id/chunks", deps.DocumentHandler.ListChunks)

			protected.GET("/ingestion-jobs/:id", deps.KnowledgeHandler.GetIngestionJob)

		}
	}

	registerWebUI(r)

	return r
}

func registerCRUD(rg *gin.RouterGroup, basePath string, idSegment string, create, list, get, update, delete gin.HandlerFunc) {
	rg.POST(basePath, create)
	rg.GET(basePath, list)
	itemPath := strings.TrimRight(basePath, "/") + "/" + strings.TrimLeft(idSegment, "/")
	rg.GET(itemPath, get)
	rg.PATCH(itemPath, update)
	rg.DELETE(itemPath, delete)
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
		if isLegacyAgentPage(c.Request.URL.Path) {
			c.Redirect(http.StatusFound, "/app/agents")
			return
		}
		c.Data(http.StatusOK, "text/html; charset=utf-8", index)
	})
}

func isLegacyAgentPage(path string) bool {
	path = strings.ToLower(strings.TrimSpace(path))
	legacyFlow := "/" + "work" + "flow"
	return strings.HasPrefix(path, legacyFlow) || strings.HasPrefix(path, "/flow-") || strings.HasPrefix(path, "/eval-") || strings.HasPrefix(path, "/canvas")
}
