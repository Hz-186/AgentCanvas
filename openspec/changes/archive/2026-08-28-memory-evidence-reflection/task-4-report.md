# Task 4 Report — Replace per-boundary jobs with session-level debounce scheduling

> change: memory-evidence-reflection | task: 4 | complexity: 🔴 | date: 2026-08-28
> branch: refactor/memory-usecase-cleanup | status: **DONE**

## Skills Loaded

- `vsdd-workflow-implement-task`
- `test-driven-development`
- `vsdd-workflow-reverse-sync` (no trigger — no artifact/code conflict found)

## Environment

- Toolchain: `D:\Users\hongze01.zhang\sdk\go1.26.6\bin\go.exe` (not on PATH), Windows host, Git Bash.
- `internal/application/memory_usecase` tests natively on Windows (no overlay needed).
- `internal/runtime/agentruntime` and `internal/infrastructure/mysql` test binaries need the approved test-only overlay (`%TEMP%\agentcanvas-task4-overlay\overlay.json`, a syscall.Flock shim for `internal/runtime/toolruntime/filesystem_path.go`; never committed) because `toolruntime` is transitively imported and does not compile natively on Windows.
- The MySQL integration subtests are DSN-gated (`AGENTCANVAS_TEST_MYSQL_DSN`) and auto-skip without a live server; the sqlmock subtests pin the SQL shape and transactional branch flow instead.
- Shipping gate: `GOOS=linux go build ./...` without overlay → exit 0.

## RED evidence (tests written before any production code)

The three test files were authored first; the paste below was re-captured at report time by stashing the GREEN implementation and running the identical tests against the pristine production code (tests unchanged in between).

### memory_usecase — compile-level RED (new scheduling API missing)

`go test ./internal/application/memory_usecase -run 'ScheduleBoundary|BoundaryWindow' -count=1`:

```text
# agentcanvas/internal/application/memory_usecase [agentcanvas/internal/application/memory_usecase.test]
internal\application\memory_usecase\durable_boundary_schedule_test.go:240:8: jobs.onLatestDurableJob undefined (type *fakeExtractionRepo has no field or method onLatestDurableJob)
internal\application\memory_usecase\durable_boundary_schedule_test.go:274:8: jobs.onLatestDurableJob undefined (type *fakeExtractionRepo has no field or method onLatestDurableJob)
internal\application\memory_usecase\durable_boundary_schedule_test.go:374:11: jobs.listByStatusCalls undefined (type *fakeExtractionRepo has no field or method listByStatusCalls)
internal\application\memory_usecase\durable_boundary_schedule_test.go:375:96: jobs.listByStatusCalls undefined (type *fakeExtractionRepo has no field or method listByStatusCalls)
FAIL	agentcanvas/internal/application/memory_usecase [build failed]
```

### agentruntime — behavioral RED (exactly the 3 new whitelist reasons not scheduled)

`go test -overlay … ./internal/runtime/agentruntime -run 'DurableTriggerWhitelist' -count=1`:

```text
--- FAIL: TestDurableTriggerWhitelist (0.00s)
    --- FAIL: TestDurableTriggerWhitelist/shouldScheduleOnlyForWhitelistedStopReasons (0.00s)
        durable_trigger_whitelist_test.go:72: stop reason "max_iterations_exceeded" scheduled 0 call(s), want 1
        durable_trigger_whitelist_test.go:72: stop reason "max_tool_calls_exceeded" scheduled 0 call(s), want 1
        durable_trigger_whitelist_test.go:72: stop reason "timeout" scheduled 0 call(s), want 1
FAIL	agentcanvas/internal/runtime/agentruntime	1.110s
```

`final_answer` passed the old `==` check; the three budget-exhaustion reasons did not — precisely the gap the whitelist closes.

### mysql — compile-level RED via `GOOS=linux go test -c`

```text
# agentcanvas/internal/infrastructure/mysql [agentcanvas/internal/infrastructure/mysql.test]
internal\infrastructure\mysql\extraction_schedule_test.go:81:29: repo.ScheduleDurableBoundary undefined (type *ExtractionJobRepository has no field or method ScheduleDurableBoundary)
internal\infrastructure\mysql\extraction_schedule_test.go:221:26: repo.RefreshPendingBoundary undefined (type *ExtractionJobRepository has no field or method RefreshPendingBoundary)
internal\infrastructure\mysql\extraction_schedule_test.go:241:24: repo.LatestCompletedDurableThrough undefined (type *ExtractionJobRepository has no field or method LatestCompletedDurableThrough)
… too many errors
```

## GREEN evidence

All 9 mandated scenarios plus the legacy/shadow/idle companions, subtest-verbatim:

```text
--- PASS: TestScheduleBoundary/shouldCreateInitialJobWhenConversationEmpty (0.00s)
--- PASS: TestScheduleBoundary/shouldRefreshPendingRowInPlace (0.00s)
--- PASS: TestScheduleBoundary/shouldCreateSingleSuccessorForRunningJob (0.00s)
--- PASS: TestScheduleBoundary/shouldCreateNewRowAfterTerminalJob (0.00s)
--- PASS: TestScheduleBoundary/shouldFallbackToSuccessorOnRefreshRace (0.00s)
--- PASS: TestScheduleBoundary/shouldDeduplicateConcurrentSuccessorViaUniqueKey (0.00s)
--- PASS: TestScheduleBoundary/shouldRecognizeLegacyFormatRowsByConversation (0.00s)
--- PASS: TestBoundaryWindow/shouldStartWindowAfterLatestCompletedDurableJob (0.00s)
--- PASS: TestBoundaryWindow/shouldKeepOutOfOrderShadowRule (0.00s)
--- PASS: TestDurableTriggerWhitelist/shouldScheduleOnlyForWhitelistedStopReasons (0.00s)
--- PASS: TestDurableTriggerWhitelist/shouldKeepSubagentAndGateExclusions (0.00s)
```

SQL-shape / transactional-flow coverage (sqlmock, run through the overlay):

```text
--- PASS: TestScheduleBoundaryRepo/shouldRefreshPendingRowInsideLockingTransaction (0.00s)
--- PASS: TestScheduleBoundaryRepo/shouldCreateInitialRowWhenConversationEmpty (0.00s)
--- PASS: TestScheduleBoundaryRepo/shouldCreateSuccessorWhenLockingReadObservesRunning (0.00s)
--- PASS: TestScheduleBoundaryRepo/shouldFallbackToSuccessorWhenRefreshAffectsZeroRows (0.00s)
--- PASS: TestScheduleBoundaryRepo/shouldRereadExistingRowOnUniqueKeyConflict (0.03s)
--- PASS: TestScheduleBoundaryRepo/shouldScopeRefreshToPendingRows (0.00s)
--- PASS: TestScheduleBoundaryRepo/shouldReadWindowStartByConversationOnly (0.00s)
--- SKIP: TestScheduleBoundaryIntegration (DSN unset — 5 live-server subtests)
```

Full package suites (regression): `memory_usecase` ok, `agentruntime` ok (overlay), `mysql` ok (overlay).

## DoD gates

- All 9 scenarios + companions green ✅
- `GOOS=linux go build ./...` — exit 0, no overlay ✅
- `GOOS=linux go test -c` — both `internal/infrastructure/mysql` and `internal/application/memory_usecase` test binaries compile ✅
- `grep -rn "durable:pending" internal/` — empty (exit 1) ✅
- Legacy rows (`durable:<o>:<c>:<through>`) still recognized by conversation-scoped queries — covered by `shouldRecognizeLegacyFormatRowsByConversation` in both the fake-backed scheduling test and the DSN-gated integration test ✅
- `go vet` clean on all four changed packages (mysql via overlay) ✅
- gofmt-clean content (verified on LF-normalized copies; repo working-tree convention is CRLF, so `gofmt -l` flags every repo file equally) ✅

## ASSERT items

1. **Queue publish exactly-once-on-create**: scenario 1 asserts 1 publish with `AvailableAt == *job.DueAt` and the created row's job/owner/conversation payload; scenarios 2 (refresh), 6 (dedup loser) assert 0 publishes; scenarios 3, 4, 5 assert exactly 1 publish for the single new row.
2. **Retired key format gone / new format present**: `grep -rn "durable:pending" internal/` empty; generation keys are produced only by `durableBoundaryKey` (pipeline) and `durableInitialKey`/`durableSuccessorKey` (repo): `durable:<o>:<c>:initial`, `durable:<o>:<c>:after-job:<id>`.
3. **`ListByStatus(200)` scan removed**: `previousBoundary` now calls `LatestCompletedDurableThrough`; `grep -rn "\.ListByStatus(" internal/` finds zero production callers; `shouldStartWindowAfterLatestCompletedDurableJob` asserts `listByStatusCalls == 0` while 250 unrelated completed jobs with larger through values sit in other conversations.
4. **FOR UPDATE lock scoped**: sqlmock pins the locking read to `SELECT * FROM memory_extraction_jobs WHERE owner_id = ? AND conversation_id = ? AND trigger_reason = ? ORDER BY id DESC LIMIT ? FOR UPDATE` — the conversation's latest durable row only, never a table-wide lock.

## Files changed

| File | Action |
|---|---|
| `internal/domain/memory/extraction.go` | MODIFIED — +3 methods on `ExtractionJobRepository`: `LatestDurableJob`, `RefreshPendingBoundary`, `LatestCompletedDurableThrough` |
| `internal/infrastructure/mysql/extraction_repo.go` | MODIFIED — primitives above + transactional `ScheduleDurableBoundary` (FOR UPDATE locking read → conditional refresh / successor / create, unique-conflict re-read) + `newBoundaryRow`/`durableInitialKey`/`durableSuccessorKey`; existing `LatestCompletedThrough` preserved unchanged per design |
| `internal/application/memory_usecase/durable_memory_pipeline.go` | MODIFIED — `NewDurableMemoryTrigger` rewritten around `scheduleDurableBoundary` (Redis `durable:pending:` burst key removed); `previousBoundary` replaced with the targeted window-start lookup |
| `internal/application/memory_usecase/durable_memory_pipeline_test.go` | MODIFIED — fake upgraded: unique `(owner_id, idempotency_key)` enforcement, pending-only refresh, MAX(id) window lookup, `listByStatusCalls` counter, `onLatestDurableJob` race hook |
| `internal/runtime/agentruntime/assembly.go` | MODIFIED — `durableTriggerStopReasons` whitelist map; `checkExtractionTrigger` consults it instead of `== StopReasonFinalAnswer` |
| `internal/application/memory_usecase/durable_boundary_schedule_test.go` | NEW — `TestScheduleBoundary` (7 subtests), `TestBoundaryWindow` (2 subtests), `TestNewDurableMemoryTriggerIgnoresIdleConversations` |
| `internal/runtime/agentruntime/durable_trigger_whitelist_test.go` | NEW — `TestDurableTriggerWhitelist` (2 subtests; `allStopReasons()` pins all 12 constants so enum growth forces a whitelist decision) |
| `internal/infrastructure/mysql/extraction_schedule_test.go` | NEW — `TestScheduleBoundaryRepo` (7 sqlmock subtests) + DSN-gated `TestScheduleBoundaryIntegration` (5 subtests) |

## Implementation notes

### Debounce branch logic (design Decision 3)

- **pending latest row** → conditional UPDATE (`WHERE id AND owner AND status='pending'`) refreshes `through_message_id`/`due_at` in place; `RowsAffected==1` → refreshed, `created=false`, no queue publish.
- **running latest row** → exactly one successor keyed `durable:<o>:<c>:after-job:<runningID>`; a second schedule call refreshes that successor instead of stacking rows.
- **terminal or absent latest row** → fresh row (`:initial` when absent).
- **0-row refresh race** (worker claimed the pending row between read and update) → defensive fall-through to successor creation; no error, no duplicate.
- **unique-key conflict** on the successor INSERT → re-read by `(owner_id, idempotency_key)` and return the winner with `created=false` (loser publishes nothing).

### Two tested scheduler paths

`scheduleDurableBoundary` type-asserts the repository to the `durableBoundaryScheduler` interface: the MySQL repo takes the single-transaction FOR UPDATE path (pinned by sqlmock); repositories without it (the hand-written fakes) compose identical semantics stepwise from the three primitives, including the 0-row-refresh fallback and the unique-key re-read. Both paths are behaviorally exercised — the fakes drive the branch logic, sqlmock drives the SQL shape and transaction flow.

### Queue contract

The wakeup (`memory:durable`, `AvailableAt = *job.DueAt`) is published only when `created=true`. Refreshes, dedup losers, and scheduler errors publish nothing. `DueAt == now + IdleTimeout` (idle defaulted to 1m if unset, matching the old burst TTL fallback).

### Whitelist (design Decision 4)

`{final_answer, max_iterations_exceeded, max_tool_calls_exceeded, timeout}` — the run answered finally or exhausted a budget. All 12 StopReason constants are enumerated by the test; ParentRunID / DelegationDepth / memoryEnabled / missing-conversation exclusions unchanged.

### Window start (design Decision 5)

`previousBoundary` = through of the conversation's latest **completed** durable job by MAX(id) (`WHERE owner AND conversation AND trigger_reason='durable' AND status='completed' ORDER BY id DESC LIMIT 1`, uses `idx_conversation_id`). The out-of-order shadow rule survives in the caller: a returned boundary `>= current.ThroughMessageID` still collapses the window to an empty result. `LatestCompletedThrough` (the MAX(through) `id < beforeJobID` aggregate) is deliberately not reused or modified.

### Legacy compatibility

Legacy rows carry the retired key format `durable:<o>:<c>:<through>`. Every new query is conversation-scoped (`conversation_id` + `trigger_reason`, no key parsing), so legacy rows are found as latest-row and window-start inputs; their successors key off the legacy row's id (`after-job:<legacyID>`). Covered in both fake and integration tests.

### Redis

The `durable:pending:%d:%d:%d` SetNX burst key is gone; debounce is now DB-native. `NewDurableMemoryTrigger`'s signature is unchanged (bootstrap wiring untouched); the `redisClient` parameter is simply unused by the new trigger body.

## Self-review checklist

- [x] Failing tests first in all three packages; RED evidence pasted above (compile-level for memory_usecase/mysql, behavioral for agentruntime)
- [x] All 9 scenario names preserved verbatim as subtests
- [x] Idempotency keys exactly `durable:<o>:<c>:initial` / `durable:<o>:<c>:after-job:<jobID>`; legacy rows still recognized
- [x] `durable:pending:` absent from the codebase (grep empty)
- [x] `ListByStatus(200)` scan removed from the window path; zero production callers remain; targeted query asserted scan-free
- [x] Queue publish: exactly once on creation with `AvailableAt=DueAt`, zero on refresh/race-loser paths
- [x] FOR UPDATE scoped to the conversation's latest durable row (sqlmock-pinned SQL)
- [x] Whitelist of exactly 4 stop reasons; subagent/gate exclusions kept
- [x] Scope respected: only the declared files + new tests for them; no commit/push; `.vsdd-state.yaml` untouched
- [x] Shipping gate `GOOS=linux go build ./...` exit 0; vet clean; gofmt-clean content

## REFACTOR notes

During GREEN, three SQL-shape corrections surfaced and were fixed without behavior change:

1. `LatestCompletedDurableThrough` was written as `Select("COALESCE(through_message_id, 0)")`; simplified to `Select("through_message_id")` — the column is `NOT NULL DEFAULT 0` in the schema, and GORM renders selected columns backquoted (`` SELECT `through_message_id` FROM ``), which the sqlmock expectation now pins.
2. The conditional-UPDATE argument order was pinned to GORM's actual output: SET keys sorted alphabetically (`due_at`, `through_message_id`, `updated_at`) followed by the WHERE order `id, owner_id, status`.
3. The standalone `RefreshPendingBoundary` primitive is wrapped by GORM's default transaction (Begin/UPDATE/Commit) — pinned in `shouldScopeRefreshToPendingRows`, consistent with the existing `TestMemoryWriteJobRepositoryUpdateValidatesBeforeSave` pattern in this package.

## Deviations

1. **Naming shape**: tasks.md writes `ScheduleBoundaryTest#should…`; Go has no classes, so the scenarios are subtests of `TestScheduleBoundary` / `TestDurableTriggerWhitelist` / `TestBoundaryWindow` with the scenario names verbatim (same convention as Tasks 1–3).
2. **Window assertion**: `shouldStartWindowAfterLatestCompletedDurableJob` asserts `boundary == 500` (the window covers messages strictly after 500) rather than a literal `> 500`, matching the scenario name "start window **after** latest completed durable job" and the tasks.md intent (250 unrelated jobs must not leak in).
3. **Process note (transparency)**: mid-verification an accidental `go fmt ./…` rewrote 59 out-of-scope files (line endings only — the repo working tree is CRLF and gofmt emits LF). All 59 were restored via `git checkout --`; `git status` was re-verified to contain only the 8 Task 4 files, and the full verification (3 suites + linux build + grep) was re-run green afterward. Repo-wide `go test ./internal/...` cannot run natively on Windows for unrelated packages (`workspace_usecase` syscall.Kill/Flock, missing `/bin/sh`, config absolute-root validation) — pre-existing environment limits untouched by this task; the shipping gate `GOOS=linux go build ./...` covers their compilation.
4. **RED paste provenance**: tests were written before any production code; the RED outputs above were re-captured at report time by stashing the implementation and running the unchanged tests against pristine code.

## Reverse Sync

None required. Design Decisions 3/4/5 matched code facts throughout; the `bootstrap/app.go` call site of `NewDurableMemoryTrigger` needed no change (signature preserved), and `LatestCompletedThrough` was left untouched as the design specifies.
