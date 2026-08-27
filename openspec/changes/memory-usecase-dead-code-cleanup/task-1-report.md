# Task 1 Report — memory-usecase-dead-code-cleanup

Branch: `refactor/memory-usecase-cleanup` | Baseline HEAD: `12b7b96` | Date: 2026-08-27
Status: **DONE_WITH_CONCERNS** (all deletions complete and verified green; concerns are environment-driven verification adaptations and two plan-vs-code-fact deviations, all recorded below and in `log.md`)

### Skills Loaded: test-driven-development, vsdd-workflow-reverse-sync

---

## RED / Baseline Evidence (collected BEFORE any deletion)

1. Baseline full build — `~/sdk/go1.26.6/bin/go.exe build ./...` at HEAD 12b7b96 exited **1**, but exclusively with PRE-EXISTING platform errors unrelated to this change (`syscall.Kill`, `syscall.Flock`, `syscall.LOCK_EX`, `syscall.LOCK_UN` are undefined in the Windows syscall package):
   - `internal/application/workspace_usecase/cleanup.go:144`, `internal/application/workspace_usecase/git.go:144,147`
   - `internal/runtime/toolruntime/filesystem_path.go:100,106`

   Proof of platform-specificity: `GOOS=linux go build ./...` exited **0** at baseline. No WSL distro installed, no Docker available, so Linux execution is not possible on this machine. The task instruction "expect exit 0" assumes a Linux environment; this deviation is handled per Reverse Sync (see Deviations).

2. Baseline scoped build — `go build ./internal/application/memory_usecase/...` also failed at baseline on Windows, because the package transitively depended on `internal/runtime/toolruntime` via `internal/domain/agent` (`internal/domain/agent/approval.go:8` imports toolruntime). Baseline `go test ./internal/application/memory_usecase/...` likewise `[build failed]` for the same reason.

3. Dead symbols present in package (grep count, nonzero as required):
   ```
   $ grep -rn "DreamWorker\|ExtractionService\|CandidateService\|MemoryCommandService" --include="*.go" internal/application/memory_usecase | wc -l
   78
   ```
   Per-file breakdown: candidate_service.go 7, command_service.go 7, dream_worker.go 13, dream_worker_test.go 12, extraction.go 14, extraction_test.go 18, service.go 5, service_test.go 2.

4. Repo-wide reference audit (before deletions): outside `internal/application/memory_usecase`, the dead symbols were referenced ONLY in files already inside the task scope:
   - `internal/bootstrap/app.go:263-264` (NewMemoryCommandService / ConfigureCommands)
   - `internal/interface/http/handler/memory_handler.go:18,22` (CandidateService)
   - `cmd/worker/main.go:356` uses `memoryusecase.DreamJobType` — the constant explicitly retained by scope item 3.
   - `internal/interface/http/router.go:144-147` wires only `List`, `Get`, `ListRecallLogs`, `SetRecallFeedback` — all kept; no router change needed.
   - `agentruntime.MemoryBatchReader` (the interface `memoryService` satisfies at `app.go:316`) requires only `GetMany` — kept.

---

## Changed / Deleted Files

Deleted (7):
- `internal/application/memory_usecase/dream_worker.go` (scope)
- `internal/application/memory_usecase/dream_worker_test.go` (scope)
- `internal/application/memory_usecase/extraction.go` (scope)
- `internal/application/memory_usecase/extraction_test.go` (scope)
- `internal/application/memory_usecase/candidate_service.go` (scope)
- `internal/application/memory_usecase/command_service.go` (scope)
- `internal/application/memory_usecase/service_test.go` (scope item 5 outcome — see Deviation D2)

Modified (5):
- `internal/application/memory_usecase/durable_memory_pipeline.go` — added the three interface definitions verbatim from dream_worker.go:27-44 (`DreamMessageRepository`, `dreamMessageBoundaryReader` with its doc comment, `dreamMessageRangeReader`) right after the import block. The surviving pipeline code needs them: `messagesThrough` asserts `dreamMessageRangeReader` and `latestDurableMessageID` asserts `dreamMessageBoundaryReader`. `dreamCompletedBoundaryReader` was NOT moved (used only by deleted dream_worker.go).
- `internal/application/memory_usecase/durable_memory_pipeline_test.go` — relocated `fakeExtractionRepo` (+6 methods, +`var _ memory.ExtractionJobRepository` assertion), `fakeDreamMessages` (+4 methods), and `errNotFound` verbatim from the deleted test files (see Deviation D1).
- `internal/application/memory_usecase/dream_config.go` — reduced to `package memory_usecase` + one-line comment + `const DreamJobType = "memory:dream"`; all imports and `DreamConfig`/`NewDreamConfig`/`NewDreamTrigger`/`ptrTime` removed (repo-wide grep confirmed zero remaining references after the deletions).
- `internal/application/memory_usecase/service.go` — removed `retriever`/`commands` fields, `NewService`, `NewServiceWithCacheAndRetriever`, `CreateMemoryRequest`, `UpdateMemoryRequest`, `Create`, `Update`, `Delete`, `List`, `listCacheKey`, `manualMemorySource`, `ConfigureCommands`, `invalidateCache`. Kept `NewServiceWithCache` (body no longer sets commands), `ConfigureRecallLogs`, `ListRecallLogs`, `SetRecallFeedback`, `ListFiltered`, `Get`, `GetMany`, `ListMemoryFilter`, `normalizedMemoryStatus`, `normalizedMemoryScope`, `containsMemoryFilter`. Import verification: `encoding/json` REMOVED (its only uses were the deleted request structs — the plan's hint "json still needed" did not hold; each import verified per instruction); context/fmt/strings/time/memory/agenterrors all still used.
- `internal/bootstrap/app.go` — lines 262-264 replaced by `memoryService := memoryusecase.NewServiceWithCache(memoryRepo, memoryCache)`; `memoryCommandService` line and `ConfigureCommands` line deleted; `ConfigureRecallLogs` kept; `memoryWriteLogRepo` still used later (`Repositories.MemoryWriteLogs`, app.go:316); `memoryRetrievalStore` still used later (`MemoryRetriever`, app.go:317).
- `internal/interface/http/handler/memory_handler.go` — removed `candidates`/`improvement` fields, `ConfigureCandidates`, `ListCandidates`, `ApproveCandidate`, `RejectCandidate`, `decideCandidate`, `Create`, `Update`, `Delete`, `memoryWritesDisabled`, and the now-unused `agentusecase` import. Kept `MemoryHandler{service}`, `NewMemoryHandler`, `List`, `Get`, `ListRecallLogs`, `SetRecallFeedback`, `intQuery`/`optionalInt64Query`/`splitQuery`.

Diff volume: 14 files changed (13 code + 1 pre-existing .vsdd-state.yaml modification from the controller), 132 insertions(+), 2374 deletions(-).

---

## GREEN Evidence (after deletions, final state)

1. Full repo build (Linux target — the repo's production platform):
   ```
   $ GOOS=linux ~/sdk/go1.26.6/bin/go.exe build ./...
   FINAL_LINUX_BUILD=PASS   (exit 0)
   ```

2. Vet over the three impacted package trees (includes test files):
   ```
   $ GOOS=linux ~/sdk/go1.26.6/bin/go.exe vet ./internal/application/memory_usecase/... ./internal/bootstrap/... ./internal/interface/http/...
   FINAL_LINUX_VET=PASS     (exit 0)
   ```

3. Tests — now runnable natively on Windows (see Deviation D4):
   ```
   $ ~/sdk/go1.26.6/bin/go.exe test -count=1 ./internal/application/memory_usecase/...
   ok  	agentcanvas/internal/application/memory_usecase	1.825s
   ```
   Verbose run (`-count=1 -v`): 9/9 tests PASS (TestDurableFileStoreAdHocNoteIsIdempotentAcrossInstances, TestDurableFileStoreResumesReservedAdHocClaim, TestDurableHandleUsesOwnedUpdateAfterClearingLeaseFields, TestDurableConsolidateEmptyInputInitializesWithoutModel, TestDurableConsolidateConsumesAdHocNotesExactlyOnce, TestDurableConsolidateFailurePreservesPreviousBaseline, TestDurableHandleRetryUsesStoredStageOneResult, TestDurablePhase2FallbackLockRejectsConcurrentWorker, TestDurablePhase2RetryDelayBacksOff). No FAIL lines. These are the surviving tests, unmodified in intent; the two relocated fakes are byte-for-byte copies.

4. Dead-symbol grep inside the package now returns zero:
   ```
   $ grep -rn "DreamWorker\|ExtractionService\|CandidateService\|MemoryCommandService" --include="*.go" internal/application/memory_usecase | wc -l
   0
   ```

5. Package-level vet on Windows native: `go vet ./internal/application/memory_usecase/...` exit 0.

---

## Reverse Sync Deviations (recorded in log.md)

- **D1 — Surviving test file depended on deleted test files (plan gap).** `durable_memory_pipeline_test.go` (not in scope) uses `fakeExtractionRepo` (defined in deleted extraction_test.go) and `fakeDreamMessages` (defined in deleted dream_worker_test.go). Deleting the files as instructed would break compilation of a surviving file. Minimal mechanical remedy applied: the two helpers (+`errNotFound` they depend on) were relocated VERBATIM into durable_memory_pipeline_test.go. Test-only code move, zero behavior change. Other helpers from the deleted test files (`fakeConversationRepo`, `fakeLeasedExtractionRepo`, `fakeCandidateWriter`, `fakeDreamMemoryRepo`, `fakeMemRepo`, `fakeCacheStore`, `fakeServiceRetriever`) had no surviving users and were deleted with their files.
- **D2 — service_test.go deleted entirely.** All 9 tests in it reference deleted symbols (Create/Update/Delete/List/NewService/NewServiceWithCacheAndRetriever/NewMemoryCommandService). Scope item 5 said to keep tests of ListFiltered/Get/GetMany/recall-log feedback — NO such tests exist in the repo today, so there was nothing to keep. An empty test file would itself be dead code, so the file was deleted. Consequence: the retained Service read methods (Get/GetMany/ListFiltered/recall-log) have zero test coverage — exactly as before this change (no coverage was lost; none existed).
- **D3 — `encoding/json` removed from service.go imports** despite the plan hint listing it as still needed. Verified each import: after deleting CreateMemoryRequest/UpdateMemoryRequest, no remaining code in service.go references json.
- **D4 — Verification commands adapted for Windows.** The three prescribed GREEN commands cannot all run natively on this Windows box because of PRE-EXISTING baseline failures (`syscall.Flock/Kill` undefined on Windows in toolruntime/workspace_usecase; present at HEAD 12b7b96 before any edit; repo has no Windows CI config, Makefile targets plain `go test`, no WSL distro, no Docker). Adaptation: `GOOS=linux go build ./...` + `GOOS=linux go vet <three patterns>` (both exit 0). Bonus effect of the deletion: memory_usecase no longer imports anything that reaches `internal/domain/agent` → `toolruntime`, so `go test ./internal/application/memory_usecase/...` now compiles and PASSES natively on Windows (verified twice, incl. `-count=1 -v`).

---

## Self-Review Checklist

- [x] Interfaces added verbatim (incl. doc comment), no renames; placed after imports in durable_memory_pipeline.go
- [x] Only the 6 scoped files + fully-consumed service_test.go deleted; no other files deleted
- [x] dream_config.go reduced to constant + one-line comment; no imports; DreamJobType still satisfies cmd/worker/main.go:356
- [x] service.go keeps exactly the KEEP list; NewServiceWithCache no longer constructs commands
- [x] app.go uses NewServiceWithCache; command-service wiring gone; ConfigureRecallLogs and all other wiring intact
- [x] memory_handler.go keeps only scoped members; agentusecase import removed; query helpers intact
- [x] router.go untouched and compatible (wires only kept handlers)
- [x] No references to deleted symbols anywhere in the repo (grep = 0 in package; repo-wide audit shows only intended sites, all handled)
- [x] Zero behavior change: handler List still calls ListFiltered with identical parameters (typo introduced during rewrite — wrong query param for SourceProjectID — caught and fixed before any verification run); retired endpoints already returned 403 stubs and were not routed
- [x] Formatting matches repo convention (repo files are CRLF throughout; gofmt -l flags every file in the repo including untouched ones — pre-existing)
- [x] No commit made (controller commits)

## Concerns

1. **Tests executed only for memory_usecase package** (the package under change). Full `go test ./...` cannot run on this Windows machine for unrelated packages (pre-existing syscall issue); Linux-target compilation of the entire repo passes. If the controller wants full test execution, it needs a Linux runner/CI.
2. **Retained Service read paths have no tests** (pre-existing gap, see D2). If coverage is desired, it should be a separate task — writing new tests exceeds this deletion-only scope.
3. The plan's expected "build exit 0 on Windows" (RED and GREEN) does not hold for the full repo on this machine due to the pre-existing platform issue; GOOS=linux equivalents were used and pass. Recommend the controller note this in the change artifacts if task 2/3 reuse the same verification commands.
