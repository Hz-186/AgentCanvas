package middleware

import (
	"agentcanvas/internal/pkg/idgen"

	"github.com/gin-gonic/gin"
)

const RequestIDKey = "request_id"

func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		requestID := c.GetHeader("X-Request-ID")
		if requestID == "" {
			requestID = idgen.NewRequestID()
		}

		c.Set(RequestIDKey, requestID)
		c.Header("X-Request-ID", requestID)

		c.Next()
	}
}