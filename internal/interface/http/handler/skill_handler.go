package handler

import (
	"encoding/json"
	"net/http"
	"strconv"

	skillusecase "agentcanvas/internal/application/skill_usecase"
	"agentcanvas/internal/domain/audit"
	agenterrors "agentcanvas/internal/pkg/errors"
	"agentcanvas/internal/pkg/response"

	"github.com/gin-gonic/gin"
)

type SkillHandler struct {
	service *skillusecase.Service
	audits  audit.Repository
}

func NewSkillHandler(service *skillusecase.Service, audits audit.Repository) *SkillHandler {
	return &SkillHandler{service: service, audits: audits}
}

func (h *SkillHandler) List(c *gin.Context) {
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

func (h *SkillHandler) Create(c *gin.Context) {
	ownerID, ok := currentUserID(c)
	if !ok {
		response.Error(c, http.StatusUnauthorized, agenterrors.CodeUnauthorized, agenterrors.ErrUnauthorized.Error())
		return
	}
	var req skillusecase.CreateSkillRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeAppError(c, err)
		return
	}
	item, err := h.service.Create(c.Request.Context(), ownerID, req)
	if err != nil {
		writeAppError(c, err)
		return
	}
	h.audit(c, ownerID, "skill.create", strconv.FormatInt(item.ID, 10), map[string]any{"name": item.Name, "source_type": item.SourceType, "skill_type": item.SkillType})
	response.OK(c, item)
}

func (h *SkillHandler) Get(c *gin.Context) {
	ownerID, id, ok := h.ownerAndID(c)
	if !ok {
		return
	}
	item, err := h.service.Get(c.Request.Context(), ownerID, id)
	if err != nil {
		writeAppError(c, err)
		return
	}
	h.audit(c, ownerID, "skill.update", strconv.FormatInt(item.ID, 10), map[string]any{"name": item.Name, "source_type": item.SourceType, "skill_type": item.SkillType})
	response.OK(c, item)
}

func (h *SkillHandler) Update(c *gin.Context) {
	ownerID, id, ok := h.ownerAndID(c)
	if !ok {
		return
	}
	var req skillusecase.UpdateSkillRequest
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

func (h *SkillHandler) Delete(c *gin.Context) {
	ownerID, id, ok := h.ownerAndID(c)
	if !ok {
		return
	}
	if err := h.service.Delete(c.Request.Context(), ownerID, id); err != nil {
		writeAppError(c, err)
		return
	}
	h.audit(c, ownerID, "skill.delete", strconv.FormatInt(id, 10), nil)
	response.OK(c, gin.H{"success": true})
}

func (h *SkillHandler) Validate(c *gin.Context) {
	ownerID, id, ok := h.ownerAndID(c)
	if !ok {
		return
	}
	item, err := h.service.Validate(c.Request.Context(), ownerID, id)
	if err != nil {
		writeAppError(c, err)
		return
	}
	h.audit(c, ownerID, "skill.validate", strconv.FormatInt(id, 10), map[string]any{"valid": item.Valid, "error": item.Error})
	response.OK(c, item)
}

func (h *SkillHandler) ownerAndID(c *gin.Context) (int64, int64, bool) {
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

func (h *SkillHandler) audit(c *gin.Context, ownerID int64, action, resourceID string, detail map[string]any) {
	if h.audits == nil || ownerID <= 0 {
		return
	}
	detailJSON := "{}"
	if detail != nil {
		if data, err := json.Marshal(detail); err == nil {
			detailJSON = string(data)
		}
	}
	_ = h.audits.Create(c.Request.Context(), &audit.Log{
		OwnerID:      ownerID,
		ActorID:      ownerID,
		Action:       action,
		ResourceType: "skill",
		ResourceID:   resourceID,
		DetailJSON:   detailJSON,
		IPAddress:    realIP(c),
		UserAgent:    c.Request.UserAgent(),
	})
}
