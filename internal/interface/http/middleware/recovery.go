package middleware

import (
	"log/slog"
	"net/http"

	"agentcanvas/internal/pkg/errors"
	"agentcanvas/internal/pkg/response"

	"github.com/gin-gonic/gin"
)

func Recovery(log *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if r := recover(); r != nil {
				log.Error("panic recovered", "error", r)
				response.Error(c, http.StatusInternalServerError, errors.CodeInternal, "internal server error")
				c.Abort()
			}
		}()
		c.Next()
	}
}
