package handler

import (
	authusecase "agentcanvas/internal/application/auth_usecase"
	agenterrors "agentcanvas/internal/pkg/errors"
	"agentcanvas/internal/pkg/response"
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

type AuthHandler struct {
	service *authusecase.Service
}

func NewAuthHandler(service *authusecase.Service) *AuthHandler {
	return &AuthHandler{service: service}
}

func (h *AuthHandler) Register(c *gin.Context) {
	var req authusecase.RegisterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeAppError(c, err)
		return
	}
	resp, err := h.service.Register(c.Request.Context(), req, clientInfoFromContext(c))
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, resp)
}

func (h *AuthHandler) Login(c *gin.Context) {
	var req authusecase.LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeAppError(c, err)
		return
	}
	resp, err := h.service.Login(c.Request.Context(), req, clientInfoFromContext(c))
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, resp)
}

func (h *AuthHandler) Refresh(c *gin.Context) {
	var req authusecase.RefreshRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeAppError(c, err)
		return
	}
	resp, err := h.service.Refresh(c.Request.Context(), req, clientInfoFromContext(c))
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, resp)
}

func (h *AuthHandler) Logout(c *gin.Context) {
	var req authusecase.LogoutRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeAppError(c, err)
		return
	}
	if err := h.service.Logout(c.Request.Context(), req); err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, gin.H{"success": true})
}

func (h *AuthHandler) Me(c *gin.Context) {
	userID, ok := currentUserID(c)
	if !ok {
		response.Error(c, http.StatusUnauthorized, agenterrors.CodeUnauthorized, agenterrors.ErrUnauthorized.Error())
		return
	}
	resp, err := h.service.Me(c.Request.Context(), userID)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, resp)
}

func (h *AuthHandler) ListAPITokens(c *gin.Context) {
	ownerID, ok := currentUserID(c)
	if !ok {
		response.Error(c, http.StatusUnauthorized, agenterrors.CodeUnauthorized, agenterrors.ErrUnauthorized.Error())
		return
	}
	resp, err := h.service.ListAPITokens(c.Request.Context(), ownerID)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, resp)
}

func (h *AuthHandler) CreateAPIToken(c *gin.Context) {
	ownerID, ok := currentUserID(c)
	if !ok {
		response.Error(c, http.StatusUnauthorized, agenterrors.CodeUnauthorized, agenterrors.ErrUnauthorized.Error())
		return
	}
	var req authusecase.CreateAPITokenRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeAppError(c, err)
		return
	}
	resp, err := h.service.CreateAPIToken(c.Request.Context(), ownerID, req)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, resp)
}

func (h *AuthHandler) DeleteAPIToken(c *gin.Context) {
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
	if err := h.service.RevokeAPIToken(c.Request.Context(), ownerID, id); err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, gin.H{"success": true})
}

func clientInfoFromContext(c *gin.Context) authusecase.ClientInfo {
	return authusecase.ClientInfo{
		UserAgent: c.Request.UserAgent(),
		IPAddress: realIP(c),
	}
}

func currentUserID(c *gin.Context) (int64, bool) {
	v, ok := c.Get("user_id")
	if !ok {
		return 0, false
	}
	switch id := v.(type) {
	case int64:
		return id, true
	case int:
		return int64(id), true
	case float64:
		return int64(id), true
	default:
		return 0, false
	}
}

func realIP(c *gin.Context) string {
	for _, header := range []string{"X-Forwarded-For", "X-Real-IP"} {
		value := strings.TrimSpace(c.GetHeader(header))
		if value == "" {
			continue
		}
		if header == "X-Forwarded-For" {
			parts := strings.Split(value, ",")
			value = strings.TrimSpace(parts[0])
		}
		if value != "" {
			return value
		}
	}
	host, _, err := net.SplitHostPort(strings.TrimSpace(c.Request.RemoteAddr))
	if err == nil {
		return host
	}
	return strings.TrimSpace(c.Request.RemoteAddr)
}

func writeAppError(c *gin.Context, err error) {
	switch {
	case err == nil:
		return
	case errors.Is(err, agenterrors.ErrInvalidInput):
		response.Error(c, http.StatusBadRequest, agenterrors.CodeBadRequest, err.Error())
	case errors.Is(err, agenterrors.ErrUnauthorized):
		response.Error(c, http.StatusUnauthorized, agenterrors.CodeUnauthorized, err.Error())
	case errors.Is(err, agenterrors.ErrNotFound):
		response.Error(c, http.StatusNotFound, agenterrors.CodeNotFound, err.Error())
	case errors.Is(err, agenterrors.ErrConflict):
		response.Error(c, http.StatusConflict, agenterrors.CodeBadRequest, err.Error())
	default:
		response.Error(c, http.StatusInternalServerError, agenterrors.CodeInternal, err.Error())
	}
}

func parseInt64Param(c *gin.Context, key string) (int64, error) {
	return strconv.ParseInt(c.Param(key), 10, 64)
}
