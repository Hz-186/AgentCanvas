package handler

import (
	"context"
	"net/http"
	"time"

	healthusecase "agentcanvas/internal/application/health_usecase"
	"agentcanvas/internal/pkg/errors"
	"agentcanvas/internal/pkg/response"

	"github.com/gin-gonic/gin"
)

type HealthHandler struct{ service *healthusecase.Service }

func NewHealthHandler(service *healthusecase.Service) *HealthHandler {
	return &HealthHandler{service: service}
}

func (h *HealthHandler) Health(c *gin.Context) {
	response.OK(c, gin.H{"status": "healthy"})
}

func (h *HealthHandler) MySQL(c *gin.Context)         { h.component(c, "mysql") }
func (h *HealthHandler) Redis(c *gin.Context)         { h.component(c, "redis") }
func (h *HealthHandler) MinIO(c *gin.Context)         { h.component(c, "minio") }
func (h *HealthHandler) Elasticsearch(c *gin.Context) { h.component(c, "elasticsearch") }

func (h *HealthHandler) component(c *gin.Context, component string) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
	defer cancel()
	if err := h.service.Check(ctx, component); err != nil {
		response.Error(c, http.StatusServiceUnavailable, errors.CodeDependencyUnavailable, err.Error())
		return
	}
	response.OK(c, gin.H{"component": component, "status": "healthy"})
}

func (h *HealthHandler) ContextSystem(c *gin.Context) {
	snapshot, err := h.service.ContextSystem(c.Request.Context())
	if err != nil {
		response.Error(c, http.StatusServiceUnavailable, errors.CodeDependencyUnavailable, err.Error())
		return
	}
	response.OK(c, snapshot)
}

func (h *HealthHandler) MemorySystem(c *gin.Context) {
	snapshot, err := h.service.MemorySystem(c.Request.Context())
	if err != nil {
		response.Error(c, http.StatusServiceUnavailable, errors.CodeDependencyUnavailable, err.Error())
		return
	}
	response.OK(c, snapshot)
}
