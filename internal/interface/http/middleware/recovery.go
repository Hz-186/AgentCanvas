package middleware

import (
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"agentcanvas/internal/pkg/errors"
	"agentcanvas/internal/pkg/observability"
	"agentcanvas/internal/pkg/response"

	"github.com/gin-gonic/gin"
)

func Recovery(log *slog.Logger) gin.HandlerFunc {
	if log == nil {
		log = slog.Default()
	}
	return func(c *gin.Context) {
		started := time.Now()
		defer func() {
			if r := recover(); r != nil {
				response.Error(c, http.StatusInternalServerError, errors.CodeInternal, "internal server error")
				route := c.FullPath()
				if route == "" {
					route = c.Request.URL.Path
				}
				attrs := []any{"event", "http.error", "phase", "http", "result", "error", "route", route, "status", c.Writer.Status(), "latency_ms", time.Since(started).Milliseconds(), "error_class", fmt.Sprintf("%T", r)}
				if correlation, ok := observability.CorrelationFromContext(c.Request.Context()); ok {
					attrs = append(attrs, "request_id", correlation.RequestID)
					if correlation.OwnerID > 0 {
						attrs = append(attrs, "owner_id", correlation.OwnerID)
					}
				}
				log.Error("http.error", attrs...)
				c.Abort()
			}
		}()
		c.Next()
	}
}
