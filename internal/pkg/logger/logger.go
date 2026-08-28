package logger

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync"

	"agentcanvas/internal/pkg/observability"
)

// MaxSerializedEventBytes bounds one serialized diagnostic event (16 KiB).
const MaxSerializedEventBytes = 16 * 1024

// envelopeOverheadBytes reserves room for the sink's own envelope fields
// (time, level, message, JSON punctuation) when enforcing the serialized cap.
const envelopeOverheadBytes = 192

func New(env string) *slog.Logger {
	if env == "local" {
		return slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
			Level: slog.LevelDebug,
		}))
	}

	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
}

// allowedDiagnosticAttributes is the metadata whitelist (design Decision 3).
// Anything outside this set is dropped at the diagnostics boundary, so
// prompts, message bodies, API keys, tool arguments/output, and RunEvent
// payload content can never reach the observation sink.
var allowedDiagnosticAttributes = map[string]struct{}{
	"event": {}, "phase": {}, "result": {},
	"request_id": {}, "owner_id": {}, "conversation_id": {}, "run_id": {},
	"turn_id": {}, "parent_run_id": {}, "step_index": {}, "tool_call_id": {},
	"route": {}, "status": {}, "provider": {}, "model": {}, "tool_name": {},
	"error_class": {}, "latency_ms": {}, "usage": {}, "error_summary": {},
}

// DiagnosticsHandler is the shared observation boundary for lifecycle
// diagnostics. It enriches each record with the correlation stored on ctx,
// drops non-whitelisted attributes, caps the serialized size, and isolates
// sink failures: a broken sink records at most one bounded sink error, is
// never retried, and never surfaces an error to the business path.
type DiagnosticsHandler struct {
	inner    slog.Handler
	fallback io.Writer

	mu         sync.Mutex
	sinkFailed bool
}

// NewDiagnosticsHandler wraps inner with the diagnostics boundary. Sink
// failures are swallowed silently (fail-open) because no fallback is set.
func NewDiagnosticsHandler(inner slog.Handler) *DiagnosticsHandler {
	return &DiagnosticsHandler{inner: inner}
}

// NewDiagnosticsHandlerWithFallback wraps inner with the diagnostics boundary
// and directs the single bounded sink-error record to fallback.
func NewDiagnosticsHandlerWithFallback(inner slog.Handler, fallback io.Writer) *DiagnosticsHandler {
	return &DiagnosticsHandler{inner: inner, fallback: fallback}
}

func (h *DiagnosticsHandler) Enabled(ctx context.Context, level slog.Level) bool {
	h.mu.Lock()
	failed := h.sinkFailed
	h.mu.Unlock()
	if failed {
		// A broken sink stays broken: never retry, never block.
		return false
	}
	return h.inner.Enabled(ctx, level)
}

func (h *DiagnosticsHandler) Handle(ctx context.Context, record slog.Record) error {
	h.mu.Lock()
	failed := h.sinkFailed
	h.mu.Unlock()
	if failed {
		return nil
	}
	bounded := slog.NewRecord(record.Time, record.Level, record.Message, record.PC)
	bounded.AddAttrs(boundedAttributes(ctx, record)...)
	if err := h.inner.Handle(ctx, bounded); err != nil {
		h.mu.Lock()
		firstFailure := !h.sinkFailed
		h.sinkFailed = true
		h.mu.Unlock()
		if firstFailure {
			h.reportSinkFailure(err)
		}
	}
	// Sink failures never change the business result.
	return nil
}

// reportSinkFailure writes at most one bounded, metadata-only sink error
// record. It never carries the original event content.
func (h *DiagnosticsHandler) reportSinkFailure(err error) {
	if h.fallback == nil {
		return
	}
	fmt.Fprintf(h.fallback, "diagnostics sink failed: error_class=%s; further diagnostic events dropped\n", ErrorClass(err))
}

func (h *DiagnosticsHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &DiagnosticsHandler{inner: h.inner.WithAttrs(attrs), fallback: h.fallback}
}

func (h *DiagnosticsHandler) WithGroup(name string) slog.Handler {
	return &DiagnosticsHandler{inner: h.inner.WithGroup(name), fallback: h.fallback}
}

// boundedAttributes whitelists the record attributes, enriches them with the
// ctx correlation, dedupes keys (first occurrence wins), and caps the
// serialized size by truncating the largest string values.
func boundedAttributes(ctx context.Context, record slog.Record) []slog.Attr {
	kept := make([]slog.Attr, 0, record.NumAttrs()+8)
	seen := make(map[string]struct{}, record.NumAttrs()+8)
	record.Attrs(func(attr slog.Attr) bool {
		attr.Value = attr.Value.Resolve()
		if _, allowed := allowedDiagnosticAttributes[attr.Key]; !allowed {
			return true
		}
		if _, dup := seen[attr.Key]; dup {
			return true
		}
		seen[attr.Key] = struct{}{}
		kept = append(kept, attr)
		return true
	})
	for _, attr := range CorrelationAttrs(ctx) {
		if _, dup := seen[attr.Key]; dup {
			continue
		}
		seen[attr.Key] = struct{}{}
		kept = append(kept, attr)
	}
	return truncateToSerializedBudget(kept, record.Message)
}

// CorrelationAttrs expands the correlation stored on ctx into whitelisted
// diagnostic attributes. Zero values are omitted so absent identifiers stay
// absent instead of being fabricated.
func CorrelationAttrs(ctx context.Context) []slog.Attr {
	correlation, ok := observability.CorrelationFromContext(ctx)
	if !ok {
		return nil
	}
	attrs := make([]slog.Attr, 0, 8)
	if correlation.RequestID != "" {
		attrs = append(attrs, slog.String("request_id", correlation.RequestID))
	}
	if correlation.OwnerID != 0 {
		attrs = append(attrs, slog.Int64("owner_id", correlation.OwnerID))
	}
	if correlation.ConversationID != 0 {
		attrs = append(attrs, slog.Int64("conversation_id", correlation.ConversationID))
	}
	if correlation.RunID != 0 {
		attrs = append(attrs, slog.Int64("run_id", correlation.RunID))
	}
	if correlation.TurnID != 0 {
		attrs = append(attrs, slog.Int64("turn_id", correlation.TurnID))
	}
	if correlation.ParentRunID != nil {
		attrs = append(attrs, slog.Int64("parent_run_id", *correlation.ParentRunID))
	}
	if correlation.StepIndex != 0 {
		attrs = append(attrs, slog.Int("step_index", correlation.StepIndex))
	}
	if correlation.ToolCallID != "" {
		attrs = append(attrs, slog.String("tool_call_id", correlation.ToolCallID))
	}
	return attrs
}

// ErrorClass derives a bounded, metadata-only classification from err: the
// error's type, never its text.
func ErrorClass(err error) string {
	if err == nil {
		return ""
	}
	return fmt.Sprintf("%T", err)
}

// truncateToSerializedBudget shrinks the largest string attribute until the
// estimated serialization fits under MaxSerializedEventBytes.
func truncateToSerializedBudget(attrs []slog.Attr, message string) []slog.Attr {
	budget := MaxSerializedEventBytes - len(message) - envelopeOverheadBytes
	for {
		size, longestIndex := serializedAttrsSize(attrs)
		if size <= budget || longestIndex < 0 {
			return attrs
		}
		text, _ := attrs[longestIndex].Value.Any().(string)
		cut := len(text) - (size - budget)
		if cut < 0 {
			cut = 0
		}
		attrs[longestIndex] = slog.String(attrs[longestIndex].Key, text[:cut])
	}
}

// serializedAttrsSize estimates the serialized attribute size and reports the
// index of the longest string attribute (-1 when none exists).
func serializedAttrsSize(attrs []slog.Attr) (int, int) {
	payload := make(map[string]any, len(attrs))
	// longestLen starts at 0 so empty strings are never selected as truncation
	// candidates; this guarantees termination once no non-empty string remains.
	longestIndex, longestLen := -1, 0
	for index, attr := range attrs {
		value := attr.Value.Resolve().Any()
		payload[attr.Key] = value
		if text, ok := value.(string); ok && len(text) > longestLen {
			longestIndex, longestLen = index, len(text)
		}
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return MaxSerializedEventBytes + 1, longestIndex
	}
	return len(data), longestIndex
}
