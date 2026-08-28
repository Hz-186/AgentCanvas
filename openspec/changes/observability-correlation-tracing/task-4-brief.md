# Task 4 Brief — Runtime, LLM, tool, and compaction lifecycle diagnostics

Implement only Task 4 of `observability-correlation-tracing`. Prior tasks: Task 1 provides `observability.Correlation` + `WithCorrelation`/`CorrelationFromContext` (`internal/pkg/observability/correlation.go`); Task 2 wired HTTP correlation; Task 3 persists/restores correlation across the async worker boundary and emits `turn.started/turn.finished/turn.failed` (see `task-3-report.md`). Read this brief first; it is the exact contract.

Files in scope (nothing else):

- Modify `internal/pkg/logger/logger.go`; create `internal/pkg/logger/logger_test.go`.
- Modify `internal/runtime/agent/model_turn.go`, `internal/runtime/agent/runner.go`, `internal/runtime/agent/auto_compaction.go`; modify `internal/runtime/agent/model_turn_test.go`, `internal/runtime/agent/runner_test.go`; create `internal/runtime/agent/auto_compaction_diagnostics_test.go`.
- Modify `internal/application/agent_usecase/run_publisher.go` and `run_publisher_test.go`.

Do NOT modify: `internal/runtime/agent/tool_batch_executor.go`, `internal/runtime/agentruntime/*`, `internal/interface/http/*`, `cmd/*`, or any Task 3 worker/service code.

## Event contract

Stable low-cardinality events owned by this task: `llm.request`, `llm.completed`, `tool.started`, `tool.completed`, `compaction.completed`. (Task 3 owns `turn.*`; do not re-emit them. Task 4 GREEN re-runs `TestTurnLifecycleDiagnostics` as regression only.) Every event MUST carry an explicit `event` attribute plus `phase` (`llm` | `tool` | `compaction`), `result` (`ok` | `error`), `latency_ms`, and the correlation attributes available on ctx (Task 3 restores them; read via `observability.CorrelationFromContext`). Error paths add `error_class` derived from the error TYPE (e.g. `fmt.Sprintf("%T", err)`), never the error text. LLM events add `provider`/`model` and a `usage` summary (token counts only); tool events add `tool_name`, `tool_call_id`, `step_index`; compaction events add conversation/run IDs and usage/token summary.

Attribute whitelist (design Decision 3): `event`, `phase`, `result`, `request_id`, `owner_id`, `conversation_id`, `run_id`, `turn_id`, `parent_run_id`, `step_index`, `tool_call_id`, `route`, `status`, `provider`, `model`, `tool_name`, `error_class`, `latency_ms`, `usage`, bounded error summary. Anything else MUST be dropped by the logger boundary. Serialized event cap 16 KiB; oversized summaries truncated/dropped. NEVER log prompts, message bodies, `cfg.APIKey`, tool arguments, tool output, compaction history/summary text, or RunEvent payload content.

## Seams (verified facts, base ff9eaf9 + Tasks 1–3)

1. `internal/pkg/logger/logger.go` currently only has `New(env string) *slog.Logger` (text for local, JSON otherwise). KEEP the `New(env)` signature (called from `cmd/api/main.go:27`, `cmd/worker/main.go:45`, `cmd/workspace-pruner/main.go:32`). Add the diagnostics boundary here: context-aware correlation enrichment, whitelist filtering, 16 KiB bound, and sink-failure isolation (a failing handler/sink records at most ONE bounded sink error without the original content, never blocks/retries, never returns an error to callers; business results unaffected). Shape is yours (filtering `slog.Handler`, emit helper, or both) — the RED tests define the observable contract.
2. `internal/runtime/agent/model_turn.go`: `executeModelTurn` (line 18) and `executeNonStreamingModelTurn` (line 67) wrap every model call. Emit `llm.request` at entry and `llm.completed` at exit (success: usage summary + result=ok; failure: result=error + error_class). `provider` comes from `cfg.ProviderType`, `model` from `req.Model` — NEVER `cfg.APIKey`. The original response and error MUST pass through unchanged (do not convert, wrap, or swallow errors for diagnostics).
3. `internal/runtime/agent/runner.go`: `Runner` struct (lines 30-38) is assembled by struct literal in `internal/runtime/agentruntime/execution.go:213` (out of scope) — you MAY add an exported diagnostics seam field (e.g. `Logger *slog.Logger`) with nil → `slog.Default()` fallback so production behavior is unchanged without adapter edits. Tool flow: `executeToolBatch` (line 539) prepares calls (`preparedToolCall`, ToolCallID steps ~581-662), execution happens in `tool_batch_executor.go` (untouched), results return to `newToolResultStep` (line 778) / `toolResultErrorCode` (line 801); steps emit via `appendStep` (994) / `emit` (1023). Emit `tool.started`/`tool.completed` from runner.go seams bracketing execution (capture start time at preparation; emit completion with `tool_name`, `tool_call_id`, `step_index`, `latency_ms`, `error_class` on failure). Correlation for step/tool fields may be enriched from ctx (Task 1 StepIndex/ToolCallID exist on `Correlation`). No full arguments/output in events.
4. `internal/runtime/agent/auto_compaction.go`: `compactRuntimeTranscript` (line 73) covers both the token-budget branch (no model call) and the summarizer branch (`compaction.Compact` at line 96, `result.Usage`, trace status completed/failed). Emit `compaction.completed` with result, `latency_ms`, usage/token summary, and conversation/run IDs (`req.ConversationID *int64`, `req.RunID`); no history or summary text.
5. `internal/application/agent_usecase/run_publisher.go`: `Emit` (lines 67-84) is DB-first: `repo.Create` MUST succeed before `publish`. Diagnostic call sites here MUST NOT change Emit's return value, error semantics, mutex behavior, or create→publish ordering, and MUST NOT read/log `event.Payload` content. Logger/sink failure MUST fail-open (repo Create → publish still happens; Emit still returns nil on success).

## RED tests (exact names from tasks.md Task 4)

1. `LoggerEventTest#shouldEmitStableMetadataAttributes` — events carry event/phase/result/latency_ms + correlation fields; no prompt/API key.
2. `ModelTurnDiagnosticsTest#shouldLogSuccessfulLLMUsage` — `llm.completed` has provider/model/token summary; response passes through unchanged.
3. `ModelTurnDiagnosticsTest#shouldLogLLMFailureAndReturnError` — provider error → `llm.completed` result=error + error_class; Execute returns the SAME error.
4. `RunnerToolDiagnosticsTest#shouldSummarizeToolFailure` — tool executor error with sensitive output → `tool.completed` has tool_name/tool_call_id/step_index/latency_ms; no full arguments/output.
5. `RunPublisherDiagnosticsTest#shouldPublishAfterAuditEvenWhenLoggerFails` — repo success + logger handler write error → RunEvent still published, order repo Create → publish, diagnostic error not returned from Emit.
6. `CompactionDiagnosticsTest#shouldLogCompactionSummary` — `compaction.completed` has result/latency_ms/usage summary/conversation+run IDs; no history text.
7. `LoggerPrivacyTest#shouldDropDisallowedAndTruncateOversizedAttributes` — authorization/prompt/full tool output dropped; >16 KiB error summary truncated so serialized event ≤ 16 KiB.
8. `LoggerFailureIsolationTest#shouldEmitAtMostOneSinkError` — sink repeatedly failing across Emit/flush → turn business result unchanged, sink error logged at most once, no blocking retry.

## Existing test scaffolding to reuse (same packages)

- `model_turn_test.go`: `streamingRunnerClient`, `fallbackOnlyRunnerClient`, `scriptedStreamingRunnerClient` and the model-turn test patterns.
- `runner_test.go`: `fakeToolClient`, `fakeRuntimeTool`, `runtimeSnapshotRepo`, tool-batch test patterns.
- `run_publisher_test.go`: `publisherEventRepo`, `blockingPublisherEventRepo`, hub fakes; existing DB-first tests MUST stay green untouched.
- `internal/pkg/observability` correlation helpers (Task 1) — enrich ctx in tests via `WithCorrelation`.

## Environment & verification (unusual host — read carefully)

- Toolchain: `/d/Users/hongze01.zhang/AppData/Local/Temp/agentcanvas-go122full/go/bin/go.exe` (Go 1.22.12), Git Bash on Windows.
- PRE-EXISTING Windows compile blocker (not yours to fix): `workspace_usecase`/`toolruntime` use Unix-only syscalls; therefore `internal/runtime/agent` and `internal/application/agent_usecase` test binaries cannot build natively. `internal/pkg/logger` and `internal/pkg/observability` build/run natively without workaround.
- Native execution for the blocked packages: reuse/recreate the Task 3 overlay harness (scratch `go test -overlay` stubs for `workspace_usecase/cleanup.go`, `workspace_usecase/git.go`, `toolruntime/filesystem_path.go` outside the repo; see `task-3-report.md` for the exact recipe; the prior stub dir `.../Temp/ac-overlay.K3o4ga` may still exist). Note `go vet` ignores `-overlay` here — run native tests with `-vet=off` and rely on the Linux gate for vet.
- Cross-compile gate (authoritative compile/vet evidence): `GOOS=linux GOARCH=amd64 go test -c` and `go vet` for `./internal/pkg/logger ./internal/runtime/agent ./internal/application/agent_usecase` must all exit 0.
- Repo is CRLF; do NOT use `gofmt -l` as a gate.

## Constraints

- Diagnostics NEVER change runtime return values, RunEvent DB-first ordering, state machines, or public signatures (`logger.New`, `Runner.Run`, `runEventEmitter.Emit` semantics preserved).
- Additive only; existing tests in all touched packages must stay green.
- TDD mandatory: RED → GREEN → REFACTOR with captured evidence for all 8 methods.
- `auto_commit: false` — no git add/commit/stash. Overlay stubs never enter the working tree.
- Code facts conflict with this contract → stop, report `REVERSE_SYNC_REQUIRED`.

## Output contract

Write the FULL report to `openspec/changes/observability-correlation-tracing/task-4-report.md`: `### Skills Loaded:`, changed files, implementation notes (logger boundary design, per-site emission points, seam fields), self-check checklist, TDD evidence (RED/GREEN/REFACTOR commands + output, full-package suites, Linux compile+vet output). Return ONLY: status (`DONE` / `DONE_WITH_CONCERNS` / `NEEDS_CONTEXT` / `BLOCKED` / `REVERSE_SYNC_REQUIRED`), one-line test summary, concerns. No commit messages.
