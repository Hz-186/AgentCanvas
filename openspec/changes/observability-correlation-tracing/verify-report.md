# Verify Report — observability-correlation-tracing

- Date: 2026-08-28 · Branch: `feature/observability-correlation-tracing` · Base: `ff9eaf9` (state `base_commit`)
- Stage: verify (post-apply, all 5 tasks `[x]`) · Mode: standard, serial · `auto_commit: false`
- Pre-skills loaded (HARD-GATE): `vsdd-workflow-verify`, `vsdd-workflow-router`, `verification-before-completion`
- Governing rule: fresh evidence before any claim (Iron Law). All test/build evidence below was produced during this verify stage unless marked otherwise.

## 1. Scope and method

All implementation is uncommitted working-tree changes (user-configured `auto_commit: false`); `git rev-list --count ff9eaf9..HEAD` = 0, so the effective change diff is `git diff ff9eaf9` + untracked files. Verification combined: fresh full-repo test run (overlay harness), fresh Linux cross-compile/vet gates, base-commit worktree reproduction of every failing package, artifact audit (proposal/spec/design/tasks/log), and per-task review-evidence audit.

Changed files (Tasks 1–5): production — `internal/pkg/observability/correlation.go` (new), `internal/pkg/logger/logger.go`, `internal/interface/http/middleware/{request_id,auth,recovery,cors}.go`, `internal/interface/http/middleware/access_log.go` (new), `internal/interface/http/router.go`, `internal/application/agent_usecase/{service,turn_worker,run_publisher}.go`, `internal/runtime/agent/{runner,model_turn,auto_compaction}.go`; tests — middleware tests (new), `router_observability_test.go` (new), `service_test.go`/`turn_worker_test.go` (new), `logger_test.go` (new), `runner_test.go`/`model_turn_test.go`/`auto_compaction_diagnostics_test.go` (new/modified), `run_publisher_test.go`, `correlation_integration_test.go` (new, Task 5). No migrations added. Protected files untouched (see §6).

## 2. Fresh build/test evidence (this verify stage)

| Gate | Command | Result |
|---|---|---|
| Repo build (Linux) | `GOOS=linux GOARCH=amd64 go build ./...` | exit 0 (`BUILD-ALL=0`) |
| Repo vet (Linux, incl. all test files) | `GOOS=linux GOARCH=amd64 go vet ./...` | exit 0 (`VET-ALL=0`) |
| Test binaries (Linux) | `go test -c` for observability / http / agent_usecase / agentruntime | 4 binaries produced (closure gate, re-confirmed at Task 5) |
| Full suite (DoD `go test ./... -count=1` equivalent) | `go test -overlay=/tmp/ac-overlay.K3o4ga/overlay.json -vet=off -count=1 ./...` | all change packages `ok`; exit 1 caused only by 4 pre-existing Windows-platform packages (proven below) |
| Focused integration suite | same overlay, `-run TestCorrelationIntegration` | `ok agentcanvas/internal/pkg/observability` — five tests green (also re-run 4× by spec reviewer, `-count=3` by quality reviewer) |

Change packages in the full run, all `ok`: `agent_usecase` (28.6s), `interface/http` (1.4s), `interface/http/middleware`, `pkg/logger`, `pkg/observability` (2.6s), `runtime/agent` (14.5s), `runtime/agentruntime` (5.1s), `runtime/eventhub` (1.1s), `runtime/compaction`, `observability`, `domain/*`, all `application/*` except workspace_usecase.

### Pre-existing failure proof (base-commit worktree `ff9eaf9`)

All four failing packages were reproduced identically on the pristine base commit (temporary worktree, since removed):

| Package | Failing test(s) | Base repro | Cause |
|---|---|---|---|
| `internal/pkg/config` | `TestGitWorkspaceConfigRequiresSafeAbsoluteRootAndDirectoryName`, `TestDockerConfigLoadsWithNATSAndMilvus` | identical failures on `ff9eaf9` | `filepath.IsAbs("/workspaces")` false on win32 |
| `internal/interface/http/handler` | `TestRunWorkspaceGitAndLifecycleHandlers` | identical failure on `ff9eaf9` (overlay build) | Windows 8.3 short path (`HONGZE~1.ZHA`) in path comparison |
| `internal/application/workspace_usecase` | `TestLockProcessStateIsFailSafe` | identical failure on `ff9eaf9` (overlay build) | Windows process-liveness semantics |
| `internal/runtime/toolruntime` | 3 × `TestWorkspaceExec*` | identical failures on `ff9eaf9` (overlay build) | `/bin/sh` absent on Windows |

Conclusion: full-suite exit 1 is fully accounted for by pre-existing platform failures with zero intersection with this change. DoD equivalence for `go test ./...` holds on this host.

## 3. Completeness — spec coverage (requirement by requirement)

| Spec requirement | Status | Evidence |
|---|---|---|
| Correlation context continuity | ✅ | Task 1 `correlation.go` (all 8 fields); Task 2 middleware writes; Task 3 InputJSON codec. Scenarios: correlated turn → Task 5 `shouldLinkHTTPStartTurnWorkerAndRuntime` + Task 2 middleware tests; missing request ID generated → `request_id_test.go` + Task 5 legacy test (generation-when-absent unchanged) |
| Asynchronous execution continuity | ✅ | Task 3 worker restore (`turn_worker.go:85-91`). Worker restores queued turn → Task 3 tests + Task 5 test 1 (separate goroutine); legacy turn no metadata → Task 5 `shouldRemainCompatibleWithLegacyRun` (no fabrication, no row rewrite, no extra durable records); malformed fallback → `turn_worker.go:83` + Task 3 `shouldFallbackForMalformedMetadata` (unit level; integration-level malformed e2e parked as covered-by-unit) |
| Structured lifecycle diagnostics | ✅ | `http.access/http.error` (Task 2 + recovery/access-log tests + router_observability_test); `turn.started/finished/failed` (Task 3 `logTurnLifecycle`, service.go:515 + tests); `llm.request/llm.completed` (Task 4 model_turn.go + tests, response/ordering untouched); `tool.started/tool.completed` (runner.go batch callback + tests incl. soft-failure classification); `compaction.completed` (auto_compaction.go + tests). conversation-cache events untouched (separate change) |
| Durable trace boundary | ✅ | No migrations (`git status` shows none). DB-first: `run_publisher.go` diagnostic only after `repo.Create`+publish; `TestRunPublisherDiagnosticsPublishesAfterAuditEvenWhenLoggerFails` + pre-existing `TestRunEventEmitterPublishesOnlyAfterAuditWrite` pass. Trace API: Task 5 `shouldKeepTraceAPIShape` asserts `run/events/steps/children` + exact 4/1/1/1 repo-call contract; pre-existing handler tests pass |
| Privacy-safe and fail-open observation | ✅ | Whitelist in `logger.go:38-44` byte-identical to spec's 20 keys; 16 KiB cap; at-most-one bounded sink error, fail-open (Task 4 `logger_test.go`). Sensitive redaction → Task 5 `shouldRejectSensitiveLogAttributes` (bait scan: prompt/api_key/JWT/Bearer/tool output; whitelist-only keys; non-vacuity precondition). Sink unavailable → Task 4 isolation tests + publisher fail-open test |
| Compatibility and bounded overhead | ✅ | X-Request-ID echo preserved (Task 2 tests + Task 5 test 5); no HTTP response schema changes; Run/Turn transitions and LLM/tool passthrough unchanged (Task 4 reviews confirmed errors/responses pass through); diagnostics only at lifecycle boundaries, never per-delta |

Spec `## Verification` acceptance items: all five map to the rows above and are satisfied.

## 4. Correctness

- Intent match: correlation flows HTTP → StartTurn persistence → async worker → runtime ctx → child/tool diagnostics with exact-value equality asserted end-to-end (Task 5 reconciliation table); degradation paths (legacy/malformed) verified to neither fail nor fabricate.
- Boundaries/error handling: fail-open isolation proven in both directions (logger sink failure; diagnostics never alter `Emit`'s DB-first result); error_class is type-based (`%T`), never error text.
- Every 🟡 task has test changes + minimal closed loops (RED→GREEN per task, captured in reports/task logs); DoD commands match the verification commands actually run.

## 5. Consistency with design

- Decision 2 event vocabulary matches implementation; `run_event.audited` was reverse-synced into design.md this session before Task 5 closure (log.md entry present).
- Task 5 file-list deviation (three Modify files left unchanged): both reviewers ACCEPTED the justification (helpers unexported in other packages' `_test.go` files are unimportable); brief explicitly permitted it. Suggestion: annotate tasks.md at archive time.
- No other deviations found.

## 6. Process integrity

| Check | Result |
|---|---|
| Serial apply | ✅ Tasks 1→5 strictly serial (log.md ordering) |
| Dual independent review per task | ✅ Tasks 1–5 each have spec + code-quality review entries with agent IDs in log.md; Task 4 and Tasks 1–2 fix rounds documented with scoped re-reviews |
| No completed task with production-only changes | ✅ every task carries tests |
| RED/GREEN/ASSERT evidence for 🟡/🔴 | ✅ task-3/4/5-report.md + log entries; Tasks 1–2 recorded in prior-session log sections |
| Reverse Sync | ✅ Task 1, Task 3, `run_event.audited` (design Decision 2) — all logged, no omissions |
| Design review | ✅ 2 rounds, PASS, recorded in state yaml |
| Branch discipline | ✅ all work on `feature/observability-correlation-tracing`; nothing on main |
| tasks.md format | ✅ five line-leading `- [x]` items; zero unchecked |
| Protected files | ✅ `eventhub/hub.go` diff is pre-existing comment reflow only; `sql-memory-es-hybrid/*`, `.codex/`, `.codegraph/`, `conversation-cache/` untouched by this change |
| Commit granularity | ⚠ Warning: 0 commits for 5 tasks — by explicit user policy (`auto_commit: false`), not process debt; commits deferred to user decision |

## 7. UNI CR

`uni_cr_enabled` not configured → default-on per workflow. **Not executable at this gate**: the mandated revision range `base_commit..HEAD` contains zero commits (auto_commit false), so no reviewable revision range exists; uni-cr's own rule halts when a diff range cannot be established, and uploading a review of uncommitted work externally was not authorized. ⚠ Warning recorded; run `$uni-cr` (Review mode) after the implementation commits are created, before PR merge.

## 8. Findings

**Critical: none.**

**Warnings (2):**
1. Zero implementation commits (user policy `auto_commit: false`) — UNI CR cannot run and no PR is possible until commits exist.
2. UNI CR skipped at verify gate for the reason above.

**Suggestions / parked minors (for follow-up; none block archive):**
- Open design question: inline-subagent path (`RunSubagent` → `runtime.Execute`) does not re-derive child correlation; inline subagent tool diagnostics inherit the parent's correlation. Task 5's child test uses the worker-derived shape and passes its contract, but confirm whether production inline-path child derivation is intended (possible small Tasks-1–4 gap).
- Resume paths carry no request_id / turn.started (silent safe degradation) — decide if resume should restore correlation too.
- turn.finished micro-race (emitted after terminal persistence) — nanosecond window; poll-until-seen hardening optional.
- WithAttrs/WithGroup derived handlers bypass whitelist + reset sink-failure state (no current derivation in use).
- Panic mid-stream can emit misleading `llm.completed result=ok`; 16 KiB budget bounds attrs but not message text; cancelled parallel-batch calls emit no tool diagnostics.
- Integration-level coverage gaps (malformed metadata e2e, llm.*/compaction.completed assertions, fail-open sink) are all covered by Tasks 1–4 unit tests.
- Windows verification constraints: native `go test ./...`/`go vet ./...` impossible (pre-existing Unix syscalls); equivalence = overlay native runs + `GOOS=linux` build/vet/test-c gates (all exit 0). `-race` unavailable on this host (no gcc for cgo).

## 9. Conclusion

**PASS — no Critical issues.** All five tasks complete with genuine TDD evidence, dual reviews, and reverse syncs; the full spec is covered; build/vet/test gates pass fresh; the only failing packages in the full suite are proven pre-existing on the base commit. The change is eligible for the archive stage.

Recommended next steps (user decision required, since `auto_commit: false`):
1. Create the implementation commits (per-task or squashed) and push the branch.
2. Run `$uni-cr` on the commit range (default-on gate deferred here).
3. Archive the change (`vsdd-workflow-archive` / openspec archive) and open the PR to main.
