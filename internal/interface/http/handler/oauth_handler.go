package handler

import (
	authusecase "agentcanvas/internal/application/auth_usecase"
	"agentcanvas/internal/pkg/response"

	"github.com/gin-gonic/gin"
)

type OAuthHandler struct {
	service *authusecase.Service
}

func NewOAuthHandler(service *authusecase.Service) *OAuthHandler {
	return &OAuthHandler{service: service}
}

func (h *OAuthHandler) GitHubRedirect(c *gin.Context) {
	url, err := h.service.BeginGitHubOAuth(c.Request.Context())
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, gin.H{"redirect_url": url})
}

func (h *OAuthHandler) GitHubCallback(c *gin.Context) {
	resp, err := h.service.HandleGitHubCallback(
		c.Request.Context(),
		c.Query("code"),
		c.Query("state"),
		clientInfoFromContext(c),
	)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, resp)
}
