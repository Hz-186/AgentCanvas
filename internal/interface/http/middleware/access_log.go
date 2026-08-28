package middleware

import (
	"log/slog"
	"net/http"
	"time"

	"agentcanvas/internal/pkg/observability"
	"github.com/gin-gonic/gin"
)

// AccessLog records one privacy-safe summary after the downstream chain has
// completed. Request and response bodies, authorization headers and tokens
// are intentionally excluded.
func AccessLog(log *slog.Logger) gin.HandlerFunc {
	if log == nil {
		log = slog.Default()
	}
	return func(c *gin.Context) {
		started := time.Now()
		c.Next()
		route := c.FullPath()
		if route == "" {
			route = c.Request.URL.Path
		}
		attrs := []any{
			"event", "http.access",
			"phase", "http",
			"result", func() string {
				if c.Writer.Status() >= http.StatusInternalServerError {
					return "error"
				}
				return "ok"
			}(),
			"route", route,
			"status", c.Writer.Status(),
			"latency_ms", time.Since(started).Milliseconds(),
		}
		if correlation, ok := observability.CorrelationFromContext(c.Request.Context()); ok {
			attrs = append(attrs, "request_id", correlation.RequestID)
			if correlation.OwnerID > 0 {
				attrs = append(attrs, "owner_id", correlation.OwnerID)
			}
		}
		log.LogAttrs(c.Request.Context(), slog.LevelInfo, "http.access", attrsToSlog(attrs)...)
	}
}

func attrsToSlog(values []any) []slog.Attr {
	attrs := make([]slog.Attr, 0, len(values)/2)
	for i := 0; i+1 < len(values); i += 2 {
		attrs = append(attrs, slog.Any(values[i].(string), values[i+1]))
	}
	return attrs
}
