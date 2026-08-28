package httpserver

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"agentcanvas/internal/interface/http/handler"
	"github.com/gin-gonic/gin"
)

func TestRouterObservabilityMiddlewareOrderAndPanicEvent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	logHandler := &routerCaptureHandler{}
	router := NewRouter(RouterDeps{
		Logger:        slog.New(logHandler),
		HealthHandler: handler.NewHealthHandler(nil),
	})
	router.GET("/observability-panic", func(c *gin.Context) { panic("boom") })
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/observability-panic", nil)
	req.Header.Set("X-Request-ID", "rid-router")
	router.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", recorder.Code)
	}
	if recorder.Header().Get("X-Request-ID") != "rid-router" {
		t.Fatalf("request id header = %q", recorder.Header().Get("X-Request-ID"))
	}
	if len(logHandler.records) < 2 {
		t.Fatalf("records = %d, want recovery and access events", len(logHandler.records))
	}
	if logHandler.records[0].Message != "http.error" || logHandler.records[1].Message != "http.access" {
		t.Fatalf("event order = %q, %q; want recovery before access", logHandler.records[0].Message, logHandler.records[1].Message)
	}
	seenError, seenAccess := false, false
	for _, record := range logHandler.records {
		attrs := routerRecordAttrs(record)
		if record.Message == "http.error" {
			seenError = true
			if attrs["event"] != "http.error" || attrs["request_id"] != "rid-router" || attrs["route"] != "/observability-panic" || attrs["status"] != int64(http.StatusInternalServerError) {
				t.Fatalf("error attrs = %#v", attrs)
			}
		}
		if record.Message == "http.access" {
			seenAccess = true
			if attrs["event"] != "http.access" || attrs["status"] != int64(http.StatusInternalServerError) || attrs["request_id"] != "rid-router" {
				t.Fatalf("access attrs = %#v", attrs)
			}
		}
	}
	if !seenError || !seenAccess {
		t.Fatalf("records missing events: %#v", logHandler.records)
	}
}

func routerRecordAttrs(record slog.Record) map[string]any {
	attrs := map[string]any{}
	record.Attrs(func(attr slog.Attr) bool {
		attrs[attr.Key] = attr.Value.Any()
		return true
	})
	return attrs
}

type routerCaptureHandler struct{ records []slog.Record }

func (h *routerCaptureHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *routerCaptureHandler) Handle(_ context.Context, r slog.Record) error {
	h.records = append(h.records, r.Clone())
	return nil
}
func (h *routerCaptureHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *routerCaptureHandler) WithGroup(string) slog.Handler      { return h }
