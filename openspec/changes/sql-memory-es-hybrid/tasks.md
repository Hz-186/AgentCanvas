# Tasks — sql-memory-es-hybrid

> complexity: 🔴 standard | phase: propose

## Wave 1 — Canonical SQL model and migration primitives

- [ ] Task 1: Add SQL canonical memory schema and Go contracts
  - complexity: 🔴
  - files: `migrations/*memory*.sql`, new `memory_artifacts`/`memory_write_jobs` migrations, `internal/domain/memory/memory.go`, new artifact/job models and repositories, config/state wiring
  - RED:
    - `MemorySchemaTest#shouldExposeUsageFields`（mock migration metadata returns current columns without `usage_count`/`last_used_at` → assert schema validation fails）
    - `MemoryArtifactRepositoryTest#shouldCreateVersionedArtifact`（mock DB transaction returns one inserted artifact → assert kind/version/checksum and owner are persisted）
    - `MemoryWriteJobRepositoryTest#shouldRejectUnknownSource`（mock validator receives source `unknown` → assert validation error and zero DB insert）
    - `MemoryArtifactRepositoryTest#shouldHandleDuplicateIdempotencyKey`（mock unique-key conflict for same owner/job key → assert existing job is returned and no duplicate is created）
    - `MemoryWriteJobRepositoryTest#shouldClaimLeaseInOrder`（mock pending rows ordered by due time and ID → assert one lease claim per row in deterministic order and exact worker ID）
  - GREEN:
    - `go test ./internal/domain/memory ./internal/infrastructure/mysql/...`
  - ASSERT:
    - Assert `usage_count`, `last_used_at`, fixed source validation, artifact kind/version, owner IDs and idempotency keys are exact.
    - Assert duplicate claims/inserts do not create a second row and short-circuit paths do not call the write transaction twice.
    - Assert migration up/down SQL is syntactically valid in the project migration harness.
  - DoD:
    - New schema/model/repository tests pass; migration validation passes; no new model uses `recall_count` for adoption usage; `memory_write_jobs` and `memory_artifacts` contracts are available to later tasks.

## Wave 2 — Unified asynchronous write pipeline

- [ ] Task 2: Route every memory producer through memory write jobs
  - complexity: 🔴
  - files: `internal/application/memory_usecase/*`, `internal/runtime/agentruntime/execution.go`, `internal/runtime/agentruntime/assembly.go`, `internal/application/agent_usecase/improvement.go`, worker bootstrap/dispatch/config
  - RED:
    - `MemoryWritePipelineTest#shouldEnqueueAdHocWithoutBlocking`（mock job queue accepts an ad-hoc payload but blocks downstream worker → assert finalization returns before worker completion）
    - `MemoryWritePipelineTest#shouldNoOpWhenPhaseOneFindsNoSignal`（mock extractor returns empty rollout/raw fields → assert no `memories` insert and job marked no-op）
    - `MemoryWritePipelineTest#shouldKeepRunSuccessfulWhenEnqueueFails`（mock queue returns an error → assert final run remains successful and warning event is emitted）
    - `MemoryWritePipelineTest#shouldRetrySqlFailure`（mock SQL transaction fails once then succeeds → assert lease retry/backoff and exactly one successful memory row）
    - `MemoryWritePipelineTest#shouldEnqueueContextOutboxAfterCommit`（mock SQL commit succeeds and outbox insert is observed → assert outbox receives exact owner/resource/content version once, and no outbox call occurs on rollback）
  - GREEN:
    - `go test ./internal/application/memory_usecase ./internal/runtime/agentruntime ./internal/application/agent_usecase ./cmd/worker`
  - ASSERT:
    - Verify finalization performs no synchronous file write or LLM consolidation call.
    - Verify queue failure is fail-open, SQL failure is retried under lease, and SQL rollback emits no ES outbox event.
    - Verify all five producers use the same job envelope and source values.
  - DoD:
    - All producer tests pass; worker claims/retries/DLQ are wired; successful runs never wait on memory writes; old direct ad-hoc file write path is unreachable.

- [ ] Task 3: Persist consolidation projections and remove file authority
  - complexity: 🔴
  - files: `internal/application/memory_usecase/durable_memory_pipeline.go`, new artifact projection service, `internal/application/memory_usecase/durable_memory_files.go`, runtime dependency wiring
  - RED:
    - `ConsolidationProjectionTest#shouldWriteHandbookAndSummaryRows`（mock single consolidation agent returns handbook/summary text → assert two versioned SQL artifacts with source references）
    - `ConsolidationProjectionTest#shouldShortCircuitEmptyBatch`（mock completed extraction list and ad-hoc list are empty → assert no LLM call and no artifact mutation）
    - `ConsolidationProjectionTest#shouldReturnRetryableErrorOnArtifactFailure`（mock artifact transaction throws → assert job remains retryable and no filesystem fallback is attempted）
    - `ConsolidationProjectionTest#shouldRetainProtectedArtifact`（mock selected inputs include a protected memory and a cold unprotected input → assert protected source remains and cold input is excluded）
    - `ConsolidationProjectionTest#shouldUseDiffBeforeConsolidation`（mock artifact version diff contains one added and one removed source → assert consolidation receives exact diff first and one call only）
  - GREEN:
    - `go test ./internal/application/memory_usecase`
  - ASSERT:
    - Assert `DurableFileStore` is not injected into Agent-facing readers and no production path writes `MEMORY.md`, `memory_summary.md`, `raw_memories.md`, rollout files or ad-hoc Markdown.
    - Assert artifact versions, source IDs, protected markers and retry status are exact.
    - Assert empty/no-op extraction avoids LLM and SQL projection writes.
  - DoD:
    - Consolidation persists all required projections in SQL, file authority is removed, and projection tests pass.

## Wave 3 — Keyword retrieval and read parity

- [ ] Task 4: Implement keyword-ranked ES memory retrieval with SQL hydration
  - complexity: 🔴
  - files: `internal/infrastructure/retrieval/context_backend.go`, keyword/context resource adapters, `internal/domain/memory/runtime_service.go`, `internal/runtime/toolruntime/memory_tools.go`, index configuration/tests
  - RED:
    - `RuntimeMemoryReadTest#shouldReturnKeywordScoreOrder`（mock ES hits scores 4.2/2.8/1.1 and SQL rows in reverse order → assert response follows ES score order）
    - `RuntimeMemoryReadTest#shouldSkipVectorBranch`（mock keyword index returns hits and vector index would fail if called → assert vector call count is zero）
    - `RuntimeMemoryReadTest#shouldReturnEmptyWhenIndexUnavailable`（mock ES keyword search throws → assert semantic read returns observable error/empty result without full-table scan）
    - `RuntimeMemoryReadTest#shouldEnforceScopeAndTruncation`（mock one foreign-owner hit, one overlong current-owner row, limit 25 → assert foreign row omitted, limit 20 and content <=6000 chars）
    - `RuntimeMemoryReadTest#shouldUseStableIDTieBreak`（mock equal ES scores for IDs 12 and 7 → assert ID 7 then 12 and exact SQL hydration IDs）
  - GREEN:
    - `go test ./internal/domain/memory ./internal/runtime/toolruntime ./internal/infrastructure/retrieval/...`
  - ASSERT:
    - Verify `_score` descending order survives SQL hydration, vector branch is zero calls, scope filters are exact, and full-table fallback is absent.
    - Verify default/maximum limits and per-entry truncation without silently padding missing rows.
    - Verify SQL status/conflict/expiry checks can suppress stale ES hits.
  - DoD:
    - Keyword-only retrieval is the sole Agent-facing detail path; ranking, tenancy, limits and no-vector behavior are tested.

- [ ] Task 5: Move automatic summary and skill reads to their owning SQL subsystems
  - complexity: 🔴
  - files: `internal/runtime/agentruntime/tools.go`, runtime dependency structs/bootstrap, skill retrieval wiring, API/tool descriptions/tests
  - RED:
    - `AutomaticMemoryBlockTest#shouldInjectBoundedAdvisorySummary`（mock SQL summary artifact returns 1201-token-equivalent text → assert bounded output, stable IDs and read_memory/freshness guidance）
    - `AutomaticMemoryBlockTest#shouldSkipDelegatedRun`（mock delegated run context → assert summary repository call count zero）
    - `AutomaticMemoryBlockTest#shouldReturnNilOnSummaryFailure`（mock SQL artifact read throws → assert no context block and no file fallback）
    - `SkillReadOwnershipTest#shouldRouteSkillQueryToSkillSubsystem`（mock skill retriever returns a workflow while memory retriever is empty → assert skill retriever called once and memory artifacts unchanged）
    - `AutomaticMemoryBlockTest#shouldPreserveAdvisoryText`（mock summary row has memory IDs and stale version → assert output includes advisory wording and freshness hint exactly once）
  - GREEN:
    - `go test ./internal/runtime/agentruntime ./internal/runtime/toolruntime ./internal/bootstrap`
  - ASSERT:
    - Verify top-level/non-delegated gating, 1200-token bound, stable IDs, advisory/freshness guidance and zero file fallback.
    - Verify `skills` is not represented as a memory artifact or searched by `read_memory`.
  - DoD:
    - Both read entries use SQL/ES or skill-owned paths with preserved semantics and passing tests.

## Wave 4 — Citation, usage lifecycle, Reflection and cleanup

- [ ] Task 6: Add citation stripping and owner-validated usage accounting
  - complexity: 🔴
  - files: citation parser/finalization stream utilities, `internal/domain/memory` usage repository, runtime result finalizer, recall/usage tests
  - RED:
    - `MemoryCitationTest#shouldStripVisibleCitationBlock`（mock final text containing a valid `<oai-mem-citation>` block → assert returned visible text excludes the full block）
    - `MemoryCitationTest#shouldCountValidIDsOncePerRun`（mock two citations repeating memory ID 101 in one run → assert one `usage_count` increment and one `last_used_at` update）
    - `MemoryCitationTest#shouldDropMalformedLineOnly`（mock block with two valid lines and one malformed line → assert two usage updates and one warning）
    - `MemoryCitationTest#shouldRejectForeignAndMissingIDs`（mock SQL owner lookup returns foreign ID 9 and missing ID 10 → assert zero usage updates for both and warning events）
    - `MemoryCitationTest#shouldNotCountReturnedUnusedMemory`（mock read returns IDs 1/2 but final citation names only 1 → assert only ID 1 usage update）
  - GREEN:
    - `go test ./internal/domain/memory ./internal/runtime/...`
  - ASSERT:
    - Verify visible text is clean, parser is line-tolerant, owner validation precedes update, per-run/thread dedupe is exact, and RecallLog remains a separate returned-candidate audit.
  - DoD:
    - Citation parsing/strip/usage tests pass with `usage_count` and `last_used_at`; no adoption logic references `recall_count`.

- [ ] Task 7: Implement usage-driven lifecycle and source-aware pruning
  - complexity: 🔴
  - files: lifecycle selection/pruning services, SQL migrations/repositories, consolidation cleanup, config defaults and tests
  - RED:
    - `MemoryLifecycleTest#shouldOrderByUsageThenRecency`（mock eligible rows with usage 8/2 and equal usage with different `last_used_at` → assert usage then recency order）
    - `MemoryLifecycleTest#shouldExcludeColdUnprotectedInputs`（mock rows outside 30-day window without consolidated protection → assert excluded from top-256 selection）
    - `MemoryLifecycleTest#shouldProtectConsolidatedRows`（mock cold row with handbook/summary protection marker → assert no direct delete and protection retained）
    - `MemoryLifecycleTest#shouldCapSelectionAt256`（mock 300 eligible rows → assert exactly 256 selected and remaining rows untouched）
    - `MemoryLifecycleTest#shouldAvoidQualityScoring`（mock lifecycle dependencies expose no LLM scorer and return usage data → assert no scorer call and deterministic SQL ordering）
  - GREEN:
    - `go test ./internal/domain/memory ./internal/application/memory_usecase ./internal/infrastructure/mysql`
  - ASSERT:
    - Assert exact 30-day and 256 defaults, source_updated_at fallback, protected marker behavior, and absence of quality-score calls.
    - Assert source-aware consolidation receives deleted IDs for surgical handbook/summary cleanup.
  - DoD:
    - Lifecycle tests pass and production configuration exposes the agreed defaults without static quality scoring.

- [ ] Task 8: Migrate Reflection/files and retire duplicate storage surfaces
  - complexity: 🔴
  - files: migration importer/checksum tooling, Reflection usecase/worker/repository/API/bootstrap/config, `memory_write_logs` migrations/model/repository, cleanup verification tests/docs
  - RED:
    - `MemoryMigrationTest#shouldImportAllArtifactKinds`（mock owner directory containing handbook, summary, raw, rollout, skill and ad-hoc files → assert SQL destinations for five artifact kinds and skill handoff without memory artifact creation）
    - `MemoryMigrationTest#shouldAbortOnChecksumOrOwnerMismatch`（mock one file hash mismatch or foreign owner path → assert migration stops before delete and reports exact offending path）
    - `ReflectionMigrationTest#shouldConvertHistoricalRows`（mock validated/disputed reflection rows → assert ordinary memories with source `reflection` and preserved metadata/status mapping）
    - `LegacyCleanupTest#shouldRemoveRetiredSurfacesAfterValidation`（mock successful migration validation → assert old files, `agent_reflections`, independent API/index/worker and `memory_write_logs` cleanup actions run once）
    - `LegacyCleanupTest#shouldKeepSourcesOnFailure`（mock ES backfill failure → assert no destructive cleanup, rerunnable migration state, and zero DROP/delete calls）
  - GREEN:
    - `go test ./internal/application/reflection_usecase ./internal/application/memory_usecase ./internal/infrastructure/mysql ./internal/interface/http`
  - ASSERT:
    - Verify every legacy file has a destination or explicit skill owner, checksums/tenant ownership gate deletion, Reflection rows are ordinary memories, and retired repositories/API routes have zero production references.
    - Verify cleanup only runs after migration and ES backfill validation; failure preserves rerun inputs.
  - DoD:
    - Migration and cleanup tests pass; old files, Reflection surfaces and `memory_write_logs` are removed only after validated import; repository/reference scans are clean.

## 任务依赖关系

```text
Task 1 (SQL contracts)
  ├── Task 2 (unified write jobs)
  │     └── Task 3 (SQL projections/file authority removal)
  ├── Task 4 (keyword retrieval)
  │     └── Task 5 (automatic summary + skill ownership)
  ├── Task 6 (citation usage)
  │     └── Task 7 (usage lifecycle)
  └── Task 8 (migration + destructive cleanup; requires Tasks 1–7)
```

- Tasks 2 and 4 can begin after Task 1 but remain serial with their own dependents because they share the SQL/context contracts.
- Tasks 3 and 5 are serial after their parent tasks; both modify runtime/bootstrap wiring and must not run in parallel.
- Tasks 6 and 7 are serial because lifecycle consumes the usage fields and deduplication semantics from citation accounting.
- Task 8 is the final convergence task and cannot be parallelized with any task because it deletes legacy files/tables/API surfaces.
- Suggested worktree groups: Group A (Tasks 1–3), Group B (Tasks 4–5), Group C (Tasks 6–7), then a single integration worktree for Task 8. Merge at the shared SQL contract and final migration checkpoints.
