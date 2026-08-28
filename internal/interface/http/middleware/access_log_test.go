package middleware

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"agentcanvas/internal/pkg/observability"
	"github.com/gin-gonic/gin"
)

type captureHandler struct {
	records []slog.Record
}

func (h *captureHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *captureHandler) Handle(_ context.Context, r slog.Record) error {
	h.records = append(h.records, r.Clone())
	return nil
}
func (h *captureHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *captureHandler) WithGroup(string) slog.Handler      { return h }

func recordAttrs(record slog.Record) map[string]any {
	attrs := map[string]any{}
	record.Attrs(func(attr slog.Attr) bool {
		attrs[attr.Key] = attr.Value.Any()
		return true
	})
	return attrs
}

func TestAccessLogMiddlewareRecordsStatusRouteLatencyAndCorrelation(t *testing.T) {
	handler := &captureHandler{}
	logger := slog.New(handler)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		ctx := observability.WithCorrelation(c.Request.Context(), observability.Correlation{RequestID: "rid-access"})
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	})
	r.Use(AccessLog(logger))
	r.POST("/widgets", func(c *gin.Context) { c.Status(http.StatusCreated) })
	recorder := httptest.NewRecorder()
	r.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/widgets", nil))
	if len(handler.records) != 1 {
		t.Fatalf("records = %d, want 1", len(handler.records))
	}
	record := handler.records[0]
	if record.Message != "http.access" {
		t.Fatalf("message = %q", record.Message)
	}
	attrs := recordAttrs(record)
	if attrs["event"] != "http.access" || attrs["phase"] != "http" || attrs["result"] != "ok" || attrs["route"] != "/widgets" || attrs["status"] != int64(http.StatusCreated) || attrs["request_id"] != "rid-access" {
		t.Fatalf("attrs = %#v", attrs)
	}
	latency, ok := attrs["latency_ms"].(int64)
	if !ok || latency < 0 {
		t.Fatalf("latency_ms = %#v", attrs["latency_ms"])
	}
}
