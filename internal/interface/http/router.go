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
	SkillHandler     *handler.SkillHandler
	AuditHandler     *handler.AuditHandler
	KnowledgeHandler *handler.KnowledgeHandler
	DocumentHandler  *handler.DocumentHandler
	DialogHandler    *handler.DialogHandler
	ChatHandler      *handler.ChatHandler
	WorkflowHandler  *handler.WorkflowHandler
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
			registerCRUD(protected, "/model-providers", ":id", deps.ProviderHandler.Create, deps.ProviderHandler.List, deps.ProviderHandler.Get, deps.ProviderHandler.Update, deps.ProviderHandler.Delete)
			protected.POST("/model-providers/:id/test", deps.ProviderHandler.Test)

			protected.GET("/api-tokens", deps.AuthHandler.ListAPITokens)
			protected.POST("/api-tokens", deps.AuthHandler.CreateAPIToken)
			protected.DELETE("/api-tokens/:id", deps.AuthHandler.DeleteAPIToken)

			protected.GET("/audit-logs", deps.AuditHandler.List)

			registerCRUD(protected, "/memories", ":id", deps.MemoryHandler.Create, deps.MemoryHandler.List, deps.MemoryHandler.Get, deps.MemoryHandler.Update, deps.MemoryHandler.Delete)

			registerCRUD(protected, "/tool-definitions", ":id", deps.ToolHandler.Create, deps.ToolHandler.List, deps.ToolHandler.Get, deps.ToolHandler.Update, deps.ToolHandler.Delete)
			protected.POST("/tool-definitions/:id/test", deps.ToolHandler.Test)
			registerCRUD(protected, "/skills", ":id", deps.SkillHandler.Create, deps.SkillHandler.List, deps.SkillHandler.Get, deps.SkillHandler.Update, deps.SkillHandler.Delete)
			protected.POST("/skills/:id/validate", deps.SkillHandler.Validate)
			registerCRUD(protected, "/tool-policies", ":id", deps.ToolHandler.CreatePolicy, deps.ToolHandler.ListPolicies, deps.ToolHandler.GetPolicy, deps.ToolHandler.UpdatePolicy, deps.ToolHandler.DeletePolicy)
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

			registerCRUD(protected, "/dialogs", ":dialog_id", deps.DialogHandler.Create, deps.DialogHandler.List, deps.DialogHandler.Get, deps.DialogHandler.Update, deps.DialogHandler.Delete)
			protected.POST("/dialogs/:dialog_id/rag/chat", deps.ChatHandler.Chat)
			protected.POST("/dialogs/:dialog_id/rag/chat/stream", deps.ChatHandler.StreamChat)
			protected.GET("/dialogs/:dialog_id/conversations", deps.ChatHandler.ListConversations)
			protected.GET("/dialogs/:dialog_id/conversations/:id", deps.ChatHandler.GetConversation)
			protected.GET("/dialogs/:dialog_id/conversations/:id/messages", deps.ChatHandler.ListMessages)
			protected.DELETE("/dialogs/:dialog_id/conversations/:id", deps.ChatHandler.DeleteConversation)

			registerCRUD(protected, "/workflows", ":id", deps.WorkflowHandler.Create, deps.WorkflowHandler.List, deps.WorkflowHandler.Get, deps.WorkflowHandler.Update, deps.WorkflowHandler.Delete)
			protected.GET("/workflows/:id/profile", deps.WorkflowHandler.GetProfile)
			protected.PATCH("/workflows/:id/profile", deps.WorkflowHandler.UpdateProfile)
			protected.POST("/workflows/:id/eval-datasets", deps.WorkflowHandler.CreateEvalDataset)
			protected.GET("/workflows/:id/eval-datasets", deps.WorkflowHandler.ListEvalDatasets)
			protected.POST("/eval-datasets/:id/cases", deps.WorkflowHandler.CreateEvalCase)
			protected.GET("/eval-datasets/:id/cases", deps.WorkflowHandler.ListEvalCases)
			protected.POST("/eval-datasets/:id/runs", deps.WorkflowHandler.RunEvalDataset)
			protected.GET("/eval-datasets/:id/runs", deps.WorkflowHandler.ListEvalRuns)
			protected.GET("/eval-datasets/:id/trend", deps.WorkflowHandler.GetEvalTrend)
			protected.GET("/eval-runs/:id/results", deps.WorkflowHandler.ListEvalResults)
			protected.POST("/workflow-teams", deps.WorkflowHandler.CreateTeam)
			protected.GET("/workflow-teams", deps.WorkflowHandler.ListTeams)
			protected.DELETE("/workflow-teams/:id", deps.WorkflowHandler.DeleteTeam)
			protected.POST("/workflow-teams/:id/members", deps.WorkflowHandler.AddTeamMember)
			protected.GET("/workflow-teams/:id/members", deps.WorkflowHandler.ListTeamMembers)
			protected.DELETE("/workflow-teams/:id/members/:workflow_id", deps.WorkflowHandler.RemoveTeamMember)
			protected.POST("/workflows/:id/flow-versions", deps.WorkflowHandler.CreateWorkflowVersion)
			protected.GET("/workflows/:id/flow-versions", deps.WorkflowHandler.ListWorkflowVersions)
			protected.GET("/flow-versions/:id", deps.WorkflowHandler.GetWorkflowVersion)
			protected.POST("/flow-versions/:id/publish", deps.WorkflowHandler.PublishWorkflowVersion)
			protected.POST("/flow-versions/:id/validate", deps.WorkflowHandler.ValidateWorkflowVersion)
			protected.POST("/workflows/:id/runs", deps.WorkflowHandler.Run)
			protected.POST("/workflows/:id/runs/stream", deps.WorkflowHandler.StreamRun)
			protected.GET("/runs/:id", deps.WorkflowHandler.GetRun)
			protected.GET("/runs/:id/events", deps.WorkflowHandler.ListRunEvents)
			protected.GET("/runs/:id/children", deps.WorkflowHandler.ListChildRuns)
			protected.GET("/runs/:id/node-logs", deps.WorkflowHandler.ListNodeLogs)
			protected.GET("/runs/:id/steps", deps.WorkflowHandler.ListRunSteps)
			protected.GET("/runs/:id/memory-write-logs", deps.WorkflowHandler.ListMemoryWriteLogs)
			protected.GET("/runs/:id/tool-invocations", deps.WorkflowHandler.ListToolInvocations)
			protected.GET("/runs/:id/trace", deps.WorkflowHandler.GetRunTrace)
			protected.POST("/runs/:id/cancel", deps.WorkflowHandler.CancelRun)
			protected.POST("/runs/:id/pause", deps.WorkflowHandler.PauseRun)
			protected.POST("/runs/:id/resume", deps.WorkflowHandler.ResumeRun)
			protected.GET("/approval-requests", deps.WorkflowHandler.ListApprovalRequests)
			protected.POST("/approval-requests/:id/approve", deps.WorkflowHandler.ApproveRequest)
			protected.POST("/approval-requests/:id/reject", deps.WorkflowHandler.RejectRequest)
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
		c.Data(http.StatusOK, "text/html; charset=utf-8", index)
	})
}
