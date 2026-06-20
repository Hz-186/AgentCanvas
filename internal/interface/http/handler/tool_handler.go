package handler

import (
	"net/http"

	toolusecase "agentcanvas/internal/application/tool_usecase"
	agenterrors "agentcanvas/internal/pkg/errors"
	"agentcanvas/internal/pkg/response"

	"github.com/gin-gonic/gin"
)

type ToolHandler struct {
	service *toolusecase.Service
}

func NewToolHandler(service *toolusecase.Service) *ToolHandler {
	return &ToolHandler{service: service}
}

func (h *ToolHandler) List(c *gin.Context) {
	ownerID, ok := currentUserID(c)
	if !ok {
		response.Error(c, http.StatusUnauthorized, agenterrors.CodeUnauthorized, agenterrors.ErrUnauthorized.Error())
		return
	}
	items, err := h.service.List(c.Request.Context(), ownerID, intQuery(c, "limit", 50), intQuery(c, "offset", 0))
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, items)
}

func (h *ToolHandler) Create(c *gin.Context) {
	ownerID, ok := currentUserID(c)
	if !ok {
		response.Error(c, http.StatusUnauthorized, agenterrors.CodeUnauthorized, agenterrors.ErrUnauthorized.Error())
		return
	}
	var req toolusecase.CreateToolRequest
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

func (h *ToolHandler) Get(c *gin.Context) {
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

func (h *ToolHandler) Update(c *gin.Context) {
	ownerID, id, ok := h.ownerAndID(c)
	if !ok {
		return
	}
	var req toolusecase.UpdateToolRequest
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

func (h *ToolHandler) Delete(c *gin.Context) {
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

func (h *ToolHandler) Test(c *gin.Context) {
	ownerID, id, ok := h.ownerAndID(c)
	if !ok {
		return
	}
	var req struct {
		Input map[string]any `json:"input"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		writeAppError(c, err)
		return
	}
	output, err := h.service.Test(c.Request.Context(), ownerID, id, req.Input)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, output)
}

func (h *ToolHandler) ownerAndID(c *gin.Context) (int64, int64, bool) {
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
