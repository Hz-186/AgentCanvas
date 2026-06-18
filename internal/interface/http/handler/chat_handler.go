package handler

import (
	chatusecase "agentcanvas/internal/application/chat_usecase"
	agenterrors "agentcanvas/internal/pkg/errors"
	"agentcanvas/internal/pkg/response"
	"net/http"

	"agentcanvas/internal/interface/http/sse"

	"github.com/gin-gonic/gin"
)

type ChatHandler struct {
	service *chatusecase.Service
}

func NewChatHandler(service *chatusecase.Service) *ChatHandler {
	return &ChatHandler{service: service}
}

func (h *ChatHandler) Chat(c *gin.Context) {
	ownerID, ok := currentUserID(c)
	if !ok {
		response.Error(c, http.StatusUnauthorized, agenterrors.CodeUnauthorized, agenterrors.ErrUnauthorized.Error())
		return
	}
	var req chatusecase.ChatRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeAppError(c, err)
		return
	}
	resp, err := h.service.Chat(c.Request.Context(), ownerID, req)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, resp)
}

func (h *ChatHandler) StreamChat(c *gin.Context) {
	ownerID, ok := currentUserID(c)
	if !ok {
		response.Error(c, http.StatusUnauthorized, agenterrors.CodeUnauthorized, agenterrors.ErrUnauthorized.Error())
		return
	}
	var req chatusecase.ChatRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeAppError(c, err)
		return
	}
	writer := sse.NewWriter(c)
	err := h.service.StreamChat(c.Request.Context(), ownerID, req, func(event chatusecase.StreamEvent) error {
		return writer.Event(event.Type, event.Data)
	})
	if err != nil {
		_ = writer.Event("error", gin.H{"message": err.Error()})
	}
}

func (h *ChatHandler) ListConversations(c *gin.Context) {
	ownerID, ok := currentUserID(c)
	if !ok {
		response.Error(c, http.StatusUnauthorized, agenterrors.CodeUnauthorized, agenterrors.ErrUnauthorized.Error())
		return
	}
	items, err := h.service.ListConversations(c.Request.Context(), ownerID)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, items)
}

func (h *ChatHandler) GetConversation(c *gin.Context) {
	ownerID, ok := currentUserID(c)
	if !ok {
		response.Error(c, http.StatusUnauthorized, agenterrors.CodeUnauthorized, agenterrors.ErrUnauthorized.Error())
		return
	}
	id, err := parseInt64Param(c, "id")
	if err != nil {
		writeAppError(c, agenterrors.ErrInvalidInput)
		return
	}
	item, err := h.service.GetConversation(c.Request.Context(), ownerID, id)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, item)
}

func (h *ChatHandler) ListMessages(c *gin.Context) {
	ownerID, ok := currentUserID(c)
	if !ok {
		response.Error(c, http.StatusUnauthorized, agenterrors.CodeUnauthorized, agenterrors.ErrUnauthorized.Error())
		return
	}
	id, err := parseInt64Param(c, "id")
	if err != nil {
		writeAppError(c, agenterrors.ErrInvalidInput)
		return
	}
	items, err := h.service.ListMessages(c.Request.Context(), ownerID, id)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, items)
}

func (h *ChatHandler) DeleteConversation(c *gin.Context) {
	ownerID, ok := currentUserID(c)
	if !ok {
		response.Error(c, http.StatusUnauthorized, agenterrors.CodeUnauthorized, agenterrors.ErrUnauthorized.Error())
		return
	}
	id, err := parseInt64Param(c, "id")
	if err != nil {
		writeAppError(c, agenterrors.ErrInvalidInput)
		return
	}
	if err := h.service.DeleteConversation(c.Request.Context(), ownerID, id); err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, gin.H{"success": true})
}
