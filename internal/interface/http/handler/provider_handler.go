package handler

import (
	providerusecase "agentcanvas/internal/application/provider_usecase"
	"agentcanvas/internal/pkg/response"
	"net/http"

	"github.com/gin-gonic/gin"
)

type ProviderHandler struct {
	service *providerusecase.Service
}

func NewProviderHandler(service *providerusecase.Service) *ProviderHandler {
	return &ProviderHandler{service: service}
}

func (h *ProviderHandler) List(c *gin.Context) {
	ownerID, ok := currentUserID(c)
	if !ok {
		response.Error(c, http.StatusUnauthorized, 44001, "unauthorized")
		return
	}
	items, err := h.service.List(c.Request.Context(), ownerID)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, items)
}

func (h *ProviderHandler) Create(c *gin.Context) {
	ownerID, ok := currentUserID(c)
	if !ok {
		response.Error(c, http.StatusUnauthorized, 44001, "unauthorized")
		return
	}
	var req providerusecase.CreateProviderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeAppError(c, err)
		return
	}
	item, err := h.service.Create(c.Request.Context(), ownerID, req, providerClientInfo(c))
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, item)
}

func (h *ProviderHandler) Get(c *gin.Context) {
	ownerID, ok := currentUserID(c)
	if !ok {
		response.Error(c, http.StatusUnauthorized, 44001, "unauthorized")
		return
	}
	id, err := parseInt64Param(c, "id")
	if err != nil {
		writeAppError(c, err)
		return
	}
	item, err := h.service.Get(c.Request.Context(), ownerID, id)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, item)
}

func (h *ProviderHandler) Update(c *gin.Context) {
	ownerID, ok := currentUserID(c)
	if !ok {
		response.Error(c, http.StatusUnauthorized, 44001, "unauthorized")
		return
	}
	id, err := parseInt64Param(c, "id")
	if err != nil {
		writeAppError(c, err)
		return
	}
	var req providerusecase.UpdateProviderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeAppError(c, err)
		return
	}
	item, err := h.service.Update(c.Request.Context(), ownerID, id, req, providerClientInfo(c))
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, item)
}

func (h *ProviderHandler) Delete(c *gin.Context) {
	ownerID, ok := currentUserID(c)
	if !ok {
		response.Error(c, http.StatusUnauthorized, 44001, "unauthorized")
		return
	}
	id, err := parseInt64Param(c, "id")
	if err != nil {
		writeAppError(c, err)
		return
	}
	if err := h.service.Delete(c.Request.Context(), ownerID, id, providerClientInfo(c)); err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, gin.H{"success": true})
}

func (h *ProviderHandler) Test(c *gin.Context) {
	ownerID, ok := currentUserID(c)
	if !ok {
		response.Error(c, http.StatusUnauthorized, 44001, "unauthorized")
		return
	}
	id, err := parseInt64Param(c, "id")
	if err != nil {
		writeAppError(c, err)
		return
	}
	item, err := h.service.Test(c.Request.Context(), ownerID, id, providerClientInfo(c))
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, item)
}

func providerClientInfo(c *gin.Context) providerusecase.ClientInfo {
	return providerusecase.ClientInfo{UserAgent: c.Request.UserAgent(), IPAddress: realIP(c)}
}
