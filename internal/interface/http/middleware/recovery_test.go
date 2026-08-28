package middleware

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"agentcanvas/internal/pkg/observability"
	"github.com/gin-gonic/gin"
)

func TestRecoveryMiddlewareConvertsPanicTo500AndLogsCorrelation(t *testing.T) {
	handler := &captureHandler{}
	r := gin.New()
	r.Use(func(c *gin.Context) {
		ctx := observability.WithCorrelation(c.Request.Context(), observability.Correlation{RequestID: "rid-panic", OwnerID: 42})
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	})
	r.Use(Recovery(slog.New(handler)))
	continued := false
	r.GET("/panic", func(c *gin.Context) {
		panic("boom")
	})
	r.GET("/after", func(c *gin.Context) { continued = true; c.Status(http.StatusNoContent) })
	recorder := httptest.NewRecorder()
	r.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/panic", nil))
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", recorder.Code)
	}
	if continued {
		t.Fatal("downstream continuation occurred after panic")
	}
	if len(handler.records) != 1 || handler.records[0].Message != "http.error" {
		t.Fatalf("records = %#v", handler.records)
	}
	attrs := recordAttrs(handler.records[0])
	if attrs["event"] != "http.error" || attrs["phase"] != "http" || attrs["result"] != "error" || attrs["route"] != "/panic" || attrs["status"] != int64(http.StatusInternalServerError) || attrs["request_id"] != "rid-panic" || attrs["owner_id"] != int64(42) {
		t.Fatalf("attrs = %#v", attrs)
	}
	latency, ok := attrs["latency_ms"].(int64)
	if !ok || latency < 0 {
		t.Fatalf("latency_ms = %#v", attrs["latency_ms"])
	}
}
