# Task 5 Report — Chunk evidence and extract structured candidates incrementally

> change: memory-evidence-reflection | task: 5 | complexity: 🔴 | date: 2026-08-28
> branch: refactor/memory-usecase-cleanup | status: **DONE**

## Skills Loaded

- `vsdd-workflow-implement-task`
- `test-driven-development`
- `vsdd-workflow-reverse-sync` (no trigger — no artifact/code conflict found; the optional reader interface was declared inside the pipeline following existing style, so `internal/domain/conversation/repository.go` needed zero changes)

## Environment

- Toolchain: `D:\Users\hongze01.zhang\sdk\go1.26.6\bin\go.exe` (not on PATH), Windows host, Git Bash.
- `internal/application/memory_usecase` tests run natively on Windows (no overlay needed).
- Shipping gate: `GOOS=linux go build ./...` without overlay → exit 0.
- Baseline before this task: full `memory_usecase` suite green.

## RED evidence (tests written before any production code, three TDD rounds)

### Round 1 — chunker (compile-level RED, new API missing)

`go test ./internal/application/memory_usecase -run 'EvidenceChunker' -count=1`:

```text
# agentcanvas/internal/application/memory_usecase [agentcanvas/internal/application/memory_usecase.test]
internal\application\memory_usecase\evidence_chunker_test.go:19:10: undefined: evidenceUnitBytes
internal\application\memory_usecase\evidence_chunker_test.go:27:27: undefined: EvidenceChunk
internal\application\memory_usecase\evidence_chunker_test.go:52:27: undefined: ChunkedEvidenceUnit
internal\application\memory_usecase\evidence_chunker_test.go:64:13: undefined: NewEvidenceChunker
internal\application\memory_usecase\evidence_chunker_test.go:165:14: undefined: evidenceChunkedBytes
… too many errors
FAIL	agentcanvas/internal/application/memory_usecase [build failed]
```

### Round 2 — candidate extraction (compile-level RED, new schema missing)

`go test ./internal/application/memory_usecase -run 'CandidateExtraction' -count=1`:

```text
# agentcanvas/internal/application/memory_usecase [agentcanvas/internal/application/memory_usecase.test]
internal\application\memory_usecase\candidate_extraction_test.go:103:61: undefined: durableExtractionResult
internal\application\memory_usecase\candidate_extraction_test.go:120:17: undefined: ExtractionCandidate
internal\application\memory_usecase\candidate_extraction_test.go:160:24: undefined: durableExtractionOutcomeExtracted
… too many errors
FAIL	agentcanvas/internal/application/memory_usecase [build failed]
```

### Round 3 — window wiring (behavioral RED, archive path not wired)

`go test ./internal/application/memory_usecase -run 'DurableWindowWiring' -count=1 -v`:

```text
--- FAIL: TestDurableWindowWiring (0.00s)
    --- FAIL: TestDurableWindowWiring/shouldReadArchivedRowsIntoRenderer (0.00s)
        durable_window_wiring_test.go:144: archive-inclusive reader calls = 0, want exactly one window read
    --- FAIL: TestDurableWindowWiring/shouldPreferArchiveInclusiveRangeReader (0.00s)
        durable_window_wiring_test.go:182: archive-inclusive calls = 0, want exactly one
FAIL	agentcanvas/internal/application/memory_usecase	1.538s
```

Exactly the wiring gap the task closes: the archive-inclusive reader existed on the repository (Task 2) but the worker never consulted it.

## GREEN evidence

DoD command `go test ./internal/application/memory_usecase -run 'EvidenceChunker|CandidateExtraction|DurableWindowWiring' -count=1 -v` — the 10 mandated scenarios verbatim, plus 3 strictness companions:

```text
--- PASS: TestCandidateExtraction (0.08s)
    --- PASS: TestCandidateExtraction/shouldParseStructuredCandidatesFromModel (0.00s)
    --- PASS: TestCandidateExtraction/shouldReturnRetryableErrorOnMalformedJSON (0.00s)
    --- PASS: TestCandidateExtraction/shouldPersistChunkCandidatesIncrementallyAndSkipOnRetry (0.08s)
    --- PASS: TestCandidateExtraction/shouldRejectCandidateMissingRequiredField (0.00s)
    --- PASS: TestCandidateExtraction/shouldAcceptEmptyEvidenceRefsForTheGate (0.00s)
    --- PASS: TestCandidateExtraction/shouldRejectResponseWithoutCandidatesArray (0.00s)
    --- PASS: TestCandidateExtraction/shouldRedactEvidenceBeforePrompt (0.00s)
--- PASS: TestDurableWindowWiring (0.00s)
    --- PASS: TestDurableWindowWiring/shouldReadArchivedRowsIntoRenderer (0.00s)
    --- PASS: TestDurableWindowWiring/shouldPreferArchiveInclusiveRangeReader (0.00s)
--- PASS: TestEvidenceChunker (0.00s)
    --- PASS: TestEvidenceChunker/shouldKeepSmallWindowSingleChunk (0.00s)
    --- PASS: TestEvidenceChunker/shouldSplitOnlyAtUnitBoundaries (0.00s)
    --- PASS: TestEvidenceChunker/shouldSliceOversizedOutputWithPartIndex (0.00s)
    --- PASS: TestEvidenceChunker/shouldOverlapTwoUnitsBetweenAdjacentChunks (0.00s)
PASS
ok  	agentcanvas/internal/application/memory_usecase
```

Regression: full package suite `go test ./internal/application/memory_usecase -count=1` → `ok` (all pre-existing tests green, including the two legacy `result_json`-seeded tests `TestDurableHandleUsesOwnedUpdateAfterClearingLeaseFields` and `TestDurableHandleRetryUsesStoredStageOneResult`, which pin that legacy pre-chunking payloads stay immutable/terminal). `go vet ./internal/application/memory_usecase` clean.

## DoD gates

- All 10 mandated scenarios green (command above) ✅
- `GOOS=linux go build ./...` — exit 0, no overlay ✅
- `go vet` clean on the changed package ✅
- gofmt-clean: the 4 new files verified with `gofmt -l` (one fix applied, see REFACTOR); the modified CRLF pipeline file verified via an LF-normalized temp copy (`gofmt -d` empty) — the working-tree CRLF file itself was never passed through gofmt (59-file incident guardrail) ✅

## ASSERT items

1. **Exact model-call counts**: S5 asserts exactly 1 call for a single chunk; S7 asserts 2 calls on the first pass (one per chunk), then exactly 3 total after retry — i.e. 0 calls for the completed chunk 0 and a single re-send of chunk 1; S9 asserts exactly 1 call for the worker-processed job.
2. **result_json keys stable (`chunks`/`merge`/`outcome`)**: S5 decodes the stored `result_json` into a generic map and asserts all three keys exist, then field-by-field deep-equals `chunks[0]` against the expected candidates, asserts `outcome="extracted"` and an empty `merge` slot. Schema = design Decision 8 (`chunks: {index: candidates}` + `merge` slot) + spec `outcome`.
3. **Missing candidate fields → parse error, never silent zeroing**: companion `shouldRejectCandidateMissingRequiredField` deletes each of the 6 fields in turn and asserts a field-named parse error; the test's bite was proven by a temporary mutation (removing the `confidence` nil-check made the test fail with a nil-pointer panic, i.e. the silent-zero path), then the check was restored. `evidence_refs` present-but-empty parses and is left to the Task 6 gate (companion `shouldAcceptEmptyEvidenceRefsForTheGate`).
4. **Chunking operates only on renderer units**: `Handle` calls `NewEvidenceRenderer().Render(messages)` then `NewEvidenceChunker().Chunk(units)` (pipeline :362-363); `EvidenceChunker.Chunk` takes `[]EvidenceUnit` — raw rows never reach the chunker. S9 proves archived rows flow renderer → prompt as `[msg <id>]` units.
5. **Backoff channels not mixed**: S6 asserts the failed job's `DueAt ≈ now + (AttemptCount+1)·minute` (the LINEAR extract channel), `Phase2AttemptCount == 0`, and no `phase2:` error prefix; the phase2 exponential path (`durablePhase2RetryDelay`) is untouched.

## Files changed

| File | Action |
|---|---|
| `internal/application/memory_usecase/durable_memory_pipeline.go` | MODIFIED (+186/−60): extract section rewritten to render → chunk → per-chunk structured extraction with incremental `result_json` persistence and resume; `messagesThrough` prefers the new optional archive-inclusive reader (`dreamMessageArchiveRangeReader`, declared next to the existing optional interfaces, existing style); Decision 10 — the extract-path no-model text dump (old :464) removed, now returns an error; retired `DurableStage1Result` type, `extract` method and orphaned `safeDurableSlug` helper deleted; consolidate-side `summarizeDurableText` sites deliberately untouched (Task 6) |
| `internal/application/memory_usecase/evidence_chunker.go` | NEW — `EvidenceChunker` (120000-byte cap via `durableMaxRolloutLen`, 2-unit overlap), `EvidenceChunk`/`ChunkedEvidenceUnit`, part_index/part_count slicing of oversized payloads, `evidenceUnitText/evidenceChunkedText` rendering shared by packing and prompts |
| `internal/application/memory_usecase/evidence_chunker_test.go` | NEW — `TestEvidenceChunker` (4 mandated scenarios) |
| `internal/application/memory_usecase/candidate_extraction_test.go` | NEW — `TestCandidateExtraction` (4 mandated scenarios + 3 strictness companions), scripted chat client, result_json decode helpers |
| `internal/application/memory_usecase/durable_window_wiring_test.go` | NEW — `TestDurableWindowWiring` (2 mandated scenarios) with counter-equipped archive-inclusive and active-only fake repositories |

`internal/domain/conversation/repository.go` and all Task 2/3/4 deliverables: zero diff.

## Implementation notes

### Chunking (design Decision 6)

- Cap stays `durableMaxRolloutLen` = 120000 bytes; packing metric = rendered unit text + per-unit separator, computed by the same functions (`evidenceChunkedText/evidenceChunkedBytes`) that build the extraction prompt, so packing and prompt size can never disagree.
- Greedy whole-unit packing: a unit that would cross the cap moves to the next chunk intact (S2).
- Oversized units (alone > cap) are pre-sliced before packing: the dominant payload (`Output` for exchange/orphan units, `Content` for text units) is cut into consecutive byte fragments; each fragment carries `PartIndex` (ascending from 0) and a consistent `PartCount`, renders ≤ cap (marker-width reserved at the worst-case digit count), and fragment payloads concatenate byte-for-byte back to the original — nothing dropped from the middle (S3: 300000-byte output → 3 parts).
- Overlap: every chunk after the first begins with copies of the previous chunk's last `min(2, len)` units, in order (S4). Overlap units ride outside the new-unit budget and every new unit is ≤ cap, so each chunk always advances the frontier (no infinite loop even when two overlap units alone exceed the cap — the degenerate case simply yields a chunk of overlap + one forced unit).
- Empty unit sequence → zero chunks → `outcome="no_output"` without any model call (nothing to extract, nothing to dump).

### result_json schema and crash-safe retry (design Decision 8)

- Schema: `{"chunks":{"<index>":[candidate…]},"merge":null,"outcome":""}`; `chunks` is `map[int][]ExtractionCandidate` (JSON keys are index strings), `merge` is the Task 7 slot, `outcome` the terminal marker.
- `extractChunks` persists the whole result into `job.ResultJSON` via `updateJob` **after every completed chunk** — a crash mid-window loses at most the in-flight chunk. The failure path then also persists via Handle's existing deferred update.
- Resume classification (`decodeDurableExtractionResult`): empty result → fresh run; non-empty WITHOUT a `chunks` key → legacy pre-chunking payload, treated as immutable/terminal (regression tests pin this); `chunks` present with empty `outcome` → partial, resumed; non-empty `outcome` → terminal. On resume the window is re-read and re-chunked, completed indexes are skipped with 0 model calls, and the final outcome `extracted` is set only after the last chunk lands.

### Candidate schema and strict parsing (design Decision 7)

- `ExtractionCandidate{Title, Content, Type, Confidence, Importance, EvidenceRefs}`; model envelope `{"candidates":[…]}`.
- Parsing goes through a pointer-field raw struct; any of the 6 missing keys is a field-named error (retryable through the normal failure path), never a silent zero. A present-but-empty `evidence_refs` parses — rejecting it is the Task 6 gate's job. Strings are trimmed; numeric range/finite checks are Task 6 gate territory as well.

### Decision 10 — no model → fail, not dump

- The extract path's dump site (old `durable_memory_pipeline.go:464`, `RawMemory: redact…, RolloutSummary: summarizeDurableText…`) is **removed**: `extractChunks` returns `errors.New("durable memory extraction requires a configured model")` (now :510), which flows through Handle's deferred into pending + linear backoff. No `raw_memory` text remains anywhere in production `memory_usecase` code (grep-verified).
- **Not removed (deliberately, per dispatch instructions and the Decision 10 site table)**: the Consolidate-side sites `durable_memory_pipeline.go:766` (no-model fallback) and `:783` (empty-summary fallback), plus the kept `consolidation_projection.go:157` — those belong to Task 6/7. Current `grep -rn "summarizeDurableText" internal/` = 4 lines (definition :890, the two Consolidate sites, projection :157); Task 6's DoD grep expects exactly 2 after it removes the Consolidate sites.
- Also deleted as part of the extract-section rewrite: `DurableStage1Result` (only consumer was the old extract) and `safeDurableSlug` (only consumer was the old extract result).

### Archive-inclusive window wiring (design Decision 2)

- New optional interface `dreamMessageArchiveRangeReader` declared beside the existing optional readers (pipeline :42-48), existing style; `messagesThrough` (:633) checks it FIRST and calls `ListThroughIncludingArchived` exactly once, then falls back to `dreamMessageRangeReader` (`ListActiveAfterThrough`), then to the legacy `ListActiveThrough` + in-pipeline after-filter — both fallbacks byte-for-byte unchanged (S10 pins all three tiers via call counters).
- Production effect with zero wiring changes: `cmd/worker/main.go:229` injects the MySQL `MessageRepository`, which gained `ListThroughIncludingArchived` in Task 2, so the worker automatically takes the archive-inclusive path; repositories without it (fakes, future stores) keep the historical active reads.

### Redaction

Units are redacted by the renderer (Task 3) before chunking; the assembled evidence block is passed through `redactDurableSecrets` again at prompt build time as defense in depth. S8 asserts the raw secret substring never reaches the recorded prompt and the `[REDACTED]` placeholder does.

## Self-review checklist

- [x] Failing tests first in all three rounds; RED evidence pasted (compile-level for rounds 1–2, behavioral for round 3)
- [x] All 10 scenario names preserved verbatim as subtests
- [x] Exact model-call counts asserted, incl. 0 calls for completed chunks on retry
- [x] result_json keys `chunks`/`merge`/`outcome` asserted stable; partial results keep `outcome=""`
- [x] Missing candidate field → parse error (mutation-proven test), no silent zeroing
- [x] Chunking only on renderer units (type signature + call order), 120k cap, unit-boundary splits, part_index/part_count slicing with lossless concatenation, exactly-2-unit overlap
- [x] Decision 10: extract path fails without a model; dump site removed; consolidate-side sites untouched for Task 6/7 (recorded above with line numbers)
- [x] Backoff channels unmixed: linear extract channel asserted; phase2 exponential untouched
- [x] Archived rows reach the renderer (S9); archive-inclusive reader preferred exactly once with unchanged fallbacks (S10)
- [x] Scope respected: only declared files + their new tests; `internal/domain/conversation/repository.go` untouched; no commit/push; `.vsdd-state.yaml` untouched
- [x] Formatting discipline: only self-created files passed through gofmt; modified CRLF file verified via LF-normalized copy
- [x] Shipping gate `GOOS=linux go build ./...` exit 0; vet clean

## REFACTOR notes

1. **S3 test corrected for overlap semantics**: the first version of `shouldSliceOversizedOutputWithPartIndex` counted fragment appearances across chunks and reported `part_count 3, want consistent 6` — fragments are legitimately repeated in adjacent chunks by the 2-unit overlap. The test now deduplicates by `part_index` (first occurrence order), which is the correct assertion of the slicing contract. Implementation unchanged.
2. `durable_window_wiring_test.go` gofmt alignment fix (struct field spacing) applied to the self-created file only.
3. The strict-parse ASSERT lock (`shouldRejectCandidateMissingRequiredField` + companions) was added after the round-2 GREEN as an ASSERT-companion; its bite was proven by temporarily mutating the `confidence` nil-check (test failed with the silent-zero nil deref), then restoring the check. The parse-error PATH itself was RED→GREEN'd end-to-end by scenario 6 (malformed JSON).

## Fix round — reviewer findings closed inside Task 5

Both reviewers returned PASS with zero Must Fix; the two Should Improve findings below were ruled production-reachable correctness gaps and fixed in this round, strict TDD (RED first) as always. Scope stayed inside `durable_memory_pipeline.go` / `evidence_chunker.go` + their tests.

### Fix 1 — resume index soundness (spec SI-2 + quality SI-1)

**Problem**: completed chunks are skipped by positional index, but the chunk plan is recomputed from the re-read window. If `previousBoundary` moves between attempts (a successor job completes before the older job's retry, shrinking the window), index 0 of the new plan maps to different evidence — stale chunk-0 candidates would be kept and the new window's first units silently never extracted.

**Fix**: the partial result now persists the window it was chunked from as additive result_json keys `window_after`/`window_through` (the three spec'd keys `chunks`/`merge`/`outcome` unchanged). Markers are set before the first per-chunk persist, so every persisted partial carries them. On resume, Handle validates the stored markers against the current window (`previousThrough`, `job.ThroughMessageID`); on ANY mismatch the partial chunks are discarded and re-extracted from scratch. A marker pair of `(0,0)` (pre-markers payload) can never be validated — `window_through` is always > 0 at extraction time — and is discarded the same way. Legacy chunk-less payloads and terminal outcomes stay immutable/terminal exactly as before; the shadow-window branch is unchanged.

**RED evidence** — step 1, compile-level (markers absent from the schema):

```text
internal\application\memory_usecase\candidate_extraction_test.go:260:14: partial.WindowAfter undefined (type durableExtractionResult has no field or method WindowAfter)
internal\application\memory_usecase\candidate_extraction_test.go:260:42: partial.WindowThrough undefined (type durableExtractionResult has no field or method WindowThrough)
… too many errors
FAIL	agentcanvas/internal/application/memory_usecase [build failed]
```

Step 2, behavioral (markers persisted, discard check NOT yet in place — proves the scenario bites the production path):

```text
--- FAIL: TestCandidateExtraction/shouldDiscardPartialChunksWhenWindowShrinksBetweenAttempts
    candidate_extraction_test.go:359: total model calls = 2, want 3: the shrunken window's chunk must be re-extracted, not skipped via the stale index
```

**GREEN evidence**: `shouldDiscardPartialChunksWhenWindowShrinksBetweenAttempts` PASS — pass 1 persists chunk 0 + markers (0,100]; a successor completing with through 80 shrinks the retry window to (80,100]; the stale chunk is discarded, the shrunken window is extracted fresh (3 total model calls), no stale candidate survives, final markers (80,100].

**Over-discarding guard** (`shouldKeepPartialChunksWhenWindowUnchanged`): covered by the existing mandated scenario `shouldPersistChunkCandidatesIncrementallyAndSkipOnRetry` — same window (0,3] on both attempts, chunk 0 skipped with 0 re-calls (3 total model calls asserted). This round added window-marker assertions to it: an always-discard regression would re-extract chunk 0 → 4 calls → the scenario fails. Cited as the guard per dispatch. The schema-key assertion in `shouldParseStructuredCandidatesFromModel` now also pins `window_after`/`window_through` presence alongside the three spec'd keys.

### Fix 2 — fragment explosion guard (quality SI-2)

**Problem**: when a unit's shell (everything except the sliceable payload) alone is ≥ the 120k cap, the slice budget clamped to 1 → one fragment (and one model call) PER BYTE of payload — unbounded.

**Fix**: in `sliceOversized`, when shell + worst-case marker leaves no usable budget, the budget is set to the full cap instead of 1: the payload is still cut into cap-sized slices, accepting the shell overflow for that pathological unit, so the fragment count stays O(payload/cap). Chosen over pass-through-whole because it keeps per-fragment payload bounded by the cap (fragments render cap + shell, still one model call each, but only ceil(payload/cap) of them). The two doc comments claiming "fragments that each fit alone" / "rendered size stays <= the cap" were corrected to state the actual guarantee, including the shell-overflow exception.

**RED evidence** (behavioral):

```text
--- FAIL: TestEvidenceChunker/shouldNotExplodeWhenUnitShellExceedsCap
    evidence_chunker_test.go:249: shell-exceeds-cap unit exploded into 300000 fragments, want O(payload/cap) <= 5
```

**GREEN evidence**: `shouldNotExplodeWhenUnitShellExceedsCap` PASS — exchange unit with 120001-byte arguments (shell alone > 120000) and a 300000-byte output yields 3 fragments (≤ 5), part_index contiguous from 0, consistent part_count, lossless concatenation, unit identity preserved, and the full `Chunk` path places every fragment (nothing silently dropped). The healthy-shell slicing scenario `shouldSliceOversizedOutputWithPartIndex` stays green unchanged (the fix only touches the budget-clamp branch).

### Fix-round gates

- `go test ./internal/application/memory_usecase -run 'EvidenceChunker|CandidateExtraction|DurableWindowWiring' -count=1` → `ok` (12 mandated scenarios + companions, all PASS)
- Full package suite `go test ./internal/application/memory_usecase -count=1` → `ok`; `go vet` clean
- `GOOS=linux go build ./...` → exit 0
- gofmt: the three LF test/chunker files `gofmt -l` clean; the modified CRLF pipeline verified via an LF-normalized temp copy (clean); no repo-wide formatting run

### Fix-round files touched

| File | Action |
|---|---|
| `internal/application/memory_usecase/durable_memory_pipeline.go` | `durableExtractionResult` gains `WindowAfter`/`WindowThrough` (`window_after`/`window_through`); Handle validates markers on resume and discards moved-window partials; markers set before the first per-chunk persist; `extractChunks` doc notes the caller-side window validation |
| `internal/application/memory_usecase/evidence_chunker.go` | `sliceOversized` budget clamp → full cap on shell overflow; `Chunk`/`sliceOversized` doc guarantees corrected |
| `internal/application/memory_usecase/candidate_extraction_test.go` | NEW subtest `shouldDiscardPartialChunksWhenWindowShrinksBetweenAttempts`; window-marker assertions added to the two neighboring scenarios (schema keys, incremental retry) |
| `internal/application/memory_usecase/evidence_chunker_test.go` | NEW subtest `shouldNotExplodeWhenUnitShellExceedsCap` |

## Deviations

1. **Naming shape** (same convention as Tasks 1–4): tasks.md writes `EvidenceChunkerTest#should…`; Go scenarios are subtests of `TestEvidenceChunker` / `TestCandidateExtraction` / `TestDurableWindowWiring` with the scenario names verbatim.
2. **Overlap units ride outside the new-unit byte budget**: Decision 6 states the 120000 cap and the 2-unit overlap as separate facts. Counting overlap against the cap can make the frontier stall (two large overlap units would leave no room for a new unit), so new units are greedily packed under the cap and overlap is copied on top. A chunk's total rendered size can exceed the cap by at most the two overlap units; oversized units are sliced so every NEW unit is ≤ cap. Degenerate corner (overlap alone > cap) documented in code: the chunk holds overlap + one forced new unit.
3. **Empty/shadow windows now write the new schema**: `{"chunks":{},"merge":null,"outcome":"no_output"}` instead of the legacy `{"raw_memory":"","rollout_summary":""}`. No test pinned the legacy empty-window payload; legacy NON-empty payloads remain immutable/terminal (both regression tests green). Task 6's `no_output` gate consumes the same outcome value.
4. **Outcome value `extracted`** introduced as the Phase-1 terminal marker (design/spec define only `no_output` explicitly; a completion marker is required so Handle can distinguish "partial, resume" from "done" without recomputing chunks). Task 6's gate may rewrite the outcome to `no_output`.
5. **Sliced-fragment payload slicing targets `Output` for tool units and `Content` for text units**; a unit oversized purely by `Arguments` (payload empty) passes through whole as a documented degenerate corner (Decision 6 scopes slicing to oversized tool outputs).
6. **result_json schema facts surfaced for artifact sync at archive** (spec reviewer requirement, Fix round): the Phase-1 `result_json` object carries exactly the keys `chunks` (map of chunk index → candidates), `merge` (Task 7 slot, null until then), `outcome` (enum: `extracted` — chunked extraction finished, gate may still rewrite to `no_output`; `no_output` — no candidates/empty window; empty string — partial, resumable), plus the additive resume markers `window_after`/`window_through` (the evidence window the partial chunks were chunked from; a retry whose window differs discards the partial). The three spec'd keys `chunks`/`merge`/`outcome` are unchanged by Fix 1; the window markers are additive. Shell-overflow fragments (Fix 2) render cap + shell bytes — the cap guarantee applies to the sliceable payload per fragment, not to pathological shells.

## Reverse Sync

None required. Design Decisions 2/6/7/8/10 matched code facts throughout; the optional interface extension stayed inside the pipeline file (existing style), so no out-of-scope artifact or file needed changes. Forward note for Task 6: the consolidate-side `summarizeDurableText` sites (pipeline :766/:783) are the remaining dump points to remove per Decision 10's site table.
