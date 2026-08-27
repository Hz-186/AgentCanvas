## Context

See `proposal.md` for motivation and behavior contracts. The current runtime wires `DurableFileStore` into both automatic summary injection and `read_memory`, while SQL memory writes and the unified Context outbox already exist but are not the sole Agent-facing path. The current Context backend uses equal-weight RRF; this design intentionally replaces it with keyword-only ES search (`vector_weight=0`) per the user decision.

## Goals / Non-Goals

**Goals:**

- Make SQL the rebuildable source of truth for atomic memories, artifacts, jobs, lifecycle and provenance.
- Preserve both current read-entry semantics while removing file-backed reads.
- Make all writes asynchronous and fail-open to the successful Agent run.
- Reuse the existing Context keyword index/outbox and SQL hydration boundary.
- Absorb Reflection and retire duplicate storage, retrieval and lifecycle surfaces.
- Provide deterministic migration, verification and cleanup of legacy files/tables.

**Non-Goals:**

- No vector retrieval in this change; `vector_weight=0` is permanent unless a future change explicitly changes the contract.
- No LLM quality scoring, confidence ranking or new memory taxonomy for Reflection.
- `skills` is not folded into memory artifacts; its independent subsystem remains authoritative.
- No synchronous ES refresh or read-time full SQL scan when the index is unavailable.

## Decisions

### 1. Canonical SQL model

Reuse `memory.Memory`/`memories` for atomic entries, replacing lifecycle names with `usage_count` and `last_used_at` via migration. Add `memory_artifacts` for versioned `handbook`, `summary`, `raw_input`, `rollout_summary` and `ad_hoc` records, and `memory_write_jobs` for the unified asynchronous command envelope. Keep `memory_extraction_jobs` as Phase 1 evidence/status, not as the general write queue. Artifact rows carry owner, kind, version, content, source references, checksums, consolidated/protected timestamps and created/updated metadata.

Alternative rejected: encoding every artifact as a `memories` row would make projections indistinguishable from atomic facts and would complicate source-aware cleanup.

### 2. Retrieval boundary

Extend the existing unified Context index resource document and SQL hydration path. The keyword query returns ES `_score` ordered IDs; equal scores use ascending `memory_id` as a deterministic tie-break. The runtime fetches SQL rows and rechecks owner, scope, active status, expiry and conflict. The vector leg is disabled by configuration and code path. The old `agentcanvas_memories_v1` store is migration-only and then removed.

Alternative rejected: a dedicated `MemoryHybridIndex` would duplicate outbox, embedding/profile and tenant filtering infrastructure.

### 3. Two read entries

Automatic context reads the versioned SQL summary artifact directly, bounded to the existing 1200-token policy and marked advisory. The block includes a short freshness/read_memory hint and stable memory IDs for citation. Detailed reads call the keyword ES + SQL hydration service, preserving 5/20/6000 limits and never returning `summary`, raw inputs or skills as memory artifacts.

### 4. Asynchronous write topology

Runtime finalization creates an idempotent `memory_write_jobs` row or enqueue envelope and returns. A worker claims jobs with lease/retry/DLQ semantics, runs Phase 1 no-op extraction where applicable, writes SQL in a transaction, and emits the existing context resource outbox. Context workers update ES independently. SQL commit is the only success boundary for facts; ES is eventually consistent.

### 5. Citation and usage

Finalization strips the citation block before emitting user-visible content. Parsing is line-oriented and tolerant: malformed lines, missing IDs and foreign-owner IDs are dropped individually, each producing an observable warning, while valid current-owner IDs continue to a deduplicated usage update. `RecallLog` remains the returned-candidate audit; `usage_count`/`last_used_at` represent model adoption and drive lifecycle.

### 6. Reflection absorption

The terminal Reflection analyzer becomes an extraction producer. Inline reflection remains a runtime-only signal unless included as evidence. Approved improvement proposals produce normal write jobs. A migration copies historical fields into ordinary memory metadata, then removes `agent_reflections`, its recall/index/worker/API path and related configuration. Status semantics map to active/revoked/superseded or audit metadata.

### 7. Migration and cleanup

The importer reads each owner directory, maps artifacts, parses atomic facts with stable provenance and computes checksums. It validates owner, counts, content hashes and duplicate keys before marking the migration complete. It then backfills Context ES from SQL, switches runtime wiring, and deletes the old directories and retired SQL surfaces. A failed migration stops before cleanup and leaves the source files available for rerun; after successful cleanup rollback is by restoring from the SQL export/backups, not by re-enabling file reads.

## Risks / Trade-offs

- [Risk] Keyword-only retrieval can miss semantically related wording. → Keep the contract explicit, retain ES score observability, and require a future change for vectors rather than silently mixing modes.
- [Risk] ES or worker lag delays discovery. → SQL is immediately authoritative; outbox lease/retry metrics and p95 ≤5s freshness SLO expose lag without blocking runs.
- [Risk] Citation IDs can be hallucinated or stale. → Parse per line, enforce owner existence checks, drop invalid IDs and warn without failing the run.
- [Risk] Projection cleanup could remove mixed-evidence text incorrectly. → Store source references/version metadata and let consolidation perform surgical removal rather than direct string deletion.
- [Risk] Deleting Reflection and write-log surfaces is breaking. → Run migration validation and repository/reference scans before destructive schema cleanup; expose migration counts and checksums.
- [Risk] Large migration may contend with workers. → Use leased batches, idempotency keys, bounded transactions and pause normal consolidation during owner-level migration.

## Migration Plan

1. Apply additive SQL schema for artifacts, write jobs, usage fields, source validation and migration bookkeeping.
2. Import durable files and historical Reflection rows into SQL with owner/content/checksum validation.
3. Backfill unified Context keyword documents from SQL; verify score ordering and tenant filters.
4. Deploy runtime wiring for SQL summary and keyword `read_memory`; enable citation strip/usage accounting.
5. Enable unified write worker and observe queue failure, retry, DLQ and p95 freshness metrics.
6. Freeze legacy writers, run a final checksum comparison, then remove `DurableFileStore` reads, old files, `agent_reflections` surfaces and `memory_write_logs`.
7. Verify rollback assets are SQL export plus database/object backups; do not restore deleted file paths as an alternate runtime authority.
