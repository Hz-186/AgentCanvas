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
	items, err := h.service.List(c.Request.Context(), ownerID, splitQuery(c.Query("memory_type")), optionalInt64Query(c, "conversation_id"), intQuery(c, "limit", 50), intQuery(c, "offset", 0))
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, items)
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
	ownerID, id, ok := h.ownerAndID(c)
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
	ownerID, id, ok := h.ownerAndID(c)
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
	ownerID, id, ok := h.ownerAndID(c)
	if !ok {
		return
	}
	if err := h.service.Delete(c.Request.Context(), ownerID, id); err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, gin.H{"success": true})
}

func (h *MemoryHandler) ownerAndID(c *gin.Context) (int64, int64, bool) {
	ownerID, ok := currentUserID(c)
	if !ok {
		response.Error(c, http.StatusUnauthorized, agenterrors.CodeUnauthorized, agenterrors.ErrUnauthorized.Error())
		return 0, 0, false
	}
	id, err := parseInt64Param(c, "id")
	if err != nil {
		writeAppError(c, agenterrors.ErrInvalidInput)
		return 0, 0, false
	}
	return ownerID, id, true
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
