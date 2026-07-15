package handler

import (
	"net/http"

	workspaceusecase "agentcanvas/internal/application/workspace_usecase"
	agenterrors "agentcanvas/internal/pkg/errors"
	"agentcanvas/internal/pkg/response"

	"github.com/gin-gonic/gin"
)

type WorkspaceHandler struct{ service *workspaceusecase.Service }

func NewWorkspaceHandler(service *workspaceusecase.Service) *WorkspaceHandler {
	return &WorkspaceHandler{service: service}
}
func (h *WorkspaceHandler) Create(c *gin.Context) {
	owner, ok := currentUserID(c)
	if !ok {
		response.Error(c, http.StatusUnauthorized, agenterrors.CodeUnauthorized, agenterrors.ErrUnauthorized.Error())
		return
	}
	var req workspaceusecase.CreateWorkspaceRequest
	if c.ShouldBindJSON(&req) != nil {
		writeAppError(c, agenterrors.ErrInvalidInput)
		return
	}
	item, err := h.service.CreateWorkspace(c.Request.Context(), owner, req)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, item)
}
func (h *WorkspaceHandler) List(c *gin.Context) {
	owner, ok := currentUserID(c)
	if !ok {
		writeAppError(c, agenterrors.ErrUnauthorized)
		return
	}
	items, err := h.service.ListWorkspaces(c.Request.Context(), owner)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, items)
}
func (h *WorkspaceHandler) Get(c *gin.Context) {
	owner, id, ok := ownerAndID(c, "id")
	if !ok {
		return
	}
	item, err := h.service.GetWorkspace(c.Request.Context(), owner, id)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, item)
}
func (h *WorkspaceHandler) Delete(c *gin.Context) {
	owner, id, ok := ownerAndID(c, "id")
	if !ok {
		return
	}
	if err := h.service.DeleteWorkspace(c.Request.Context(), owner, id); err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, gin.H{"success": true})
}
func (h *WorkspaceHandler) CreatePack(c *gin.Context) {
	owner, workspaceID, ok := ownerAndID(c, "id")
	if !ok {
		return
	}
	var req workspaceusecase.CreatePackRequest
	if c.ShouldBindJSON(&req) != nil {
		writeAppError(c, agenterrors.ErrInvalidInput)
		return
	}
	item, err := h.service.CreatePack(c.Request.Context(), owner, workspaceID, req)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, item)
}
func (h *WorkspaceHandler) ListPacks(c *gin.Context) {
	owner, workspaceID, ok := ownerAndID(c, "id")
	if !ok {
		return
	}
	items, err := h.service.ListPacks(c.Request.Context(), owner, workspaceID)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, items)
}
func (h *WorkspaceHandler) GetPack(c *gin.Context) {
	owner, id, ok := ownerAndID(c, "id")
	if !ok {
		return
	}
	item, err := h.service.GetPack(c.Request.Context(), owner, id)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, item)
}
func (h *WorkspaceHandler) DeletePack(c *gin.Context) {
	owner, id, ok := ownerAndID(c, "id")
	if !ok {
		return
	}
	if err := h.service.DeletePack(c.Request.Context(), owner, id); err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, gin.H{"success": true})
}
