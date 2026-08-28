# Task 7 Report — Merge multi-chunk candidates and verify consolidation inputs

> change: memory-evidence-reflection | task: 7 | complexity: 🟡 | date: 2026-08-28
> branch: refactor/memory-usecase-cleanup | status: **DONE**

## Skills Loaded

- `vsdd-workflow-implement-task`
- `test-driven-development`
- `vsdd-workflow-reverse-sync` (no trigger — Decisions 6/7/8 matched code facts throughout; the merge pass slots into the exact seam Task 6 reserved in `gateCandidates()` and the Task-6 MF-1 ordering, no artifact conflict found)

## Environment

- Toolchain: `D:\Users\hongze01.zhang\sdk\go1.26.6\bin\go.exe` (not on PATH), Windows host, Git Bash.
- `internal/application/memory_usecase` tests run natively on Windows (no overlay needed).
- Shipping gate: `GOOS=linux go build ./...` without overlay → exit 0.
- Baseline before this task: full `memory_usecase` suite green (Task 6 final state, `ok 1.524s`).

## RED evidence (tests written before any production code)

`merge_pass_test.go` (TestMergePass: 4 mandated scenarios + 2 companions) and `consolidation_input_test.go` (TestConsolidationInput: 2 mandated scenarios) written first; zero production lines touched. `go test ./internal/application/memory_usecase -run 'MergePass|ConsolidationInput' -count=1 -v` (verbatim):

```text
=== RUN   TestConsolidationInput
=== RUN   TestConsolidationInput/shouldIncludeExtractionSourceMemories
=== RUN   TestConsolidationInput/shouldNotEnqueueConsolidationOnNoOutput
    consolidation_input_test.go:101: handle: durable memory phase2 is already running
--- FAIL: TestConsolidationInput (0.00s)
    --- PASS: TestConsolidationInput/shouldIncludeExtractionSourceMemories (0.00s)
    --- FAIL: TestConsolidationInput/shouldNotEnqueueConsolidationOnNoOutput (0.00s)
=== RUN   TestMergePass
=== RUN   TestMergePass/shouldMergeCandidatesAcrossChunksOnce
    merge_pass_test.go:137: model calls = 3, want 3 per-chunk extractions + exactly one merge
=== RUN   TestMergePass/shouldSkipMergeForSingleChunk
=== RUN   TestMergePass/shouldKeepChunkCandidatesOnMergeFailure
    merge_pass_test.go:270: handle error = <nil>, want the merge failure
=== RUN   TestMergePass/shouldReGateMergedOutput
    merge_pass_test.go:364: model calls = 2, want 2 extractions + 1 merge
=== RUN   TestMergePass/shouldGateEmptyMergeOutputAsNoOutput
    merge_pass_test.go:409: handle: durable memory phase2 is already running
=== RUN   TestMergePass/shouldNotReRunMergeAfterEnqueueFailure
    merge_pass_test.go:455: pass 1 model calls = 2, want 2 extractions + 1 merge
--- FAIL: TestMergePass (0.31s)
    --- FAIL: TestMergePass/shouldMergeCandidatesAcrossChunksOnce (0.08s)
    --- PASS: TestMergePass/shouldSkipMergeForSingleChunk (0.00s)
    --- FAIL: TestMergePass/shouldKeepChunkCandidatesOnMergeFailure (0.08s)
    --- FAIL: TestMergePass/shouldReGateMergedOutput (0.05s)
    --- FAIL: TestMergePass/shouldGateEmptyMergeOutputAsNoOutput (0.06s)
    --- FAIL: TestMergePass/shouldNotReRunMergeAfterEnqueueFailure (0.03s)
FAIL	agentcanvas/internal/application/memory_usecase	1.750s
FAIL
```

Behavioral RED: 4 of the 6 mandated scenarios (`shouldMergeCandidatesAcrossChunksOnce`, `shouldKeepChunkCandidatesOnMergeFailure`, `shouldReGateMergedOutput`, `shouldNotEnqueueConsolidationOnNoOutput`) plus both companions failed against the missing merge pass / missing no_output consolidation gate. Two mandated scenarios passed immediately and are kept as regression locks (same precedent as Task 6 deviation #3): `shouldSkipMergeForSingleChunk` locks Task 6's `gateCandidates()` chunk-flatten fallback (single chunk never needed a merge), and `shouldIncludeExtractionSourceMemories` locks the extraction-source mapping already delivered with the consolidation input wiring. No test panicked at RED: every indexed-prompt assertion is guarded by a call-count assertion that fails first.

## GREEN evidence

DoD command `go test ./internal/application/memory_usecase -run 'MergePass|ConsolidationInput' -count=1 -v` (verbatim, post-REFACTOR):

```text
=== RUN   TestConsolidationInput
--- PASS: TestConsolidationInput (0.00s)
    --- PASS: TestConsolidationInput/shouldIncludeExtractionSourceMemories (0.00s)
    --- PASS: TestConsolidationInput/shouldNotEnqueueConsolidationOnNoOutput (0.00s)
=== RUN   TestMergePass
--- PASS: TestMergePass (0.50s)
    --- PASS: TestMergePass/shouldMergeCandidatesAcrossChunksOnce (0.13s)
    --- PASS: TestMergePass/shouldSkipMergeForSingleChunk (0.00s)
    --- PASS: TestMergePass/shouldKeepChunkCandidatesOnMergeFailure (0.16s)
    --- PASS: TestMergePass/shouldReGateMergedOutput (0.08s)
    --- PASS: TestMergePass/shouldGateEmptyMergeOutputAsNoOutput (0.05s)
    --- PASS: TestMergePass/shouldNotReRunMergeAfterEnqueueFailure (0.08s)
PASS
ok  	agentcanvas/internal/application/memory_usecase	1.933s
```

All 6 mandated scenario names green verbatim, plus 2 companions.

## DoD gates

- `go test ./internal/application/memory_usecase -run 'MergePass|ConsolidationInput' -count=1` → `ok 1.874s` ✅
- Full package regression `go test ./internal/application/memory_usecase -count=1` → `ok 2.005s` (all Task 2–6 suites green, including `shouldPersistChunkCandidatesIncrementallyAndSkipOnRetry` updated for the merge call and both `NoModelConsolidate` scenarios) ✅
- `GOOS=linux go build ./...` → exit 0, no overlay ✅
- `go vet ./internal/application/memory_usecase` → clean ✅
- `grep -rn "summarizeDurableText" internal/ | grep -v _test` → still EXACTLY 2 lines (definition `durable_memory_pipeline.go:1183`, projection call `consolidation_projection.go:157`); the merge pass introduced no new fallback site ✅
- Formatting discipline: `go fmt` applied ONLY to the 5 files this task touched (see REFACTOR); no repo-wide formatter run ✅

## ASSERT items

1. **Merge uses the SAME model config as extraction — no new config item.** `mergeChunkCandidates` calls `w.chatClient.Chat(ctx, w.cfg.Provider, llm.ChatRequest{Model: w.cfg.Model, ...})` — byte-for-byte the same seam and config fields `extractChunkCandidates` uses. No field was added to `DurableMemoryConfig`. Scenario 1 records every request's provider config AND model through a scripted fake (`scriptedMergeChatClient`) and asserts all 4 calls (3 extractions + 1 merge) equal `mergeTestProvider` / `"test-extraction-model"`.
2. **Merge-failure retry stays on the Phase-1 LINEAR channel and preserves Phase-1 semantics.** Scenario 3 asserts after the failed merge: status `pending`, `AttemptCount == 1`, `DueAt ≈ before + (AttemptCount+1)·minute` (tolerance −30s/+90s, the linear window), `Phase2AttemptCount == 0`, and ErrorMessage WITHOUT a `phase2:` prefix. The merge error returns as-is from Handle, so the existing deferred pending-persist does the bookkeeping — no new retry path was introduced, and it is explicitly distinguished from the `phase2:` exponential channel. The retry then makes ZERO extraction calls and re-sends ONLY the merge (total calls 5 = 3 extractions + failed merge + merge).

## Files changed

| File | Action |
|---|---|
| `internal/application/memory_usecase/durable_memory_pipeline.go` | MODIFIED (+119/−10): merge pass — `durableMergeSummaryRunes` (:674), `mergeExtractionChunks` (:687, guard + merge + immediate persist of the merge slot), `mergeChunkCandidates` (:708, same chat seam/model), `buildMergePrompt` (:717), `mergeChunkSummary` (:741, redacted whitespace-collapsed rune-bounded digest); Handle calls the merge between extraction and gating (:395); `gateCandidates()` changed `len(r.Merge) > 0` → `r.Merge != nil` (:559) so an empty merge verdict is honored; consolidate gated on `result.Outcome != durableExtractionOutcomeNoOutput` (:447) |
| `internal/application/memory_usecase/merge_pass_test.go` | NEW — `TestMergePass`: 4 mandated scenarios + companions `shouldGateEmptyMergeOutputAsNoOutput`, `shouldNotReRunMergeAfterEnqueueFailure`; `scriptedMergeChatClient` (records requests AND provider configs), `newMergePassWorker`, `mergeSecretMessageRow` |
| `internal/application/memory_usecase/consolidation_input_test.go` | NEW — `TestConsolidationInput`: both mandated scenarios; `countingConsolidationSourceReader` (call counter around the fake source repo) |
| `internal/application/memory_usecase/candidate_extraction_test.go` | MODIFIED (updated test): `shouldPersistChunkCandidatesIncrementallyAndSkipOnRetry` total call count 3 → 4 with an added merge-prompt assertion (the retry completes both chunks, so the merge pass now runs once) |
| `internal/application/memory_usecase/no_model_consolidate_test.go` | MODIFIED (updated test): `shouldFailOnEmptySummaryInsteadOfFallback` extraction call now returns one gate-accepted candidate (was `{"candidates":[]}`); under the new gate a no_output job never reaches consolidation, so the empty-summary failure could no longer be exercised otherwise. Write-job assertion updated 0 → 1 (the accepted candidate is enqueued before consolidation fails) |

`consolidation_projection.go`: zero diff — `gatherConsolidationInputs` already lived in `durable_memory_pipeline.go` and needed only verification, not change. All other files, including every Task 2–6 deliverable: zero diff.

## Implementation notes

### Merge pass (design Decision 6)

- `mergeExtractionChunks` guard: `result.Outcome != "" || len(chunks) <= 1 || result.Merge != nil` → no-op. So: terminal/shadow-window outcomes skip it; single-chunk jobs skip it (candidates flow straight to the gate via the chunk-flatten fallback); a FILLED merge slot is never re-run.
- On success the merged list lands in the `merge` slot and is persisted IMMEDIATELY (`updateJob` with the attempt's `leaseOwner`) before gating/enqueue, mirroring the per-chunk incremental persistence: an enqueue failure's retry re-enters, skips completed chunks AND the filled merge slot with zero model calls, and re-gates the stored merge output (locked by companion `shouldNotReRunMergeAfterEnqueueFailure`).
- On merge failure nothing is persisted (Merge stays nil), the per-chunk candidates stay intact in result_json, and the error propagates to the deferred pending-persist → linear Phase-1 retry re-attempts ONLY the merge (extraction chunks all present → skipped with zero calls). Locked by scenario 3.
- `buildMergePrompt`: instructions (dedupe across chunks, prefer multi-chunk support, no invented facts, empty-array verdict allowed) + `CHUNK SUMMARIES:` one `[chunk N] <digest>` line per chunk + `CANDIDATES:` one `[chunk N] <json>` line per per-chunk candidate. Candidates are marshaled verbatim (json tags match the extraction envelope), so the merge model sees exactly what each chunk produced.
- `mergeChunkSummary`: joins `evidenceChunkedText` per unit, passes through `redactDurableSecrets` (defense in depth on top of renderer redaction), collapses whitespace with `strings.Fields`, and rune-truncates to `durableMergeSummaryRunes` (1200) + "...". Scenario 1 pins: merge prompt carries each chunk's digest marker (`[msg 1]`/`[msg 3]`/`[msg 7]` — the overlap-shifted chunk heads), the cross-chunk duplicate title exactly once per source chunk (3×), no raw secret, and the `[REDACTED]` placeholder.
- Merge response parsing reuses `parseExtractionCandidates(extractDurableJSON(...))` — identical envelope semantics to per-chunk extraction, including the non-nil-empty result for `{"candidates":[]}`.

### Gate interaction (design Decision 7)

- `gateCandidates()` now keys on `r.Merge != nil`: nil means "merge never ran" (single chunk / pre-merge partial → flatten chunks); non-nil empty means "merge ran and produced nothing" — the gate honors that verdict and must NOT fall back to the per-chunk candidates behind the merge's back. Locked by companion `shouldGateEmptyMergeOutputAsNoOutput` (empty merge → zero write jobs, outcome `no_output`, and — via the held phase2 fallback lock — no consolidation attempt).
- Scenario 4 (`shouldReGateMergedOutput`): merge output containing one low-confidence candidate (0.55) is re-gated through the unchanged pure gate; only the accepted candidate is wired (`extraction:1:0`), the rejection is recorded in `rejections` with a confidence reason, and the full merge output stays preserved in the `merge` slot.

### no_output consolidation gate (narrowest correct gate)

- `consolidate(ctx, ownerID)` is owner-scoped; the gate sits at the single call site in Handle (:447): `if result.Outcome != durableExtractionOutcomeNoOutput`. A no_output job — whether from an empty extraction gate, an empty merge verdict, or the shadow-window branch — carries no new evidence into the owner's memory, so the lock, evidence gather, agent and phase2 bookkeeping all stay untouched; the next job with actual output re-triggers consolidation for the owner. Scenario 6 holds the package-level `durablePhase2FallbackLock` for the whole Handle so ANY consolidation attempt would hard-fail, and additionally asserts the counting source reader was called 0 times, `Phase2AttemptCount == 0`, `ErrorMessage == ""`, zero artifacts, zero write jobs.
- Legacy/terminal stored results decode with `Outcome == ""` — consolidation still runs for them (unchanged behavior; verified against `TestDurableHandleRetryUsesStoredStageOneResult`, `TestNoModelConsolidate/shouldFailWithoutModelDump`, `TestDurableHandleUsesOwnedUpdateAfterClearingLeaseFields`, all green).

### Verification of consolidation inputs (scenario 5)

- `worker.gatherConsolidationInputs` called directly with 2 extraction + 2 ad_hoc memory rows: returns all 4 sorted by SourceID with `extraction → rollout` and `ad_hoc → ad_hoc` kinds, correct conversation IDs (nil → 0), verbatim RawMemory and non-zero SourceAt. This behavior already matched the spec (regression lock); `consolidation_projection.go` needed no change.

## Self-review checklist

- [x] Failing tests first; RED evidence pasted verbatim; no production code before the failing tests
- [x] All 6 mandated scenario names preserved verbatim as subtests; all green (2 of them regression locks passing at RED, documented)
- [x] Merge called EXACTLY once for N>1 chunks, over ALL chunk candidates + per-chunk summaries (scenario 1: call count 4, prompt assertions)
- [x] Single-chunk jobs: zero merge calls, candidates straight to gate (scenario 2)
- [x] Merge failure: pending + linear channel, all chunk candidates intact, retry = 0 extraction calls + only merge re-sent (scenario 3, calls 4 → 5)
- [x] Merged output re-gated; rejected candidates not wired (scenario 4)
- [x] Empty merge verdict honored as no_output, no chunk-candidate fallback (companion)
- [x] Merge slot persisted pre-enqueue; enqueue-failure retry re-gates stored merge with zero model calls (companion)
- [x] gatherConsolidationInputs maps both sources with correct tags (scenario 5)
- [x] no_output job never triggers consolidation — lock, gather, artifacts, bookkeeping all provably untouched (scenario 6)
- [x] ASSERT items verified (same model config; linear-channel merge retry)
- [x] Decision 10 invariant kept: summarizeDurableText still exactly 2 grep hits; no new fallback site
- [x] Scope respected: only declared file + new/updated tests; no commit/push; `.vsdd-state.yaml` untouched
- [x] Formatting discipline: `go fmt` only on the 5 touched files

## REFACTOR notes

1. `go fmt` on the 5 touched files after GREEN reported nothing to change (already gofmt-clean); full suite, `GOOS=linux go build ./...` and `go vet` re-verified afterwards. No repo-wide formatter run.
2. Post-GREEN regression repair (TDD "other tests fail → fix now"): `TestCandidateExtraction/shouldPersistChunkCandidatesIncrementallyAndSkipOnRetry` asserted 3 total model calls for a 2-chunk job whose retry completes both chunks — the new merge pass adds exactly one call. Updated the expectation to 4 and added an assertion that call 4 is the merge prompt carrying BOTH chunks' candidates (the test's original invariant — chunk 0 not re-sent on retry — is preserved by the unchanged `chat.prompt(2)` assertion).
3. `TestNoModelConsolidate/shouldFailOnEmptySummaryInsteadOfFallback` updated for the same reason the gate exists: a `{"candidates":[]}` extraction now completes no_output and never reaches consolidation, so the test seeds one gate-accepted candidate instead (its write-job assertion moves 0 → 1 accordingly; the empty-summary failure and zero-artifact assertions are unchanged).

## Deviations

1. **Naming shape** (same convention as Tasks 1–6): tasks.md writes `MergePassTest#should…` / `ConsolidationInputTest#should…`; Go scenarios are subtests of `TestMergePass` / `TestConsolidationInput` with the scenario names verbatim.
2. **Two mandated scenarios passed immediately at RED** and serve as regression locks: `shouldSkipMergeForSingleChunk` (Task 6's gate fallback already skipped nonexistent merges) and `shouldIncludeExtractionSourceMemories` (extraction-source mapping already delivered with the consolidation input wiring). Both were kept exactly as specified; behavioral RED came from the other four scenarios + companions (Task 6 deviation #3 precedent).
3. **Two companion scenarios added** beyond the six mandated: `shouldGateEmptyMergeOutputAsNoOutput` (pins the nil-vs-empty merge-slot semantics — without it `gateCandidates` could silently fall back to chunk candidates when the merge legitimately returns nothing; this also forced the `len(r.Merge) > 0` → `r.Merge != nil` fix) and `shouldNotReRunMergeAfterEnqueueFailure` (pins pre-enqueue persistence of the merge slot — without it an enqueue-failure retry would pay for a second merge call).
4. **The shadow-window branch also skips consolidation** under the outcome gate: it sets `no_output` directly, and a shadowed window by definition contributed no new evidence. No existing test asserted consolidation after a shadow-window Handle (all such tests use empty sources), and the gate is mandated by the tasks.md Task 7 scenario `shouldNotEnqueueConsolidationOnNoOutput` ("no_output 完成的任务 → 断言不触发 Phase 2 入队").
5. **Updated two pre-existing tests** (`candidate_extraction_test.go`, `no_model_consolidate_test.go`) — both updates are direct, minimal consequences of the new behavior (merge call count; no_output gate), in scope as "updated tests in memory_usecase".

## Reverse Sync

None required. Design Decisions 6/7/8 matched code facts throughout: the `merge` result_json slot existed since Task 5's schema, `gateCandidates()` already documented the merge-first intent, the resume semantics (`chunks` + empty outcome → resumable) absorbed the merge slot without schema change, and the linear/phase2 channel split needed no new bookkeeping. Forward notes for Task 8+: the merge prompt's `CHUNK SUMMARIES`/`CANDIDATES` section markers and the `[chunk N]` line prefixes are stable, test-pinned strings; `durableMergeSummaryRunes = 1200` is the single knob for merge-prompt size.
