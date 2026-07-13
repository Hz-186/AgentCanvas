package handler

import (
	"context"
	"net/http"

	reflectionusecase "agentcanvas/internal/application/reflection_usecase"
	"agentcanvas/internal/domain/reflection"
	agenterrors "agentcanvas/internal/pkg/errors"
	"agentcanvas/internal/pkg/response"

	"github.com/gin-gonic/gin"
)

type reflectionHTTPService interface {
	List(context.Context, int64, int64, string, int, int) ([]reflection.Reflection, error)
	SetStatus(context.Context, int64, int64, int64, reflectionusecase.UpdateStatusRequest) error
	Feedback(context.Context, int64, int64, int64, reflectionusecase.FeedbackRequest) error
}

type ReflectionHandler struct{ service reflectionHTTPService }

func NewReflectionHandler(service reflectionHTTPService) *ReflectionHandler {
	return &ReflectionHandler{service: service}
}

func (h *ReflectionHandler) List(c *gin.Context) {
	ownerID, ok := currentUserID(c)
	if !ok {
		response.Error(c, http.StatusUnauthorized, agenterrors.CodeUnauthorized, agenterrors.ErrUnauthorized.Error())
		return
	}
	workflowID, err := parseInt64Param(c, "id")
	if err != nil {
		writeAppError(c, agenterrors.ErrInvalidInput)
		return
	}
	items, err := h.service.List(c.Request.Context(), ownerID, workflowID, c.Query("status"), intQuery(c, "limit", 50), intQuery(c, "offset", 0))
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, items)
}

func (h *ReflectionHandler) SetStatus(c *gin.Context) {
	ownerID, ok := currentUserID(c)
	if !ok {
		response.Error(c, http.StatusUnauthorized, agenterrors.CodeUnauthorized, agenterrors.ErrUnauthorized.Error())
		return
	}
	workflowID, err := parseInt64Param(c, "id")
	if err != nil {
		writeAppError(c, agenterrors.ErrInvalidInput)
		return
	}
	reflectionID, err := parseInt64Param(c, "reflection_id")
	if err != nil {
		writeAppError(c, agenterrors.ErrInvalidInput)
		return
	}
	var req reflectionusecase.UpdateStatusRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeAppError(c, agenterrors.ErrInvalidInput)
		return
	}
	if err := h.service.SetStatus(c.Request.Context(), ownerID, workflowID, reflectionID, req); err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, gin.H{"success": true})
}

func (h *ReflectionHandler) Feedback(c *gin.Context) {
	ownerID, ok := currentUserID(c)
	if !ok {
		response.Error(c, http.StatusUnauthorized, agenterrors.CodeUnauthorized, agenterrors.ErrUnauthorized.Error())
		return
	}
	runID, err := parseInt64Param(c, "id")
	if err != nil {
		writeAppError(c, agenterrors.ErrInvalidInput)
		return
	}
	reflectionID, err := parseInt64Param(c, "reflection_id")
	if err != nil {
		writeAppError(c, agenterrors.ErrInvalidInput)
		return
	}
	var req reflectionusecase.FeedbackRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeAppError(c, agenterrors.ErrInvalidInput)
		return
	}
	if err := h.service.Feedback(c.Request.Context(), ownerID, runID, reflectionID, req); err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, gin.H{"success": true})
}
