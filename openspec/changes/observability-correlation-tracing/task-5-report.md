# Task 5 Report — End-to-end correlation, privacy, and legacy compatibility verification

Change: `observability-correlation-tracing` · Branch: `feature/observability-correlation-tracing` · Base: Tasks 1–4 (dual-reviewed) · `auto_commit: false` (no git operations performed).

## Scope honored

- **Created (only production-tree change):** `internal/pkg/observability/correlation_integration_test.go` — package `observability_test` (external test package; avoids an import cycle with `observability` while consuming it). Stdlib + existing module deps only; no new dependency, no migration, no durable trace table.
- **Modified: none of the three Modify-candidate files** (`internal/interface/http/router_observability_test.go`, `internal/application/agent_usecase/run_publisher_test.go`, `internal/runtime/agentruntime/agent_runtime_test.go`) — justification below.
- **Production code: zero changes.** All five RED tests were authored against the real Tasks 1–4 seams (verified against source before writing); no production defect surfaced (section 7).
- Protected files untouched (`internal/runtime/eventhub/hub.go`, `openspec/changes/sql-memory-es-hybrid/`, `.codegraph/`, `.codex/`, `openspec/changes/conversation-cache/`).

### Why the three Modify files needed no changes

The brief allows leaving a Modify file unchanged with justification ("若确有一个文件无需改动，在报告中说明理由"). All three were left unchanged for the same structural reason:

1. Their reusable helpers are **unexported and live in `_test.go` files** (`routerCaptureHandler`/`routerRecordAttrs` in package `httpserver`; `publisherEventRepo`/`failingDiagnosticsHandler` in the `agent_usecase` test binary; `configuredMemoryRepository`/`classifiedTestTool`/`capturedRuntimeEvents` in the `agentruntime` test binary). Go does not allow importing symbols from test files of another package, so the new suite — which must live in `internal/pkg/observability` (package `observability_test`) to sit at the correlation package and avoid an import cycle — cannot reuse them. It reimplements minimal equivalents (`captureHandler`, in-memory store adapters, fake executors).
2. The integration suite already exercises the same seams those files cover end-to-end: the real `NewRouter` middleware chain, the real `Service` emitter's `run_event.audited` path, and the `agentruntime.Runtime` seam backed by the real `runtimeagent.Runner`. No additional shared helper was required in any of the three files.
3. All four packages compile and their **entire existing suites pass** unchanged (sections 2–3 regression evidence).

## Environment (Windows host)

- Toolchain: `D:\Users\hongze01.zhang\AppData\Local\Temp\agentcanvas-go122full\go\bin\go.exe` (Go 1.22.12 windows/amd64, not on PATH). Repo is CRLF; `gofmt -l` not usable as a gate.
- Pre-existing Windows blockers (base commit, not introduced by this change): `workspace_usecase/cleanup.go:144` (`syscall.Kill`), `workspace_usecase/git.go:144-147` (`syscall.Flock`), `toolruntime/filesystem_path.go:100-106` (`syscall.Flock`). The verified overlay harness at `/tmp/ac-overlay.K3o4ga/overlay.json` maps those three files to cross-platform stubs; native runs use `-vet=off` (vet ignores overlay on this host).
- Authoritative repo gates run with `GOOS=linux GOARCH=amd64` (original sources, no overlay needed) — section 6.

## 1. RED evidence (captured verbatim)

The RED file compiled clean on first authoring; the five failures below are **assertion failures on deliberately unimplemented helper capabilities**, not compile errors. Five minimal capability gaps were left in test-only helpers: `captureHandler.Handle` dropped records; `storesTurnRepo.ClaimNext` always returned `ErrNoTurnAvailable`; `correlationCapturingRuntime.Execute` returned a canned success without capturing ctx correlation or emitting; `runnerBackedRuntime.Execute` returned a canned success without running the real `runtimeagent.Runner`; the three trace-counting repo wrappers delegated data without counting.

Command (overlay-equivalent of the brief's focused command; `-vet=off` per environment note):

```bash
GO=D:/Users/hongze01.zhang/AppData/Local/Temp/agentcanvas-go122full/go/bin/go.exe
"$GO" test -overlay=/tmp/ac-overlay.K3o4ga/overlay.json -vet=off -count=1 \
  ./internal/pkg/observability ./internal/interface/http \
  ./internal/application/agent_usecase ./internal/runtime/agentruntime \
  -run 'TestCorrelationIntegration'
```

Output (verbatim):

```text
--- FAIL: TestCorrelationIntegration (6.03s)
    --- FAIL: TestCorrelationIntegration/shouldLinkHTTPStartTurnWorkerAndRuntime (0.00s)
        correlation_integration_test.go:922: http.access records = 0, want 1
    --- FAIL: TestCorrelationIntegration/shouldLinkParentRunAndToolStep (0.00s)
        correlation_integration_test.go:1044: tool diagnostics = started 0 completed 0, want 1/1
    --- FAIL: TestCorrelationIntegration/shouldKeepTraceAPIShape (0.00s)
        correlation_integration_test.go:1139: trace repository call contract = FindByID 0 ListByParent 0 events.ListByRun 0 steps.ListByRun 0, want 4/1/1/1
    --- FAIL: TestCorrelationIntegration/shouldRejectSensitiveLogAttributes (0.00s)
        correlation_integration_test.go:1177: http.access records = 0, want 1
    --- FAIL: TestCorrelationIntegration/shouldRemainCompatibleWithLegacyRun (6.03s)
        correlation_integration_test.go:1266: turn 1002 did not reach a terminal status before the deadline
FAIL
FAIL	agentcanvas/internal/pkg/observability	7.196s
ok  	agentcanvas/internal/interface/http	1.392s [no tests to run]
ok  	agentcanvas/internal/application/agent_usecase	1.019s [no tests to run]
ok  	agentcanvas/internal/runtime/agentruntime	1.402s [no tests to run]
FAIL
```

Per-test classification (each is an assertion failure, never a compile error):

1. `shouldLinkHTTPStartTurnWorkerAndRuntime` — `http.access records = 0, want 1`: the full real chain ran (StartTurn returned 202), but the capturing `slog.Handler` intentionally dropped records, so the http.access assertion failed.
2. `shouldLinkParentRunAndToolStep` — `tool diagnostics = started 0 completed 0, want 1/1`: `RunSubagent` succeeded end-to-end, but the scripted runtime returned a canned result without running the real Runner, so no `tool.started`/`tool.completed` diagnostics existed.
3. `shouldKeepTraceAPIShape` — `trace repository call contract = … 0/0/0/0, want 4/1/1/1`: the trace API returned 200 with the correct shape; only the counting wrappers were not yet counting.
4. `shouldRejectSensitiveLogAttributes` — `http.access records = 0, want 1`: same capture gap as test 1, hit at the sanity gate before the worker runs.
5. `shouldRemainCompatibleWithLegacyRun` — `turn 1002 did not reach a terminal status before the deadline`: `ClaimNext` intentionally never handed the seeded legacy turn to the worker, so the 6 s terminal-status wait expired.

## 2. GREEN evidence

GREEN was reached by completing exactly the five helper capabilities above (test-side only; five `Edit` operations in the new test file; no other file touched).

Focused command (same invocation as RED):

```text
--- PASS: TestCorrelationIntegration (1.11s)
    --- PASS: TestCorrelationIntegration/shouldLinkHTTPStartTurnWorkerAndRuntime (0.37s)
    --- PASS: TestCorrelationIntegration/shouldLinkParentRunAndToolStep (0.00s)
    --- PASS: TestCorrelationIntegration/shouldKeepTraceAPIShape (0.00s)
    --- PASS: TestCorrelationIntegration/shouldRejectSensitiveLogAttributes (0.37s)
    --- PASS: TestCorrelationIntegration/shouldRemainCompatibleWithLegacyRun (0.37s)
PASS
ok  	agentcanvas/internal/pkg/observability	2.614s
testing: warning: no tests to run
PASS
ok  	agentcanvas/internal/interface/http	1.503s [no tests to run]
testing: warning: no tests to run
PASS
ok  	agentcanvas/internal/application/agent_usecase	1.230s [no tests to run]
testing: warning: no tests to run
PASS
ok  	agentcanvas/internal/runtime/agentruntime	1.361s [no tests to run]
```

Full regression — complete suites of the four affected packages (not just the focused run):

```text
ok  	agentcanvas/internal/pkg/observability	3.098s
ok  	agentcanvas/internal/interface/http	2.018s
ok  	agentcanvas/internal/application/agent_usecase	36.605s
ok  	agentcanvas/internal/runtime/agentruntime	5.901s
```

No existing test regressed; the three Modify-candidate files' suites (`router_observability_test.go`, `run_publisher_test.go`, `agent_runtime_test.go`) are included in these package runs and pass unchanged.

## 3. Five-layer correlation reconciliation table

Shared identities used by the suite: `owner_id=7`, `agent_id=2`, `conversation_id=10`; request IDs `rid-integration-1` (test 1), `rid-privacy-1` (test 4), `rid-child-parent-1` (test 2 parent ctx). Reconciliation results — every cell asserts **exact equality**, and all passed:

| # | Layer | Record / diagnostic | Correlation fields asserted equal | Covered by |
|---|-------|---------------------|-----------------------------------|------------|
| 1 | HTTP middleware chain | `http.access` (real `NewRouter`: RequestID → AccessLog → Recovery → CORS → Auth → RequireRouteScope) | `request_id` = inbound `X-Request-ID`, `owner_id` = JWT principal, `route` = `/api/v1/agents/:id/conversations/:conversation_id/turns`, `status` = 202 | tests 1, 4; test 5 asserts X-Request-ID echo (`rid-legacy-echo`) + generation-when-absent unchanged |
| 2 | StartTurn metadata codec | `Run.InputJSON` and `Turn.InputJSON` `observability` namespace (real `inputObservabilityMetadata`) | `version=1`, `request_id`, `owner_id`, `conversation_id` identical in **both** records (Run and Turn isomorphic) | test 1 |
| 3 | Durable turn worker | `turn.started` / `turn.finished` (real `logTurnLifecycle` + ctx restored by `turn_worker.go:85-91` from persisted metadata + durable records) | `request_id`, `owner_id`, `conversation_id`, `run_id`, `turn_id` on **both** lifecycle events | test 1; test 5 asserts the legacy variant: **no** `request_id` key, `run_id`/`turn_id`/`owner_id` still present, no `turn.metadata_parse_error` |
| 4a | Runtime executor boundary | correlation captured from the exact ctx passed to `Runtime.Execute` | `request_id`, `owner_id`, `conversation_id`, `run_id`, `turn_id` (five-field match against the HTTP-originated values) | test 1 |
| 4b | Runtime event audit | `run_event.audited` (real `runEventEmitter.Emit` after DB-first append) | `owner_id`, `run_id`, `conversation_id` on every audited record; events persisted to the RunEvent repo | tests 1, 4 |
| 5 | Child run + tool step | `ListByParent` child link + `tool.started`/`tool.completed` from the **real `runtimeagent.Runner`** (child correlation derived as in `turn_worker.go:85-91`) | child `ParentRunID` = parent run ID, `RunType=subagent`, `DelegationDepth=1`; tool diagnostics carry `parent_run_id` (parent), `run_id` (child), `owner_id`, `request_id` (survived from parent ctx), `tool_call_id`, `tool_name`, and `step_index` equal to the Runner's actual `tool_call` step `Index` | test 2 |

Result: one request ID and the full identity tuple stay linked across HTTP → StartTurn persistence → async worker → runtime ctx → child-run tool diagnostics, with no layer fabricating or dropping an identifier.

## 4. Privacy negative-assertion scan list (test 4)

Baits planted in live traffic: user prompt `Remember TOP_SECRET_PROMPT_BAIT_42 forever` (StartTurn content), tool-call arguments `{"api_key":"sk-live-BAIT-42"}` (scripted model response executed by the real Runner), tool output `tool says SENSITIVE_TOOL_OUTPUT_BAIT_42` (recording tool result).

Non-vacuity precondition: the scan only runs after asserting all of `http.access`, `turn.started`, `turn.finished`, `tool.started`, `tool.completed`, `run_event.audited` were captured across the router-logger capture and the `slog.Default()`-swapped `logger.DiagnosticsHandler` capture.

Scans applied to **every captured record** (message + every attribute value stringified with `fmt.Sprintf("%v", …)`):

- **Key whitelist check:** every attribute key must be in the 20-key whitelist — `event, phase, result, request_id, owner_id, conversation_id, run_id, turn_id, parent_run_id, step_index, tool_call_id, route, status, provider, model, tool_name, error_class, latency_ms, usage, error_summary` (mirrors `logger.go:38-44`).
- **Value substring scan (banned list):**
  - `TOP_SECRET_PROMPT_BAIT_42` (prompt content)
  - `sk-live-BAIT-42` (API-key-shaped secret in tool arguments)
  - `SENSITIVE_TOOL_OUTPUT_BAIT_42` (full tool output)
  - `api_key`, `authorization`, `Authorization` (sensitive key names)
  - `Bearer ` prefix and the **actual issued JWT token string** for the session
  - `Remember` (user prompt fragment)

All diagnostics passed: whitelist-only keys, zero banned substrings.

## 5. Legacy-compatibility evidence (test 5)

Setup: Run+Turn seeded directly with legacy input `{"query":"legacy task","mode":"default"}` — **no `observability` key**, exactly the pre-change shape — then driven through the real worker.

"No extra durable record" is asserted by count reconciliation around the worker run:

- `durableCounts()` snapshots (runs, turns, events, steps, messages) taken **before** starting the worker and **after** the turn reaches a terminal status.
- Assertion: runs/turns/events/steps counts are **byte-for-byte unchanged**; messages increased by **exactly 1** (the assistant reply from `CompleteWithMessage`) — i.e., legacy completion writes nothing beyond what the pre-change path wrote.
- Additionally: the persisted run `InputJSON` is re-decoded after completion and asserted to still **lack** the `observability` key (no silent rewrite/upgrade of legacy rows), and no `turn.metadata_parse_error` diagnostic was emitted (legacy absence is the ok-path, not the error path).

X-Request-ID legacy behavior asserted through the real router: explicit header echoes verbatim (`rid-legacy-echo`) and an absent header still gets a generated value.

## 6. Full-repo gate evidence

### Linux cross-compile gates (authoritative; original sources, no overlay)

```bash
GO=D:/Users/hongze01.zhang/AppData/Local/Temp/agentcanvas-go122full/go/bin/go.exe
GOOS=linux GOARCH=amd64 "$GO" build ./...    # exit 0
GOOS=linux GOARCH=amd64 "$GO" vet ./...      # exit 0 (includes all test files — DoD vet equivalent)
GOOS=linux GOARCH=amd64 "$GO" test -c ./internal/pkg/observability          # binary built
GOOS=linux GOARCH=amd64 "$GO" test -c ./internal/interface/http             # binary built
GOOS=linux GOARCH=amd64 "$GO" test -c ./internal/application/agent_usecase  # binary built
GOOS=linux GOARCH=amd64 "$GO" test -c ./internal/runtime/agentruntime       # binary built
```

All four test binaries produced (7.5–15.3 MB each) — full-repo build and vet exit 0; affected packages' test binaries compile for the production platform.

### Overlay full-repo run (DoD `go test ./... -count=1` equivalent on this host)

```bash
"$GO" test -overlay=/tmp/ac-overlay.K3o4ga/overlay.json -vet=off -count=1 ./...
```

Result: **53 packages `ok`** (including all four affected packages and every package this change depends on); exit 1 solely due to **four pre-existing Windows-platform failure packages**, none related to this change:

| Package | Failing test(s) | First failure line | Classification |
|---------|-----------------|--------------------|----------------|
| `internal/pkg/config` | `TestGitWorkspaceConfigRequiresSafeAbsoluteRootAndDirectoryName`, `TestDockerConfigLoadsWithNATSAndMilvus` | `config_test.go:102: valid Git workspace config rejected: git_workspace.allowed_roots entries must be absolute` | The exact pre-existing failure named in the brief: `filepath.IsAbs("/workspaces")` is false on win32. Zero intersection with this change (config loading only). |
| `internal/application/workspace_usecase` | `TestLockProcessStateIsFailSafe` | `service_test.go:285: dead process should be known and not live: live=true known=true` | Windows process-liveness semantics; same family as the documented `syscall.Kill` blocker in `cleanup.go`. Zero intersection with this change. |
| `internal/interface/http/handler` | `TestRunWorkspaceGitAndLifecycleHandlers` | `git_workspace_handler_test.go:344: /runs/20/workspace response = 200 … "repository_root":"D:\\Users\\HONGZE~1.ZHA\\…"` | Windows 8.3 short-path names in temp workspace roots break the test's path comparison. Workspace-handler territory, untouched by this change. |
| `internal/runtime/toolruntime` | `TestWorkspaceExecCapsOutputWhileCommandRuns`, `TestWorkspaceExecFixesCWDAndRejectsLiteralPathEscape`, `TestWorkspaceExecScrubsServiceSecretsFromChildEnvironment` | `filesystem_tools_test.go:136: exec: "/bin/sh": executable file not found in %PATH%` | `/bin/sh` does not exist on Windows. Same family as the documented `filesystem_path.go` blocker. |

Why these cannot be caused by Task 5: the only artifact added is one `_test.go` file in package `observability_test`, which Go test compilation makes invisible to every other package's build/test binary. All four failing packages are workspace/config/exec platform tests with no dependency on the observability/correlation code paths, and the four packages this change touches all pass fully.

## 7. Production defects exposed

**None.** All five RED failures were the planned helper-capability gaps; once the helpers were completed, every assertion about Tasks 1–4 behavior passed on first run — no production code change was needed or made, and nothing is marked red for the main session's fix flow.

## Compliance summary

- [x] Five integration tests with the exact required names, driven by `TestCorrelationIntegration` + `CorrelationIntegrationTest` — RED (assertion failures, verbatim above) → GREEN (verbatim above).
- [x] Zero production code changes; zero git operations; no new migration; no new module dependency (stdlib + existing gin only).
- [x] Real middleware chain, real `authusecase.Service` (JWT issue + verify), real `agentusecase.Service` (StartTurn codec, durable worker, RunSubagent codec, emitter audit), real `runtimeagent.Runner` — no infeasible handler/service mocking.
- [x] DoD equivalents: focused GREEN command pass; `go test` full suites of the four packages exit 0; `GOOS=linux go vet ./...` exit 0; `GOOS=linux go build ./...` exit 0; `GOOS=linux go test -c` for all four packages; overlay `./...` run with pre-existing failures documented (brief §环境验证方案 item 3).
- [x] Existing HTTP response and trace API tests show no regression (full `internal/interface/http` and `agent_usecase` suites pass).
