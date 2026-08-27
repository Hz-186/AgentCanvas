package handler

import (
	"net/http"
	"strconv"
	"strings"

	memoryusecase "agentcanvas/internal/application/memory_usecase"
	agenterrors "agentcanvas/internal/pkg/errors"
	"agentcanvas/internal/pkg/response"

	"github.com/gin-gonic/gin"
)

type MemoryHandler struct {
	service *memoryusecase.Service
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
	// The file-backed durable memory store is the production source of truth. This
	// endpoint remains a read-only migration/audit view over SQL and accepts
	// provenance/status filters only; memory taxonomy and scope selectors are
	// intentionally not part of the Agent-facing API.
	items, err := h.service.ListFiltered(c.Request.Context(), ownerID, memoryusecase.ListMemoryFilter{
		SourceConversationID: optionalInt64Query(c, "source_conversation_id"), SourceProjectID: optionalInt64Query(c, "source_project_id"),
		Statuses: splitQuery(c.Query("status")), Sources: splitQuery(c.Query("source")), Limit: intQuery(c, "limit", 50), Offset: intQuery(c, "offset", 0),
	})
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, items)
}

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
