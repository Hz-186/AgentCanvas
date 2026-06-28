package handler

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	toolusecase "agentcanvas/internal/application/tool_usecase"
	"agentcanvas/internal/domain/tool"
	agenterrors "agentcanvas/internal/pkg/errors"
	"agentcanvas/internal/pkg/response"
	"agentcanvas/internal/runtime/toolruntime"

	"github.com/gin-gonic/gin"
)

type ToolHandler struct {
	service    *toolusecase.Service
	policyRepo tool.PolicyRepository
	packRepo   tool.PackRepository
	mcpRepo    tool.MCPRepository
}

func NewToolHandler(service *toolusecase.Service, policyRepo tool.PolicyRepository, packRepo tool.PackRepository, mcpRepo tool.MCPRepository) *ToolHandler {
	return &ToolHandler{service: service, policyRepo: policyRepo, packRepo: packRepo, mcpRepo: mcpRepo}
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

func (h *ToolHandler) CreatePolicy(c *gin.Context) {
	ownerID, ok := currentUserID(c)
	if !ok {
		response.Error(c, http.StatusUnauthorized, agenterrors.CodeUnauthorized, agenterrors.ErrUnauthorized.Error())
		return
	}
	var item tool.ToolPolicy
	if err := c.ShouldBindJSON(&item); err != nil {
		writeAppError(c, err)
		return
	}
	item.OwnerID = ownerID
	item.ID = 0
	if err := h.policyRepo.Create(c.Request.Context(), &item); err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, item)
}

func (h *ToolHandler) GetPolicy(c *gin.Context) {
	ownerID, id, ok := h.ownerAndID(c)
	if !ok {
		return
	}
	item, err := h.policyRepo.FindByID(c.Request.Context(), ownerID, id)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, item)
}

func (h *ToolHandler) ListPolicies(c *gin.Context) {
	ownerID, ok := currentUserID(c)
	if !ok {
		response.Error(c, http.StatusUnauthorized, agenterrors.CodeUnauthorized, agenterrors.ErrUnauthorized.Error())
		return
	}
	items, err := h.policyRepo.List(c.Request.Context(), ownerID)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, items)
}

func (h *ToolHandler) UpdatePolicy(c *gin.Context) {
	ownerID, id, ok := h.ownerAndID(c)
	if !ok {
		return
	}
	var item tool.ToolPolicy
	if err := c.ShouldBindJSON(&item); err != nil {
		writeAppError(c, err)
		return
	}
	item.ID = id
	item.OwnerID = ownerID
	if err := h.policyRepo.Update(c.Request.Context(), &item); err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, item)
}

func (h *ToolHandler) DeletePolicy(c *gin.Context) {
	ownerID, id, ok := h.ownerAndID(c)
	if !ok {
		return
	}
	if err := h.policyRepo.Delete(c.Request.Context(), ownerID, id); err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, gin.H{"success": true})
}

func (h *ToolHandler) CreatePack(c *gin.Context) {
	ownerID, ok := currentUserID(c)
	if !ok {
		response.Error(c, http.StatusUnauthorized, agenterrors.CodeUnauthorized, agenterrors.ErrUnauthorized.Error())
		return
	}
	var item tool.ToolPack
	if err := c.ShouldBindJSON(&item); err != nil {
		writeAppError(c, err)
		return
	}
	item.OwnerID = ownerID
	item.ID = 0
	if err := h.packRepo.CreatePack(c.Request.Context(), &item); err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, item)
}

func (h *ToolHandler) GetPack(c *gin.Context) {
	ownerID, id, ok := h.ownerAndID(c)
	if !ok {
		return
	}
	item, err := h.packRepo.FindPackByID(c.Request.Context(), ownerID, id)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, item)
}

func (h *ToolHandler) ListPacks(c *gin.Context) {
	ownerID, ok := currentUserID(c)
	if !ok {
		response.Error(c, http.StatusUnauthorized, agenterrors.CodeUnauthorized, agenterrors.ErrUnauthorized.Error())
		return
	}
	items, err := h.packRepo.ListPacks(c.Request.Context(), ownerID)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, items)
}

func (h *ToolHandler) UpdatePack(c *gin.Context) {
	ownerID, id, ok := h.ownerAndID(c)
	if !ok {
		return
	}
	var item tool.ToolPack
	if err := c.ShouldBindJSON(&item); err != nil {
		writeAppError(c, err)
		return
	}
	item.ID = id
	item.OwnerID = ownerID
	if err := h.packRepo.UpdatePack(c.Request.Context(), &item); err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, item)
}

func (h *ToolHandler) DeletePack(c *gin.Context) {
	ownerID, id, ok := h.ownerAndID(c)
	if !ok {
		return
	}
	if err := h.packRepo.DeletePack(c.Request.Context(), ownerID, id); err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, gin.H{"success": true})
}

func (h *ToolHandler) AddPackItem(c *gin.Context) {
	ownerID, packID, ok := h.ownerAndPackID(c)
	if !ok {
		return
	}
	var req struct {
		ToolID int64 `json:"tool_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		writeAppError(c, err)
		return
	}
	item := tool.ToolPackItem{OwnerID: ownerID, PackID: packID, ToolID: req.ToolID}
	if err := h.packRepo.AddItem(c.Request.Context(), &item); err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, item)
}

func (h *ToolHandler) RemovePackItem(c *gin.Context) {
	ownerID, packID, ok := h.ownerAndPackID(c)
	if !ok {
		return
	}
	var req struct {
		ToolID int64 `json:"tool_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		writeAppError(c, err)
		return
	}
	if err := h.packRepo.RemoveItem(c.Request.Context(), ownerID, packID, req.ToolID); err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, gin.H{"success": true})
}

func (h *ToolHandler) ListPackItems(c *gin.Context) {
	ownerID, packID, ok := h.ownerAndPackID(c)
	if !ok {
		return
	}
	items, err := h.packRepo.ListItems(c.Request.Context(), ownerID, packID)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, items)
}

func (h *ToolHandler) ownerAndPackID(c *gin.Context) (int64, int64, bool) {
	ownerID, ok := currentUserID(c)
	if !ok {
		response.Error(c, http.StatusUnauthorized, agenterrors.CodeUnauthorized, agenterrors.ErrUnauthorized.Error())
		return 0, 0, false
	}
	packID, err := parseInt64Param(c, "id")
	if err != nil {
		writeAppError(c, agenterrors.ErrInvalidInput)
		return 0, 0, false
	}
	return ownerID, packID, true
}

func (h *ToolHandler) CreateMCPServer(c *gin.Context) {
	ownerID, ok := currentUserID(c)
	if !ok {
		response.Error(c, http.StatusUnauthorized, agenterrors.CodeUnauthorized, agenterrors.ErrUnauthorized.Error())
		return
	}
	var item tool.MCPServer
	if err := c.ShouldBindJSON(&item); err != nil {
		writeAppError(c, err)
		return
	}
	if err := normalizeMCPServerRequest(&item); err != nil {
		writeAppError(c, err)
		return
	}
	item.ID = 0
	item.OwnerID = ownerID
	if err := h.mcpRepo.CreateServer(c.Request.Context(), &item); err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, item)
}

func (h *ToolHandler) ListMCPServers(c *gin.Context) {
	ownerID, ok := currentUserID(c)
	if !ok {
		response.Error(c, http.StatusUnauthorized, agenterrors.CodeUnauthorized, agenterrors.ErrUnauthorized.Error())
		return
	}
	items, err := h.mcpRepo.ListServers(c.Request.Context(), ownerID)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, items)
}

func (h *ToolHandler) GetMCPServer(c *gin.Context) {
	ownerID, id, ok := h.ownerAndID(c)
	if !ok {
		return
	}
	item, err := h.mcpRepo.FindServerByID(c.Request.Context(), ownerID, id)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, item)
}

func (h *ToolHandler) UpdateMCPServer(c *gin.Context) {
	ownerID, id, ok := h.ownerAndID(c)
	if !ok {
		return
	}
	item, err := h.mcpRepo.FindServerByID(c.Request.Context(), ownerID, id)
	if err != nil {
		writeAppError(c, err)
		return
	}
	var req tool.MCPServer
	if err := c.ShouldBindJSON(&req); err != nil {
		writeAppError(c, err)
		return
	}
	if strings.TrimSpace(req.Name) != "" {
		item.Name = strings.TrimSpace(req.Name)
	}
	if strings.TrimSpace(req.Transport) != "" {
		item.Transport = strings.TrimSpace(req.Transport)
	}
	if req.EndpointURL != "" {
		item.EndpointURL = strings.TrimSpace(req.EndpointURL)
	}
	if req.Command != "" {
		item.Command = strings.TrimSpace(req.Command)
	}
	if len(req.ArgsJSON) > 0 {
		item.ArgsJSON = req.ArgsJSON
	}
	if len(req.EnvJSON) > 0 {
		item.EnvJSON = req.EnvJSON
	}
	if req.Status == tool.MCPStatusActive || req.Status == tool.MCPStatusDisabled {
		item.Status = req.Status
	}
	if err := normalizeMCPServerRequest(item); err != nil {
		writeAppError(c, err)
		return
	}
	if err := h.mcpRepo.UpdateServer(c.Request.Context(), item); err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, item)
}

func (h *ToolHandler) DeleteMCPServer(c *gin.Context) {
	ownerID, id, ok := h.ownerAndID(c)
	if !ok {
		return
	}
	if err := h.mcpRepo.DeleteServer(c.Request.Context(), ownerID, id); err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, gin.H{"success": true})
}

func (h *ToolHandler) RefreshMCPServer(c *gin.Context) {
	ownerID, id, ok := h.ownerAndID(c)
	if !ok {
		return
	}
	item, err := h.mcpRepo.FindServerByID(c.Request.Context(), ownerID, id)
	if err != nil {
		writeAppError(c, err)
		return
	}
	client := mcpClientFromServer(item)
	discovered, err := client.Discover(c.Request.Context())
	now := time.Now().UTC()
	if err != nil {
		item.LastError = err.Error()
		_ = h.mcpRepo.UpdateServer(c.Request.Context(), item)
		writeAppError(c, err)
		return
	}
	cached := make([]tool.MCPToolCache, 0, len(discovered))
	for _, def := range discovered {
		cached = append(cached, tool.MCPToolCache{
			OwnerID:        ownerID,
			ServerID:       id,
			ToolName:       def.Name,
			Description:    def.Description,
			ParametersJSON: def.Parameters,
			SchemaHash:     hashJSON(def.Parameters),
			CachedAt:       now,
		})
	}
	if err := h.mcpRepo.ReplaceToolCache(c.Request.Context(), ownerID, id, cached); err != nil {
		writeAppError(c, err)
		return
	}
	item.LastError = ""
	item.DiscoveredAt = &now
	if err := h.mcpRepo.UpdateServer(c.Request.Context(), item); err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, gin.H{"server": item, "tools": cached})
}

func (h *ToolHandler) ListMCPTools(c *gin.Context) {
	ownerID, id, ok := h.ownerAndID(c)
	if !ok {
		return
	}
	items, err := h.mcpRepo.ListToolCache(c.Request.Context(), ownerID, id)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, items)
}

func normalizeMCPServerRequest(item *tool.MCPServer) error {
	item.Name = strings.TrimSpace(item.Name)
	item.Transport = strings.TrimSpace(item.Transport)
	if item.Transport == "" {
		item.Transport = tool.MCPTransportSSE
	}
	item.EndpointURL = strings.TrimSpace(item.EndpointURL)
	item.Command = strings.TrimSpace(item.Command)
	if item.Name == "" {
		return agenterrors.ErrInvalidInput
	}
	switch item.Transport {
	case tool.MCPTransportSSE:
		if item.EndpointURL == "" {
			return agenterrors.ErrInvalidInput
		}
	case tool.MCPTransportStdio:
		if item.Command == "" {
			return agenterrors.ErrInvalidInput
		}
	default:
		return agenterrors.ErrInvalidInput
	}
	if len(item.ArgsJSON) == 0 {
		item.ArgsJSON = json.RawMessage("[]")
	}
	if len(item.EnvJSON) == 0 {
		item.EnvJSON = json.RawMessage("{}")
	}
	return nil
}

func mcpClientFromServer(item *tool.MCPServer) *toolruntime.MCPClient {
	if item.Transport == tool.MCPTransportStdio {
		return toolruntime.NewMCPStdioClient(item.Name, item.Command, item.ArgsSlice(), item.EnvMap())
	}
	return toolruntime.NewMCPClient(item.Name, item.EndpointURL)
}

func hashJSON(raw json.RawMessage) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
