package logger

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"log/slog"

	"agentcanvas/internal/pkg/observability"
)

// capturingSlogHandler records every record that reaches the inner sink so
// tests can assert what the diagnostics boundary let through.
type capturingSlogHandler struct {
	mu      sync.Mutex
	records []capturedSlogRecord
}

type capturedSlogRecord struct {
	level slog.Level
	msg   string
	attrs map[string]any
}

func (*capturingSlogHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *capturingSlogHandler) Handle(_ context.Context, record slog.Record) error {
	captured := capturedSlogRecord{level: record.Level, msg: record.Message, attrs: map[string]any{}}
	record.Attrs(func(attr slog.Attr) bool {
		captured.attrs[attr.Key] = attr.Value.Any()
		return true
	})
	h.mu.Lock()
	h.records = append(h.records, captured)
	h.mu.Unlock()
	return nil
}

func (h *capturingSlogHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *capturingSlogHandler) WithGroup(string) slog.Handler      { return h }

func (h *capturingSlogHandler) last(t *testing.T) capturedSlogRecord {
	t.Helper()
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.records) == 0 {
		t.Fatal("expected at least one captured log record")
	}
	return h.records[len(h.records)-1]
}

func (h *capturingSlogHandler) containsValue(needle string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, record := range h.records {
		if strings.Contains(record.msg, needle) {
			return true
		}
		for _, value := range record.attrs {
			if strings.Contains(stringifyAttrValue(value), needle) {
				return true
			}
		}
	}
	return false
}

func stringifyAttrValue(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	default:
		data, err := json.Marshal(value)
		if err != nil {
			return ""
		}
		return string(data)
	}
}

// failingSlogHandler simulates a broken sink (disk full, closed pipe, ...).
type failingSlogHandler struct {
	mu    sync.Mutex
	calls int
	err   error
}

func (*failingSlogHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *failingSlogHandler) Handle(context.Context, slog.Record) error {
	h.mu.Lock()
	h.calls++
	h.mu.Unlock()
	return h.err
}

func (h *failingSlogHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *failingSlogHandler) WithGroup(string) slog.Handler      { return h }

func correlationTestContext() context.Context {
	parentRunID := int64(77)
	return observability.WithCorrelation(context.Background(), observability.Correlation{}.
		WithRequestID("rid-evt-1").
		WithOwnerID(3).
		WithConversationID(20).
		WithRunID(401).
		WithTurnID(201).
		WithParentRunID(&parentRunID))
}

func TestLoggerEventEmitsStableMetadataAttributes(t *testing.T) {
	inner := &capturingSlogHandler{}
	sink := slog.New(NewDiagnosticsHandler(inner))
	ctx := correlationTestContext()

	sink.Log(ctx, slog.LevelInfo, "llm.completed",
		"event", "llm.completed", "phase", "llm", "result", "ok", "latency_ms", 42,
		"provider", "openai_compatible", "model", "gpt-4",
		"prompt", "SECRET-PROMPT-BODY", "api_key", "SECRET-API-KEY")

	record := inner.last(t)
	for key, want := range map[string]any{
		"event":           "llm.completed",
		"phase":           "llm",
		"result":          "ok",
		"latency_ms":      int64(42),
		"provider":        "openai_compatible",
		"model":           "gpt-4",
		"request_id":      "rid-evt-1",
		"owner_id":        int64(3),
		"conversation_id": int64(20),
		"run_id":          int64(401),
		"turn_id":         int64(201),
		"parent_run_id":   int64(77),
	} {
		if got, ok := record.attrs[key]; !ok || got != want {
			t.Fatalf("attribute %q = %#v (present=%v), want %#v; attrs=%#v", key, got, ok, want, record.attrs)
		}
	}
	for _, forbidden := range []string{"prompt", "api_key"} {
		if _, ok := record.attrs[forbidden]; ok {
			t.Fatalf("forbidden attribute %q leaked into the diagnostic event: %#v", forbidden, record.attrs)
		}
	}
	if inner.containsValue("SECRET-PROMPT-BODY") || inner.containsValue("SECRET-API-KEY") {
		t.Fatal("prompt or API key content leaked into the diagnostic event")
	}
}

func TestLoggerPrivacyDropsDisallowedAndTruncatesOversizedAttributes(t *testing.T) {
	serialized := &bytes.Buffer{}
	inner := slog.NewJSONHandler(serialized, &slog.HandlerOptions{Level: slog.LevelDebug})
	sink := slog.New(NewDiagnosticsHandler(inner))
	ctx := correlationTestContext()

	oversizedSummary := strings.Repeat("e", 40*1024)
	sink.Log(ctx, slog.LevelError, "tool.completed",
		"event", "tool.completed", "phase", "tool", "result", "error", "latency_ms", 7,
		"tool_name", "shell", "tool_call_id", "call_1", "error_class", "*errors.errorString",
		"authorization", "Bearer secret-token",
		"prompt", "user prompt body",
		"tool_output", strings.Repeat("o", 8*1024),
		"error_summary", oversizedSummary)

	if serialized.Len() == 0 {
		t.Fatal("bounded event was dropped entirely even though truncation was possible")
	}
	if serialized.Len() > MaxSerializedEventBytes {
		t.Fatalf("serialized event exceeds the 16 KiB bound: %d bytes", serialized.Len())
	}
	var line map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(serialized.Bytes()), &line); err != nil {
		t.Fatalf("diagnostic event is not valid JSON: %v", err)
	}
	for _, forbidden := range []string{"authorization", "prompt", "tool_output"} {
		if _, ok := line[forbidden]; ok {
			t.Fatalf("disallowed attribute %q survived the whitelist boundary: %v", forbidden, line)
		}
	}
	for key, want := range map[string]any{
		"event":       "tool.completed",
		"phase":       "tool",
		"result":      "error",
		"error_class": "*errors.errorString",
		"tool_name":   "shell",
		"request_id":  "rid-evt-1",
	} {
		if line[key] != want {
			t.Fatalf("whitelisted attribute %q = %#v, want %#v", key, line[key], want)
		}
	}
	summary, _ := line["error_summary"].(string)
	if summary == "" || len(summary) >= len(oversizedSummary) {
		t.Fatalf("oversized error summary was not truncated: len=%d", len(summary))
	}
	if strings.Contains(serialized.String(), "Bearer secret-token") || strings.Contains(serialized.String(), "user prompt body") {
		t.Fatal("sensitive content leaked through the diagnostics boundary")
	}
}

func TestLoggerFailureIsolationEmitsAtMostOneSinkError(t *testing.T) {
	failing := &failingSlogHandler{err: errors.New("sink broken")}
	fallback := &bytes.Buffer{}
	sink := slog.New(NewDiagnosticsHandlerWithFallback(failing, fallback))

	businessResult := ""
	for i := 0; i < 5; i++ {
		// The business path keeps producing results while the observation sink
		// is unavailable; logging must never block, retry, or surface errors.
		started := time.Now()
		sink.Log(context.Background(), slog.LevelInfo, "turn.finished",
			"event", "turn.finished", "phase", "turn", "result", "ok", "latency_ms", i)
		if elapsed := time.Since(started); elapsed > time.Second {
			t.Fatalf("diagnostics logging blocked the business path for %s", elapsed)
		}
		businessResult = "finished"
	}

	if businessResult != "finished" {
		t.Fatalf("sink failure changed the business result: %q", businessResult)
	}
	if failing.calls != 1 {
		t.Fatalf("failing sink must not be retried per event: %d calls", failing.calls)
	}
	sinkErrors := strings.Count(strings.TrimSpace(fallback.String()), "\n") + 1
	if strings.TrimSpace(fallback.String()) == "" {
		t.Fatal("sink failure was not recorded at all")
	}
	if sinkErrors != 1 {
		t.Fatalf("expected at most one bounded sink error record, got %d: %q", sinkErrors, fallback.String())
	}
	if strings.Contains(fallback.String(), "turn.finished") || strings.Contains(fallback.String(), "phase") {
		t.Fatalf("sink error record must not carry the original event content: %q", fallback.String())
	}
}
