package handler

import (
	"net/http"
	"strconv"
	"strings"

	agentusecase "agentcanvas/internal/application/agent_usecase"
	memoryusecase "agentcanvas/internal/application/memory_usecase"
	agenterrors "agentcanvas/internal/pkg/errors"
	"agentcanvas/internal/pkg/response"

	"github.com/gin-gonic/gin"
)

type MemoryHandler struct {
	service     *memoryusecase.Service
	candidates  *memoryusecase.CandidateService
	improvement *agentusecase.ImprovementService
}

func (h *MemoryHandler) ConfigureCandidates(candidates *memoryusecase.CandidateService, improvement *agentusecase.ImprovementService) {
	h.candidates, h.improvement = candidates, improvement
}

func NewMemoryHandler(service *memoryusecase.Service) *MemoryHandler {
	return &MemoryHandler{service: service}
}

func (h *MemoryHandler) List(c *gin.Context) {
	ownerID, ok := currentUserID(c)
	if !ok {
		response.Error(c, http.StatusUnauthorized, agenterrors.CodeUnauthorized, agenterrors.ErrUnauthorized.Error())
		return
	}
	items, err := h.service.ListFiltered(c.Request.Context(), ownerID, memoryusecase.ListMemoryFilter{
		MemoryTypes: splitQuery(c.Query("memory_type")), ConversationID: optionalInt64Query(c, "conversation_id"),
		Statuses: splitQuery(c.Query("status")), ScopeTypes: splitQuery(c.Query("scope_type")), ScopeID: optionalInt64Query(c, "scope_id"),
		Sources: splitQuery(c.Query("source")), Limit: intQuery(c, "limit", 50), Offset: intQuery(c, "offset", 0),
	})
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, items)
}

func (h *MemoryHandler) ListCandidates(c *gin.Context) {
	ownerID, ok := currentUserID(c)
	if !ok {
		response.Error(c, http.StatusUnauthorized, agenterrors.CodeUnauthorized, agenterrors.ErrUnauthorized.Error())
		return
	}
	if h.candidates == nil {
		response.Error(c, http.StatusInternalServerError, agenterrors.CodeInternal, "memory candidate service is not configured")
		return
	}
	items, err := h.candidates.List(c.Request.Context(), ownerID, strings.TrimSpace(c.Query("status")), intQuery(c, "limit", 100))
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, items)
}

func (h *MemoryHandler) ApproveCandidate(c *gin.Context) { h.decideCandidate(c, true) }
func (h *MemoryHandler) RejectCandidate(c *gin.Context)  { h.decideCandidate(c, false) }

func (h *MemoryHandler) ListRecallLogs(c *gin.Context) {
	ownerID, ok := currentUserID(c)
	if !ok {
		response.Error(c, http.StatusUnauthorized, agenterrors.CodeUnauthorized, agenterrors.ErrUnauthorized.Error())
		return
	}
	memoryID := int64(0)
	if value := optionalInt64Query(c, "memory_id"); value != nil {
		memoryID = *value
	}
	items, err := h.service.ListRecallLogs(c.Request.Context(), ownerID, memoryID, intQuery(c, "limit", 50))
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, items)
}

func (h *MemoryHandler) SetRecallFeedback(c *gin.Context) {
	ownerID, id, ok := ownerAndID(c, "id")
	if !ok {
		return
	}
	var request struct {
		Feedback string `json:"feedback" binding:"required"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		writeAppError(c, err)
		return
	}
	if err := h.service.SetRecallFeedback(c.Request.Context(), ownerID, id, request.Feedback); err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, gin.H{"success": true})
}

func (h *MemoryHandler) decideCandidate(c *gin.Context, approved bool) {
	ownerID, ok := currentUserID(c)
	if !ok {
		response.Error(c, http.StatusUnauthorized, agenterrors.CodeUnauthorized, agenterrors.ErrUnauthorized.Error())
		return
	}
	proposalID, err := parseInt64Param(c, "id")
	if err != nil || h.improvement == nil {
		writeAppError(c, agenterrors.ErrInvalidInput)
		return
	}
	var request struct {
		Note string `json:"note"`
	}
	if c.Request.ContentLength > 0 {
		if err := c.ShouldBindJSON(&request); err != nil {
			writeAppError(c, err)
			return
		}
	}
	item, err := h.improvement.DecideMemoryProposal(c.Request.Context(), ownerID, proposalID, approved, request.Note)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, item)
}

func (h *MemoryHandler) Create(c *gin.Context) {
	ownerID, ok := currentUserID(c)
	if !ok {
		response.Error(c, http.StatusUnauthorized, agenterrors.CodeUnauthorized, agenterrors.ErrUnauthorized.Error())
		return
	}
	var req memoryusecase.CreateMemoryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeAppError(c, err)
		return
	}
	item, err := h.service.Create(c.Request.Context(), ownerID, req)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, item)
}

func (h *MemoryHandler) Get(c *gin.Context) {
	ownerID, id, ok := ownerAndID(c, "id")
	if !ok {
		return
	}
	item, err := h.service.Get(c.Request.Context(), ownerID, id)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, item)
}

func (h *MemoryHandler) Update(c *gin.Context) {
	ownerID, id, ok := ownerAndID(c, "id")
	if !ok {
		return
	}
	var req memoryusecase.UpdateMemoryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeAppError(c, err)
		return
	}
	item, err := h.service.Update(c.Request.Context(), ownerID, id, req)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, item)
}

func (h *MemoryHandler) Delete(c *gin.Context) {
	ownerID, id, ok := ownerAndID(c, "id")
	if !ok {
		return
	}
	if err := h.service.Delete(c.Request.Context(), ownerID, id); err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, gin.H{"success": true})
}

func intQuery(c *gin.Context, name string, fallback int) int {
	value := strings.TrimSpace(c.Query(name))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func optionalInt64Query(c *gin.Context, name string) *int64 {
	value := strings.TrimSpace(c.Query(name))
	if value == "" {
		return nil
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed <= 0 {
		return nil
	}
	return &parsed
}

func splitQuery(value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	raw := strings.Split(value, ",")
	items := make([]string, 0, len(raw))
	for _, item := range raw {
		if item = strings.TrimSpace(item); item != "" {
			items = append(items, item)
		}
	}
	return items
}
