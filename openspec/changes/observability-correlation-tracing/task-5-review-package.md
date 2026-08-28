# Task 5 Review Package — end-to-end correlation, privacy, legacy compatibility

Change: `observability-correlation-tracing` · Branch: `feature/observability-correlation-tracing` · `auto_commit: false` (nothing committed; Tasks 1–4 + Task 5 are all uncommitted working-tree changes).

## Review scope (Task 5 artifacts only)

Task 5 is a **pure verification task** — Tasks 1–4 production code is out of scope except where needed to judge whether the tests exercise real seams.

| Artifact | Path | Status |
|---|---|---|
| Integration suite (1382 lines, package `observability_test`) | `internal/pkg/observability/correlation_integration_test.go` | **Created** — the only code artifact |
| Implementer report | `openspec/changes/observability-correlation-tracing/task-5-report.md` | Created |
| Implementer brief | `openspec/changes/observability-correlation-tracing/task-5-brief.md` | Reference |

**Known deviation to adjudicate:** tasks.md lists three Modify files (`internal/interface/http/router_observability_test.go`, `internal/application/agent_usecase/run_publisher_test.go`, `internal/runtime/agentruntime/agent_runtime_test.go`). None was modified; the report (§"Why the three Modify files needed no changes") justifies this because their helpers are unexported inside `_test.go` files of other packages and therefore unimportable from `observability_test`. The brief explicitly permitted leaving a Modify file unchanged with justification. Judge whether the justification and the coverage achieved are acceptable, or whether the deviation requires artifact reverse-sync.

## Task 5 contract (from tasks.md)

- RED → GREEN: five methods under `CorrelationIntegrationTest`, exact names `shouldLinkHTTPStartTurnWorkerAndRuntime`, `shouldLinkParentRunAndToolStep`, `shouldKeepTraceAPIShape`, `shouldRejectSensitiveLogAttributes`, `shouldRemainCompatibleWithLegacyRun` (correlation_integration_test.go:978, 1055, 1144, 1220, 1291).
- Harness mandate: `httptest` Gin chain; in-memory Run/Turn/RunEvent/RunStep repositories; real `authusecase.Service`/`agentusecase.Service` with injectable fakes; fake runtime executor; capturing `slog.Handler`; no infeasible handler/service mocks.
- DoD: five tests green; `go test ./... -count=1` exit 0; `go vet ./...` exit 0; no new durable trace migration; no regressions in existing HTTP response / trace API tests.

## Tasks 1–4 seams the suite must exercise (for judging non-vacuity)

- Correlation API: `internal/pkg/observability/correlation.go` (`WithCorrelation`, `CorrelationFromContext`, immutable `With*`).
- Middleware chain: `internal/interface/http/router.go:45-48` RequestID → AccessLog → Recovery → CORS; protected group adds Auth + RequireRouteScope (:72-73). Task 2 writes correlation in `request_id.go`/`auth.go`; `http.access`/`http.error` from access_log/recovery middleware.
- StartTurn codec: `internal/application/agent_usecase/service.go` — `inputObservabilityMetadata` (~:469-489), merged into Run+Turn InputJSON at :875; decode at :490-495; `logTurnLifecycle` at :515.
- Worker restore: `internal/application/agent_usecase/turn_worker.go:83-91` (malformed → one bounded `turn.metadata_parse_error`; ctx correlation restore before runtime).
- Runtime diagnostics: `internal/pkg/logger/logger.go:46-59` DiagnosticsHandler (whitelist at logger.go:38-44, 16 KiB cap, fail-open); `internal/runtime/agent/runner.go:40-42` `Runner.Logger` seam; `llm.request/llm.completed` at model_turn.go:198+; `tool.started/tool.completed` in runner batch callback; `compaction.completed` in auto_compaction.go; `run_event.audited` at run_publisher.go:99-104.
- Trace API: `internal/interface/http/handler/agent_handler.go:673-703` GetRunTrace → GetRun, ListRunEvents(ctx, owner, run, 0), ListRunSteps, ListChildRuns; response `gin.H{"run","events","steps","children"}`.
- Event vocabulary/whitelist and privacy rules: `specs/correlated-agent-observability/spec.md` and `design.md` Decision 2 (note: `run_event.audited` was reverse-synced into design.md this session; see `log.md` "Reverse Sync — run_event.audited vocabulary").

## Main-session verification already performed (fresh, post-Task-5)

- Focused suite (overlay harness `/tmp/ac-overlay.K3o4ga/overlay.json`, `-vet=off`): `ok agentcanvas/internal/pkg/observability 2.960s` — five tests pass; other three packages `[no tests to run]` for the `-run` filter.
- `GOOS=linux GOARCH=amd64 go test -c` for all four packages → binaries produced (obs/http/au/art).
- `GOOS=linux GOARCH=amd64 go vet ./...` exit 0 and `go build ./...` exit 0 (repo-wide, includes all test files).
- Implementer's overlay full-repo run: 53 packages ok; 4 failing packages documented as pre-existing Windows-platform failures (`pkg/config` IsAbs, workspace process-liveness, handler 8.3 short-path, toolruntime `/bin/sh`) — report §6.

## Review questions

### Spec review
1. Do the five tests, as written, verify what spec.md requires for correlation propagation, privacy (metadata-only diagnostics, no prompt/authorization/api_key/full tool args/output), legacy compatibility, and trace API contract stability?
2. Is the RED evidence genuine TDD (assertion failures on deliberately incomplete helpers, not compile errors)?
3. DoD: is the `go test ./...` / `go vet ./...` equivalence evidence (Linux gates + overlay full run + documented pre-existing failures) acceptable for this Windows host?
4. Is the three-Modify-file deviation acceptable, or does it require artifact reverse-sync?
5. Any spec requirement in Tasks 1–4 that Task 5 should have covered but didn't?

### Code-quality review
1. Non-vacuity: do the tests really drive the real router/auth/service/worker/Runner seams, or do they short-circuit through their own fakes in ways that make assertions tautological?
2. Flakiness risks: deadline waits (6 s terminal-status waits), goroutine/worker lifecycle teardown, `slog.SetDefault` swaps, eventhub usage, ordering assumptions.
3. Correctness of the privacy scan (whitelist mirror of logger.go:38-44; banned-substring coverage; non-vacuity preconditions).
4. Test-code quality: duplication, naming, failure messages, reuse where possible, consistency with neighboring test files' style.
5. Confirm zero production-code changes and no accidental touches to protected files (`internal/runtime/eventhub/hub.go`, `openspec/changes/sql-memory-es-hybrid/`, `.codex/`, `.codegraph/`, `openspec/changes/conversation-cache/`).

## Verdict protocol

Return exactly one verdict — `PASS` or `BLOCKED` — with findings classified Critical / Important / Minor. Minor findings may be parked for the final review; any Critical or Important finding means BLOCKED with a concrete fix instruction.
