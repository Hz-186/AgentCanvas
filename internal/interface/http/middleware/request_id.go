package middleware

import (
	"agentcanvas/internal/pkg/idgen"
	"agentcanvas/internal/pkg/observability"

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
		correlation, _ := observability.CorrelationFromContext(c.Request.Context())
		c.Request = c.Request.WithContext(observability.WithCorrelation(c.Request.Context(), correlation.WithRequestID(requestID)))

		c.Next()
	}
}
