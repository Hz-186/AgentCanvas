package handler

import (
	"net/http"

	knowledgeusecase "agentcanvas/internal/application/knowledge_usecase"
	agenterrors "agentcanvas/internal/pkg/errors"
	"agentcanvas/internal/pkg/response"

	"github.com/gin-gonic/gin"
)

type KnowledgeHandler struct {
	service *knowledgeusecase.Service
}

func NewKnowledgeHandler(service *knowledgeusecase.Service) *KnowledgeHandler {
	return &KnowledgeHandler{service: service}
}

func (h *KnowledgeHandler) Create(c *gin.Context) {
	ownerID, ok := currentUserID(c)
	if !ok {
		response.Error(c, http.StatusUnauthorized, agenterrors.CodeUnauthorized, agenterrors.ErrUnauthorized.Error())
		return
	}
	var req knowledgeusecase.CreateKnowledgeBaseRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeAppError(c, err)
		return
	}
	item, err := h.service.CreateKnowledgeBase(c.Request.Context(), ownerID, req, knowledgeClientInfo(c))
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, item)
}

func (h *KnowledgeHandler) List(c *gin.Context) {
	ownerID, ok := currentUserID(c)
	if !ok {
		response.Error(c, http.StatusUnauthorized, agenterrors.CodeUnauthorized, agenterrors.ErrUnauthorized.Error())
		return
	}
	items, err := h.service.ListKnowledgeBases(c.Request.Context(), ownerID)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, items)
}

func (h *KnowledgeHandler) Get(c *gin.Context) {
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
	item, err := h.service.GetKnowledgeBase(c.Request.Context(), ownerID, id)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, item)
}

func (h *KnowledgeHandler) Update(c *gin.Context) {
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
	var req knowledgeusecase.UpdateKnowledgeBaseRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeAppError(c, err)
		return
	}
	item, err := h.service.UpdateKnowledgeBase(c.Request.Context(), ownerID, id, req, knowledgeClientInfo(c))
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, item)
}

func (h *KnowledgeHandler) Delete(c *gin.Context) {
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
	if err := h.service.DeleteKnowledgeBase(c.Request.Context(), ownerID, id, knowledgeClientInfo(c)); err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, gin.H{"success": true})
}

func (h *KnowledgeHandler) Reindex(c *gin.Context) {
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
	resp, err := h.service.ReindexKnowledgeBase(c.Request.Context(), ownerID, id, knowledgeClientInfo(c))
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, resp)
}

func (h *KnowledgeHandler) Search(c *gin.Context) {
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
	var req knowledgeusecase.SearchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeAppError(c, err)
		return
	}
	resp, err := h.service.Search(c.Request.Context(), ownerID, id, req)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, resp)
}

func (h *KnowledgeHandler) UploadDocument(c *gin.Context) {
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
	fileHeader, err := c.FormFile("file")
	if err != nil {
		writeAppError(c, agenterrors.ErrInvalidInput)
		return
	}
	resp, err := h.service.UploadDocument(c.Request.Context(), ownerID, id, knowledgeusecase.UploadDocumentRequest{
		Name:        c.PostForm("name"),
		FileHeader:  fileHeader,
		ContentType: fileHeader.Header.Get("Content-Type"),
	}, knowledgeClientInfo(c))
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, resp)
}

func (h *KnowledgeHandler) ListDocuments(c *gin.Context) {
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
	items, err := h.service.ListDocuments(c.Request.Context(), ownerID, id)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, items)
}

func (h *KnowledgeHandler) GetIngestionJob(c *gin.Context) {
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
	job, err := h.service.GetIngestionJob(c.Request.Context(), ownerID, id)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, job)
}

func knowledgeClientInfo(c *gin.Context) knowledgeusecase.ClientInfo {
	return knowledgeusecase.ClientInfo{UserAgent: c.Request.UserAgent(), IPAddress: realIP(c)}
}
