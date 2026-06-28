package handler

import (
	"net/http"
	"strings"

	agentusecase "agentcanvas/internal/application/agent_usecase"
	"agentcanvas/internal/interface/http/sse"
	agenterrors "agentcanvas/internal/pkg/errors"
	"agentcanvas/internal/pkg/response"
	runtimeevent "agentcanvas/internal/runtime/event"

	"github.com/gin-gonic/gin"
)

type AgentHandler struct {
	service *agentusecase.Service
}

func NewAgentHandler(service *agentusecase.Service) *AgentHandler {
	return &AgentHandler{service: service}
}

func (h *AgentHandler) Create(c *gin.Context) {
	ownerID, ok := currentUserID(c)
	if !ok {
		response.Error(c, http.StatusUnauthorized, agenterrors.CodeUnauthorized, agenterrors.ErrUnauthorized.Error())
		return
	}
	var req agentusecase.CreateAgentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeAppError(c, err)
		return
	}
	item, err := h.service.CreateAgent(c.Request.Context(), ownerID, req)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, item)
}

func (h *AgentHandler) List(c *gin.Context) {
	ownerID, ok := currentUserID(c)
	if !ok {
		response.Error(c, http.StatusUnauthorized, agenterrors.CodeUnauthorized, agenterrors.ErrUnauthorized.Error())
		return
	}
	items, err := h.service.ListAgents(c.Request.Context(), ownerID)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, items)
}

func (h *AgentHandler) Get(c *gin.Context) {
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
	item, err := h.service.GetAgent(c.Request.Context(), ownerID, id)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, item)
}

func (h *AgentHandler) Update(c *gin.Context) {
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
	var req agentusecase.UpdateAgentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeAppError(c, err)
		return
	}
	item, err := h.service.UpdateAgent(c.Request.Context(), ownerID, id, req)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, item)
}

func (h *AgentHandler) GetProfile(c *gin.Context) {
	ownerID, agentID, ok := h.ownerAndAgentID(c)
	if !ok {
		return
	}
	item, err := h.service.GetAgentProfile(c.Request.Context(), ownerID, agentID)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, item)
}

func (h *AgentHandler) UpdateProfile(c *gin.Context) {
	ownerID, agentID, ok := h.ownerAndAgentID(c)
	if !ok {
		return
	}
	var req agentusecase.UpdateAgentProfileRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeAppError(c, err)
		return
	}
	item, err := h.service.UpdateAgentProfile(c.Request.Context(), ownerID, agentID, req)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, item)
}

func (h *AgentHandler) CreateEvalDataset(c *gin.Context) {
	ownerID, agentID, ok := h.ownerAndAgentID(c)
	if !ok {
		return
	}
	var req agentusecase.CreateEvalDatasetRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeAppError(c, err)
		return
	}
	item, err := h.service.CreateEvalDataset(c.Request.Context(), ownerID, agentID, req)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, item)
}

func (h *AgentHandler) ListEvalDatasets(c *gin.Context) {
	ownerID, agentID, ok := h.ownerAndAgentID(c)
	if !ok {
		return
	}
	items, err := h.service.ListEvalDatasets(c.Request.Context(), ownerID, agentID)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, items)
}

func (h *AgentHandler) CreateEvalCase(c *gin.Context) {
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

func (h *AgentHandler) ListEvalCases(c *gin.Context) {
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

func (h *AgentHandler) RunEvalDataset(c *gin.Context) {
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

func (h *AgentHandler) ListEvalRuns(c *gin.Context) {
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

func (h *AgentHandler) ListEvalResults(c *gin.Context) {
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

func (h *AgentHandler) CreateTeam(c *gin.Context) {
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

func (h *AgentHandler) ListTeams(c *gin.Context) {
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

func (h *AgentHandler) DeleteTeam(c *gin.Context) {
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

func (h *AgentHandler) AddTeamMember(c *gin.Context) {
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

func (h *AgentHandler) ListTeamMembers(c *gin.Context) {
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

func (h *AgentHandler) RemoveTeamMember(c *gin.Context) {
	ownerID, teamID, ok := h.ownerAndID(c, "id")
	if !ok {
		return
	}
	agentID, err := parseInt64Param(c, "agent_id")
	if err != nil {
		writeAppError(c, agenterrors.ErrInvalidInput)
		return
	}
	if err := h.service.RemoveTeamMember(c.Request.Context(), ownerID, teamID, agentID); err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, gin.H{"success": true})
}

func (h *AgentHandler) Delete(c *gin.Context) {
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
	if err := h.service.DeleteAgent(c.Request.Context(), ownerID, id); err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, gin.H{"success": true})
}

func (h *AgentHandler) CreateFlowVersion(c *gin.Context) {
	ownerID, agentID, ok := h.ownerAndAgentID(c)
	if !ok {
		return
	}
	var req agentusecase.CreateFlowVersionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeAppError(c, err)
		return
	}
	item, err := h.service.CreateFlowVersion(c.Request.Context(), ownerID, agentID, req)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, item)
}

func (h *AgentHandler) ListFlowVersions(c *gin.Context) {
	ownerID, agentID, ok := h.ownerAndAgentID(c)
	if !ok {
		return
	}
	items, err := h.service.ListFlowVersions(c.Request.Context(), ownerID, agentID)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, items)
}

func (h *AgentHandler) GetFlowVersion(c *gin.Context) {
	ownerID, id, ok := h.ownerAndID(c, "id")
	if !ok {
		return
	}
	item, err := h.service.GetFlowVersion(c.Request.Context(), ownerID, id)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, item)
}

func (h *AgentHandler) PublishFlowVersion(c *gin.Context) {
	ownerID, id, ok := h.ownerAndID(c, "id")
	if !ok {
		return
	}
	item, err := h.service.PublishFlowVersion(c.Request.Context(), ownerID, id)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, item)
}

func (h *AgentHandler) ValidateFlowVersion(c *gin.Context) {
	ownerID, id, ok := h.ownerAndID(c, "id")
	if !ok {
		return
	}
	if err := h.service.ValidateFlowVersion(c.Request.Context(), ownerID, id); err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, gin.H{"valid": true})
}

func (h *AgentHandler) Run(c *gin.Context) {
	ownerID, agentID, ok := h.ownerAndAgentID(c)
	if !ok {
		return
	}
	var req agentusecase.RunAgentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeAppError(c, err)
		return
	}
	run, output, err := h.service.RunAgent(c.Request.Context(), ownerID, agentID, req)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, gin.H{"run": run, "output": output})
}

func (h *AgentHandler) StreamRun(c *gin.Context) {
	ownerID, agentID, ok := h.ownerAndAgentID(c)
	if !ok {
		return
	}
	var req agentusecase.RunAgentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeAppError(c, err)
		return
	}
	writer := sse.NewWriter(c)
	run, output, err := h.service.StreamRunAgent(c.Request.Context(), ownerID, agentID, req, func(event runtimeevent.Event) error {
		return writer.Event(event.Type, event)
	})
	if err != nil {
		_ = writer.Event("error", gin.H{"error": err.Error()})
		return
	}
	_ = writer.Event("done", gin.H{"run": run, "output": output})
}

func (h *AgentHandler) GetRun(c *gin.Context) {
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

func (h *AgentHandler) ListRunEvents(c *gin.Context) {
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

func (h *AgentHandler) ListChildRuns(c *gin.Context) {
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

func (h *AgentHandler) ListNodeLogs(c *gin.Context) {
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

func (h *AgentHandler) ListRunSteps(c *gin.Context) {
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

func (h *AgentHandler) ListMemoryWriteLogs(c *gin.Context) {
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

func (h *AgentHandler) ListToolInvocations(c *gin.Context) {
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

func (h *AgentHandler) CancelRun(c *gin.Context) {
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

func (h *AgentHandler) PauseRun(c *gin.Context) {
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

func (h *AgentHandler) ListApprovalRequests(c *gin.Context) {
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

func (h *AgentHandler) ApproveRequest(c *gin.Context) {
	h.decideApproval(c, true)
}

func (h *AgentHandler) RejectRequest(c *gin.Context) {
	h.decideApproval(c, false)
}

func (h *AgentHandler) ResumeRun(c *gin.Context) {
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

func (h *AgentHandler) decideApproval(c *gin.Context, approve bool) {
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

func (h *AgentHandler) ownerAndAgentID(c *gin.Context) (int64, int64, bool) {
	return h.ownerAndID(c, "id")
}

func (h *AgentHandler) ownerAndID(c *gin.Context, param string) (int64, int64, bool) {
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
