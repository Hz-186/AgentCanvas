package handler

import (
	"strconv"

	workspaceusecase "agentcanvas/internal/application/workspace_usecase"
	agenterrors "agentcanvas/internal/pkg/errors"
	"agentcanvas/internal/pkg/response"

	"github.com/gin-gonic/gin"
)

type ProjectHandler struct{ service *workspaceusecase.Service }

func NewProjectHandler(service *workspaceusecase.Service) *ProjectHandler {
	return &ProjectHandler{service: service}
}

func (h *ProjectHandler) Create(c *gin.Context) {
	ownerID, ok := requireOwner(c)
	if !ok {
		return
	}
	var req workspaceusecase.CreateProjectRequest
	if err := bindStrictAgentJSON(c, &req); err != nil {
		writeAppError(c, agenterrors.ErrInvalidInput)
		return
	}
	item, err := h.service.CreateProject(c.Request.Context(), ownerID, req)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, item)
}
func (h *ProjectHandler) List(c *gin.Context) {
	ownerID, ok := requireOwner(c)
	if !ok {
		return
	}
	include := c.Query("include_archived") == "true"
	items, err := h.service.ListProjects(c.Request.Context(), ownerID, include)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, items)
}
func (h *ProjectHandler) Get(c *gin.Context) {
	ownerID, id, ok := ownerAndID(c, "id")
	if !ok {
		return
	}
	item, err := h.service.GetProject(c.Request.Context(), ownerID, id)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, item)
}
func (h *ProjectHandler) Update(c *gin.Context) {
	ownerID, id, ok := ownerAndID(c, "id")
	if !ok {
		return
	}
	var req workspaceusecase.UpdateProjectRequest
	if err := bindStrictAgentJSON(c, &req); err != nil {
		writeAppError(c, agenterrors.ErrInvalidInput)
		return
	}
	item, err := h.service.UpdateProject(c.Request.Context(), ownerID, id, req)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, item)
}
func (h *ProjectHandler) Delete(c *gin.Context) {
	ownerID, id, ok := ownerAndID(c, "id")
	if !ok {
		return
	}
	if err := h.service.ArchiveProject(c.Request.Context(), ownerID, id); err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, gin.H{"success": true})
}

func (h *ProjectHandler) AddFolder(c *gin.Context) {
	ownerID, projectID, ok := ownerAndID(c, "id")
	if !ok {
		return
	}
	var req workspaceusecase.AddFolderRequest
	if err := bindStrictAgentJSON(c, &req); err != nil {
		writeAppError(c, agenterrors.ErrInvalidInput)
		return
	}
	item, err := h.service.AddFolder(c.Request.Context(), ownerID, projectID, req)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, item)
}
func (h *ProjectHandler) ListFolders(c *gin.Context) {
	ownerID, projectID, ok := ownerAndID(c, "id")
	if !ok {
		return
	}
	item, err := h.service.GetProject(c.Request.Context(), ownerID, projectID)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, item.Folders)
}
func (h *ProjectHandler) DeleteFolder(c *gin.Context) {
	ownerID, projectID, ok := ownerAndID(c, "id")
	if !ok {
		return
	}
	folderID, err := strconv.ParseInt(c.Param("folder_id"), 10, 64)
	if err != nil {
		writeAppError(c, agenterrors.ErrInvalidInput)
		return
	}
	if err := h.service.DeleteFolder(c.Request.Context(), ownerID, projectID, folderID); err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, gin.H{"success": true})
}

func (h *ProjectHandler) GitStatus(c *gin.Context) {
	ownerID, projectID, ok := ownerAndID(c, "id")
	if !ok {
		return
	}
	project, err := h.service.GetProject(c.Request.Context(), ownerID, projectID)
	if err != nil {
		writeAppError(c, err)
		return
	}
	value, err := h.service.ProjectGitStatus(c.Request.Context(), project)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, value)
}
func (h *ProjectHandler) GitBranches(c *gin.Context) {
	ownerID, projectID, ok := ownerAndID(c, "id")
	if !ok {
		return
	}
	project, err := h.service.GetProject(c.Request.Context(), ownerID, projectID)
	if err != nil {
		writeAppError(c, err)
		return
	}
	value, err := h.service.GitBranches(c.Request.Context(), project)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, value)
}
func (h *ProjectHandler) GitWorktrees(c *gin.Context) {
	ownerID, projectID, ok := ownerAndID(c, "id")
	if !ok {
		return
	}
	project, err := h.service.GetProject(c.Request.Context(), ownerID, projectID)
	if err != nil {
		writeAppError(c, err)
		return
	}
	value, err := h.service.GitWorktrees(c.Request.Context(), project)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, value)
}
