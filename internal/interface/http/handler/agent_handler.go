package handler

import (
	"net/http"

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
