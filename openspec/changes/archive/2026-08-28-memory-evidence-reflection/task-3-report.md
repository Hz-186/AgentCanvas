# Task 3 Report — Build the durable evidence renderer

> change: memory-evidence-reflection | task: 3 | complexity: 🔴 | date: 2026-08-28
> branch: refactor/memory-usecase-cleanup | status: **DONE**

## Skills Loaded

- `vsdd-workflow-implement-task`
- `test-driven-development`
- `vsdd-workflow-reverse-sync` (no trigger — no artifact/code conflict found)

## Environment

- Toolchain: `D:\Users\hongze01.zhang\sdk\go1.26.6\bin\go.exe` (not on PATH), Windows host.
- `internal/application/memory_usecase` compiles and tests natively on Windows (no toolruntime dependency — verified; no overlay needed).
- Shipping gate: `GOOS=linux go build ./...` without overlay → exit 0.

## RED evidence (written before any production code)

Test file authored first; production file did not exist. `go test ./internal/application/memory_usecase -run EvidenceRenderer -count=1`:

```text
# agentcanvas/internal/application/memory_usecase [agentcanvas/internal/application/memory_usecase.test]
internal\application\memory_usecase\evidence_renderer_test.go:81:40: undefined: EvidenceUnit
internal\application\memory_usecase\evidence_renderer_test.go:84:7: undefined: EvidenceErrorStateUnknown
internal\application\memory_usecase\evidence_renderer_test.go:84:34: undefined: EvidenceErrorStateSuccess
internal\application\memory_usecase\evidence_renderer_test.go:84:61: undefined: EvidenceErrorStateFailure
internal\application\memory_usecase\evidence_renderer_test.go:91:46: undefined: EvidenceUnit
internal\application\memory_usecase\evidence_renderer_test.go:107:15: undefined: NewEvidenceRenderer
internal\application\memory_usecase\evidence_renderer_test.go:113:19: undefined: EvidenceUnitText
internal\application\memory_usecase\evidence_renderer_test.go:145:15: undefined: NewEvidenceRenderer
internal\application\memory_usecase\evidence_renderer_test.go:151:20: undefined: EvidenceUnitExchange
internal\application\memory_usecase\evidence_renderer_test.go:193:15: undefined: NewEvidenceRenderer
internal\application\memory_usecase\evidence_renderer_test.go:193:15: too many errors
FAIL	agentcanvas/internal/application/memory_usecase [build failed]
```

Compile-level RED (feature missing, not a typo) — standard Go TDD shape for a new API surface.

Mutation check (test honesty): temporarily changing the metadata default from `EvidenceErrorStateUnknown` to `EvidenceErrorStateSuccess` produced

```text
--- FAIL: TestEvidenceRenderer/shouldMarkMissingErrorStateAsUnknown (0.00s)
FAIL	agentcanvas/internal/application/memory_usecase
```

then the mutation was reverted (verified by grep; tests green again).

## GREEN evidence

`go test ./internal/application/memory_usecase -run EvidenceRenderer -count=1 -v`:

```text
=== RUN   TestEvidenceRenderer
--- PASS: TestEvidenceRenderer (0.00s)
    --- PASS: TestEvidenceRenderer/shouldRenderTextUnitsWithIdentityAndRedaction (0.00s)
    --- PASS: TestEvidenceRenderer/shouldPairCallAndOutputByToolCallID (0.00s)
    --- PASS: TestEvidenceRenderer/shouldMarkMissingErrorStateAsUnknown (0.00s)
    --- PASS: TestEvidenceRenderer/shouldCountSameArgFailuresAndDetectRecovery (0.00s)
    --- PASS: TestEvidenceRenderer/shouldRenderOrphanOutputWithoutPanic (0.00s)
    --- PASS: TestEvidenceRenderer/shouldExcludeReasoningSystemEchoAndDeveloper (0.00s)
    --- PASS: TestEvidenceRenderer/shouldPreserveMessageIDOrder (0.00s)
PASS
ok  	agentcanvas/internal/application/memory_usecase	1.543s
```

Full package suite (regression): `go test ./internal/application/memory_usecase -count=1` → `ok` (all pre-existing tests stay green).

## DoD gates

- `go test ./internal/application/memory_usecase -run EvidenceRenderer` — all 7 subtests green ✅
- `GOOS=linux go build ./...` — exit 0, no overlay ✅
- `go vet ./internal/application/memory_usecase` — clean ✅
- `gofmt` — no diffs on either new file ✅

## Files changed

| File | Action |
|---|---|
| `internal/application/memory_usecase/evidence_renderer.go` | NEW — renderer implementation |
| `internal/application/memory_usecase/evidence_renderer_test.go` | NEW — `TestEvidenceRenderer` with the 7 verbatim scenario subtests |

`durable_memory_pipeline.go` was READ only — not modified. `redactDurableSecrets` verified at `durable_memory_pipeline.go:712` with signature `func redactDurableSecrets(value string) string`; reused as-is (same package, no signature change → no Reverse Sync).

## Implementation notes

### API (locked by tests)

- `NewEvidenceRenderer()` → `*EvidenceRenderer` (stateless, pure: rows → units; no repo/LLM calls — Task 5 wires it in).
- `Render(messages []conversation.Message) []EvidenceUnit` — tolerates unordered input (sorts a copy by id), returns units ascending by anchor message id.
- `EvidenceUnitKind`: `text` | `exchange` | `orphan_output`.
- `EvidenceErrorState` tri-state: `unknown` | `success` | `failure`, mutually exclusive by construction (single string enum; `assertTriState` locks it in tests).
- `EvidenceUnit` fields: `Kind, MessageID, RunID, Role, Content, ToolCallID, ToolName, Arguments, Output, ErrorCode, ErrorState, FailureCount, Recovered`.

### Behavior decisions (all covered by the 7 scenarios + ASSERT items)

1. **Redaction before unit entry**: every payload surface — text `Content`, serialized `Arguments`, tool `Output` — passes through `redactDurableSecrets` at unit construction. Locked by scenario 1 (text) and scenario 2 (arguments + output of an exchange), with `assertNoRawSecret` scanning all three fields for the raw substring `abcd1234efgh`.
2. **Pairing by exact tool_call_id only**: calls are indexed in a map keyed by `tool_call_id`; outputs bind only on exact id match. Scenario 2 asserts two same-named `search_code` calls in different runs never cross-pair (distinct output texts stay on their own units).
3. **Tri-state parsing**: a small typed accessor (`parseEvidenceMetadata`) unmarshals `tool_call_id/tool_name/arguments/is_error/error_code` with `*bool` for `is_error` — key absent → `unknown`, `true` → `failure`, `false` → `success`. Malformed/empty JSON degrades to unknown, never panics. Scenario 3 feeds failure-sounding output text ("command failed with exit code 1") on a legacy row and asserts `unknown` — no text heuristics.
4. **Failure counting & recovery** (`countEvidenceFailures`): fingerprint = `tool_name + "\x00" + canonical(arguments)`; argument canonicalization is key-order-insensitive (Decode with `UseNumber` → Marshal, preserving numeric literals) with a trimmed-raw fallback for unparsable arguments. A success closes the streak (`Recovered=true` on the success unit and back-filled onto the streak's failure units) and resets the count; unknown states neither count nor reset. Success unit carries `FailureCount` = failures immediately preceding it. Locked by scenario 4 including independent-args fingerprint and post-recovery fresh streak.
5. **Orphan outputs kept**: unmatched outputs render as standalone `orphan_output` units anchored on their own row id, carrying `tool_call_id`/`tool_name`; explicit `recover()` guard in the test.
6. **Exclusions**: content types `reasoning`/`system_echo` and roles `developer`/`system` yield 0 units (spec: "developer/system injected content MUST be excluded").
7. **Defensive edges**: function_call rows with empty/unparsable `tool_call_id` are skipped (unpairable); calls whose output never reached the window are not rendered (no evidence yet); duplicate call rows with the same id emit once (`emitted` guard); duplicate outputs for an already-bound call render as orphan units (evidence preserved rather than silently dropped).

## Self-review checklist

- [x] Every behavior has a failing-test-first record (compile RED + mutation check)
- [x] All 7 scenario names preserved verbatim as subtests of `TestEvidenceRenderer`
- [x] ASSERT items locked: redaction covers text/arguments/output; exact-id pairing (no cross-run name pairing); tri-state mutual exclusion asserted on exchange units in scenarios 2/3/4/5
- [x] Renderer is pure (imports only `encoding/json`, `sort`, `strings`, `conversation`) — no repo/LLM/config access
- [x] Reuses Task 1 metadata shape (`is_error`/`error_code` keys, legacy two-key rows) and Task 2's `[]conversation.Message` window rows
- [x] Scope respected: only the declared new files; no other file touched
- [x] `GOOS=linux go build ./...` exit 0 without overlay; full package suite green; vet/fmt clean
- [x] No commit/push performed (main session owns commits)
- [x] `.vsdd-state.yaml` untouched (no Reverse Sync)

## REFACTOR notes

After GREEN: introduced a per-row `metaAt` memoized accessor so each row's `metadata_json` is parsed at most once (was parsed up to twice for call/output rows), and simplified the orphan-binding condition (`toolCallID != ""` checked before map lookup). Behavior unchanged — all tests stayed green across the refactor (`go test ./internal/application/memory_usecase -count=1` → ok).

## Deviations

None material. One additive, spec-grounded choice: scenario `shouldExcludeReasoningSystemEchoAndDeveloper` also seeds a `role=system` text row (asserting 0 units overall). The scenario name lists three kinds, but the spec requirement explicitly covers "developer/system injected content", so the extra row locks the spec sentence without changing the scenario identity.

## Reverse Sync

None required. All authoritative inputs matched code facts: `redactDurableSecrets` location/signature (pipeline:712), Task 1 metadata key shapes, Task 2's `ListThroughIncludingArchived` row type.
