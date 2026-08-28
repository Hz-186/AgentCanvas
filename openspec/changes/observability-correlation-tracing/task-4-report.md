# Task 4 Report — Runtime, LLM, tool, and compaction lifecycle diagnostics

Change: `observability-correlation-tracing` · Branch: `feature/observability-correlation-tracing` · Base: ff9eaf9 + Tasks 1–3 · `auto_commit: false` (no git operations performed; overlay stubs kept out of the working tree).

### Skills Loaded:
- `test-driven-development` (RED → GREEN → REFACTOR with captured evidence; evidence before assertions)
- `vsdd-workflow-reverse-sync` (stop-and-report on any spec/code conflict; none found — no `REVERSE_SYNC_REQUIRED`)

## Scope honored

Modified only the in-scope files:
- `internal/pkg/logger/logger.go` (boundary) + **created** `internal/pkg/logger/logger_test.go`
- `internal/runtime/agent/model_turn.go`, `runner.go`, `auto_compaction.go` + `model_turn_test.go`, `runner_test.go`, **created** `auto_compaction_diagnostics_test.go`
- `internal/application/agent_usecase/run_publisher.go` + `run_publisher_test.go`

Not touched: `tool_batch_executor.go`, `internal/runtime/agentruntime/*`, `internal/interface/http/*`, `cmd/*`, Task 3 worker/service code. `logger.New(env)` signature unchanged (still called unchanged from `cmd/api`, `cmd/worker`, `cmd/workspace-pruner`).

## Implementation notes

### 1. Logger boundary (`internal/pkg/logger/logger.go`)

Shape chosen: a filtering `slog.Handler` (`DiagnosticsHandler`) plus small emit helpers (`CorrelationAttrs`, `ErrorClass`). `New(env)` is unchanged; the boundary is applied by wrapping the sink handler.

- **`MaxSerializedEventBytes = 16 * 1024`** — serialized cap. A fixed `envelopeOverheadBytes` reserve (time/level/msg/punctuation) is subtracted so the sink's own envelope plus attributes stay ≤ 16 KiB.
- **Whitelist filtering (design Decision 3)** — `boundedAttributes` drops any attribute key not in `allowedDiagnosticAttributes` (`event, phase, result, request_id, owner_id, conversation_id, run_id, turn_id, parent_run_id, step_index, tool_call_id, route, status, provider, model, tool_name, error_class, latency_ms, usage, error_summary`). Prompts, message bodies, API keys, tool arguments/output, compaction history/summary text, and RunEvent payload content can never reach the sink.
- **Context-aware correlation enrichment** — `CorrelationAttrs(ctx)` reads `observability.CorrelationFromContext(ctx)` and appends `request_id/owner_id/conversation_id/run_id/turn_id/parent_run_id/step_index/tool_call_id`, omitting zero values (absent identifiers stay absent, never fabricated). Explicit caller attributes win (dedupe keeps first occurrence).
- **16 KiB bound** — `truncateToSerializedBudget` repeatedly shrinks the longest string attribute until the JSON-serialized attribute set fits the budget. `serializedAttrsSize` seeds `longestLen=0` so empty strings are never truncation candidates, guaranteeing termination.
- **Sink-failure isolation (fail-open)** — on the first `inner.Handle` error the handler flips `sinkFailed` under a mutex and writes **at most one** bounded, metadata-only sink-error record to the optional `fallback` writer (error TYPE only via `ErrorClass`, never the original event content). Subsequent `Enabled`/`Handle` short-circuit: no retry, no blocking, `Handle` always returns `nil`. Business results are unaffected. `NewDiagnosticsHandler` (no fallback) swallows sink failures silently; `NewDiagnosticsHandlerWithFallback` records the single sink error.
- `ErrorClass(err)` returns `fmt.Sprintf("%T", err)` (type only, never text). `WithAttrs`/`WithGroup` propagate both `inner` and `fallback`.

### 2. Per-site emission points

All events carry explicit `event` + `phase` + `result` + `latency_ms`; correlation attributes arrive via the boundary from ctx. Error paths add `error_class` from `%T`, never error text.

- **LLM — `model_turn.go`.** `executeModelTurn` converted to named returns `(response, err)`. After the initial `ctx.Err()` guard it emits `llm.request` (provider=`cfg.ProviderType`, model=`req.Model`, never `cfg.APIKey`) and registers a single `defer` that emits exactly one `llm.completed` on every return path (streaming, capability fallback, non-streaming). Success adds a token-only `usage` map (`prompt_tokens/completion_tokens/total_tokens`) and `result=ok`; failure adds `result=error` + `error_class`. The original response/error pass through untouched — diagnostics only read them.
- **Tool — `runner.go`.** Added exported seam `Logger *slog.Logger` to `Runner` with nil→`slog.Default()` fallback (`diagnosticsLogger()`), so `agentruntime/execution.go` (out of scope) needs no edit and production behavior is unchanged. `preparedToolCall` gained `stepIndex` (captured from `toolStep.Index` at preparation). In `executeToolBatch`'s execution callback, `logToolStarted` brackets entry and `logToolCompleted` brackets exit with `tool_name`, `tool_call_id`, `step_index`, `latency_ms`; failure adds `error_class` from the tool error type (or the batch executor's structured `error_code` for error-results). No arguments/output are read. `tool.started` uses `result=ok`; `tool.completed` uses `result=ok|error` (warn level on error).
- **Compaction — `auto_compaction.go`.** `compactRuntimeTranscript` emits exactly one `compaction.completed` per exit: token-budget branch, summarizer failure, and summarizer success. Carries `result`, `latency_ms`, a token-only `usage` summary (`prompt/completion/total/before/after/saved_tokens`), and conversation/run IDs (`req.ConversationID` deref, `req.RunID`). No history or summary text is read; failure adds `error_class` (type only).
- **RunEvent boundary — `run_publisher.go`.** `runEventEmitter` gained an optional `diagnostics *slog.Logger` seam (nil→`slog.Default()`), wired to `s.diagnosticsLogger()` in `newRunEventEmitter`. `Emit` calls `logRunEventAudited` **after** `repo.Create` succeeds **and** `publish` runs — a side-effect diagnostic that only reads `event.Type`/IDs, never `event.Payload`. It cannot change Emit's return value, error semantics, mutex behavior, or create→publish order, and sink failure fails open (Emit still returns nil on success).

## Self-check checklist

- [x] Every event carries explicit `event`, `phase`, `result`, `latency_ms` + ctx correlation attributes.
- [x] Error paths add `error_class` from error TYPE (`%T`), never error text.
- [x] LLM events add `provider`/`model` + token-only `usage`; never `cfg.APIKey`.
- [x] Tool events add `tool_name`, `tool_call_id`, `step_index`; no arguments/output.
- [x] Compaction events add conversation/run IDs + token summary; no history/summary text.
- [x] Whitelist drops anything else (`authorization`, `prompt`, `tool_output`, etc.).
- [x] Serialized event capped at 16 KiB; oversized summaries truncated.
- [x] Sink failure → at most one bounded sink error, no retry/block, business result unchanged, fail-open.
- [x] Runtime return values, error identities, RunEvent DB-first ordering, and public signatures unchanged (verified by full-package suites + untouched DB-first tests).
- [x] `turn.*` NOT re-emitted (Task 3 ownership); Task 3 `TestTurnLifecycleDiagnostics` re-run as regression only.
- [x] No `REVERSE_SYNC_REQUIRED` — code facts matched the brief.

## TDD evidence

### RED (captured)

RED for the three logger methods manifested as real assertion failures (boundary absent → pass-through); RED for the runtime/usecase methods manifested as compile failures on brand-new symbols only (`Runner.Logger`, `logger.NewDiagnosticsHandler`, `runEventEmitter.diagnostics`), which the brief permits.

Logger assertion RED (this session, before boundary implementation):
```
--- FAIL: TestLoggerEventEmitsStableMetadataAttributes (0.00s)
    logger_test.go:143: attribute "turn_id" = <nil> (present=false), want 201; ...
--- FAIL: TestLoggerPrivacyDropsDisallowedAndTruncatesOversizedAttributes (0.06s)
    logger_test.go:175: serialized event exceeds the 16 KiB bound: 49490 bytes
--- FAIL: TestLoggerFailureIsolationEmitsAtMostOneSinkError (0.00s)
    logger_test.go:229: failing sink must not be retried per event: 5 calls
FAIL	agentcanvas/internal/pkg/logger
```

New-symbol compile RED (RED #1, from `go test`/`go vet` on the three packages before the seams existed):
```
internal/pkg/logger: undefined: NewDiagnosticsHandler / MaxSerializedEventBytes / NewDiagnosticsHandlerWithFallback
internal/runtime/agent: unknown field Logger in struct literal of type Runner; undefined: logger.NewDiagnosticsHandler
internal/application/agent_usecase: unknown field diagnostics in struct literal of type runEventEmitter
```

An additional genuine build-failure step surfaced during GREEN on Go 1.22 (`slog.Attr.Resolve` is Go 1.24+), fixed by resolving `attr.Value` instead:
```
internal\pkg\logger\logger.go:128:15: attr.Resolve undefined (type slog.Attr has no field or method Resolve)
```

### GREEN (focused suite, incl. Task 3 regression)

```
go test ./internal/pkg/logger ./internal/runtime/agent ./internal/application/agent_usecase \
  -run 'Test(LoggerEvent|ModelTurnDiagnostics|RunnerToolDiagnostics|RunPublisherDiagnostics|CompactionDiagnostics|LoggerPrivacy|LoggerFailureIsolation|TurnLifecycleDiagnostics)' \
  -count=1 -v -overlay "$OV" -vet=off
```
```
--- PASS: TestLoggerEventEmitsStableMetadataAttributes (0.00s)
--- PASS: TestLoggerPrivacyDropsDisallowedAndTruncatesOversizedAttributes (0.04s)
--- PASS: TestLoggerFailureIsolationEmitsAtMostOneSinkError (0.00s)
--- PASS: TestCompactionDiagnosticsLogsCompactionSummary (3.69s)   [summarizer/token_budget/summarizer_failure]
--- PASS: TestModelTurnDiagnosticsLogsSuccessfulLLMUsage (0.00s)
--- PASS: TestModelTurnDiagnosticsLogsLLMFailureAndReturnsError (0.00s)
--- PASS: TestRunnerToolDiagnosticsSummarizesToolFailure (0.00s)
--- PASS: TestRunPublisherDiagnosticsPublishesAfterAuditEvenWhenLoggerFails (0.00s)
--- PASS: TestTurnLifecycleDiagnosticsLogsTurnLifecycleEvents (0.00s)   [success/failure — Task 3 regression]
ok  agentcanvas/internal/pkg/logger
ok  agentcanvas/internal/runtime/agent
ok  agentcanvas/internal/application/agent_usecase
```

### REFACTOR
No behavior-changing refactor needed after GREEN; the boundary and emitters landed minimal. Confirmed no regressions by re-running the full suites below.

### Full-package suites (DoD)

```
go test ./internal/pkg/logger ./internal/runtime/agent ./internal/application/agent_usecase -count=1 -overlay "$OV" -vet=off
```
```
ok  agentcanvas/internal/pkg/logger            0.973s
ok  agentcanvas/internal/runtime/agent         17.961s
ok  agentcanvas/internal/application/agent_usecase  33.576s
EXIT=0
```
(Existing RunEvent DB-first tests included and passing. Task 1 `./internal/pkg/observability` also re-run green as a sanity check.)

### Linux cross-compile + vet gate (authoritative compile/vet evidence)

```
GOOS=linux GOARCH=amd64 go test -c  (logger, runtime/agent, application/agent_usecase)  → COMPILE-OK for all three
GOOS=linux GOARCH=amd64 go vet ./internal/pkg/logger ./internal/runtime/agent ./internal/application/agent_usecase
VET_EXIT=0
```

Cross-compile binaries were emitted to a scratch temp dir and removed; nothing entered the repo tree.

## Environment notes (unusual host)
- Toolchain Go 1.22.12 at `/d/Users/hongze01.zhang/AppData/Local/Temp/agentcanvas-go122full/go/bin/go.exe`.
- Pre-existing Windows Unix-syscall blocker in `workspace_usecase`/`toolruntime` means `internal/runtime/agent` and `internal/application/agent_usecase` test binaries can't build natively; reused the Task 3 overlay harness at `D:/Users/hongze01.zhang/AppData/Local/Temp/ac-overlay.K3o4ga` (`-overlay …/overlay.json`, `-vet=off` for native runs). `internal/pkg/logger` builds/runs natively. Linux gate is the authoritative compile/vet evidence.
- Go 1.22 lacks `slog.Attr.Resolve` (Go 1.24+); used `attr.Value.Resolve()`.

## Concerns / observations
- Minor doc inconsistency (not blocking): task-4-brief says "8 RED methods" while `tasks.md` DoD says "九个/9". The GREEN regex covers the 8 new methods plus Task 3's `TestTurnLifecycleDiagnostics` regression (= 9); all pass.
- `llm.request` and `tool.started` report `latency_ms: 0` (entry events); completion events carry the measured latency. This matches the contract's "every event carries latency_ms" while keeping entry events meaningful.
- No change to durable state, RunEvent ordering, state machines, or public signatures.

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
