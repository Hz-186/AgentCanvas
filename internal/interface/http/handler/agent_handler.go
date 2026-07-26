package handler

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	agentusecase "agentcanvas/internal/application/agent_usecase"
	"agentcanvas/internal/domain/conversation"
	"agentcanvas/internal/interface/http/sse"
	agenterrors "agentcanvas/internal/pkg/errors"
	"agentcanvas/internal/pkg/response"

	"github.com/gin-gonic/gin"
)

type AgentHandler struct {
	service     *agentusecase.Service
	improvement *agentusecase.ImprovementService
}

func NewAgentHandler(service *agentusecase.Service, improvement ...*agentusecase.ImprovementService) *AgentHandler {
	handler := &AgentHandler{service: service}
	if len(improvement) > 0 {
		handler.improvement = improvement[0]
	}
	return handler
}

func (h *AgentHandler) Create(c *gin.Context) {
	ownerID, ok := requireOwner(c)
	if !ok {
		return
	}
	var req agentusecase.CreateAgentRequest
	if err := bindStrictAgentJSON(c, &req); err != nil {
		writeAppError(c, agenterrors.ErrInvalidInput)
		return
	}
	item, err := h.service.CreateAgent(c.Request.Context(), ownerID, req)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, item)
}

func (h *AgentHandler) List(c *gin.Context) {
	ownerID, ok := requireOwner(c)
	if !ok {
		return
	}
	items, err := h.service.ListAgents(c.Request.Context(), ownerID)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, items)
}

func (h *AgentHandler) Get(c *gin.Context) {
	ownerID, id, ok := ownerAndID(c, "id")
	if !ok {
		return
	}
	item, err := h.service.GetAgent(c.Request.Context(), ownerID, id)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, item)
}

func (h *AgentHandler) Update(c *gin.Context) {
	ownerID, id, ok := ownerAndID(c, "id")
	if !ok {
		return
	}
	var req agentusecase.UpdateAgentRequest
	if err := bindStrictAgentJSON(c, &req); err != nil {
		writeAppError(c, agenterrors.ErrInvalidInput)
		return
	}
	item, err := h.service.UpdateAgent(c.Request.Context(), ownerID, id, req)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, item)
}

func (h *AgentHandler) UpdateSettings(c *gin.Context) {
	ownerID, id, ok := ownerAndID(c, "id")
	if !ok {
		return
	}
	var settings agentusecase.AgentEditableSettings
	if err := bindStrictAgentJSON(c, &settings); err != nil {
		writeAppError(c, agenterrors.ErrInvalidInput)
		return
	}
	item, err := h.service.UpdateAgentSettings(c.Request.Context(), ownerID, id, settings)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, item)
}

func (h *AgentHandler) Delete(c *gin.Context) {
	ownerID, id, ok := ownerAndID(c, "id")
	if !ok {
		return
	}
	if err := h.service.DeleteAgent(c.Request.Context(), ownerID, id); err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, gin.H{"success": true})
}

func (h *AgentHandler) Validate(c *gin.Context) {
	ownerID, id, ok := ownerAndID(c, "id")
	if !ok {
		return
	}
	result, err := h.service.ValidateAgent(c.Request.Context(), ownerID, id)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, result)
}

func (h *AgentHandler) Publish(c *gin.Context) {
	ownerID, id, ok := ownerAndID(c, "id")
	if !ok {
		return
	}
	item, err := h.service.Publish(c.Request.Context(), ownerID, id)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, item)
}

func (h *AgentHandler) ListReleases(c *gin.Context) {
	ownerID, id, ok := ownerAndID(c, "id")
	if !ok {
		return
	}
	items, err := h.service.ListReleases(c.Request.Context(), ownerID, id)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, items)
}

func (h *AgentHandler) GetRelease(c *gin.Context) {
	ownerID, id, ok := ownerAndID(c, "id")
	if !ok {
		return
	}
	item, err := h.service.GetRelease(c.Request.Context(), ownerID, id)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, item)
}

func (h *AgentHandler) Capabilities(c *gin.Context) {
	ownerID, id, ok := ownerAndID(c, "id")
	if !ok {
		return
	}
	item, err := h.service.Capabilities(c.Request.Context(), ownerID, id)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, item)
}

func (h *AgentHandler) CreateConversation(c *gin.Context) {
	ownerID, agentID, ok := ownerAndID(c, "id")
	if !ok {
		return
	}
	var req agentusecase.CreateConversationRequest
	if c.Request.ContentLength > 0 {
		if err := bindStrictAgentJSON(c, &req); err != nil {
			writeAppError(c, agenterrors.ErrInvalidInput)
			return
		}
	}
	item, err := h.service.CreateConversation(c.Request.Context(), ownerID, agentID, req)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, item)
}

func (h *AgentHandler) UpdateConversationMode(c *gin.Context) {
	ownerID, agentID, conversationID, ok := agentConversationIDs(c)
	if !ok {
		return
	}
	var req agentusecase.UpdateConversationModeRequest
	if err := bindStrictAgentJSON(c, &req); err != nil {
		writeAppError(c, agenterrors.ErrInvalidInput)
		return
	}
	item, err := h.service.UpdateConversationMode(c.Request.Context(), ownerID, agentID, conversationID, req)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, item)
}

func (h *AgentHandler) ListConversations(c *gin.Context) {
	ownerID, agentID, ok := ownerAndID(c, "id")
	if !ok {
		return
	}
	items, err := h.service.ListConversations(c.Request.Context(), ownerID, agentID)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, items)
}

func (h *AgentHandler) ListMessages(c *gin.Context) {
	ownerID, agentID, conversationID, ok := agentConversationIDs(c)
	if !ok {
		return
	}
	items, err := h.service.ListMessages(c.Request.Context(), ownerID, agentID, conversationID)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, items)
}

func (h *AgentHandler) DeleteConversation(c *gin.Context) {
	ownerID, agentID, conversationID, ok := agentConversationIDs(c)
	if !ok {
		return
	}
	if err := h.service.DeleteConversation(c.Request.Context(), ownerID, agentID, conversationID); err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, gin.H{"success": true})
}

func (h *AgentHandler) ForkConversation(c *gin.Context)    { h.fork(c, false) }
func (h *AgentHandler) UpgradeConversation(c *gin.Context) { h.fork(c, true) }
func (h *AgentHandler) fork(c *gin.Context, upgrade bool) {
	ownerID, agentID, conversationID, ok := agentConversationIDs(c)
	if !ok {
		return
	}
	item, err := h.service.ForkConversation(c.Request.Context(), ownerID, agentID, conversationID, upgrade)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, item)
}

func (h *AgentHandler) StartTurn(c *gin.Context) {
	ownerID, agentID, conversationID, ok := agentConversationIDs(c)
	if !ok {
		return
	}
	var req agentusecase.CreateTurnRequest
	if err := bindStrictAgentJSON(c, &req); err != nil {
		writeAppError(c, agenterrors.ErrInvalidInput)
		return
	}
	accepted, err := h.service.StartTurn(c.Request.Context(), ownerID, agentID, conversationID, c.GetHeader("Idempotency-Key"), req)
	if err != nil {
		writeAppError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, response.Body{Code: 0, Message: http.StatusText(http.StatusAccepted), Data: accepted})
}

func bindStrictAgentJSON(c *gin.Context, target any) error {
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return io.ErrUnexpectedEOF
		}
		return err
	}
	return nil
}

func (h *AgentHandler) GetTurn(c *gin.Context) {
	ownerID, id, ok := ownerAndID(c, "id")
	if !ok {
		return
	}
	item, err := h.service.GetTurn(c.Request.Context(), ownerID, id)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, item)
}

func (h *AgentHandler) GetLatestTurn(c *gin.Context) {
	ownerID, agentID, conversationID, ok := agentConversationIDs(c)
	if !ok {
		return
	}
	item, err := h.service.GetLatestTurn(c.Request.Context(), ownerID, agentID, conversationID)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, item)
}

func (h *AgentHandler) SearchSessions(c *gin.Context) {
	ownerID, agentID, ok := ownerAndID(c, "id")
	if !ok {
		return
	}
	request := conversation.MessageSearchRequest{Query: strings.TrimSpace(c.Query("q"))}
	if value, err := strconv.ParseInt(strings.TrimSpace(c.Query("conversation_id")), 10, 64); err == nil && value > 0 {
		request.ConversationID = &value
	}
	if value, err := strconv.Atoi(strings.TrimSpace(c.Query("limit"))); err == nil {
		request.Limit = value
	}
	if value, err := time.Parse(time.RFC3339, strings.TrimSpace(c.Query("from"))); err == nil {
		request.From = &value
	}
	if value, err := time.Parse(time.RFC3339, strings.TrimSpace(c.Query("to"))); err == nil {
		request.To = &value
	}
	items, err := h.service.SearchSessions(c.Request.Context(), ownerID, agentID, request)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, items)
}

func (h *AgentHandler) ListImprovementReviews(c *gin.Context) {
	ownerID, agentID, ok := ownerAndID(c, "id")
	if !ok {
		return
	}
	if h.improvement == nil {
		writeAppError(c, agenterrors.ErrNotFound)
		return
	}
	items, err := h.improvement.ListReviews(c.Request.Context(), ownerID, agentID)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, items)
}

func (h *AgentHandler) ListChangeProposals(c *gin.Context) {
	ownerID, agentID, ok := ownerAndID(c, "id")
	if !ok {
		return
	}
	if h.improvement == nil {
		writeAppError(c, agenterrors.ErrNotFound)
		return
	}
	items, err := h.improvement.ListProposals(c.Request.Context(), ownerID, agentID, strings.TrimSpace(c.Query("status")))
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, items)
}

func (h *AgentHandler) ApproveChangeProposal(c *gin.Context) { h.decideChangeProposal(c, true) }
func (h *AgentHandler) RejectChangeProposal(c *gin.Context)  { h.decideChangeProposal(c, false) }
func (h *AgentHandler) decideChangeProposal(c *gin.Context, approved bool) {
	ownerID, proposalID, ok := ownerAndID(c, "id")
	if !ok {
		return
	}
	if h.improvement == nil {
		writeAppError(c, agenterrors.ErrNotFound)
		return
	}
	var request struct {
		Note string `json:"note"`
	}
	_ = c.ShouldBindJSON(&request)
	item, err := h.improvement.DecideProposal(c.Request.Context(), ownerID, proposalID, approved, request.Note)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, item)
}

func (h *AgentHandler) StreamRunEvents(c *gin.Context) {
	ownerID, runID, ok := ownerAndID(c, "id")
	if !ok {
		return
	}
	afterID, _ := strconv.ParseInt(strings.TrimSpace(c.Query("after_id")), 10, 64)
	if headerID, err := strconv.ParseInt(strings.TrimSpace(c.GetHeader("Last-Event-ID")), 10, 64); err == nil && headerID > afterID {
		afterID = headerID
	}
	if _, err := h.service.GetRun(c.Request.Context(), ownerID, runID); err != nil {
		writeAppError(c, err)
		return
	}
	writer := sse.NewWriter(c)
	ticker := time.NewTicker(400 * time.Millisecond)
	defer ticker.Stop()
	for {
		items, err := h.service.ListRunEvents(c.Request.Context(), ownerID, runID, afterID)
		if err != nil {
			_ = writer.Event("error", gin.H{"message": err.Error()})
			return
		}
		for _, item := range items {
			if err := writer.EventWithID(item.ID, item.EventType, item); err != nil {
				return
			}
			afterID = item.ID
		}
		run, err := h.service.GetRun(c.Request.Context(), ownerID, runID)
		if err != nil {
			return
		}
		if run.Status != "queued" && run.Status != "running" && run.Status != "resuming" {
			_ = writer.Event("run_status", run)
			return
		}
		select {
		case <-c.Request.Context().Done():
			return
		case <-ticker.C:
		}
	}
}

func requireOwner(c *gin.Context) (int64, bool) {
	ownerID, ok := currentUserID(c)
	if !ok {
		response.Error(c, http.StatusUnauthorized, agenterrors.CodeUnauthorized, agenterrors.ErrUnauthorized.Error())
	}
	return ownerID, ok
}

func ownerAndID(c *gin.Context, key string) (int64, int64, bool) {
	ownerID, ok := requireOwner(c)
	if !ok {
		return 0, 0, false
	}
	id, err := parseInt64Param(c, key)
	if err != nil || id <= 0 {
		writeAppError(c, agenterrors.ErrInvalidInput)
		return 0, 0, false
	}
	return ownerID, id, true
}

func agentConversationIDs(c *gin.Context) (int64, int64, int64, bool) {
	ownerID, agentID, ok := ownerAndID(c, "id")
	if !ok {
		return 0, 0, 0, false
	}
	conversationID, err := parseInt64Param(c, "conversation_id")
	if err != nil || conversationID <= 0 {
		writeAppError(c, agenterrors.ErrInvalidInput)
		return 0, 0, 0, false
	}
	return ownerID, agentID, conversationID, true
}
