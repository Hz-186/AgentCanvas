package handler

import (
	"context"
	"net/url"

	authusecase "agentcanvas/internal/application/auth_usecase"
	"agentcanvas/internal/pkg/response"

	"github.com/gin-gonic/gin"
)

type OAuthService interface {
	BeginGitHubOAuth(ctx context.Context) (string, error)
	HandleGitHubCallbackCode(ctx context.Context, code, state string, client authusecase.ClientInfo) (string, error)
	ExchangeOAuthCode(ctx context.Context, code string) (*authusecase.AuthResponse, error)
}

type OAuthHandler struct {
	service OAuthService
}

func NewOAuthHandler(service OAuthService) *OAuthHandler {
	return &OAuthHandler{service: service}
}

func (h *OAuthHandler) GitHubRedirect(c *gin.Context) {
	redirectURL, err := h.service.BeginGitHubOAuth(c.Request.Context())
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, gin.H{"redirect_url": redirectURL})
}

func (h *OAuthHandler) GitHubCallback(c *gin.Context) {
	loginCode, err := h.service.HandleGitHubCallbackCode(
		c.Request.Context(),
		c.Query("code"),
		c.Query("state"),
		clientInfoFromContext(c),
	)
	if err != nil {
		v := url.Values{}
		v.Set("error", err.Error())
		c.Redirect(302, "/login?"+v.Encode())
		return
	}
	v := url.Values{}
	v.Set("oauth_code", loginCode)
	c.Redirect(302, "/login?"+v.Encode())
}

func (h *OAuthHandler) ExchangeCode(c *gin.Context) {
	var req struct {
		Code string `json:"code" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		writeAppError(c, err)
		return
	}
	resp, err := h.service.ExchangeOAuthCode(c.Request.Context(), req.Code)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, resp)
}
