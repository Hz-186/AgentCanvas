# Task 3 Report — Persist and restore async turn correlation metadata

Change: `observability-correlation-tracing` | Branch: `feature/observability-correlation-tracing`
Base commit: `ff9eaf9` | Date: 2026-08-28 | Status: DONE

### Skills Loaded:

- `test-driven-development` (D:\Users\hongze01.zhang\.zcode\skills\test-driven-development) — loaded before any other action; RED→GREEN→REFACTOR followed for every behavior.
- `vsdd-workflow-reverse-sync` (D:\Users\hongze01.zhang\.zcode\skills\vsdd-workflow-reverse-sync) — loaded; no code-fact/spec conflict was found, so no reverse sync was required.

## Changed files (scope respected, nothing else touched)

- Modified: `internal/application/agent_usecase/service.go` (+95/-1)
- Modified: `internal/application/agent_usecase/turn_worker.go` (+21)
- Created: `internal/application/agent_usecase/service_test.go` (2 StartTurnCorrelation tests + `correlationTurnRepo` fake)
- Created: `internal/application/agent_usecase/turn_worker_test.go` (4 TurnWorkerCorrelation tests + 1 TurnLifecycleDiagnostics test with success/failure subtests, plus `correlationRuntime`, `failingRunRepo`, `countingTurnRepo`, `capturingLogHandler` fakes)

No middleware/logger/runtime/eventhub/openspec artifact was modified. `NewService(...)` public signature unchanged. No git add/commit/stash/checkout executed (`auto_commit=false`).

## Implementation notes

### 1. Metadata schema (service.go)

`StartTurn` now builds the input map and merges ONE additive namespace before marshaling (the same bytes still go to both `Run.InputJSON` and `Turn.InputJSON`):

```json
{
  "query": "...", "mode": "...", "manual_compaction": false,
  "observability": {"version": 1, "request_id": "rid-...", "owner_id": 3, "conversation_id": 20}
}
```

- `inputObservabilityMetadata(ctx, ownerID, conversationID)` writes `version:1`, `request_id` (from `observability.CorrelationFromContext` — empty string when no correlation is present, never fabricated), `owner_id`, `conversation_id`. Run/turn IDs are deliberately NOT persisted: they are not assigned before `CreateWithArtifacts`; per design decision 1 the worker fills them from the durable records it loads (RunIdentity supplementation).
- `query`, `mode`, `manual_compaction` are preserved byte-for-byte in meaning; only the `observability` key is added.
- Idempotency short-circuit (service.go:739-745) is untouched and precedes input construction, so replayed requests perform zero create calls and never overwrite existing metadata (asserted by `TestStartTurnCorrelationKeepsIdempotentExistingTurnMetadata`).

### 2. Restore logic (turn_worker.go `execute`)

After run load + definition decode + `decodeInputJSON(turn.InputJSON)` (reused as instructed), before the emitter/runtime:

- `decodeObservabilityRequestID(input)` (service.go): absent key → `("", true)` legacy path, silent; non-object or `version != 1` → `("", false)` malformed path. JSON numbers decode to `float64`, so `version` is compared as `float64(1)`.
- On malformed metadata ONE bounded diagnostic `event=turn.metadata_parse_error` (`phase=turn, result=error, error_class=invalid_observability_metadata`, run/turn IDs, latency 0, Warn level) is emitted; execution continues.
- `ctx = observability.WithCorrelation(ctx, Correlation{}.WithRequestID(requestID).WithOwnerID(turn.OwnerID).WithConversationID(turn.ConversationID).WithRunID(run.ID).WithTurnID(turn.ID).WithParentRunID(run.ParentRunID))` — restored purely from persisted records; the original HTTP context is never consulted (tests pass `context.Background()`).

### 3. Diagnostics seam (service.go)

- New unexported field `diagnostics *slog.Logger` on `Service` (assembled via struct literal in tests; zero value means default).
- `diagnosticsLogger()` returns `s.diagnostics` when set, else `slog.Default()` — minimal injectable seam; Task 4 can wire a whitelist handler later. `slog.Log` never returns errors → fail-open by construction; diagnostics never propagate into business return values.
- `logTurnLifecycle(ctx, event, result, level, turn, run, latencyMS, errorClass)` is the single emitter. EVERY event carries explicit `event`, `phase=turn`, `result`, `latency_ms`; plus `request_id` (from ctx correlation, only when non-empty), `owner_id`/`conversation_id`/`turn_id` (from turn), `run_id` and `parent_run_id` (from run, or `turn.RunID` when run is nil), and `error_class` on failure. All attribute names are inside the spec whitelist.
- `turnErrorClass(cause)` = `fmt.Sprintf("%T", cause)` — bounded type classification; full error messages never enter logs (metadata-only privacy rule).

### 4. Event placement (durable ordering unchanged)

- `turn.started` (Info, latency 0): in `turnWorker.execute` after the run→running transition is persisted (`TransitionStatus` + `UpdateRunOwned`), immediately before `s.runtime.Execute` — proven by a runtime hook snapshot in the test.
- `turn.finished` (Ok, latency = `run.LatencyMS`): in `completeTurn` after the terminal persistence succeeds, on both success branches (subagent `UpdateRunOwned`; normal `UpdateRunOwned`/`CompleteWithMessage`), before `publishRunSnapshot`. Paused/waiting-human/cancelled branches intentionally emit nothing (not finished/failed transitions).
- `turn.failed` (Error, `error_class`, latency = `run.LatencyMS` when a run is loaded): in `failTurn` after the failed-state persistence succeeds (both `run==nil` and run-present branches). The cancelled branch and lease-lost early returns emit nothing. This single choke point also covers the run-load-error path (brief item 6): `execute` → `failTurn(ctx, turn, nil, err)` → `turn.failed` with `run_id` (from `turn.RunID`) and `turn_id`, runtime never invoked.
- All emissions sit between existing durable operations (after the persistence write, before publish/snapshot) — no RunEvent/RunStep/status-transition order changed; `countingTurnRepo` assertions prove no extra durable writes were introduced.

## Self-check checklist

- [x] Versioned additive namespace `{"version":1,...}` merged into both Run and Turn InputJSON; `query`/`mode`/`manual_compaction` preserved.
- [x] Idempotent replay: 0 create calls, existing object returned, existing metadata not overwritten.
- [x] Worker restores request/owner/conversation/run/turn/parent correlation before runtime; no HTTP-context dependence.
- [x] Legacy input (no observability key) executes with persisted IDs, empty request ID, no fabricated ID, no parse diagnostic.
- [x] Malformed metadata (non-object, version=99) ignored safely: business fields kept, execution continues, exactly one bounded parse diagnostic, business outcome unchanged.
- [x] Run load error: runtime Execute 0 calls, existing fail path, diagnostic carries run/turn identifiers, semantics unchanged.
- [x] `turn.started`/`turn.finished`/`turn.failed` each emitted exactly once per lifecycle with explicit `event`, `phase=turn`, `result`, `latency_ms`, correlation attrs; `error_class` on failure.
- [x] Metadata-only: no query/prompt/token bodies/full error messages in diagnostics; all attrs inside the whitelist.
- [x] Durable state ordering and status transitions unchanged (counting-fake assertions); diagnostics are side-effect logs only.
- [x] `NewService` signature unchanged; `decodeInputJSON` reused; logger/runtime/eventhub/middleware untouched; overlay stubs never entered the repo tree.
- [x] No git staging/commit.

## TDD evidence

Environment: Go 1.22.12 at `D:/Users/hongze01.zhang/AppData/Local/Temp/agentcanvas-go122full/go/bin/go.exe`, Git Bash on Windows.

### Overlay harness (outside repo)

Pre-existing blocker reproduced first on pristine tree: `go test ./internal/application/agent_usecase` fails with `undefined: syscall.Kill` / `syscall.Flock` in `workspace_usecase` and `toolruntime`. Harness at `D:/Users/hongze01.zhang/AppData/Local/Temp/ac-overlay.K3o4ga` (mktemp dir, never inside the working tree):

- `workspace_usecase/cleanup.go`: `syscall.Kill(pid, syscall.Signal(0))` → `_ = pid; err = error(nil)`; `errors.Is(err, syscall.ESRCH)` → `false`; `syscall` import removed.
- `workspace_usecase/git.go`: both `syscall.Flock(...LOCK_EX/LOCK_UN)` calls → `error(nil)` equivalents; `syscall` import removed.
- `toolruntime/filesystem_path.go`: both flock calls → `error(nil)` equivalents; `syscall` import removed.
- `overlay.json` maps the three repo paths to these stubs. All other bytes identical.

Known host quirk: `go vet` (both the default vet pass inside `go test` and standalone `go vet -overlay`) does not honor the overlay on this Windows host. Native test runs therefore use `-vet=off`; the authoritative vet gate is the GOOS=linux cross-compile below (original files compile there; no overlay needed), which exits 0.

### RED #1 (compile failure for the brand-new seam)

```
$ go test -overlay "$STUB/overlay.json" -vet=off ./internal/application/agent_usecase \
    -run 'Test(StartTurnCorrelation|TurnWorkerCorrelation|TurnLifecycleDiagnostics)' -count=1
# agentcanvas/internal/application/agent_usecase [agentcanvas/internal/application/agent_usecase.test]
internal\application\agent_usecase\turn_worker_test.go:128:10: service.diagnostics undefined (type *Service has no field or method diagnostics)
FAIL	agentcanvas/internal/application/agent_usecase [build failed]
```

Minimal step: added the `diagnostics` field + `diagnosticsLogger()` (no behavior).

### RED #2 (behavioral assertion failures, all seven methods)

```
--- FAIL: TestStartTurnCorrelationPersistsRequestMetadata
    service_test.go:71: run input has no observability namespace: map[manual_compaction:false mode:plan query:investigate]
--- FAIL: TestTurnWorkerCorrelationRestoresQueuedTurnContext
    turn_worker_test.go:148: runtime context must carry restored correlation
--- FAIL: TestTurnWorkerCorrelationFallsBackForLegacyMetadata
    turn_worker_test.go:175: legacy turn must still restore persisted identifiers
--- FAIL: TestTurnWorkerCorrelationFallsBackForMalformedMetadata (both subtests non-object / unsupported-version)
    turn_worker_test.go:216: malformed metadata must degrade to persisted identifiers only: ok=false {...}
--- FAIL: TestTurnWorkerCorrelationStopsBeforeRuntimeOnRunLoadError
    turn_worker_test.go:250: expected one turn.failed diagnostic, got 0
--- FAIL: TestTurnLifecycleDiagnosticsLogsTurnLifecycleEvents (success and failure subtests)
    turn_worker_test.go:275: turn.started must be emitted exactly once before runtime execution, got 0
FAIL	agentcanvas/internal/application/agent_usecase	1.355s
```

Note: `TestStartTurnCorrelationKeepsIdempotentExistingTurnMetadata` passed at RED #2 — it is a characterization guard for already-correct idempotency behavior (zero create calls, no metadata overwrite) that my implementation could have broken; it participated in RED #1 (compile failure) and stayed green through GREEN.

### GREEN (focused, verbose)

```
$ go test -overlay "$STUB/overlay.json" -vet=off ./internal/application/agent_usecase \
    -run 'Test(StartTurnCorrelation|TurnWorkerCorrelation|TurnLifecycleDiagnostics)' -count=1 -v
--- PASS: TestStartTurnCorrelationPersistsRequestMetadata (0.00s)
--- PASS: TestStartTurnCorrelationKeepsIdempotentExistingTurnMetadata (0.00s)
--- PASS: TestTurnWorkerCorrelationRestoresQueuedTurnContext (0.00s)
--- PASS: TestTurnWorkerCorrelationFallsBackForLegacyMetadata (0.00s)
--- PASS: TestTurnWorkerCorrelationFallsBackForMalformedMetadata (0.00s)
    --- PASS: .../non-object, .../unsupported-version
--- PASS: TestTurnWorkerCorrelationStopsBeforeRuntimeOnRunLoadError (0.00s)
--- PASS: TestTurnLifecycleDiagnosticsLogsTurnLifecycleEvents (0.00s)
    --- PASS: .../success, .../failure
ok  	agentcanvas/internal/application/agent_usecase	1.288s
```

### REFACTOR

Cosmetic alignment fix in `service_test.go` fixture only; no behavior change. Focused suite re-run after refactor: `ok agentcanvas/internal/application/agent_usecase 1.380s`.

### Full package suite (no regression, includes all pre-existing StartTurn/executeTurn/worker tests)

```
$ go test -overlay "$STUB/overlay.json" -vet=off ./internal/application/agent_usecase -count=1
ok  	agentcanvas/internal/application/agent_usecase	39.728s   (exit 0)
```

Baseline before any of my changes: `ok agentcanvas/internal/application/agent_usecase 38.321s` under the same harness — identical result set, no regressions.

### Cross-compile gate (no overlay)

```
$ GOOS=linux GOARCH=amd64 go test -c -o <tmp> ./internal/application/agent_usecase   → exit 0
$ GOOS=linux GOARCH=amd64 go vet ./internal/application/agent_usecase                → exit 0
```

## Concerns / observations for later tasks

- The resume paths (`turnWorker.resume`, `executeResumeTurnOwned`) do not restore correlation; events emitted from `completeTurn`/`failTurn` on those paths still carry owner/run/turn IDs with empty request_id (degraded, not fabricated). Task 3's brief scopes restoration to the queued-turn worker flow; flag for Task 4/5 if resume-path correlation is wanted.
- Subagent runs created outside `StartTurn` (runSubagent) do not receive the observability namespace; child-run correlation is Task 4/5 scope per tasks.md.
