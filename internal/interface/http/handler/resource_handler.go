package handler

import (
	"net/http"

	resourceusecase "agentcanvas/internal/application/resource_usecase"
	"agentcanvas/internal/domain/resource"
	agenterrors "agentcanvas/internal/pkg/errors"
	"agentcanvas/internal/pkg/response"

	"github.com/gin-gonic/gin"
)

type ResourceHandler struct {
	service *resourceusecase.Service
}

func NewResourceHandler(service *resourceusecase.Service) *ResourceHandler {
	return &ResourceHandler{service: service}
}

func (h *ResourceHandler) List(c *gin.Context) {
	ownerID, ok := currentUserID(c)
	if !ok {
		response.Error(c, http.StatusUnauthorized, agenterrors.CodeUnauthorized, agenterrors.ErrUnauthorized.Error())
		return
	}
	kind := resource.Kind(c.Param("kind"))
	if !kind.Valid() {
		writeAppError(c, agenterrors.ErrInvalidInput)
		return
	}
	page, err := h.service.List(c.Request.Context(), ownerID, kind, resource.ListOptions{
		Limit:  intQuery(c, "limit", 25),
		Cursor: c.Query("cursor"),
	})
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, page)
}
