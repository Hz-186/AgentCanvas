# Task 4 Review Package — observability-correlation-tracing

BASE commit (no commits since; auto_commit=false): ff9eaf914eedd477874df0164308b5849788f6b2

## git diff --stat (tracked task files)
 .../application/agent_usecase/run_publisher.go     |  28 +++
 .../agent_usecase/run_publisher_test.go            | 103 ++++++++++
 internal/pkg/logger/logger.go                      | 213 +++++++++++++++++++++
 internal/runtime/agent/auto_compaction.go          |  37 ++++
 internal/runtime/agent/model_turn.go               |  45 ++++-
 internal/runtime/agent/model_turn_test.go          | 106 ++++++++++
 internal/runtime/agent/runner.go                   |  52 +++++
 internal/runtime/agent/runner_test.go              | 140 ++++++++++++++
 8 files changed, 723 insertions(+), 1 deletion(-)

## Untracked new files
?? internal/pkg/logger/logger_test.go
?? internal/runtime/agent/auto_compaction_diagnostics_test.go

## git diff -U10 (tracked task files)
diff --git a/internal/application/agent_usecase/run_publisher.go b/internal/application/agent_usecase/run_publisher.go
index 3b881eb..a3738db 100644
--- a/internal/application/agent_usecase/run_publisher.go
+++ b/internal/application/agent_usecase/run_publisher.go
@@ -1,17 +1,18 @@
 package agent_usecase
 
 import (
 	"context"
 	"encoding/json"
 	"errors"
 	"fmt"
+	"log/slog"
 	"sync"
 	"time"
 
 	"agentcanvas/internal/domain"
 	agentdomain "agentcanvas/internal/domain/agent"
 	"agentcanvas/internal/domain/conversation"
 	workspacedomain "agentcanvas/internal/domain/workspace"
 	"agentcanvas/internal/infrastructure/llm"
 	runtimeagent "agentcanvas/internal/runtime/agent"
 	agentruntime "agentcanvas/internal/runtime/agentruntime"
@@ -20,36 +21,41 @@ import (
 )
 
 type runStreamHub = eventhub.Hub
 
 type runEventEmitter struct {
 	repo           agentdomain.RunEventRepository
 	hub            runStreamHub
 	ownerID, runID int64
 	conversationID *int64
 	onUsage        func(context.Context, int64, llm.Usage)
+	// diagnostics is the optional fail-open observation seam for RunEvent
+	// lifecycle diagnostics. It only ever sees event metadata, never the
+	// payload. Nil falls back to slog.Default.
+	diagnostics *slog.Logger
 
 	// mu is the per-run publication lane. It keeps sequence reservation, the
 	// durable audit append, and live publication in the same order while also
 	// preventing model deltas from overtaking durable lifecycle events.
 	mu               sync.Mutex
 	assistantSegment string
 	reasoningSegment string
 	planSegment      string
 }
 
 func (s *Service) ConfigureEventHub(hub runStreamHub) {
 	s.streamHub = hub
 }
 
 func (s *Service) newRunEventEmitter(ownerID, runID int64, conversationID *int64) *runEventEmitter {
 	return &runEventEmitter{repo: s.events, hub: s.streamHub, ownerID: ownerID, runID: runID, conversationID: conversationID,
+		diagnostics: s.diagnosticsLogger(),
 		onUsage: func(ctx context.Context, runID int64, usage llm.Usage) {
 			s.accountGoalUsageEvent(ctx, ownerID, runID, usage)
 		}}
 }
 
 var ErrRunStreamUnavailable = errors.New("run event stream is not configured")
 
 func (s *Service) SubscribeRunStream(ctx context.Context, ownerID, runID int64, afterSeq uint64) ([]eventhub.StreamEvent, <-chan eventhub.StreamEvent, func(), error) {
 	if _, err := s.GetRun(ctx, ownerID, runID); err != nil {
 		return nil, nil, nil, err
@@ -73,23 +79,45 @@ func (e *runEventEmitter) Emit(ctx context.Context, event runtimeevent.Event) er
 	if event.CreatedAt.IsZero() {
 		event.CreatedAt = time.Now().UTC()
 	}
 	projected := e.prepare(projectRuntimeEvent(event, e.conversationID))
 	payload, _ := json.Marshal(event.Payload)
 	if err := e.repo.Create(ctx, &agentdomain.RunEvent{ImmutableModel: domain.ImmutableModel{OwnerID: e.ownerID, CreatedAt: event.CreatedAt}, RunID: event.RunID, EventType: event.Type,
 		PayloadJSON: payload}); err != nil {
 		return err
 	}
 	e.publish(projected)
+	// Side-effect diagnostic only: it runs after the DB-first audit and
+	// publication, never reads the payload, and never changes Emit's result.
+	e.logRunEventAudited(ctx, event)
 	return nil
 }
 
+// logRunEventAudited emits one bounded, metadata-only diagnostic for a
+// persisted RunEvent. Logger/sink failure fails open: slog never returns an
+// error to this call site.
+func (e *runEventEmitter) logRunEventAudited(ctx context.Context, event runtimeevent.Event) {
+	attrs := []any{"event", "run_event.audited", "phase", "run_event", "result", "ok",
+		"status", event.Type, "owner_id", e.ownerID, "run_id", e.runID}
+	if e.conversationID != nil {
+		attrs = append(attrs, "conversation_id", *e.conversationID)
+	}
+	e.diagnosticsLogger().Log(ctx, slog.LevelInfo, "run_event.audited", attrs...)
+}
+
+func (e *runEventEmitter) diagnosticsLogger() *slog.Logger {
+	if e.diagnostics != nil {
+		return e.diagnostics
+	}
+	return slog.Default()
+}
+
 // EmitModelEvent is live-only by design. In particular, reasoning never
 // enters the RunEvent repository and therefore cannot leak through history or
 // terminal snapshots.
 func (e *runEventEmitter) EmitModelEvent(ctx context.Context, modelEvent llm.ModelStreamEvent) error {
 	if modelEvent.Kind == llm.ModelUsage && e.onUsage != nil {
 		e.onUsage(ctx, e.runID, modelEvent.Usage)
 	}
 	if e.hub == nil {
 		return nil
 	}
diff --git a/internal/application/agent_usecase/run_publisher_test.go b/internal/application/agent_usecase/run_publisher_test.go
index d54a755..425ebb2 100644
--- a/internal/application/agent_usecase/run_publisher_test.go
+++ b/internal/application/agent_usecase/run_publisher_test.go
@@ -1,26 +1,29 @@
 package agent_usecase
 
 import (
 	"context"
 	"encoding/json"
 	"errors"
+	"log/slog"
 	"strings"
+	"sync"
 	"sync/atomic"
 	"testing"
 	"time"
 
 	"agentcanvas/internal/domain"
 	agentdomain "agentcanvas/internal/domain/agent"
 	"agentcanvas/internal/domain/conversation"
 	workspacedomain "agentcanvas/internal/domain/workspace"
 	"agentcanvas/internal/infrastructure/llm"
+	"agentcanvas/internal/pkg/observability"
 	runtimeevent "agentcanvas/internal/runtime/event"
 	"agentcanvas/internal/runtime/eventhub"
 )
 
 type publisherEventRepo struct {
 	items []agentdomain.RunEvent
 	err   error
 }
 
 func (r *publisherEventRepo) Create(_ context.Context, item *agentdomain.RunEvent) error {
@@ -238,10 +241,110 @@ func TestPausedSnapshotKeepsStreamOpenForResume(t *testing.T) {
 		t.Fatalf("pause event = %+v", got)
 	}
 	emitter := &runEventEmitter{hub: hub, runID: 12}
 	if err := emitter.EmitModelEvent(context.Background(), llm.ModelStreamEvent{Kind: llm.ModelTextStart}); err != nil {
 		t.Fatal(err)
 	}
 	if got := receiveStreamEvent(t, live); got.Kind != "assistant.start" {
 		t.Fatalf("resume event = %+v", got)
 	}
 }
+
+type publisherOrderTracker struct {
+	mu  sync.Mutex
+	ops []string
+}
+
+func (o *publisherOrderTracker) record(op string) {
+	o.mu.Lock()
+	defer o.mu.Unlock()
+	o.ops = append(o.ops, op)
+}
+
+func (o *publisherOrderTracker) snapshot() []string {
+	o.mu.Lock()
+	defer o.mu.Unlock()
+	return append([]string(nil), o.ops...)
+}
+
+type orderingRunEventRepo struct {
+	tracker *publisherOrderTracker
+	err     error
+}
+
+func (r *orderingRunEventRepo) Create(_ context.Context, _ *agentdomain.RunEvent) error {
+	if r.err != nil {
+		return r.err
+	}
+	r.tracker.record("create")
+	return nil
+}
+
+func (r *orderingRunEventRepo) ListByRun(context.Context, int64, int64) ([]agentdomain.RunEvent, error) {
+	return nil, nil
+}
+
+type recordingOrderHub struct {
+	inner   runStreamHub
+	tracker *publisherOrderTracker
+}
+
+func (h *recordingOrderHub) Prepare(runID int64, event eventhub.StreamEvent) eventhub.StreamEvent {
+	return h.inner.Prepare(runID, event)
+}
+
+func (h *recordingOrderHub) PublishPrepared(event eventhub.StreamEvent) {
+	h.tracker.record("publish")
+	h.inner.PublishPrepared(event)
+}
+
+func (h *recordingOrderHub) Subscribe(runID int64, afterSeq uint64) ([]eventhub.StreamEvent, <-chan eventhub.StreamEvent, func()) {
+	return h.inner.Subscribe(runID, afterSeq)
+}
+
+func (h *recordingOrderHub) Snapshot(runID int64) (eventhub.StreamEvent, error) { return h.inner.Snapshot(runID) }
+
+func (h *recordingOrderHub) CloseRun(runID int64, terminal eventhub.StreamEvent) {
+	h.inner.CloseRun(runID, terminal)
+}
+
+type failingDiagnosticsHandler struct {
+	calls atomic.Int32
+}
+
+func (h *failingDiagnosticsHandler) Enabled(context.Context, slog.Level) bool { return true }
+
+func (h *failingDiagnosticsHandler) Handle(context.Context, slog.Record) error {
+	h.calls.Add(1)
+	return errors.New("diagnostics sink unavailable")
+}
+
+func (h *failingDiagnosticsHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
+func (h *failingDiagnosticsHandler) WithGroup(string) slog.Handler      { return h }
+
+func TestRunPublisherDiagnosticsPublishesAfterAuditEvenWhenLoggerFails(t *testing.T) {
+	tracker := &publisherOrderTracker{}
+	repo := &orderingRunEventRepo{tracker: tracker}
+	innerHub := eventhub.NewMemoryHub(eventhub.Config{SubscriberBuffer: 4})
+	hub := &recordingOrderHub{inner: innerHub, tracker: tracker}
+	_, live, cancel := hub.Subscribe(9, 0)
+	defer cancel()
+	failing := &failingDiagnosticsHandler{}
+	emitter := &runEventEmitter{repo: repo, hub: hub, ownerID: 1, runID: 9, diagnostics: slog.New(failing)}
+	ctx := observability.WithCorrelation(context.Background(), observability.Correlation{}.
+		WithRequestID("rid-pub-1").WithOwnerID(1).WithRunID(9))
+
+	payload := map[string]any{"content": "payload-secret"}
+	if err := emitter.Emit(ctx, runtimeevent.Event{Type: runtimeevent.AgentStarted, Payload: payload}); err != nil {
+		t.Fatalf("diagnostics failure must not change Emit's business result: %v", err)
+	}
+	if failing.calls.Load() == 0 {
+		t.Fatal("diagnostics were not attempted during Emit")
+	}
+	ops := tracker.snapshot()
+	if len(ops) != 2 || ops[0] != "create" || ops[1] != "publish" {
+		t.Fatalf("RunEvent must stay DB-first (create -> publish) despite logger failure: %v", ops)
+	}
+	if got := receiveStreamEvent(t, live); got.Kind != "status.update" {
+		t.Fatalf("run event was not published after audit despite logger failure: %+v", got)
+	}
+}
diff --git a/internal/pkg/logger/logger.go b/internal/pkg/logger/logger.go
index ea1dba7..b51937a 100644
--- a/internal/pkg/logger/logger.go
+++ b/internal/pkg/logger/logger.go
@@ -1,18 +1,231 @@
 package logger
 
 import (
+	"context"
+	"encoding/json"
+	"fmt"
+	"io"
 	"log/slog"
 	"os"
+	"sync"
+
+	"agentcanvas/internal/pkg/observability"
 )
 
+// MaxSerializedEventBytes bounds one serialized diagnostic event (16 KiB).
+const MaxSerializedEventBytes = 16 * 1024
+
+// envelopeOverheadBytes reserves room for the sink's own envelope fields
+// (time, level, message, JSON punctuation) when enforcing the serialized cap.
+const envelopeOverheadBytes = 192
+
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
+
+// allowedDiagnosticAttributes is the metadata whitelist (design Decision 3).
+// Anything outside this set is dropped at the diagnostics boundary, so
+// prompts, message bodies, API keys, tool arguments/output, and RunEvent
+// payload content can never reach the observation sink.
+var allowedDiagnosticAttributes = map[string]struct{}{
+	"event": {}, "phase": {}, "result": {},
+	"request_id": {}, "owner_id": {}, "conversation_id": {}, "run_id": {},
+	"turn_id": {}, "parent_run_id": {}, "step_index": {}, "tool_call_id": {},
+	"route": {}, "status": {}, "provider": {}, "model": {}, "tool_name": {},
+	"error_class": {}, "latency_ms": {}, "usage": {}, "error_summary": {},
+}
+
+// DiagnosticsHandler is the shared observation boundary for lifecycle
+// diagnostics. It enriches each record with the correlation stored on ctx,
+// drops non-whitelisted attributes, caps the serialized size, and isolates
+// sink failures: a broken sink records at most one bounded sink error, is
+// never retried, and never surfaces an error to the business path.
+type DiagnosticsHandler struct {
+	inner    slog.Handler
+	fallback io.Writer
+
+	mu         sync.Mutex
+	sinkFailed bool
+}
+
+// NewDiagnosticsHandler wraps inner with the diagnostics boundary. Sink
+// failures are swallowed silently (fail-open) because no fallback is set.
+func NewDiagnosticsHandler(inner slog.Handler) *DiagnosticsHandler {
+	return &DiagnosticsHandler{inner: inner}
+}
+
+// NewDiagnosticsHandlerWithFallback wraps inner with the diagnostics boundary
+// and directs the single bounded sink-error record to fallback.
+func NewDiagnosticsHandlerWithFallback(inner slog.Handler, fallback io.Writer) *DiagnosticsHandler {
+	return &DiagnosticsHandler{inner: inner, fallback: fallback}
+}
+
+func (h *DiagnosticsHandler) Enabled(ctx context.Context, level slog.Level) bool {
+	h.mu.Lock()
+	failed := h.sinkFailed
+	h.mu.Unlock()
+	if failed {
+		// A broken sink stays broken: never retry, never block.
+		return false
+	}
+	return h.inner.Enabled(ctx, level)
+}
+
+func (h *DiagnosticsHandler) Handle(ctx context.Context, record slog.Record) error {
+	h.mu.Lock()
+	failed := h.sinkFailed
+	h.mu.Unlock()
+	if failed {
+		return nil
+	}
+	bounded := slog.NewRecord(record.Time, record.Level, record.Message, record.PC)
+	bounded.AddAttrs(boundedAttributes(ctx, record)...)
+	if err := h.inner.Handle(ctx, bounded); err != nil {
+		h.mu.Lock()
+		firstFailure := !h.sinkFailed
+		h.sinkFailed = true
+		h.mu.Unlock()
+		if firstFailure {
+			h.reportSinkFailure(err)
+		}
+	}
+	// Sink failures never change the business result.
+	return nil
+}
+
+// reportSinkFailure writes at most one bounded, metadata-only sink error
+// record. It never carries the original event content.
+func (h *DiagnosticsHandler) reportSinkFailure(err error) {
+	if h.fallback == nil {
+		return
+	}
+	fmt.Fprintf(h.fallback, "diagnostics sink failed: error_class=%s; further diagnostic events dropped\n", ErrorClass(err))
+}
+
+func (h *DiagnosticsHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
+	return &DiagnosticsHandler{inner: h.inner.WithAttrs(attrs), fallback: h.fallback}
+}
+
+func (h *DiagnosticsHandler) WithGroup(name string) slog.Handler {
+	return &DiagnosticsHandler{inner: h.inner.WithGroup(name), fallback: h.fallback}
+}
+
+// boundedAttributes whitelists the record attributes, enriches them with the
+// ctx correlation, dedupes keys (first occurrence wins), and caps the
+// serialized size by truncating the largest string values.
+func boundedAttributes(ctx context.Context, record slog.Record) []slog.Attr {
+	kept := make([]slog.Attr, 0, record.NumAttrs()+8)
+	seen := make(map[string]struct{}, record.NumAttrs()+8)
+	record.Attrs(func(attr slog.Attr) bool {
+		attr.Value = attr.Value.Resolve()
+		if _, allowed := allowedDiagnosticAttributes[attr.Key]; !allowed {
+			return true
+		}
+		if _, dup := seen[attr.Key]; dup {
+			return true
+		}
+		seen[attr.Key] = struct{}{}
+		kept = append(kept, attr)
+		return true
+	})
+	for _, attr := range CorrelationAttrs(ctx) {
+		if _, dup := seen[attr.Key]; dup {
+			continue
+		}
+		seen[attr.Key] = struct{}{}
+		kept = append(kept, attr)
+	}
+	return truncateToSerializedBudget(kept, record.Message)
+}
+
+// CorrelationAttrs expands the correlation stored on ctx into whitelisted
+// diagnostic attributes. Zero values are omitted so absent identifiers stay
+// absent instead of being fabricated.
+func CorrelationAttrs(ctx context.Context) []slog.Attr {
+	correlation, ok := observability.CorrelationFromContext(ctx)
+	if !ok {
+		return nil
+	}
+	attrs := make([]slog.Attr, 0, 8)
+	if correlation.RequestID != "" {
+		attrs = append(attrs, slog.String("request_id", correlation.RequestID))
+	}
+	if correlation.OwnerID != 0 {
+		attrs = append(attrs, slog.Int64("owner_id", correlation.OwnerID))
+	}
+	if correlation.ConversationID != 0 {
+		attrs = append(attrs, slog.Int64("conversation_id", correlation.ConversationID))
+	}
+	if correlation.RunID != 0 {
+		attrs = append(attrs, slog.Int64("run_id", correlation.RunID))
+	}
+	if correlation.TurnID != 0 {
+		attrs = append(attrs, slog.Int64("turn_id", correlation.TurnID))
+	}
+	if correlation.ParentRunID != nil {
+		attrs = append(attrs, slog.Int64("parent_run_id", *correlation.ParentRunID))
+	}
+	if correlation.StepIndex != 0 {
+		attrs = append(attrs, slog.Int("step_index", correlation.StepIndex))
+	}
+	if correlation.ToolCallID != "" {
+		attrs = append(attrs, slog.String("tool_call_id", correlation.ToolCallID))
+	}
+	return attrs
+}
+
+// ErrorClass derives a bounded, metadata-only classification from err: the
+// error's type, never its text.
+func ErrorClass(err error) string {
+	if err == nil {
+		return ""
+	}
+	return fmt.Sprintf("%T", err)
+}
+
+// truncateToSerializedBudget shrinks the largest string attribute until the
+// estimated serialization fits under MaxSerializedEventBytes.
+func truncateToSerializedBudget(attrs []slog.Attr, message string) []slog.Attr {
+	budget := MaxSerializedEventBytes - len(message) - envelopeOverheadBytes
+	for {
+		size, longestIndex := serializedAttrsSize(attrs)
+		if size <= budget || longestIndex < 0 {
+			return attrs
+		}
+		text, _ := attrs[longestIndex].Value.Any().(string)
+		cut := len(text) - (size - budget)
+		if cut < 0 {
+			cut = 0
+		}
+		attrs[longestIndex] = slog.String(attrs[longestIndex].Key, text[:cut])
+	}
+}
+
+// serializedAttrsSize estimates the serialized attribute size and reports the
+// index of the longest string attribute (-1 when none exists).
+func serializedAttrsSize(attrs []slog.Attr) (int, int) {
+	payload := make(map[string]any, len(attrs))
+	// longestLen starts at 0 so empty strings are never selected as truncation
+	// candidates; this guarantees termination once no non-empty string remains.
+	longestIndex, longestLen := -1, 0
+	for index, attr := range attrs {
+		value := attr.Value.Resolve().Any()
+		payload[attr.Key] = value
+		if text, ok := value.(string); ok && len(text) > longestLen {
+			longestIndex, longestLen = index, len(text)
+		}
+	}
+	data, err := json.Marshal(payload)
+	if err != nil {
+		return MaxSerializedEventBytes + 1, longestIndex
+	}
+	return len(data), longestIndex
+}
diff --git a/internal/runtime/agent/auto_compaction.go b/internal/runtime/agent/auto_compaction.go
index 2f7b7f2..73fd8eb 100644
--- a/internal/runtime/agent/auto_compaction.go
+++ b/internal/runtime/agent/auto_compaction.go
@@ -1,25 +1,27 @@
 package agent
 
 import (
 	"context"
 	"crypto/sha256"
 	"encoding/hex"
 	"encoding/json"
 	"errors"
 	"fmt"
+	"log/slog"
 	"strings"
 	"time"
 
 	"agentcanvas/internal/domain"
 	"agentcanvas/internal/domain/conversation"
 	"agentcanvas/internal/infrastructure/llm"
+	"agentcanvas/internal/pkg/logger"
 	"agentcanvas/internal/pkg/tokencounter"
 	"agentcanvas/internal/runtime/compaction"
 
 	"gorm.io/gorm"
 )
 
 func autoCompactLimit(req RunRequest) int {
 	window := req.ContextWindowTokens
 	if window <= 0 {
 		window = req.MaxInputTokens
@@ -66,58 +68,93 @@ func runtimeTokenStatus(req RunRequest, baseMessages, transcript []llm.ChatMessa
 	hard := hardPromptTokenLimit(req)
 	return runtimeTokenStatusResult{Measured: measured, Limit: limit, HardLimit: hard, TokenLimitReached: measured >= limit || total >= hard}
 }
 
 // compactRuntimeTranscript delegates to the shared compaction core: the
 // complete history (tool entries included) goes to the summarizer and the
 // result contains only retained user messages plus a final user-role summary.
 func (r *Runner) compactRuntimeTranscript(ctx context.Context, req RunRequest, transcript []llm.ChatMessage) ([]llm.ChatMessage, llm.Usage, *CompactionTrace) {
 	history := runtimeCompactionHistory(req, transcript)
 	beforeTokens := modelMessagesTokens(req, history)
+	started := r.now()
 	trace := &CompactionTrace{Trigger: "runtime", Scope: autoCompactScope(req), Status: "completed"}
 	if req.TokenBudgetCompaction {
 		var retained []llm.ChatMessage
 		if req.RetainClientDeveloperMessages {
 			retained = retainMessagesByRole(req, history, conversation.RoleDeveloper, compaction.UserMessageBudgetTokens)
 		}
 		trace.AfterTokens = modelMessagesTokens(req, retained)
 		trace.SavedTokens = maxInt(0, beforeTokens-trace.AfterTokens)
+		r.logCompactionCompleted(ctx, req, nil, llm.Usage{}, beforeTokens, trace, started)
 		return retained, llm.Usage{}, trace
 	}
 	provider, model := req.CompactionProvider, strings.TrimSpace(req.CompactionModel)
 	if strings.TrimSpace(provider.ProviderType) == "" || model == "" {
 		provider, model = req.Provider, req.Model
 	}
 	coreReq := compaction.Request{
 		SystemPrompt:  req.SystemPrompt,
 		CompactPrompt: req.CompactPrompt,
 		Provider:      provider,
 		Model:         model,
 	}
 	result, err := compaction.Compact(ctx, chatClientAdapter{r.LLM}, coreReq, compaction.FromChat(history))
 	trace.ModelCalled = true
 	trace.Summary = result.Summary
 	if err != nil {
 		trace.Status = "failed"
 		trace.Error = err.Error()
+		r.logCompactionCompleted(ctx, req, err, result.Usage, beforeTokens, trace, started)
 		return transcript, result.Usage, trace
 	}
 	kept := make([]llm.ChatMessage, 0, len(result.Retained)+1)
 	for _, entry := range result.Retained {
 		kept = append(kept, llm.ChatMessage{Role: entry.Role, Content: entry.Content})
 	}
 	kept = append(kept, llm.ChatMessage{Role: conversation.RoleUser, Content: compaction.SummaryPrefix + result.Summary})
 	trace.AfterTokens = modelMessagesTokens(req, kept)
 	trace.SavedTokens = maxInt(0, beforeTokens-trace.AfterTokens)
+	r.logCompactionCompleted(ctx, req, nil, result.Usage, beforeTokens, trace, started)
 	return kept, result.Usage, trace
 }
 
+// logCompactionCompleted emits the bounded, metadata-only compaction.completed
+// diagnostic. Only token counts and identifiers are reported; history and
+// summary text never enter diagnostics.
+func (r *Runner) logCompactionCompleted(ctx context.Context, req RunRequest, compactionErr error, usage llm.Usage, beforeTokens int, trace *CompactionTrace, started time.Time) {
+	resultValue := "ok"
+	level := slog.LevelInfo
+	if compactionErr != nil || trace.Status == "failed" {
+		resultValue, level = "error", slog.LevelError
+	}
+	attrs := []any{"event", "compaction.completed", "phase", "compaction", "result", resultValue,
+		"latency_ms", int(r.now().Sub(started).Milliseconds()),
+		"usage", map[string]int{
+			"prompt_tokens":     usage.PromptTokens,
+			"completion_tokens": usage.CompletionTokens,
+			"total_tokens":      usage.TotalTokens,
+			"before_tokens":     beforeTokens,
+			"after_tokens":      trace.AfterTokens,
+			"saved_tokens":      trace.SavedTokens,
+		}}
+	if req.ConversationID != nil {
+		attrs = append(attrs, "conversation_id", *req.ConversationID)
+	}
+	if req.RunID != 0 {
+		attrs = append(attrs, "run_id", req.RunID)
+	}
+	if compactionErr != nil {
+		attrs = append(attrs, "error_class", logger.ErrorClass(compactionErr))
+	}
+	r.diagnosticsLogger().Log(ctx, level, "compaction.completed", attrs...)
+}
+
 // chatClientAdapter lets the core package drive a ToolCallingClient through
 // the plain Chat interface.
 type chatClientAdapter struct{ client llm.ToolCallingClient }
 
 func (a chatClientAdapter) Chat(ctx context.Context, cfg llm.ChatProviderConfig, req llm.ChatRequest) (*llm.ChatResponse, error) {
 	response, err := a.client.ChatWithTools(ctx, cfg, llm.ToolChatRequest{Model: req.Model, Messages: req.Messages, Temperature: req.Temperature})
 	if err != nil || response == nil {
 		return nil, err
 	}
 	return &llm.ChatResponse{Content: response.Message.Content, Usage: response.Usage}, nil
diff --git a/internal/runtime/agent/model_turn.go b/internal/runtime/agent/model_turn.go
index c2ea85d..d8f1382 100644
--- a/internal/runtime/agent/model_turn.go
+++ b/internal/runtime/agent/model_turn.go
@@ -1,31 +1,42 @@
 package agent
 
 import (
 	"context"
 	"errors"
 	"fmt"
+	"log/slog"
+	"time"
 
 	"agentcanvas/internal/infrastructure/llm"
+	"agentcanvas/internal/pkg/logger"
 )
 
 // ModelEventEmitter receives provider-neutral model events. It is separate
 // from StepEmitter because streamed deltas are transient UI transport, while
 // RunStep is a durable execution trace.
 type ModelEventEmitter func(context.Context, llm.ModelStreamEvent) error
 
 var ErrEmptyModelResponse = errors.New("model returned an empty response")
 
-func (r *Runner) executeModelTurn(ctx context.Context, cfg llm.ChatProviderConfig, req llm.ToolChatRequest) (*llm.ToolChatResponse, error) {
+func (r *Runner) executeModelTurn(ctx context.Context, cfg llm.ChatProviderConfig, req llm.ToolChatRequest) (response *llm.ToolChatResponse, err error) {
 	if err := ctx.Err(); err != nil {
 		return nil, err
 	}
+	started := r.now()
+	r.logLLMRequest(ctx, cfg, req)
+	// The completion diagnostic observes the final named returns, so every
+	// return path (streaming, capability fallback, non-streaming) emits
+	// exactly one llm.completed without touching the pass-through values.
+	defer func() {
+		r.logLLMCompleted(ctx, cfg, req, response, err, started)
+	}()
 	if streaming, ok := r.LLM.(llm.ToolStreamingClient); ok {
 		state := modelTurnStreamState{}
 		response, err := streaming.StreamChatWithTools(ctx, cfg, req, func(event llm.ModelStreamEvent) error {
 			return state.forward(ctx, r, event)
 		})
 		if errors.Is(err, llm.ErrToolStreamingUnsupported) && !state.emittedAny && state.emitterErr == nil {
 			return r.executeNonStreamingModelTurn(ctx, cfg, req)
 		}
 		if err != nil {
 			if state.emitterErr == nil && state.terminal == "" {
@@ -177,20 +188,52 @@ func (s *modelTurnStreamState) emitParserFinish(ctx context.Context, runner *Run
 	return nil
 }
 
 func (r *Runner) emitModelEvent(ctx context.Context, event llm.ModelStreamEvent) error {
 	if r.OnModelEvent == nil {
 		return nil
 	}
 	return r.OnModelEvent(ctx, event)
 }
 
+// logLLMRequest emits the bounded, metadata-only llm.request diagnostic. The
+// provider API key never enters diagnostics.
+func (r *Runner) logLLMRequest(ctx context.Context, cfg llm.ChatProviderConfig, req llm.ToolChatRequest) {
+	r.diagnosticsLogger().Log(ctx, slog.LevelInfo, "llm.request",
+		"event", "llm.request", "phase", "llm", "result", "ok", "latency_ms", 0,
+		"provider", cfg.ProviderType, "model", req.Model)
+}
+
+// logLLMCompleted emits the bounded, metadata-only llm.completed diagnostic
+// with usage token counts only. The original response/error pass through
+// unchanged; on failure the error TYPE is reported, never the error text.
+func (r *Runner) logLLMCompleted(ctx context.Context, cfg llm.ChatProviderConfig, req llm.ToolChatRequest, response *llm.ToolChatResponse, callErr error, started time.Time) {
+	latencyMS := int(r.now().Sub(started).Milliseconds())
+	if callErr != nil {
+		r.diagnosticsLogger().Log(ctx, slog.LevelError, "llm.completed",
+			"event", "llm.completed", "phase", "llm", "result", "error",
+			"provider", cfg.ProviderType, "model", req.Model,
+			"latency_ms", latencyMS, "error_class", logger.ErrorClass(callErr))
+		return
+	}
+	attrs := []any{"event", "llm.completed", "phase", "llm", "result", "ok",
+		"provider", cfg.ProviderType, "model", req.Model, "latency_ms", latencyMS}
+	if response != nil {
+		attrs = append(attrs, "usage", map[string]int{
+			"prompt_tokens":     response.Usage.PromptTokens,
+			"completion_tokens": response.Usage.CompletionTokens,
+			"total_tokens":      response.Usage.TotalTokens,
+		})
+	}
+	r.diagnosticsLogger().Log(ctx, slog.LevelInfo, "llm.completed", attrs...)
+}
+
 // emitAccumulatedModelEvents keeps non-streaming clients compatible with the
 // same semantic callback contract. These callbacks are not real-time, but
 // consumers never need a second event vocabulary during migration.
 func (r *Runner) emitAccumulatedModelEvents(ctx context.Context, response *llm.ToolChatResponse) error {
 	if response == nil {
 		return ErrEmptyModelResponse
 	}
 	if response.Message.Content != "" || response.ProposedPlan != "" {
 		parser := &llm.ProposedPlanStreamParser{}
 		events := append(parser.Push(response.Message.Content), parser.Finish()...)
diff --git a/internal/runtime/agent/model_turn_test.go b/internal/runtime/agent/model_turn_test.go
index 62b1d81..25b14c6 100644
--- a/internal/runtime/agent/model_turn_test.go
+++ b/internal/runtime/agent/model_turn_test.go
@@ -1,26 +1,132 @@
 package agent
 
 import (
 	"context"
 	"encoding/json"
 	"errors"
+	"fmt"
+	"log/slog"
 	"net/http"
 	"net/http/httptest"
 	"strings"
 	"testing"
 
 	"agentcanvas/internal/domain/conversation"
 	"agentcanvas/internal/infrastructure/llm"
+	"agentcanvas/internal/pkg/logger"
+	"agentcanvas/internal/pkg/observability"
 	"agentcanvas/internal/runtime/toolruntime"
 )
 
+type modelTurnProviderError struct{ message string }
+
+func (e *modelTurnProviderError) Error() string { return e.message }
+
+func modelTurnDiagnosticsContext() context.Context {
+	return observability.WithCorrelation(context.Background(), observability.Correlation{}.
+		WithRequestID("rid-llm-1").
+		WithOwnerID(3).
+		WithConversationID(20).
+		WithRunID(401).
+		WithTurnID(201))
+}
+
+func TestModelTurnDiagnosticsLogsSuccessfulLLMUsage(t *testing.T) {
+	want := &llm.ToolChatResponse{
+		Message: llm.ChatMessage{Role: conversation.RoleAssistant, Content: "answer"},
+		Usage:   llm.Usage{PromptTokens: 5, CompletionTokens: 7, TotalTokens: 12},
+	}
+	client := &fallbackOnlyRunnerClient{response: want}
+	captured := &diagnosticsCapturingHandler{}
+	runner := &Runner{LLM: client, Logger: slog.New(logger.NewDiagnosticsHandler(captured))}
+
+	response, err := runner.executeModelTurn(modelTurnDiagnosticsContext(), llm.ChatProviderConfig{ProviderType: "openai_compatible", APIKey: "secret-api-key"}, llm.ToolChatRequest{Model: "gpt-4"})
+	if err != nil || response != want {
+		t.Fatalf("successful model turn must pass the response through unchanged: response=%+v err=%v", response, err)
+	}
+	requests := captured.eventsNamed("llm.request")
+	if len(requests) != 1 {
+		t.Fatalf("expected exactly one llm.request event, got %d", len(requests))
+	}
+	for key, value := range map[string]any{"event": "llm.request", "phase": "llm", "result": "ok", "provider": "openai_compatible", "model": "gpt-4"} {
+		if requests[0].attrs[key] != value {
+			t.Fatalf("llm.request attribute %q = %#v, want %#v", key, requests[0].attrs[key], value)
+		}
+	}
+	completed := captured.eventsNamed("llm.completed")
+	if len(completed) != 1 {
+		t.Fatalf("expected exactly one llm.completed event, got %d", len(completed))
+	}
+	for key, value := range map[string]any{
+		"event":      "llm.completed",
+		"phase":      "llm",
+		"result":     "ok",
+		"provider":   "openai_compatible",
+		"model":      "gpt-4",
+		"request_id": "rid-llm-1",
+		"run_id":     int64(401),
+		"turn_id":    int64(201),
+	} {
+		if completed[0].attrs[key] != value {
+			t.Fatalf("llm.completed attribute %q = %#v, want %#v", key, completed[0].attrs[key], value)
+		}
+	}
+	if latencyMS, ok := completed[0].attrs["latency_ms"].(int64); !ok || latencyMS < 0 {
+		t.Fatalf("llm.completed latency_ms = %#v, want non-negative int", completed[0].attrs["latency_ms"])
+	}
+	usage, ok := completed[0].attrs["usage"].(map[string]int)
+	if !ok || usage["prompt_tokens"] != 5 || usage["completion_tokens"] != 7 || usage["total_tokens"] != 12 {
+		t.Fatalf("llm.completed usage summary = %#v, want token counts 5/7/12", completed[0].attrs["usage"])
+	}
+	if captured.containsValue("secret-api-key") {
+		t.Fatal("provider API key leaked into LLM diagnostics")
+	}
+}
+
+func TestModelTurnDiagnosticsLogsLLMFailureAndReturnsError(t *testing.T) {
+	providerErr := &modelTurnProviderError{message: "provider failed with key=secret-api-key"}
+	client := &fallbackOnlyRunnerClient{err: providerErr}
+	captured := &diagnosticsCapturingHandler{}
+	runner := &Runner{LLM: client, Logger: slog.New(logger.NewDiagnosticsHandler(captured))}
+
+	response, err := runner.executeModelTurn(modelTurnDiagnosticsContext(), llm.ChatProviderConfig{ProviderType: "openai_compatible"}, llm.ToolChatRequest{Model: "gpt-4"})
+	if response != nil || err != providerErr {
+		t.Fatalf("LLM failure must return the exact provider error: response=%+v err=%v", response, err)
+	}
+	completed := captured.eventsNamed("llm.completed")
+	if len(completed) != 1 {
+		t.Fatalf("expected exactly one llm.completed event, got %d", len(completed))
+	}
+	for key, value := range map[string]any{
+		"event":       "llm.completed",
+		"phase":       "llm",
+		"result":      "error",
+		"error_class": fmt.Sprintf("%T", providerErr),
+		"provider":    "openai_compatible",
+		"model":       "gpt-4",
+	} {
+		if completed[0].attrs[key] != value {
+			t.Fatalf("llm.completed attribute %q = %#v, want %#v", key, completed[0].attrs[key], value)
+		}
+	}
+	if _, hasUsage := completed[0].attrs["usage"]; hasUsage {
+		t.Fatalf("failed LLM turn must not report usage: %#v", completed[0].attrs)
+	}
+	if len(captured.eventsNamed("llm.request")) != 1 {
+		t.Fatalf("expected exactly one llm.request event, got %d", len(captured.eventsNamed("llm.request")))
+	}
+	if captured.containsValue("secret-api-key") || captured.containsValue(providerErr.message) {
+		t.Fatal("LLM diagnostics leaked error text or API key content")
+	}
+}
+
 type streamingRunnerClient struct {
 	responses []llm.ToolChatResponse
 	requests  []llm.ToolChatRequest
 	streamed  int
 	fallback  int
 }
 
 type fallbackOnlyRunnerClient struct {
 	response *llm.ToolChatResponse
 	err      error
diff --git a/internal/runtime/agent/runner.go b/internal/runtime/agent/runner.go
index 9445f01..7a0f579 100644
--- a/internal/runtime/agent/runner.go
+++ b/internal/runtime/agent/runner.go
@@ -1,24 +1,26 @@
 package agent
 
 import (
 	"context"
 	"errors"
 	"fmt"
+	"log/slog"
 	"slices"
 	"strings"
 	"time"
 
 	"agentcanvas/internal/domain/conversation"
 	"agentcanvas/internal/domain/memory"
 	"agentcanvas/internal/infrastructure/llm"
 	"agentcanvas/internal/observability"
+	"agentcanvas/internal/pkg/logger"
 	"agentcanvas/internal/pkg/strutil"
 	"agentcanvas/internal/runtime/compaction"
 	"agentcanvas/internal/runtime/harness/hooks"
 	"agentcanvas/internal/runtime/harness/rules"
 	"agentcanvas/internal/runtime/toolruntime"
 )
 
 var ErrNoToolCallingClient = errors.New("llm client does not support tool calling")
 var ErrMandatoryRuleBudgetExceeded = errors.New("mandatory rules exceed the configured input context budget")
 var ErrContextOverflow = errors.New("context exceeds the model input window")
@@ -28,26 +30,38 @@ var ErrRunPaused = errors.New("run paused")
 type StepEmitter func(ctx context.Context, step RunStep) error
 
 type Runner struct {
 	LLM          llm.ToolCallingClient
 	OnStep       StepEmitter
 	OnModelEvent ModelEventEmitter
 	Now          func() time.Time
 	ProviderID   int64
 	ModelName    string
 	Snapshots    conversation.SnapshotRepository
+	// Logger is the optional diagnostics seam for tool/LLM/compaction
+	// lifecycle events. Nil keeps production behavior via slog.Default.
+	Logger *slog.Logger
 }
 
 func NewRunner(client llm.ToolCallingClient) *Runner {
 	return &Runner{LLM: client}
 }
 
+// diagnosticsLogger is the fail-open observation seam for lifecycle
+// diagnostics. Diagnostics never change runtime results.
+func (r *Runner) diagnosticsLogger() *slog.Logger {
+	if r.Logger != nil {
+		return r.Logger
+	}
+	return slog.Default()
+}
+
 func (r *Runner) Run(ctx context.Context, req RunRequest) (*RunResult, error) {
 	if r.LLM == nil {
 		return nil, ErrNoToolCallingClient
 	}
 	if strings.TrimSpace(req.Model) == "" {
 		return nil, fmt.Errorf("agent model is required")
 	}
 	task := strings.TrimSpace(req.Task)
 	if task == "" {
 		return nil, fmt.Errorf("agent task is required")
@@ -527,20 +541,21 @@ func appendUniqueStrings(left, right []string) []string {
 
 type preparedToolCall struct {
 	call       llm.ToolCall
 	tool       toolruntime.RuntimeTool
 	metadata   toolruntime.ToolMetadata
 	execCtx    context.Context
 	execCancel context.CancelFunc
 	result     *toolruntime.ToolResult
 	err        error
 	latencyMS  int
+	stepIndex  int
 }
 
 func (r *Runner) executeToolBatch(
 	ctx context.Context,
 	req RunRequest,
 	result *RunResult,
 	messages []llm.ChatMessage,
 	calls []llm.ToolCall,
 	toolHooks hooks.ToolHookChain,
 	contextTrace ContextTrace,
@@ -577,20 +592,21 @@ func (r *Runner) executeToolBatch(
 			return true, messages
 		}
 		toolStep := r.appendStep(result, RunStep{
 			Type:          StepTypeToolCall,
 			ToolCallID:    call.ID,
 			ToolName:      call.Name,
 			ArgumentsJSON: call.Arguments,
 			ProviderID:    r.ProviderID,
 			Model:         r.ModelName,
 		})
+		toolStepIndex := toolStep.Index
 		_ = r.emit(ctx, toolStep)
 		toolImpl := normalized.Tool
 		if normalized.Issue != nil || toolImpl == nil {
 			result.StopReason = StopReasonToolNameNotFound
 			errMessage := fmt.Sprintf("tool %s is not available", call.Name)
 			if normalized.Issue != nil {
 				errMessage = normalized.Issue.Message
 			}
 			messages = append(messages, toolMessage(call.ID, errMessage))
 			step := r.appendStep(result, RunStep{
@@ -673,35 +689,37 @@ func (r *Runner) executeToolBatch(
 		execCtx := pre.Context
 		if execCtx == nil {
 			execCtx = ctx
 		}
 		prepared = append(prepared, preparedToolCall{
 			call:       call,
 			tool:       toolImpl,
 			metadata:   metadata,
 			execCtx:    execCtx,
 			execCancel: pre.Cancel,
+			stepIndex:  toolStepIndex,
 		})
 	}
 
 	plannedCalls := make([]NormalizedToolCall, len(prepared))
 	for index := range prepared {
 		item := &prepared[index]
 		plannedCalls[index] = NormalizedToolCall{
 			Call:     item.call,
 			Tool:     item.tool,
 			Metadata: item.metadata,
 		}
 	}
 	segments := PlanToolBatch(plannedCalls, nil)
 	executions := ExecuteToolBatch(ctx, segments, req.MaxParallelTools, func(_ context.Context, batchItem ToolBatchItem) (*toolruntime.ToolResult, error) {
 		item := &prepared[batchItem.Index]
+		r.logToolStarted(ctx, item)
 		started := r.now()
 		toolResult, toolErr := item.tool.Execute(item.execCtx, toolruntime.ToolRunContext{
 			OwnerID:                     req.OwnerID,
 			AgentID:                     req.AgentID,
 			RunID:                       req.RunID,
 			Mode:                        req.Mode,
 			DelegationDepth:             req.DelegationDepth,
 			ConversationID:              req.ConversationID,
 			ProjectID:                   req.ProjectID,
 			Task:                        req.Task,
@@ -709,20 +727,21 @@ func (r *Runner) executeToolBatch(
 			EmitEvent:                   req.EmitEvent,
 			GoalRepository:              req.GoalRepository,
 			GoalTokenBudgetCeiling:      req.GoalTokenBudgetCeiling,
 			DefaultModeRequestUserInput: req.DefaultModeRequestUserInput,
 		}, item.call.Arguments)
 		if item.execCancel != nil {
 			item.execCancel()
 			item.execCancel = nil
 		}
 		item.latencyMS = int(r.now().Sub(started).Milliseconds())
+		r.logToolCompleted(ctx, item, toolErr)
 		return toolResult, toolErr
 	})
 	for _, execution := range executions {
 		item := &prepared[execution.Index]
 		item.result = execution.Result
 		item.err = execution.Err
 		if item.execCancel != nil {
 			item.execCancel()
 		}
 	}
@@ -799,20 +818,53 @@ func (r *Runner) newToolResultStep(item *preparedToolCall, post hooks.PostToolUs
 // stores in the result metadata. Success results and plain execution errors
 // carry no code.
 func toolResultErrorCode(result *toolruntime.ToolResult) string {
 	if result == nil {
 		return ""
 	}
 	code, _ := result.Metadata["error_code"].(string)
 	return code
 }
 
+// logToolStarted emits the bounded, metadata-only tool.started diagnostic.
+// It never includes tool arguments.
+func (r *Runner) logToolStarted(ctx context.Context, item *preparedToolCall) {
+	r.diagnosticsLogger().Log(ctx, slog.LevelInfo, "tool.started",
+		"event", "tool.started", "phase", "tool", "result", "ok", "latency_ms", 0,
+		"tool_name", item.call.Name, "tool_call_id", item.call.ID, "step_index", item.stepIndex)
+}
+
+// logToolCompleted emits the bounded, metadata-only tool.completed diagnostic.
+// On failure it reports the error TYPE only; tool output never enters
+// diagnostics and the original error still flows to the caller unchanged.
+func (r *Runner) logToolCompleted(ctx context.Context, item *preparedToolCall, toolErr error) {
+	resultValue := "ok"
+	errorClass := ""
+	switch {
+	case toolErr != nil:
+		resultValue, errorClass = "error", logger.ErrorClass(toolErr)
+	case item.result != nil && item.result.IsError:
+		resultValue, errorClass = "error", toolResultErrorCode(item.result)
+	}
+	level := slog.LevelInfo
+	if resultValue == "error" {
+		level = slog.LevelWarn
+	}
+	attrs := []any{"event", "tool.completed", "phase", "tool", "result", resultValue,
+		"tool_name", item.call.Name, "tool_call_id", item.call.ID, "step_index", item.stepIndex,
+		"latency_ms", item.latencyMS}
+	if errorClass != "" {
+		attrs = append(attrs, "error_class", errorClass)
+	}
+	r.diagnosticsLogger().Log(ctx, level, "tool.completed", attrs...)
+}
+
 func checkpointFromMessages(
 	req RunRequest,
 	messages []llm.ChatMessage,
 	contextTrace ContextTrace,
 	toolNames []string,
 	pending *llm.ToolCall,
 	stopReason string,
 	iteration int,
 	toolCalls int,
 ) *Checkpoint {
diff --git a/internal/runtime/agent/runner_test.go b/internal/runtime/agent/runner_test.go
index d6db39f..3c75044 100644
--- a/internal/runtime/agent/runner_test.go
+++ b/internal/runtime/agent/runner_test.go
@@ -1,35 +1,97 @@
 package agent
 
 import (
 	"context"
 	"encoding/json"
 	"errors"
+	"fmt"
+	"log/slog"
 	"strings"
 	"sync"
 	"testing"
 	"time"
 
 	"agentcanvas/internal/domain/conversation"
 	reflectiondomain "agentcanvas/internal/domain/reflection"
 	"agentcanvas/internal/infrastructure/llm"
+	"agentcanvas/internal/pkg/logger"
+	"agentcanvas/internal/pkg/observability"
 	"agentcanvas/internal/runtime/compaction"
 	"agentcanvas/internal/runtime/harness/rules"
 	"agentcanvas/internal/runtime/toolruntime"
 )
 
 type fakeToolClient struct {
 	responses []llm.ToolChatResponse
 	requests  []llm.ToolChatRequest
 	errs      []error
 }
 
+// diagnosticsCapturingHandler records every diagnostic record emitted through
+// an injected Runner.Logger seam so tests can assert event contracts.
+type diagnosticsCapturingHandler struct {
+	mu     sync.Mutex
+	events []capturedDiagnosticEvent
+}
+
+type capturedDiagnosticEvent struct {
+	level slog.Level
+	msg   string
+	attrs map[string]any
+}
+
+func (*diagnosticsCapturingHandler) Enabled(context.Context, slog.Level) bool { return true }
+
+func (h *diagnosticsCapturingHandler) Handle(_ context.Context, record slog.Record) error {
+	event := capturedDiagnosticEvent{level: record.Level, msg: record.Message, attrs: map[string]any{}}
+	record.Attrs(func(attr slog.Attr) bool {
+		event.attrs[attr.Key] = attr.Value.Any()
+		return true
+	})
+	h.mu.Lock()
+	h.events = append(h.events, event)
+	h.mu.Unlock()
+	return nil
+}
+
+func (h *diagnosticsCapturingHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
+func (h *diagnosticsCapturingHandler) WithGroup(string) slog.Handler      { return h }
+
+func (h *diagnosticsCapturingHandler) eventsNamed(name string) []capturedDiagnosticEvent {
+	h.mu.Lock()
+	defer h.mu.Unlock()
+	matched := make([]capturedDiagnosticEvent, 0)
+	for _, event := range h.events {
+		if event.attrs["event"] == name {
+			matched = append(matched, event)
+		}
+	}
+	return matched
+}
+
+func (h *diagnosticsCapturingHandler) containsValue(needle string) bool {
+	h.mu.Lock()
+	defer h.mu.Unlock()
+	for _, event := range h.events {
+		if strings.Contains(event.msg, needle) {
+			return true
+		}
+		for _, value := range event.attrs {
+			if strings.Contains(fmt.Sprintf("%v", value), needle) {
+				return true
+			}
+		}
+	}
+	return false
+}
+
 type runtimeSnapshotRepo struct {
 	current     *conversation.Compaction
 	completed   *conversation.Compaction
 	completeErr error
 	claimed     bool
 	released    bool
 }
 
 func (r *runtimeSnapshotRepo) FindCurrentSnapshot(context.Context, int64, int64) (*conversation.Compaction, error) {
 	return r.current, nil
@@ -1853,10 +1915,88 @@ func TestDelegationPairPersistsExactlyTwoEntriesViaParentSink(t *testing.T) {
 	if entries[0].ContentType != conversation.ContentTypeFunctionCall || entries[0].ToolName != "worker_a" {
 		t.Fatalf("first entry must be the delegation function_call: %+v", entries[0])
 	}
 	if entries[1].ContentType != conversation.ContentTypeFunctionCallOutput || entries[1].ToolCallID != "call_sub" {
 		t.Fatalf("second entry must be the delegation output paired to call_sub: %+v", entries[1])
 	}
 	if entries[2].ContentType != conversation.ContentTypeText || entries[2].Role != conversation.RoleAssistant {
 		t.Fatalf("last entry must be the final answer text: %+v", entries[2])
 	}
 }
+
+type diagnosticToolError struct{ message string }
+
+func (e *diagnosticToolError) Error() string { return e.message }
+
+type errorRuntimeTool struct {
+	name string
+	err  error
+}
+
+func (t *errorRuntimeTool) Name() string        { return t.name }
+func (t *errorRuntimeTool) Description() string { return "error tool" }
+func (t *errorRuntimeTool) Parameters() json.RawMessage {
+	return json.RawMessage(`{"type":"object","properties":{"secret":{"type":"string"}}}`)
+}
+func (t *errorRuntimeTool) Metadata() toolruntime.ToolMetadata {
+	return toolruntime.ToolMetadata{RiskLevel: toolruntime.RiskLow}
+}
+func (t *errorRuntimeTool) Execute(context.Context, toolruntime.ToolRunContext, json.RawMessage) (*toolruntime.ToolResult, error) {
+	return nil, t.err
+}
+
+func TestRunnerToolDiagnosticsSummarizesToolFailure(t *testing.T) {
+	client := &fakeToolClient{responses: []llm.ToolChatResponse{
+		{Message: llm.ChatMessage{Role: conversation.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "call_diag", Name: "diag_tool", Arguments: json.RawMessage(`{"secret":"top_secret_arg"}`)}}}},
+		{Message: llm.ChatMessage{Role: conversation.RoleAssistant, Content: "done"}},
+	}}
+	toolErr := &diagnosticToolError{message: "tool failed: top_secret_output"}
+	tool := &errorRuntimeTool{name: "diag_tool", err: toolErr}
+	captured := &diagnosticsCapturingHandler{}
+	runner := &Runner{LLM: client, Logger: slog.New(logger.NewDiagnosticsHandler(captured))}
+	ctx := observability.WithCorrelation(context.Background(), observability.Correlation{}.
+		WithRequestID("rid-tool-1").WithOwnerID(1).WithConversationID(20).WithRunID(2).WithTurnID(3))
+
+	result, err := runner.Run(ctx, RunRequest{
+		OwnerID: 1, RunID: 2, Model: "m", Task: "probe", MaxIterations: 3, MaxToolCalls: 2,
+		Tools: []toolruntime.RuntimeTool{tool},
+	})
+	if err != nil || result.FinalAnswer != "done" {
+		t.Fatalf("tool failure must stay recoverable: result=%+v err=%v", result, err)
+	}
+	started := captured.eventsNamed("tool.started")
+	completed := captured.eventsNamed("tool.completed")
+	if len(started) != 1 || len(completed) != 1 {
+		t.Fatalf("expected one tool.started/tool.completed pair: started=%d completed=%d events=%+v", len(started), len(completed), captured.events)
+	}
+	for key, value := range map[string]any{"event": "tool.started", "phase": "tool", "result": "ok", "tool_name": "diag_tool", "tool_call_id": "call_diag"} {
+		if started[0].attrs[key] != value {
+			t.Fatalf("tool.started attribute %q = %#v, want %#v", key, started[0].attrs[key], value)
+		}
+	}
+	if stepIndex, ok := started[0].attrs["step_index"].(int64); !ok || stepIndex <= 0 {
+		t.Fatalf("tool.started step_index = %#v, want positive int", started[0].attrs["step_index"])
+	}
+	for key, value := range map[string]any{
+		"event":        "tool.completed",
+		"phase":        "tool",
+		"result":       "error",
+		"tool_name":    "diag_tool",
+		"tool_call_id": "call_diag",
+		"error_class":  fmt.Sprintf("%T", toolErr),
+		"request_id":   "rid-tool-1",
+		"run_id":       int64(2),
+	} {
+		if completed[0].attrs[key] != value {
+			t.Fatalf("tool.completed attribute %q = %#v, want %#v", key, completed[0].attrs[key], value)
+		}
+	}
+	if stepIndex, ok := completed[0].attrs["step_index"].(int64); !ok || stepIndex <= 0 {
+		t.Fatalf("tool.completed step_index = %#v, want positive int", completed[0].attrs["step_index"])
+	}
+	if latencyMS, ok := completed[0].attrs["latency_ms"].(int64); !ok || latencyMS < 0 {
+		t.Fatalf("tool.completed latency_ms = %#v, want non-negative int", completed[0].attrs["latency_ms"])
+	}
+	if captured.containsValue("top_secret_arg") || captured.containsValue("top_secret_output") {
+		t.Fatal("tool diagnostics leaked full arguments or output content")
+	}
+}

## NEW FILE: internal/pkg/logger/logger_test.go
```go
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
```

## NEW FILE: internal/runtime/agent/auto_compaction_diagnostics_test.go
```go
package agent

import (
	"context"
	"log/slog"
	"testing"

	"agentcanvas/internal/domain/conversation"
	"agentcanvas/internal/infrastructure/llm"
	"agentcanvas/internal/pkg/logger"
	"agentcanvas/internal/pkg/observability"
)

func compactionDiagnosticsContext() context.Context {
	return observability.WithCorrelation(context.Background(), observability.Correlation{}.
		WithRequestID("rid-compact-1").
		WithOwnerID(3).
		WithConversationID(20).
		WithRunID(401).
		WithTurnID(201))
}

func TestCompactionDiagnosticsLogsCompactionSummary(t *testing.T) {
	conversationID := int64(20)
	newRequest := func() RunRequest {
		return RunRequest{
			Provider:       llm.ChatProviderConfig{ProviderType: "openai_compatible"},
			Model:          "gpt-4o",
			Task:           "task",
			ConversationID: &conversationID,
			RunID:          401,
		}
	}
	transcript := []llm.ChatMessage{
		{Role: conversation.RoleAssistant, Content: "sensitive-history-body"},
		{Role: conversation.RoleTool, Content: "result"},
	}

	t.Run("summarizer", func(t *testing.T) {
		client := &fakeToolClient{responses: []llm.ToolChatResponse{
			{Message: llm.ChatMessage{Role: conversation.RoleAssistant, Content: "summary: compacted"}, Usage: llm.Usage{PromptTokens: 11, CompletionTokens: 13, TotalTokens: 24}},
		}}
		captured := &diagnosticsCapturingHandler{}
		runner := &Runner{LLM: client, Logger: slog.New(logger.NewDiagnosticsHandler(captured))}

		compacted, _, trace := runner.compactRuntimeTranscript(compactionDiagnosticsContext(), newRequest(), transcript)
		if trace == nil || trace.Status != "completed" || len(compacted) == 0 {
			t.Fatalf("summarizer compaction must complete: trace=%+v compacted=%+v", trace, compacted)
		}
		events := captured.eventsNamed("compaction.completed")
		if len(events) != 1 {
			t.Fatalf("expected exactly one compaction.completed event, got %d", len(events))
		}
		for key, value := range map[string]any{
			"event":           "compaction.completed",
			"phase":           "compaction",
			"result":          "ok",
			"conversation_id": int64(20),
			"run_id":          int64(401),
			"request_id":      "rid-compact-1",
		} {
			if events[0].attrs[key] != value {
				t.Fatalf("compaction.completed attribute %q = %#v, want %#v", key, events[0].attrs[key], value)
			}
		}
		if latencyMS, ok := events[0].attrs["latency_ms"].(int64); !ok || latencyMS < 0 {
			t.Fatalf("compaction.completed latency_ms = %#v, want non-negative int", events[0].attrs["latency_ms"])
		}
		usage, ok := events[0].attrs["usage"].(map[string]int)
		if !ok {
			t.Fatalf("compaction.completed usage summary missing: %#v", events[0].attrs)
		}
		if usage["prompt_tokens"] != 11 || usage["completion_tokens"] != 13 || usage["total_tokens"] != 24 {
			t.Fatalf("compaction usage token summary = %#v, want 11/13/24", usage)
		}
		if usage["before_tokens"] <= 0 || usage["after_tokens"] < 0 || usage["saved_tokens"] < 0 {
			t.Fatalf("compaction token summary must carry before/after/saved counts: %#v", usage)
		}
		if captured.containsValue("sensitive-history-body") || captured.containsValue("compacted") {
			t.Fatal("compaction diagnostics leaked history or summary text")
		}
	})

	t.Run("token budget", func(t *testing.T) {
		client := &fakeToolClient{}
		captured := &diagnosticsCapturingHandler{}
		runner := &Runner{LLM: client, Logger: slog.New(logger.NewDiagnosticsHandler(captured))}
		request := newRequest()
		request.TokenBudgetCompaction = true

		_, _, trace := runner.compactRuntimeTranscript(compactionDiagnosticsContext(), request, transcript)
		if trace == nil || trace.Status != "completed" || trace.ModelCalled {
			t.Fatalf("token-budget compaction must complete without a model call: trace=%+v", trace)
		}
		events := captured.eventsNamed("compaction.completed")
		if len(events) != 1 {
			t.Fatalf("expected exactly one compaction.completed event, got %d", len(events))
		}
		if events[0].attrs["result"] != "ok" || events[0].attrs["conversation_id"] != int64(20) || events[0].attrs["run_id"] != int64(401) {
			t.Fatalf("token-budget compaction event mismatch: %#v", events[0].attrs)
		}
		usage, ok := events[0].attrs["usage"].(map[string]int)
		if !ok || usage["total_tokens"] != 0 || usage["before_tokens"] <= 0 {
			t.Fatalf("token-budget usage summary must stay zero-model with before tokens: %#v", events[0].attrs["usage"])
		}
	})

	t.Run("summarizer failure", func(t *testing.T) {
		client := &fakeToolClient{errs: []error{
			&modelTurnProviderError{message: "summarizer unavailable"},
			&modelTurnProviderError{message: "summarizer unavailable"},
			&modelTurnProviderError{message: "summarizer unavailable"},
		}}
		captured := &diagnosticsCapturingHandler{}
		runner := &Runner{LLM: client, Logger: slog.New(logger.NewDiagnosticsHandler(captured))}

		_, _, trace := runner.compactRuntimeTranscript(compactionDiagnosticsContext(), newRequest(), transcript)
		if trace == nil || trace.Status != "failed" {
			t.Fatalf("summarizer failure must keep the failed trace status: trace=%+v", trace)
		}
		events := captured.eventsNamed("compaction.completed")
		if len(events) != 1 {
			t.Fatalf("expected exactly one compaction.completed event, got %d", len(events))
		}
		if events[0].attrs["result"] != "error" {
			t.Fatalf("failed compaction event result = %#v, want error", events[0].attrs["result"])
		}
		errorClass, _ := events[0].attrs["error_class"].(string)
		if errorClass == "" {
			t.Fatalf("failed compaction event must carry an error_class: %#v", events[0].attrs)
		}
		if captured.containsValue("summarizer unavailable") || captured.containsValue("sensitive-history-body") {
			t.Fatal("compaction diagnostics leaked error text or history content")
		}
	})
}
```
