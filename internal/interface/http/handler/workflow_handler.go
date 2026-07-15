package handler

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	agentusecase "agentcanvas/internal/application/workflow_usecase"
	"agentcanvas/internal/domain/workflow"
	"agentcanvas/internal/interface/http/sse"
	agenterrors "agentcanvas/internal/pkg/errors"
	"agentcanvas/internal/pkg/response"
	runtimeevent "agentcanvas/internal/runtime/event"

	"github.com/gin-gonic/gin"
)

type WorkflowHandler struct {
	service  *agentusecase.Service
	ruleSets workflowRuleSetHTTPService
}

func NewWorkflowHandler(service *agentusecase.Service) *WorkflowHandler {
	return &WorkflowHandler{service: service, ruleSets: service}
}

type workflowRuleSetHTTPService interface {
	ListRuleSets(ctx context.Context, ownerID, workflowID int64) ([]workflow.RuleSet, error)
	CreateRuleSet(ctx context.Context, ownerID, workflowID int64, req agentusecase.CreateRuleSetRequest) (*workflow.RuleSet, error)
	GetRuleSet(ctx context.Context, ownerID, workflowID, ruleSetID int64) (*workflow.RuleSet, error)
	UpdateRuleSet(ctx context.Context, ownerID, workflowID, ruleSetID int64, req agentusecase.UpdateRuleSetRequest) (*workflow.RuleSet, error)
	PublishRuleSet(ctx context.Context, ownerID, workflowID, ruleSetID int64, idempotencyKey string, req agentusecase.PublishRuleSetRequest) (*workflow.RuleCompileJob, error)
	GetRuleCompileJob(ctx context.Context, ownerID, workflowID, jobID int64) (*workflow.RuleCompileJob, error)
	ReviewRuleSet(ctx context.Context, ownerID, workflowID, ruleSetID, actorID int64, req agentusecase.ReviewRuleSetRequest) (*workflow.RuleSet, error)
	RollbackRuleSet(ctx context.Context, ownerID, workflowID, ruleSetID, actorID int64) (*workflow.RuleSet, error)
}

func (h *WorkflowHandler) Create(c *gin.Context) {
	ownerID, ok := currentUserID(c)
	if !ok {
		response.Error(c, http.StatusUnauthorized, agenterrors.CodeUnauthorized, agenterrors.ErrUnauthorized.Error())
		return
	}
	var req agentusecase.CreateWorkflowRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeAppError(c, err)
		return
	}
	item, err := h.service.CreateWorkflow(c.Request.Context(), ownerID, req)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, item)
}

func (h *WorkflowHandler) List(c *gin.Context) {
	ownerID, ok := currentUserID(c)
	if !ok {
		response.Error(c, http.StatusUnauthorized, agenterrors.CodeUnauthorized, agenterrors.ErrUnauthorized.Error())
		return
	}
	items, err := h.service.ListWorkflows(c.Request.Context(), ownerID)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, items)
}

func (h *WorkflowHandler) Get(c *gin.Context) {
	ownerID, ok := currentUserID(c)
	if !ok {
		response.Error(c, http.StatusUnauthorized, agenterrors.CodeUnauthorized, agenterrors.ErrUnauthorized.Error())
		return
	}
	id, err := parseInt64Param(c, "id")
	if err != nil {
		writeAppError(c, agenterrors.ErrInvalidInput)
		return
	}
	item, err := h.service.GetWorkflow(c.Request.Context(), ownerID, id)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, item)
}

func (h *WorkflowHandler) Update(c *gin.Context) {
	ownerID, ok := currentUserID(c)
	if !ok {
		response.Error(c, http.StatusUnauthorized, agenterrors.CodeUnauthorized, agenterrors.ErrUnauthorized.Error())
		return
	}
	id, err := parseInt64Param(c, "id")
	if err != nil {
		writeAppError(c, agenterrors.ErrInvalidInput)
		return
	}
	var req agentusecase.UpdateWorkflowRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeAppError(c, err)
		return
	}
	item, err := h.service.UpdateWorkflow(c.Request.Context(), ownerID, id, req)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, item)
}

func (h *WorkflowHandler) GetProfile(c *gin.Context) {
	ownerID, workflowID, ok := h.ownerAndWorkflowID(c)
	if !ok {
		return
	}
	item, err := h.service.GetWorkflowProfile(c.Request.Context(), ownerID, workflowID)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, item)
}

func (h *WorkflowHandler) UpdateProfile(c *gin.Context) {
	ownerID, workflowID, ok := h.ownerAndWorkflowID(c)
	if !ok {
		return
	}
	var req agentusecase.UpdateWorkflowProfileRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeAppError(c, err)
		return
	}
	item, err := h.service.UpdateWorkflowProfile(c.Request.Context(), ownerID, workflowID, req)
	if err != nil {
		writeAppError(c, err)
		return
	}
	if req.ContextPolicyJSON != nil {
		c.Header("Deprecation", "true")
		c.Header("Link", "</api/v1/workflows/:id/rule-sets>; rel=successor-version")
	}
	response.OK(c, item)
}

func (h *WorkflowHandler) ListRuleSets(c *gin.Context) {
	ownerID, workflowID, ok := h.ownerAndWorkflowID(c)
	if !ok {
		return
	}
	items, err := h.ruleSets.ListRuleSets(c.Request.Context(), ownerID, workflowID)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, items)
}

func (h *WorkflowHandler) CreateRuleSet(c *gin.Context) {
	ownerID, workflowID, ok := h.ownerAndWorkflowID(c)
	if !ok {
		return
	}
	var req agentusecase.CreateRuleSetRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeAppError(c, fmt.Errorf("%w: %v", agenterrors.ErrInvalidInput, err))
		return
	}
	item, err := h.ruleSets.CreateRuleSet(c.Request.Context(), ownerID, workflowID, req)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, item)
}

func (h *WorkflowHandler) GetRuleSet(c *gin.Context) {
	ownerID, workflowID, ok := h.ownerAndWorkflowID(c)
	if !ok {
		return
	}
	ruleSetID, err := parseInt64Param(c, "rule_set_id")
	if err != nil {
		writeAppError(c, agenterrors.ErrInvalidInput)
		return
	}
	item, err := h.ruleSets.GetRuleSet(c.Request.Context(), ownerID, workflowID, ruleSetID)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, item)
}

func (h *WorkflowHandler) UpdateRuleSet(c *gin.Context) {
	ownerID, workflowID, ok := h.ownerAndWorkflowID(c)
	if !ok {
		return
	}
	ruleSetID, err := parseInt64Param(c, "rule_set_id")
	if err != nil {
		writeAppError(c, agenterrors.ErrInvalidInput)
		return
	}
	var req agentusecase.UpdateRuleSetRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeAppError(c, fmt.Errorf("%w: %v", agenterrors.ErrInvalidInput, err))
		return
	}
	item, err := h.ruleSets.UpdateRuleSet(c.Request.Context(), ownerID, workflowID, ruleSetID, req)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, item)
}

func (h *WorkflowHandler) PublishRuleSet(c *gin.Context) {
	ownerID, workflowID, ok := h.ownerAndWorkflowID(c)
	if !ok {
		return
	}
	ruleSetID, err := parseInt64Param(c, "rule_set_id")
	if err != nil {
		writeAppError(c, agenterrors.ErrInvalidInput)
		return
	}
	var req agentusecase.PublishRuleSetRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeAppError(c, err)
		return
	}
	job, err := h.ruleSets.PublishRuleSet(c.Request.Context(), ownerID, workflowID, ruleSetID, c.GetHeader("Idempotency-Key"), req)
	if err != nil {
		writeAppError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, response.Body{Code: 0, Message: http.StatusText(http.StatusAccepted), Data: job})
}

func (h *WorkflowHandler) GetRuleCompileJob(c *gin.Context) {
	ownerID, workflowID, ok := h.ownerAndWorkflowID(c)
	if !ok {
		return
	}
	jobID, err := parseInt64Param(c, "job_id")
	if err != nil {
		writeAppError(c, agenterrors.ErrInvalidInput)
		return
	}
	job, err := h.ruleSets.GetRuleCompileJob(c.Request.Context(), ownerID, workflowID, jobID)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, job)
}

func (h *WorkflowHandler) ReviewRuleSet(c *gin.Context) {
	ownerID, workflowID, ok := h.ownerAndWorkflowID(c)
	if !ok {
		return
	}
	ruleSetID, err := parseInt64Param(c, "rule_set_id")
	if err != nil {
		writeAppError(c, agenterrors.ErrInvalidInput)
		return
	}
	var req agentusecase.ReviewRuleSetRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeAppError(c, err)
		return
	}
	item, err := h.ruleSets.ReviewRuleSet(c.Request.Context(), ownerID, workflowID, ruleSetID, ownerID, req)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, item)
}

func (h *WorkflowHandler) RollbackRuleSet(c *gin.Context) {
	ownerID, workflowID, ok := h.ownerAndWorkflowID(c)
	if !ok {
		return
	}
	ruleSetID, err := parseInt64Param(c, "rule_set_id")
	if err != nil {
		writeAppError(c, agenterrors.ErrInvalidInput)
		return
	}
	item, err := h.ruleSets.RollbackRuleSet(c.Request.Context(), ownerID, workflowID, ruleSetID, ownerID)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, item)
}

func (h *WorkflowHandler) CreateEvalDataset(c *gin.Context) {
	ownerID, workflowID, ok := h.ownerAndWorkflowID(c)
	if !ok {
		return
	}
	var req agentusecase.CreateEvalDatasetRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeAppError(c, err)
		return
	}
	item, err := h.service.CreateEvalDataset(c.Request.Context(), ownerID, workflowID, req)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, item)
}

func (h *WorkflowHandler) ListEvalDatasets(c *gin.Context) {
	ownerID, workflowID, ok := h.ownerAndWorkflowID(c)
	if !ok {
		return
	}
	items, err := h.service.ListEvalDatasets(c.Request.Context(), ownerID, workflowID)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, items)
}

func (h *WorkflowHandler) CreateEvalCase(c *gin.Context) {
	ownerID, datasetID, ok := h.ownerAndID(c, "id")
	if !ok {
		return
	}
	var req agentusecase.CreateEvalCaseRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeAppError(c, err)
		return
	}
	item, err := h.service.CreateEvalCase(c.Request.Context(), ownerID, datasetID, req)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, item)
}

func (h *WorkflowHandler) ListEvalCases(c *gin.Context) {
	ownerID, datasetID, ok := h.ownerAndID(c, "id")
	if !ok {
		return
	}
	items, err := h.service.ListEvalCases(c.Request.Context(), ownerID, datasetID)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, items)
}

func (h *WorkflowHandler) RunEvalDataset(c *gin.Context) {
	ownerID, datasetID, ok := h.ownerAndID(c, "id")
	if !ok {
		return
	}
	var req agentusecase.RunEvalDatasetRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeAppError(c, err)
		return
	}
	evalRun, results, err := h.service.RunEvalDataset(c.Request.Context(), ownerID, datasetID, req)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, gin.H{"eval_run": evalRun, "results": results})
}

func (h *WorkflowHandler) ListEvalRuns(c *gin.Context) {
	ownerID, datasetID, ok := h.ownerAndID(c, "id")
	if !ok {
		return
	}
	items, err := h.service.ListEvalRuns(c.Request.Context(), ownerID, datasetID)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, items)
}

func (h *WorkflowHandler) GetEvalTrend(c *gin.Context) {
	ownerID, datasetID, ok := h.ownerAndID(c, "id")
	if !ok {
		return
	}
	item, err := h.service.GetEvalTrend(c.Request.Context(), ownerID, datasetID)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, item)
}

func (h *WorkflowHandler) ListEvalResults(c *gin.Context) {
	ownerID, evalRunID, ok := h.ownerAndID(c, "id")
	if !ok {
		return
	}
	items, err := h.service.ListEvalResults(c.Request.Context(), ownerID, evalRunID)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, items)
}

func (h *WorkflowHandler) CreateTeam(c *gin.Context) {
	ownerID, ok := currentUserID(c)
	if !ok {
		response.Error(c, http.StatusUnauthorized, agenterrors.CodeUnauthorized, agenterrors.ErrUnauthorized.Error())
		return
	}
	var req agentusecase.CreateTeamRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeAppError(c, err)
		return
	}
	item, err := h.service.CreateTeam(c.Request.Context(), ownerID, req)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, item)
}

func (h *WorkflowHandler) ListTeams(c *gin.Context) {
	ownerID, ok := currentUserID(c)
	if !ok {
		response.Error(c, http.StatusUnauthorized, agenterrors.CodeUnauthorized, agenterrors.ErrUnauthorized.Error())
		return
	}
	items, err := h.service.ListTeams(c.Request.Context(), ownerID)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, items)
}

func (h *WorkflowHandler) DeleteTeam(c *gin.Context) {
	ownerID, teamID, ok := h.ownerAndID(c, "id")
	if !ok {
		return
	}
	if err := h.service.DeleteTeam(c.Request.Context(), ownerID, teamID); err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, gin.H{"success": true})
}

func (h *WorkflowHandler) AddTeamMember(c *gin.Context) {
	ownerID, teamID, ok := h.ownerAndID(c, "id")
	if !ok {
		return
	}
	var req agentusecase.AddTeamMemberRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeAppError(c, err)
		return
	}
	item, err := h.service.AddTeamMember(c.Request.Context(), ownerID, teamID, req)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, item)
}

func (h *WorkflowHandler) ListTeamMembers(c *gin.Context) {
	ownerID, teamID, ok := h.ownerAndID(c, "id")
	if !ok {
		return
	}
	items, err := h.service.ListTeamMembers(c.Request.Context(), ownerID, teamID)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, items)
}

func (h *WorkflowHandler) RemoveTeamMember(c *gin.Context) {
	ownerID, teamID, ok := h.ownerAndID(c, "id")
	if !ok {
		return
	}
	workflowID, err := parseInt64Param(c, "workflow_id")
	if err != nil {
		writeAppError(c, agenterrors.ErrInvalidInput)
		return
	}
	if err := h.service.RemoveTeamMember(c.Request.Context(), ownerID, teamID, workflowID); err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, gin.H{"success": true})
}

func (h *WorkflowHandler) Delete(c *gin.Context) {
	ownerID, ok := currentUserID(c)
	if !ok {
		response.Error(c, http.StatusUnauthorized, agenterrors.CodeUnauthorized, agenterrors.ErrUnauthorized.Error())
		return
	}
	id, err := parseInt64Param(c, "id")
	if err != nil {
		writeAppError(c, agenterrors.ErrInvalidInput)
		return
	}
	if err := h.service.DeleteWorkflow(c.Request.Context(), ownerID, id); err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, gin.H{"success": true})
}

func (h *WorkflowHandler) CreateWorkflowVersion(c *gin.Context) {
	ownerID, workflowID, ok := h.ownerAndWorkflowID(c)
	if !ok {
		return
	}
	var req agentusecase.CreateWorkflowVersionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeAppError(c, err)
		return
	}
	item, err := h.service.CreateWorkflowVersion(c.Request.Context(), ownerID, workflowID, req)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, item)
}

func (h *WorkflowHandler) ListWorkflowVersions(c *gin.Context) {
	ownerID, workflowID, ok := h.ownerAndWorkflowID(c)
	if !ok {
		return
	}
	items, err := h.service.ListWorkflowVersions(c.Request.Context(), ownerID, workflowID)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, items)
}

func (h *WorkflowHandler) GetWorkflowVersion(c *gin.Context) {
	ownerID, id, ok := h.ownerAndID(c, "id")
	if !ok {
		return
	}
	item, err := h.service.GetWorkflowVersion(c.Request.Context(), ownerID, id)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, item)
}

func (h *WorkflowHandler) PublishWorkflowVersion(c *gin.Context) {
	ownerID, id, ok := h.ownerAndID(c, "id")
	if !ok {
		return
	}
	item, err := h.service.PublishWorkflowVersion(c.Request.Context(), ownerID, id)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, item)
}

func (h *WorkflowHandler) ValidateWorkflowVersion(c *gin.Context) {
	ownerID, id, ok := h.ownerAndID(c, "id")
	if !ok {
		return
	}
	if err := h.service.ValidateWorkflowVersion(c.Request.Context(), ownerID, id); err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, gin.H{"valid": true})
}

func (h *WorkflowHandler) Run(c *gin.Context) {
	ownerID, workflowID, ok := h.ownerAndWorkflowID(c)
	if !ok {
		return
	}
	var req agentusecase.RunWorkflowRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeAppError(c, err)
		return
	}
	run, output, err := h.service.RunWorkflow(c.Request.Context(), ownerID, workflowID, req)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, gin.H{"run": run, "output": output})
}

func (h *WorkflowHandler) StreamRun(c *gin.Context) {
	ownerID, workflowID, ok := h.ownerAndWorkflowID(c)
	if !ok {
		return
	}
	var req agentusecase.RunWorkflowRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeAppError(c, err)
		return
	}
	writer := sse.NewWriter(c)
	run, output, err := h.service.StreamRunWorkflow(c.Request.Context(), ownerID, workflowID, req, func(event runtimeevent.Event) error {
		return writer.Event(event.Type, event)
	})
	if err != nil {
		_ = writer.Event("error", gin.H{"error": err.Error()})
		return
	}
	_ = writer.Event("done", gin.H{"run": run, "output": output})
}

func (h *WorkflowHandler) CreateConversation(c *gin.Context) {
	ownerID, workflowID, ok := h.ownerAndWorkflowID(c)
	if !ok {
		return
	}
	var req agentusecase.CreateWorkflowConversationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeAppError(c, err)
		return
	}
	item, err := h.service.CreateWorkflowConversation(c.Request.Context(), ownerID, workflowID, req)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, item)
}

func (h *WorkflowHandler) ListConversations(c *gin.Context) {
	ownerID, workflowID, ok := h.ownerAndWorkflowID(c)
	if !ok {
		return
	}
	items, err := h.service.ListWorkflowConversations(c.Request.Context(), ownerID, workflowID)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, items)
}

func (h *WorkflowHandler) GetConversation(c *gin.Context) {
	ownerID, workflowID, conversationID, ok := h.ownerWorkflowAndConversationID(c)
	if !ok {
		return
	}
	item, err := h.service.GetWorkflowConversation(c.Request.Context(), ownerID, workflowID, conversationID)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, item)
}

func (h *WorkflowHandler) ListConversationMessages(c *gin.Context) {
	ownerID, workflowID, conversationID, ok := h.ownerWorkflowAndConversationID(c)
	if !ok {
		return
	}
	items, err := h.service.ListWorkflowConversationMessages(c.Request.Context(), ownerID, workflowID, conversationID)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, items)
}

func (h *WorkflowHandler) DeleteConversation(c *gin.Context) {
	ownerID, workflowID, conversationID, ok := h.ownerWorkflowAndConversationID(c)
	if !ok {
		return
	}
	if err := h.service.DeleteWorkflowConversation(c.Request.Context(), ownerID, workflowID, conversationID); err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, gin.H{"success": true})
}

func (h *WorkflowHandler) StreamConversationMessage(c *gin.Context) {
	ownerID, workflowID, conversationID, ok := h.ownerWorkflowAndConversationID(c)
	if !ok {
		return
	}
	var req agentusecase.WorkflowMessageRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeAppError(c, err)
		return
	}
	writer := sse.NewWriter(c)
	result, err := h.service.StreamWorkflowMessage(c.Request.Context(), ownerID, workflowID, conversationID, req, func(event runtimeevent.Event) error {
		return writer.Event(event.Type, event)
	})
	if err != nil {
		_ = writer.Event("error", gin.H{"error": err.Error()})
		return
	}
	_ = writer.Event("done", result)
}

func (h *WorkflowHandler) GetRun(c *gin.Context) {
	ownerID, id, ok := h.ownerAndID(c, "id")
	if !ok {
		return
	}
	item, err := h.service.GetRun(c.Request.Context(), ownerID, id)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, item)
}

func (h *WorkflowHandler) ListRunEvents(c *gin.Context) {
	ownerID, id, ok := h.ownerAndID(c, "id")
	if !ok {
		return
	}
	items, err := h.service.ListRunEvents(c.Request.Context(), ownerID, id)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, items)
}

func (h *WorkflowHandler) ListChildRuns(c *gin.Context) {
	ownerID, id, ok := h.ownerAndID(c, "id")
	if !ok {
		return
	}
	items, err := h.service.ListChildRuns(c.Request.Context(), ownerID, id)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, items)
}

func (h *WorkflowHandler) ListNodeLogs(c *gin.Context) {
	ownerID, id, ok := h.ownerAndID(c, "id")
	if !ok {
		return
	}
	items, err := h.service.ListNodeLogs(c.Request.Context(), ownerID, id)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, items)
}

func (h *WorkflowHandler) ListRunSteps(c *gin.Context) {
	ownerID, id, ok := h.ownerAndID(c, "id")
	if !ok {
		return
	}
	items, err := h.service.ListRunSteps(c.Request.Context(), ownerID, id)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, items)
}

func (h *WorkflowHandler) ListMemoryWriteLogs(c *gin.Context) {
	ownerID, id, ok := h.ownerAndID(c, "id")
	if !ok {
		return
	}
	items, err := h.service.ListMemoryWriteLogs(c.Request.Context(), ownerID, id)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, items)
}

func (h *WorkflowHandler) ListToolInvocations(c *gin.Context) {
	ownerID, id, ok := h.ownerAndID(c, "id")
	if !ok {
		return
	}
	items, err := h.service.ListToolInvocations(c.Request.Context(), ownerID, id)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, items)
}

func (h *WorkflowHandler) GetRunTrace(c *gin.Context) {
	ownerID, id, ok := h.ownerAndID(c, "id")
	if !ok {
		return
	}
	item, err := h.service.GetRunTrace(c.Request.Context(), ownerID, id)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, item)
}

func (h *WorkflowHandler) CancelRun(c *gin.Context) {
	ownerID, id, ok := h.ownerAndID(c, "id")
	if !ok {
		return
	}
	item, err := h.service.CancelRun(c.Request.Context(), ownerID, id)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, item)
}

func (h *WorkflowHandler) PauseRun(c *gin.Context) {
	ownerID, id, ok := h.ownerAndID(c, "id")
	if !ok {
		return
	}
	item, err := h.service.PauseRun(c.Request.Context(), ownerID, id)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, item)
}

func (h *WorkflowHandler) ListApprovalRequests(c *gin.Context) {
	ownerID, ok := currentUserID(c)
	if !ok {
		response.Error(c, http.StatusUnauthorized, agenterrors.CodeUnauthorized, agenterrors.ErrUnauthorized.Error())
		return
	}
	items, err := h.service.ListApprovalRequests(c.Request.Context(), ownerID, strings.TrimSpace(c.Query("status")))
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, items)
}

func (h *WorkflowHandler) ApproveRequest(c *gin.Context) {
	h.decideApproval(c, true)
}

func (h *WorkflowHandler) RejectRequest(c *gin.Context) {
	h.decideApproval(c, false)
}

func (h *WorkflowHandler) ResumeRun(c *gin.Context) {
	ownerID, runID, ok := h.ownerAndID(c, "id")
	if !ok {
		return
	}
	item, err := h.service.ResumeRun(c.Request.Context(), ownerID, runID)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, item)
}

func (h *WorkflowHandler) decideApproval(c *gin.Context, approve bool) {
	ownerID, approvalID, ok := h.ownerAndID(c, "id")
	if !ok {
		return
	}
	var req agentusecase.DecideApprovalRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeAppError(c, err)
		return
	}
	var (
		item any
		err  error
	)
	if approve {
		item, err = h.service.ApproveRequest(c.Request.Context(), ownerID, approvalID, req)
	} else {
		item, err = h.service.RejectRequest(c.Request.Context(), ownerID, approvalID, req)
	}
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, item)
}

func (h *WorkflowHandler) ownerAndWorkflowID(c *gin.Context) (int64, int64, bool) {
	return h.ownerAndID(c, "id")
}

func (h *WorkflowHandler) ownerWorkflowAndConversationID(c *gin.Context) (int64, int64, int64, bool) {
	ownerID, workflowID, ok := h.ownerAndWorkflowID(c)
	if !ok {
		return 0, 0, 0, false
	}
	conversationID, err := parseInt64Param(c, "conversation_id")
	if err != nil {
		writeAppError(c, agenterrors.ErrInvalidInput)
		return 0, 0, 0, false
	}
	return ownerID, workflowID, conversationID, true
}

func (h *WorkflowHandler) ownerAndID(c *gin.Context, param string) (int64, int64, bool) {
	ownerID, ok := currentUserID(c)
	if !ok {
		response.Error(c, http.StatusUnauthorized, agenterrors.CodeUnauthorized, agenterrors.ErrUnauthorized.Error())
		return 0, 0, false
	}
	id, err := parseInt64Param(c, param)
	if err != nil {
		writeAppError(c, agenterrors.ErrInvalidInput)
		return 0, 0, false
	}
	return ownerID, id, true
}
