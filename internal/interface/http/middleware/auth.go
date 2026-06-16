package middleware

import (
	authusecase "agentcanvas/internal/application/auth_usecase"
	authdomain "agentcanvas/internal/domain/auth"
	agenterrors "agentcanvas/internal/pkg/errors"
	"agentcanvas/internal/pkg/response"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

const UserIDKey = "user_id"

func Auth(authService *authusecase.Service, apiTokens authdomain.APITokenRepository) gin.HandlerFunc {
	return func(c *gin.Context) {
		authorization := strings.TrimSpace(c.GetHeader("Authorization"))
		if authorization == "" {
			response.Error(c, http.StatusUnauthorized, agenterrors.CodeUnauthorized, agenterrors.ErrUnauthorized.Error())
			c.Abort()
			return
		}
		parts := strings.SplitN(authorization, " ", 2)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			response.Error(c, http.StatusUnauthorized, agenterrors.CodeUnauthorized, agenterrors.ErrUnauthorized.Error())
			c.Abort()
			return
		}
		token := strings.TrimSpace(parts[1])
		if token == "" {
			response.Error(c, http.StatusUnauthorized, agenterrors.CodeUnauthorized, agenterrors.ErrUnauthorized.Error())
			c.Abort()
			return
		}

		if claims, err := authService.VerifyAccessToken(token); err == nil {
			c.Set(UserIDKey, claims.UserID)
			c.Next()
			return
		}

		if strings.HasPrefix(token, "ac_") {
			hash := authService.HashToken(token)
			apiToken, err := apiTokens.FindActiveByHash(c.Request.Context(), hash, time.Now().UTC())
			if err == nil {
				c.Set(UserIDKey, apiToken.OwnerID)
				c.Next()
				return
			}
			if err != nil && err != gorm.ErrRecordNotFound {
				response.Error(c, http.StatusInternalServerError, agenterrors.CodeInternal, err.Error())
				c.Abort()
				return
			}
		}

		response.Error(c, http.StatusUnauthorized, agenterrors.CodeUnauthorized, agenterrors.ErrUnauthorized.Error())
		c.Abort()
	}
}
