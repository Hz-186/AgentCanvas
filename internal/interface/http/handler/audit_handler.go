package handler

import (
	auditusecase "agentcanvas/internal/application/audit_usecase"
	agenterrors "agentcanvas/internal/pkg/errors"
	"agentcanvas/internal/pkg/response"
	"strconv"

	"github.com/gin-gonic/gin"
)

type AuditHandler struct {
	service *auditusecase.Service
}

func NewAuditHandler(service *auditusecase.Service) *AuditHandler {
	return &AuditHandler{service: service}
}

func (h *AuditHandler) List(c *gin.Context) {
	ownerID, ok := currentUserID(c)
	if !ok {
		writeAppError(c, agenterrors.ErrUnauthorized)
		return
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	items, err := h.service.List(c.Request.Context(), ownerID, limit, offset)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, items)
}
