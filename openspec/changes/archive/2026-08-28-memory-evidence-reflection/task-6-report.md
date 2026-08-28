# Task 6 Report — Gate candidates and wire writes through memory_write_jobs

> change: memory-evidence-reflection | task: 6 | complexity: 🔴 | date: 2026-08-28
> branch: refactor/memory-usecase-cleanup | status: **DONE**

## Skills Loaded

- `vsdd-workflow-implement-task`
- `test-driven-development`
- `vsdd-workflow-reverse-sync` (no trigger — Decisions 7/9/10 matched code facts throughout; `internal/bootstrap/app.go` was declared in scope but constructs no durable worker, so it needed zero changes — recorded as deviation, not a conflict)

## Environment

- Toolchain: `D:\Users\hongze01.zhang\sdk\go1.26.6\bin\go.exe` (not on PATH), Windows host, Git Bash.
- `internal/application/memory_usecase` tests run natively on Windows (no overlay needed).
- Shipping gate: `GOOS=linux go build ./...` without overlay → exit 0.
- Baseline before this task: full `memory_usecase` suite green (Task 5 final state).
- Note: Rounds 1–2 RED/GREEN ran in the pre-compaction part of this session; their evidence below is faithfully summarized. Rounds 3 and all final gates carry verbatim output captured live.

## RED evidence (tests written before any production code, three TDD rounds)

### Round 1 — quality gate (compile-level RED, gate API missing)

`candidate_gate_test.go` written first with the 6 mandated scenarios. `go test ./internal/application/memory_usecase -run 'CandidateGate' -count=1` failed at compile level on the not-yet-existing gate API (`gateExtractionCandidates`, `gateExtractionCandidate`, `candidateGateRejection`, gate constants — all `undefined`). After implementing the gate block in `durable_memory_pipeline.go`, all 6 scenarios went GREEN.

### Round 2 — write wiring (compile-level RED, option + adapter missing)

`extraction_write_wiring_test.go` written next (scenarios 7+8 plus the `fakeMemoryRowRepo` mirroring the MySQL conflict-tolerant Create). Two RED stages:

1. Test-compile error (my test typo): `fakeMemoryRowRepo does not implement memory.Repository (wrong type for method UpdateDecayedImportance)` — missing `ownerID int64` parameter. Fixed the fake's signature per TDD's "fix test errors until the test fails correctly".
2. Clean feature-level RED: `undefined: WithExtractionWrites` (option and `ExtractionWriteEnqueuer` did not exist yet), plus the missing `extraction:<job-id>:<index>` enqueue behavior.

After implementing the option/interface in `durable_memory_pipeline.go` and the new `extraction_write_adapter.go`, scenarios 7+8 went GREEN. Post-GREEN, three Task 5 tests (`shouldParseStructuredCandidatesFromModel`, `shouldPersistChunkCandidatesIncrementallyAndSkipOnRetry`, `shouldDiscardPartialChunksWhenWindowShrinksBetweenAttempts`) failed with `handle: durable memory extraction write pipeline is not configured` — completing with passing candidates now requires the enqueuer. Fixed by wiring a no-op write pipeline into the shared `newCandidateExtractionWorker` test helper (legitimate behavior change; per TDD "other tests fail → fix now"). Full suite green afterwards.

### Round 3 — dedup key strategy + consolidate-side Decision 10 sites (behavioral RED, verbatim)

`go test ./internal/application/memory_usecase -run 'ExtractionWriteWiring|NoModelConsolidate|DedupPolicyScope' -count=1`:

```text
--- FAIL: TestExtractionWriteWiring (0.00s)
    --- FAIL: TestExtractionWriteWiring/shouldDeduplicateIdenticalContentAcrossJobs (0.00s)
        extraction_write_wiring_test.go:323: memory rows = 2, want exactly one after cross-job dedup
--- FAIL: TestNoModelConsolidate (0.00s)
    --- FAIL: TestNoModelConsolidate/shouldFailWithoutModelDump (0.00s)
        no_model_consolidate_test.go:56: handle error = <nil>, want the missing-model consolidation failure
    --- FAIL: TestNoModelConsolidate/shouldFailOnEmptySummaryInsteadOfFallback (0.00s)
        no_model_consolidate_test.go:114: handle error = <nil>, want the empty-summary consolidation failure
FAIL	agentcanvas/internal/application/memory_usecase	1.420s
FAIL
```

Exactly the two gaps Task 6 closes: SQLMemoryWriter still deduped extraction rows by the job idempotency key (2 rows instead of 1), and Consolidate still returned the `summarizeDurableText` fallbacks (nil error instead of failing). First run also surfaced one test-setup error in `TestDedupPolicyScope` (`json: error calling MarshalJSON … unexpected end of JSON input` — the proposal request's empty `Evidence` string is invalid JSON for the adapter's metadata marshal); fixed by supplying valid evidence JSON in the test. The two wiring/regression locks — `shouldFailWithoutModelInsteadOfDumpingText` (extract-side no-model error delivered by Task 5) and `shouldNotAlterNonExtractionSources` (existing default-key semantics) — passed as locks by design.

## GREEN evidence

DoD command `go test ./internal/application/memory_usecase -run 'CandidateGate|ExtractionWriteWiring|NoModelConsolidate|DedupPolicyScope' -count=1 -v` — all 12 mandated scenario names verbatim (plus 1 companion):

```text
=== RUN   TestCandidateGate
--- PASS: TestCandidateGate (0.00s)
    --- PASS: TestCandidateGate/shouldAcceptFullyValidCandidate (0.00s)
    --- PASS: TestCandidateGate/shouldRejectBelowConfidenceOrImportance (0.00s)
    --- PASS: TestCandidateGate/shouldAcceptExactThresholdValues (0.00s)
    --- PASS: TestCandidateGate/shouldRejectBlankFieldsOrMissingEvidence (0.00s)
    --- PASS: TestCandidateGate/shouldRejectNonFiniteOrOutOfRangeScores (0.00s)
    --- PASS: TestCandidateGate/shouldRejectContentEmptyAfterRedaction (0.00s)
=== RUN   TestDedupPolicyScope
--- PASS: TestDedupPolicyScope (0.00s)
    --- PASS: TestDedupPolicyScope/shouldNotAlterNonExtractionSources (0.00s)
=== RUN   TestExtractionWriteWiring
--- PASS: TestExtractionWriteWiring (0.00s)
    --- PASS: TestExtractionWriteWiring/shouldEnqueueGatedCandidatesWithExtractionKeys (0.00s)
    --- PASS: TestExtractionWriteWiring/shouldCompleteNoOutputWithoutWrites (0.00s)
    --- PASS: TestExtractionWriteWiring/shouldDeduplicateIdenticalContentAcrossJobs (0.00s)
    --- PASS: TestExtractionWriteWiring/shouldFailWithoutModelInsteadOfDumpingText (0.00s)
=== RUN   TestNoModelConsolidate
--- PASS: TestNoModelConsolidate (0.00s)
    --- PASS: TestNoModelConsolidate/shouldFailWithoutModelDump (0.00s)
    --- PASS: TestNoModelConsolidate/shouldFailOnEmptySummaryInsteadOfFallback (0.00s)
PASS
ok  	agentcanvas/internal/application/memory_usecase	1.552s
```

Regression: full package suite `go test ./internal/application/memory_usecase -count=1` → `ok` (all pre-existing tests green, including the Task 5 chunking/extraction/window suites and the legacy result_json regression tests). `GOOS=linux go vet ./internal/application/memory_usecase ./cmd/worker ./internal/bootstrap` → exit 0. (Host-platform `go vet` reports pre-existing `syscall.Flock` undefined errors in the untouched `internal/runtime/toolruntime` — POSIX-only code on a Windows host, unrelated to this task.)

## DoD gates

- All 12 mandated scenarios green (command above) ✅
- `GOOS=linux go build ./...` — exit 0, no overlay (re-run after the final gofmt pass) ✅
- `grep -rn "summarizeDurableText" internal/` — EXACTLY 2 lines: the kept projection call `consolidation_projection.go:157` and the definition `durable_memory_pipeline.go:1066` ✅
- `GOOS=linux go vet` clean on all touched packages ✅
- Formatting discipline: `go fmt` applied ONLY to the 9 files this task created/modified (see REFACTOR); no repo-wide formatter run ✅

## ASSERT items

1. **Scenario 9 ON CONFLICT semantics**: `fakeMemoryRowRepo.Create` mirrors the MySQL repository exactly — `ON CONFLICT (owner_id, deduplication_key) DO NOTHING` + re-read of the surviving row (verified against `internal/infrastructure/mysql/memory_repo.go:34-47`), with a `conflictHits` counter. The scenario asserts 2 write jobs (`extraction:42:0`, `extraction:43:0`) but exactly 1 memory row, 1 conflict hit, and the surviving row's key equals `hex(sha256(memory_type + "\n" + normalize(content)))` independently recomputed in the test (`^[0-9a-f]{64}$`).
2. **Gate reusability for Task 7 without implementing merge**: `gateCandidates()` (pipeline :524) returns the Merge slot when filled, else flattens chunks in ascending index order; `gateExtractionCandidates` is a pure function Task 7's merge pass can re-gate through. Merge is NOT implemented — the slot stays empty in Phase 1.
3. **Backoff channels not mixed**: scenario 10 asserts the extract-side failure stays in the LINEAR channel — `DueAt ≈ before + (AttemptCount+1)·minute` (tolerance −30s/+90s), `Phase2AttemptCount == 0`, no `phase2:` prefix in ErrorMessage. Scenario 11 asserts the consolidate-side failure rides the phase2 EXPONENTIAL channel — `DueAt ≈ before + durablePhase2RetryDelay(1)` (tolerance −15s/+45s, deliberately narrower than the linear window so a mixed-up channel fails), `Phase2AttemptCount == 1`, ErrorMessage prefixed `phase2:`. Phase-1 status stays `completed` on both consolidate failures.
4. **Non-extraction dedup keys locked verbatim**: scenario 12 enqueues through the real production adapters — `ad_hoc:42`, `reflection:run:9`, `proposal:11` — and asserts each memory row's `DeduplicationKey` equals the job idempotency key verbatim, per source. Consolidation then runs through the same worker and writes 2 artifact rows with zero new write jobs and zero new memory rows (`rows.createCalls()` unchanged) — consolidation never touches SQLMemoryWriter.
5. **Exactly-once enqueue** (corrected by the MF-1 Fix round below): the enqueue call sits INSIDE the Phase-1 finalization branch of Handle (pipeline :415), so phase2 retries never re-enqueue; the terminal outcome is persisted ONLY after a successful enqueue (:418-427), so an enqueue failure keeps the stored result resumable (chunks present, empty outcome) and the retry re-enters the block, skips completed chunks with zero model calls, re-gates and re-enqueues. The `(owner_id, idempotency_key)` unique key short-circuit (mirrored in `fakeWriteJobRepo` and in MySQL `MemoryWriteJobRepository.Create`) makes the re-enqueue exactly-once — locked by `shouldReenqueueCandidatesAfterTransientEnqueueFailure`.

## Files changed

| File | Action |
|---|---|
| `internal/application/memory_usecase/durable_memory_pipeline.go` | MODIFIED (+~160): `ExtractionWriteEnqueuer` interface + `WithExtractionWrites` option + `writes` field; Handle gates at finalization (`finalizeExtractionOutcome`, :398) and enqueues accepted candidates (:408); `durableExtractionResult.Rejections` (additive, `rejections,omitempty`); `gateCandidates()` merge-aware stream selector; deterministic gate block (:702-788) — constants, `candidateGateRejection`, `gateExtractionCandidates`/`gateExtractionCandidate`/`gateExtractionScore`, `hasEvidenceRef`, redaction-placeholder regex + `redactedCandidateContent`; Decision 10 — Consolidate no-model fallback (:941→:942) and empty-summary fallback (:958→:959) now return errors |
| `internal/application/memory_usecase/extraction_write_adapter.go` | NEW — `extractionWriteSource`, `extractionWriteKey` (`extraction:<job-id>:<index>`), `extractionWriteRequest` (archival, conversation-scoped, redacted title/content, provenance metadata), `enqueueExtractionWrites` (nil-enqueuer guard errors, 0 candidates no-op) |
| `internal/application/memory_usecase/memory_write_pipeline.go` | MODIFIED (+29/−4): SQLMemoryWriter computes the content-hash DeduplicationKey ONLY for `source=extraction` (:278); all other sources keep the default job-idempotency-key guard (:285); new `extractionContentDeduplicationKey` (:305) = hex(sha256(memory_type + "\n" + whitespace-normalized content)); imports `crypto/sha256`, `encoding/hex` |
| `cmd/worker/main.go` | MODIFIED: `writeJobPipeline` construction moved above the durable worker; `memoryusecase.WithExtractionWrites(writeJobPipeline)` injected (:242) |
| `internal/application/memory_usecase/candidate_gate_test.go` | NEW — `TestCandidateGate` (6 mandated scenarios) |
| `internal/application/memory_usecase/extraction_write_wiring_test.go` | NEW — `TestExtractionWriteWiring` (4 mandated scenarios), `fakeMemoryRowRepo` (conflict-tolerant memory.Repository mirror with counters), pipeline/worker helpers |
| `internal/application/memory_usecase/no_model_consolidate_test.go` | NEW — `TestNoModelConsolidate/shouldFailWithoutModelDump` (mandated) + companion `shouldFailOnEmptySummaryInsteadOfFallback` |
| `internal/application/memory_usecase/dedup_policy_scope_test.go` | NEW — `TestDedupPolicyScope/shouldNotAlterNonExtractionSources` (mandated) |
| `internal/application/memory_usecase/candidate_extraction_test.go` | MODIFIED (helper only): `newCandidateExtractionWorker` wires a no-op write pipeline so Task 5 completion paths satisfy the new enqueuer requirement |

`internal/bootstrap/app.go`: zero diff (see Deviations). All other files, including every Task 2–5 deliverable: zero diff.

## Implementation notes

### Quality gates (design Decision 7)

- Gate order per candidate: finite/range scores first (`gateExtractionScore` rejects NaN/±Inf/<0/>1 with `"<name> is not a finite score in [0,1]"`), then `confidence >= 0.7` (>= semantics — exact 0.7 accepted), `importance >= 0.5` (exact 0.5 accepted), blank title, blank content, missing evidence refs (at least one non-blank ref required), and finally content-empty-after-redaction.
- Redaction-empty check: `redactDurableSecrets` never turns non-empty input into empty output (it inserts placeholders), so the gate strips the placeholder set (`[REDACTED]`, `[REDACTED PRIVATE KEY]`) via `durableRedactionPlaceholder` and trims; a candidate whose content is entirely secret material (e.g. `-----BEGIN RSA PRIVATE KEY-----`) leaves nothing durable and is rejected.
- Every rejection is recorded in `result_json` as `{index, title, reason}` (`rejections,omitempty`) so gate decisions stay observable; index is the candidate's position in the gated stream.
- All candidates dropped → outcome rewritten to `no_output`, job still completes `completed`, and NO write job is enqueued (scenario 8).

### Write wiring (design Decision 9)

- Envelope: one write job per accepted candidate, `source=extraction`, idempotency key `extraction:<job-id>:<index>` (index = position in the job's gated stream, stable across retries). Payload: owner + conversation scope, `SourceConversationID` set for provenance, `memory_type=archival`, title/content passed through `redactDurableSecrets` again as defense in depth, importance from the candidate, and `{candidate_type, confidence, evidence_refs}` in metadata_json.
- Dedup split of responsibilities: the idempotency key makes the WRITE JOB exactly-once; the memory ROW's DeduplicationKey is computed by SQLMemoryWriter from content for `source=extraction` only — `hex(sha256(memory_type + "\n" + normalize(content)))`, normalize = `strings.Fields` + single-space join. Overlapping windows across consecutive jobs re-extracting the same lesson collapse onto one `(owner_id, deduplication_key)` slot via the existing conflict-tolerant Create (DoNothing + re-read). 64-hex fits the varchar(191) column (schema verified at migrations/000001:757,767 — no migration needed).
- All non-extraction sources keep the historical default: DeduplicationKey = job idempotency key verbatim (scenario 12 locks this for ad_hoc/reflection/proposal, and consolidation stays on the artifact projection).

### Decision 10 — consolidate side

- Both fallback sites removed: no model / blank model config → `errors.New("durable memory consolidation requires a configured model")` (model check trimmed, matching the extract-side style); empty model summary → `errors.New("durable memory consolidation returned an empty summary")`. The errors flow through Handle's phase2 path: Phase-1 status stays `completed`, `Phase2AttemptCount` increments, ErrorMessage gets the `phase2:` prefix, DueAt uses the exponential channel. No artifact is written on either failure.
- Kept deliberately: `if result.Memory == "" { result.Memory = raw }` (not in Decision 10's removal table) and the projection's summary derivation at `consolidation_projection.go:157`. Grep count is now exactly 2 (call + definition).

### Injection

- `cmd/worker/main.go`: `writeJobPipeline` is now constructed before `durableMemoryWorker` and injected via `WithExtractionWrites` — the same pipeline instance the write-job worker drains, so extraction writes and the worker loop share one queue.

## Self-review checklist

- [x] Failing tests first in all three rounds; RED evidence recorded (compile-level rounds 1–2, behavioral round 3 verbatim)
- [x] All 12 scenario names preserved verbatim as subtests; all green
- [x] Gate thresholds >= semantics with exact-threshold acceptance scenario; rejections recorded with reasons
- [x] Merge slot consumed by `gateCandidates()` when filled (Task 7 ready) but merge NOT implemented
- [x] Enqueue exactly-once: inside Phase-1 finalization branch; retry re-enqueue short-circuits on the unique key
- [x] Scenario 9 fake mirrors MySQL ON CONFLICT (owner_id, deduplication_key) DoNothing + re-read; surviving row key independently recomputed
- [x] Non-extraction sources' dedup keys locked verbatim; consolidation proven to bypass SQLMemoryWriter
- [x] Backoff channels unmixed and proven by non-overlapping DueAt tolerance windows (linear vs phase2 exponential)
- [x] Decision 10 consolidate sites removed; grep count exactly 2; `GOOS=linux go build ./...` exit 0
- [x] Scope respected: only declared files + new tests; `bootstrap/app.go` zero diff (justified); no commit/push; `.vsdd-state.yaml` untouched
- [x] Formatting discipline: `go fmt` only on the 9 touched files; no repo-wide run

## REFACTOR notes

1. `go fmt` normalized 3 of the 9 touched files (`durable_memory_pipeline.go`, `extraction_write_wiring_test.go`, `cmd/worker/main.go`) after GREEN; full suite, linux build and grep gate re-verified afterwards.
2. Test-comment rewording: the first version of `no_model_consolidate_test.go` named `summarizeDurableText` literally in two comments, which inflated the DoD grep to 4 lines. Comments reworded ("truncated-text-dump fallback sites", "truncation fallback") so `grep -rn "summarizeDurableText" internal/` returns exactly the 2 production lines.
3. Round-3 RED test fixes (per TDD "fix test errors until the test fails correctly"): proposal request in scenario 12 needed valid `Evidence` JSON (the adapter marshals it verbatim); the round-2 fake repo's `UpdateDecayedImportance` signature was corrected to the real interface shape.
4. Round-2 post-GREEN repair: three Task 5 tests broke on the new "write pipeline is not configured" guard; the shared `newCandidateExtractionWorker` helper now wires a no-op pipeline (the only behavioral change outside Task 6's own tests).

## Fix round — reviewer finding MF-1 closed inside Task 6

Code-quality review returned FAIL with one Must Fix, confirmed real against the code and fixed in this round under strict TDD (RED first). Scope stayed inside `durable_memory_pipeline.go` + `extraction_write_wiring_test.go`.

### MF-1 — enqueue failure silently lost all gated candidates on retry

**Problem (confirmed sequence, pre-fix lines)**: `finalizeExtractionOutcome` set the terminal outcome (:398) → `job.ResultJSON` marshaled WITH the terminal outcome (:400) → `enqueueExtractionWrites` failed (:408) → Handle returned the error → the deferred handler persisted the job as pending INCLUDING the terminal result_json → on retry the re-entry condition (`len(ResultJSON)==0 || (resumable && result.Outcome=="")`) was FALSE because the outcome was set → the whole gate+enqueue block was skipped → the job completed with ZERO write jobs. Candidates permanently lost (`previousBoundary` guarantees no later job re-extracts the window); a partial enqueue failure lost candidates k..n-1. This contradicted the pre-fix comment at :404-407 and ASSERT #5 above.

**Fix** (required direction adopted as-is):

1. `finalizeExtractionOutcome` split into `gateExtractionResult` (:550): gates the merged-or-single-chunk stream and records rejections but does NOT touch the outcome. The terminal outcome is set and persisted only after the enqueue succeeds.
2. `extractChunks` no longer sets `outcome=extracted` in memory after the last chunk (old :617 removed): per-chunk persistence already happened with an empty outcome, and the terminal marker is now Handle's to persist post-enqueue. The empty-window `no_output` branch inside `extractChunks` is unchanged (nothing to enqueue).
3. Handle restructured (:395-427): gate only when the outcome is still empty (extract path); marshal result_json with the EMPTY outcome; enqueue; on success set `extracted` (or `no_output` when the gate dropped everything) and re-marshal. The shadow-window branch (:371) keeps setting `no_output` directly — nothing to enqueue, `enqueueExtractionWrites` with zero candidates is a successful no-op.

**RED evidence** (verbatim) — the new assertion bites the exact bug:

```text
=== RUN   TestExtractionWriteWiring/shouldReenqueueCandidatesAfterTransientEnqueueFailure
    extraction_write_wiring_test.go:434: failed enqueue persisted terminal outcome "extracted", want empty so the retry re-enters gate+enqueue
--- FAIL: TestExtractionWriteWiring (0.00s)
    --- FAIL: TestExtractionWriteWiring/shouldReenqueueCandidatesAfterTransientEnqueueFailure (0.00s)
    --- PASS: TestExtractionWriteWiring/shouldKeepNoOutputOutcomeForShadowWindow (0.00s)
FAIL	agentcanvas/internal/application/memory_usecase	1.132s
```

The guard scenario passed immediately (the shadow-window branch was never broken — invariant 2 locked). The first scenario uses the fake write-job repo's existing `failCreate` hook set before pass 1 and cleared before the retry (transient failure, exactly "failCreate on the first write-job Create").

**GREEN evidence** (verbatim):

```text
--- PASS: TestExtractionWriteWiring (0.00s)
    --- PASS: TestExtractionWriteWiring/shouldEnqueueGatedCandidatesWithExtractionKeys (0.00s)
    --- PASS: TestExtractionWriteWiring/shouldCompleteNoOutputWithoutWrites (0.00s)
    --- PASS: TestExtractionWriteWiring/shouldDeduplicateIdenticalContentAcrossJobs (0.00s)
    --- PASS: TestExtractionWriteWiring/shouldFailWithoutModelInsteadOfDumpingText (0.00s)
    --- PASS: TestExtractionWriteWiring/shouldReenqueueCandidatesAfterTransientEnqueueFailure (0.00s)
    --- PASS: TestExtractionWriteWiring/shouldKeepNoOutputOutcomeForShadowWindow (0.00s)
PASS
ok  	agentcanvas/internal/application/memory_usecase	1.485s
```

Pass-1 assertions that would have caught the bug: pending status, AttemptCount 1, linear DueAt ((attempt+1) min, −30s/+90s window), result_json carries chunk 0 with both candidates and an EMPTY outcome, zero write jobs. Retry assertions: 1 total extraction-model call (the completed chunk skipped), write jobs == candidate count with keys `extraction:55:0`/`extraction:55:1`, no duplicates, completed status, final outcome `extracted`.

**Final control-flow ordering** (pipeline, post-fix):

1. :392 `extractChunks` — per-chunk model calls with incremental persistence; every persisted partial carries chunks + window markers with EMPTY outcome; no terminal marker set inside.
2. :395-402 gate (`gateExtractionResult`, pure) when outcome still empty; rejections recorded.
3. :404 marshal result_json — extract path: chunks + rejections + markers, outcome EMPTY.
4. :415 `enqueueExtractionWrites` — on failure: return error; deferred persists pending with the empty-outcome result; retry re-enters (:368 true), markers validate, completed chunks skipped with 0 model calls, re-gate (idempotent), re-enqueue — the `(owner_id, idempotency_key)` unique key + ON CONFLICT DO NOTHING keeps it exactly-once.
5. :418-427 only after enqueue success: outcome set (`extracted` / `no_output`) and result_json re-marshaled terminal.
6. :433 terminal `updateJob` — if THIS fails after a successful enqueue, the deferred persist carries the terminal result_json; the retry decodes outcome != "" → :368 false → block skipped → completed with NO double enqueue (invariant 4 verified by trace; enqueue happens strictly before the terminal outcome exists anywhere).

Shadow-window branch (:370-371) and empty-evidence branch (`extractChunks` :589-592) set `no_output` directly with zero candidates — the enqueue step is their successful no-op and their persisted outcome is harmless (nothing can be lost).

### Fix-round gates

- `go test ./internal/application/memory_usecase -count=1` → `ok` (full suite, including all Task 5 suites — `shouldPersistChunkCandidatesIncrementallyAndSkipOnRetry` still pins partial-persist-with-empty-outcome and final outcome `extracted`, now set post-enqueue)
- `GOOS=linux go build ./...` → exit 0
- `GOOS=linux go vet ./internal/application/memory_usecase` → clean
- `grep -rn "summarizeDurableText" internal/` still exactly 2 lines
- gofmt: the two touched files already gofmt-clean (`go fmt` reported nothing to change); no repo-wide run

### Fix-round files touched

| File | Action |
|---|---|
| `internal/application/memory_usecase/durable_memory_pipeline.go` | `finalizeExtractionOutcome` → `gateExtractionResult` (outcome untouched); `extractChunks` no longer sets `extracted`; Handle gates with empty outcome, marshals, enqueues, then sets+marshals the terminal outcome; comment at :408-414 rewritten — its exactly-once claim is now true |
| `internal/application/memory_usecase/extraction_write_wiring_test.go` | NEW subtests `shouldReenqueueCandidatesAfterTransientEnqueueFailure` (bug-catcher) and `shouldKeepNoOutputOutcomeForShadowWindow` (invariant-2 guard); imports `errors`, `domain` |

## Deviations

1. **Naming shape** (same convention as Tasks 1–5): tasks.md writes `CandidateGateTest#should…` etc.; Go scenarios are subtests of `TestCandidateGate` / `TestExtractionWriteWiring` / `TestNoModelConsolidate` / `TestDedupPolicyScope` with the scenario names verbatim.
2. **`internal/bootstrap/app.go` unchanged**: declared in the task scope for injection, but it constructs no `DurableMemoryWorker` (only `NewDurableMemoryTrigger` and the ad-hoc pipeline) — the worker is constructed solely in `cmd/worker/main.go`, where the injection was made. No change was needed or made.
3. **Scenarios 10 and 12 passed without new production code** at their RED run: scenario 10 locks the extract-side no-model error delivered by Task 5; scenario 12 locks the pre-existing default-key semantics for non-extraction sources. Both were mandated as wiring/regression locks; the round-3 behavioral RED came from scenarios 9 and 11 (+companion).
4. **Extraction branch overrides any request-provided DeduplicationKey** (`SQLMemoryWriter.Write`): for `source=extraction` the writer unconditionally computes the content hash. The extraction adapter never supplies a key, and Decision 9 assigns content-hash computation to the writer for this source.
5. **Extraction rows are archival + conversation-scoped** with `candidate_type`/`confidence`/`evidence_refs` carried in metadata_json: no candidate-type→memory-type mapping exists in the artifacts, so provenance follows the existing adapters' metadata style (AdHoc precedent).

## Reverse Sync

None required. Design Decisions 7/9/10 matched code facts throughout (threshold semantics, key formats, ON CONFLICT behavior, phase2 channel all verified in code before implementation). Forward notes: Task 7 may fill the `merge` slot — `gateCandidates()` already prefers it and the gate is a reusable pure function; the `rejections` result_json key is additive and pre-gate payloads omit it.
