# Task 4 Fix Round 1 — Scoped Re-Review Package

Finding under re-review: unreachable `item.result.IsError` branch in `logToolCompleted` (runner.go:845 pre-fix).

Fix summary (implementer): logToolCompleted now receives the callback-scope tool result alongside the Go error; soft failures (IsError=true, nil Go error) classify as result=error with structured error_code as error_class; new covering test TestRunnerToolDiagnosticsClassifiesSoftToolFailure (RED then GREEN).

## git diff -U10 (current working tree vs BASE, fix-scoped files)
diff --git a/internal/runtime/agent/runner.go b/internal/runtime/agent/runner.go
index 9445f01..589fa3b 100644
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
+		r.logToolCompleted(ctx, item, toolResult, toolErr)
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
@@ -799,20 +818,56 @@ func (r *Runner) newToolResultStep(item *preparedToolCall, post hooks.PostToolUs
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
+// It classifies from the callback-scope toolResult/toolErr: item.result is
+// only assigned after ExecuteToolBatch returns, so it is unusable here. On
+// failure it reports the error TYPE (or the structured error_code for
+// IsError results without a Go error); tool output never enters diagnostics
+// and the original result/error still flow to the caller unchanged.
+func (r *Runner) logToolCompleted(ctx context.Context, item *preparedToolCall, toolResult *toolruntime.ToolResult, toolErr error) {
+	resultValue := "ok"
+	errorClass := ""
+	switch {
+	case toolErr != nil:
+		resultValue, errorClass = "error", logger.ErrorClass(toolErr)
+	case toolResult != nil && toolResult.IsError:
+		resultValue, errorClass = "error", toolResultErrorCode(toolResult)
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
index d6db39f..a71a6f9 100644
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
@@ -1853,10 +1915,148 @@ func TestDelegationPairPersistsExactlyTwoEntriesViaParentSink(t *testing.T) {
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
+
+type softErrorRuntimeTool struct {
+	name string
+}
+
+func (t *softErrorRuntimeTool) Name() string        { return t.name }
+func (t *softErrorRuntimeTool) Description() string { return "soft error tool" }
+func (t *softErrorRuntimeTool) Parameters() json.RawMessage {
+	return json.RawMessage(`{"type":"object","properties":{}}`)
+}
+func (t *softErrorRuntimeTool) Metadata() toolruntime.ToolMetadata {
+	return toolruntime.ToolMetadata{RiskLevel: toolruntime.RiskLow}
+}
+func (t *softErrorRuntimeTool) Execute(context.Context, toolruntime.ToolRunContext, json.RawMessage) (*toolruntime.ToolResult, error) {
+	// Structured failure: IsError=true with no Go error.
+	return &toolruntime.ToolResult{
+		ContentText: "soft failure body",
+		IsError:     true,
+		Metadata:    map[string]any{"error_code": "soft_failure"},
+	}, nil
+}
+
+func TestRunnerToolDiagnosticsClassifiesSoftToolFailure(t *testing.T) {
+	client := &fakeToolClient{responses: []llm.ToolChatResponse{
+		{Message: llm.ChatMessage{Role: conversation.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "call_soft", Name: "soft_tool", Arguments: json.RawMessage(`{}`)}}}},
+		{Message: llm.ChatMessage{Role: conversation.RoleAssistant, Content: "done"}},
+	}}
+	tool := &softErrorRuntimeTool{name: "soft_tool"}
+	captured := &diagnosticsCapturingHandler{}
+	runner := &Runner{LLM: client, Logger: slog.New(logger.NewDiagnosticsHandler(captured))}
+	ctx := observability.WithCorrelation(context.Background(), observability.Correlation{}.
+		WithRequestID("rid-tool-soft").WithOwnerID(1).WithRunID(2))
+
+	result, err := runner.Run(ctx, RunRequest{
+		OwnerID: 1, RunID: 2, Model: "m", Task: "probe", MaxIterations: 3, MaxToolCalls: 2,
+		Tools: []toolruntime.RuntimeTool{tool},
+	})
+	if err != nil || result.FinalAnswer != "done" {
+		t.Fatalf("soft tool failure must stay recoverable: result=%+v err=%v", result, err)
+	}
+	completed := captured.eventsNamed("tool.completed")
+	if len(completed) != 1 {
+		t.Fatalf("expected exactly one tool.completed event, got %d", len(completed))
+	}
+	for key, value := range map[string]any{
+		"event":        "tool.completed",
+		"phase":        "tool",
+		"result":       "error",
+		"tool_name":    "soft_tool",
+		"tool_call_id": "call_soft",
+		"error_class":  "soft_failure",
+	} {
+		if completed[0].attrs[key] != value {
+			t.Fatalf("tool.completed attribute %q = %#v, want %#v", key, completed[0].attrs[key], value)
+		}
+	}
+	if captured.containsValue("soft failure body") {
+		t.Fatal("tool diagnostics leaked output content for a soft failure")
+	}
+}

## Fix report section
```
## Fix Round 1

### Finding (code-quality review, Important)
`logToolCompleted`'s `case item.result != nil && item.result.IsError:` branch was dead code: the only call site is inside the `ExecuteToolBatch` execution callback, where `item.result` is still nil (it is assigned only after `ExecuteToolBatch` returns). A soft failure — a tool returning `(*toolruntime.ToolResult{IsError: true}, nil)` — would be misreported as `tool.completed result="ok"` with no `error_class`.

### Option chosen: pass the callback-scope `toolResult` (reviewer's preferred fix)
Rationale: the spec scenario "Tool call fails" requires every failed tool execution to classify as `result=error` with a bounded `error_class`; deletion would keep soft failures (structured `IsError` results with no Go error — a real shape `executeOne` passes through untouched) misreported as `ok`, so the branch must be reachable, not removed.

Changes (`internal/runtime/agent/runner.go` only):
- `logToolCompleted(ctx, item, toolResult, toolErr)` now takes the actual `toolResult` returned by `item.tool.Execute` in the callback; classification is `toolErr != nil` → `error_class = logger.ErrorClass(toolErr)`, else `toolResult.IsError` → `error_class = toolResultErrorCode(toolResult)` (the structured `error_code` the codebase already records in result metadata and lifts into `RunStep.ErrorCode`).
- If a soft failure carries neither a Go error nor a structured `error_code`, the event still reports `result=error` but omits `error_class` rather than fabricating a classification (metadata-only honesty).

### Covering test (TDD)
Added to `internal/runtime/agent/runner_test.go`: `softErrorRuntimeTool` (returns `&toolruntime.ToolResult{ContentText: "soft failure body", IsError: true, Metadata: {"error_code": "soft_failure"}}`, nil error) and `TestRunnerToolDiagnosticsClassifiesSoftToolFailure`, asserting one `tool.completed` with `result=error`, `tool_name`, `tool_call_id`, `error_class="soft_failure"`, recoverable run (`FinalAnswer "done"`), and no output-content leak.

RED (against pre-fix code):
```
=== RUN   TestRunnerToolDiagnosticsClassifiesSoftToolFailure
    runner_test.go:2056: tool.completed attribute "error_class" = <nil>, want "soft_failure"
--- FAIL: TestRunnerToolDiagnosticsClassifiesSoftToolFailure (0.00s)
FAIL	agentcanvas/internal/runtime/agent	1.496s
```

GREEN (covering tests):
```
=== RUN   TestRunnerToolDiagnosticsSummarizesToolFailure
--- PASS: TestRunnerToolDiagnosticsSummarizesToolFailure (0.00s)
=== RUN   TestRunnerToolDiagnosticsClassifiesSoftToolFailure
--- PASS: TestRunnerToolDiagnosticsClassifiesSoftToolFailure (0.00s)
ok  agentcanvas/internal/runtime/agent	1.515s
```

### Re-verification
Focused Task 4 suite (three packages, overlay + `-vet=off`):
```
ok  agentcanvas/internal/pkg/logger                 0.871s
ok  agentcanvas/internal/runtime/agent              5.596s
ok  agentcanvas/internal/application/agent_usecase  1.381s
```
Full `internal/runtime/agent` package suite:
```
ok  agentcanvas/internal/runtime/agent	21.494s   (exit 0)
```
Linux gate (authoritative compile/vet):
```
GOOS=linux GOARCH=amd64 go test -c ./internal/runtime/agent  → COMPILE-OK
GOOS=linux GOARCH=amd64 go vet ./internal/runtime/agent ./internal/pkg/logger ./internal/application/agent_usecase → exit 0
```

Scope: only `internal/runtime/agent/runner.go` and `runner_test.go` (both already in Task 4 scope). No git operations; no other artifact edits.
```
