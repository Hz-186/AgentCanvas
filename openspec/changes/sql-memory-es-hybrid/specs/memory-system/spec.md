## Purpose

Defines a single SQL-first memory contract for durable entries, retrieval, asynchronous writes, citation usage accounting, lifecycle, and Reflection absorption across AgentCanvas runtime and workers.

## ADDED Requirements

### Requirement: SQL is the sole memory fact source

The system MUST store each atomic durable memory as one row in SQL `memories`, and MUST use SQL `memory_artifacts`/extraction records for handbook, summary, raw input, rollout summary, and ad-hoc provenance. ES MUST be treated only as a rebuildable retrieval replica; any ES hit MUST be hydrated and authorization-checked from SQL before it is returned.

#### Scenario: Atomic memory is stored as one SQL row

- **GIVEN** an extraction produces the fact “the user prefers concise answers”
- **WHEN** the asynchronous write job commits the fact
- **THEN** SQL contains one `memories` row with content, scope, source, provenance and lifecycle fields
- **AND** the fact is not required to be embedded in a single owner-wide Markdown file

#### Scenario: ES is stale or missing

- **GIVEN** SQL contains a recallable memory but its ES document is absent or stale
- **WHEN** a read request is evaluated
- **THEN** SQL remains the authoritative record and the index worker can rebuild the ES document
- **AND** a stale ES hit MUST NOT override SQL status, scope, content or deletion state

### Requirement: All legacy durable artifacts have an explicit SQL destination

The system MUST map `MEMORY.md` to a versioned owner-level `handbook` artifact, `memory_summary.md` to a versioned owner-level `summary` artifact, `raw_memories.md` to SQL extraction raw input/result records, each `rollout_summaries` file to a provenance-bearing `rollout_summary` record, and each ad-hoc note to an asynchronous input record that can produce ordinary memory rows. `skills` MUST NOT be classified as memory artifacts; the skill subsystem owns its read path.

#### Scenario: Consolidation persists projections

- **GIVEN** a consolidation job has new extraction inputs and a prior handbook version
- **WHEN** consolidation succeeds
- **THEN** SQL stores a new versioned handbook and summary artifact plus provenance to the selected memory rows
- **AND** no filesystem artifact is required for future Agent reads

#### Scenario: Skills are read by the skill subsystem

- **GIVEN** a user asks for a reusable workflow that is stored as a skill
- **WHEN** the runtime resolves the request
- **THEN** the skill subsystem handles retrieval
- **AND** `read_memory` MUST NOT claim ownership of `skills` as a memory artifact type

### Requirement: Retrieval uses permanent keyword-only ES search with SQL hydration

The system MUST use the unified Context ES keyword index with `vector_weight=0` as the permanent retrieval contract. Keyword results MUST be ordered by ES `_score` descending with a deterministic `memory_id` tie-break, then hydrated from SQL with owner, agent, project and conversation scope checks. The system MUST preserve default limit 5, maximum limit 20, and maximum 6000 characters per returned entry.

#### Scenario: Keyword query returns ranked memories

- **GIVEN** an owner-scoped query matches three indexed memories with ES scores 4.2, 2.8 and 1.1
- **WHEN** `read_memory` executes
- **THEN** the response preserves that score order after SQL hydration
- **AND** no vector query is required or invoked

#### Scenario: Scope prevents cross-tenant hydration

- **GIVEN** ES returns a memory ID belonging to another owner or disallowed scope
- **WHEN** SQL hydration validates the hit
- **THEN** the memory is omitted and its usage is not incremented

#### Scenario: Limits and truncation are preserved

- **GIVEN** the caller omits `limit` or requests a value above 20
- **WHEN** `read_memory` executes
- **THEN** the effective limit is 5 for an omitted/invalid value and never exceeds 20
- **AND** each returned content is truncated to at most 6000 characters

### Requirement: Automatic summary and detailed read remain semantically equivalent

The system MUST read the automatic top-level summary from the SQL `summary` projection only for non-delegated top-level runs, preserve the 1200-token budget and advisory positioning, and include a concise instruction explaining when to call `read_memory` and that summaries may be stale. `read_memory` MUST use the same owner authorization model as the summary and MUST NOT read retired durable-memory files.

#### Scenario: Top-level run receives bounded advisory summary

- **GIVEN** memory is enabled and the run has no parent or delegation depth
- **WHEN** runtime assembly builds context
- **THEN** it injects the bounded SQL summary with advisory wording and the read_memory freshness guidance

#### Scenario: Delegated run receives no automatic summary

- **GIVEN** the run is delegated or has a parent run
- **WHEN** runtime assembly builds context
- **THEN** no automatic memory summary block is injected

### Requirement: Memory writes are unified and non-blocking

The system MUST enqueue ad-hoc, extraction, consolidation, proposal and reflection-derived writes through `memory_write_jobs`. The main Agent run MUST NOT synchronously write files, wait for LLM consolidation, wait for SQL/ES indexing, or fail because enqueue, worker, SQL, LLM or ES processing failed. A successful SQL commit MUST enqueue context index work for eventual ES projection.

#### Scenario: Successful run enqueues without waiting

- **GIVEN** a final answer contains explicit memory intent
- **WHEN** the run finalizes
- **THEN** it records an idempotent `memory_write_jobs` input and returns without waiting for consolidation or ES refresh

#### Scenario: Queue or worker failure is fail-open to the run

- **GIVEN** enqueue or downstream processing returns an error
- **WHEN** the user run has otherwise produced a successful final answer
- **THEN** the run remains successful
- **AND** the error is observable and eligible for retry/DLQ handling

#### Scenario: Freshness target is best effort

- **GIVEN** SQL commit succeeds and the context worker is healthy
- **WHEN** the outbox and ES keyword update complete
- **THEN** the new memory SHOULD be searchable within p95 5 seconds
- **AND** a synchronous ES refresh MUST NOT be required for correctness

### Requirement: Citation self-report drives usage accounting

The system MUST support a Codex-compatible `<oai-mem-citation>` block containing stable memory IDs and provenance. Before presenting the final answer, the system MUST strip the block from visible text. The parser MUST process valid lines independently, discard malformed lines without rejecting the whole block, verify each ID exists and belongs to the current owner, and increment `usage_count`/`last_used_at` once per memory per run/thread. Returned-but-unused memories MUST NOT be counted.

#### Scenario: Valid citation is stripped and counted

- **GIVEN** the final answer cites two current-owner memory IDs
- **WHEN** finalization parses the citation
- **THEN** the visible answer excludes the citation block
- **AND** each cited memory is counted once for the run/thread

#### Scenario: Malformed citation line is isolated

- **GIVEN** a citation block contains two valid lines and one malformed line
- **WHEN** the parser processes it
- **THEN** the two valid lines are accounted for
- **AND** the malformed line is dropped with an observable warning

#### Scenario: Foreign or stale IDs cannot gain usage

- **GIVEN** a citation names a missing ID or an ID owned by another tenant
- **WHEN** ownership validation runs
- **THEN** that ID is silently ignored for usage accounting
- **AND** the system records a warning without failing the run

### Requirement: Lifecycle is usage-driven and protects consolidated evidence

The system MUST use only `usage_count` and `last_used_at` for usage-driven promotion/demotion, with `source_updated_at` as the never-used fallback. By default, consolidation selection MUST consider a 30-day window and at most 256 entries, order by usage then recency, protect entries already represented in handbook/summary, and remove stale unprotected inputs through source-aware cleanup. The system MUST NOT use LLM quality scores or static quality fields for this lifecycle.

#### Scenario: Frequently cited memory is selected first

- **GIVEN** two eligible memories have usage counts 8 and 2
- **WHEN** consolidation selects up to 256 entries
- **THEN** the count-8 memory is ordered first
- **AND** recency breaks an equal-count tie

#### Scenario: Cold unprotected input is pruned

- **GIVEN** an input has no usage within 30 days and is not represented in a protected projection
- **WHEN** retention pruning runs
- **THEN** it is excluded from future consolidation and becomes eligible for deletion

#### Scenario: Protected consolidated evidence survives coldness

- **GIVEN** a memory was represented in handbook/summary and later receives no usage
- **WHEN** retention pruning runs
- **THEN** the source row is protected from direct deletion
- **AND** any stale handbook text is removed only through source-aware consolidation cleanup

### Requirement: Reflection is absorbed into ordinary memory

The system MUST not expose Reflection as a distinct memory type, retrieval path or lifecycle. Inline reflection MUST remain runtime-only unless selected as extraction evidence; terminal reflection analysis MUST produce ordinary memories with `source=reflection`; approved improvement proposals MUST use `source=proposal`; historical `agent_reflections` MUST be migrated with provenance metadata and deleted, together with its independent API, index and worker, after validation.

#### Scenario: Reflection candidate becomes a normal memory

- **GIVEN** the terminal worker identifies a reusable lesson with evidence
- **WHEN** the unified extraction/write pipeline persists it
- **THEN** SQL contains a normal memory row with source `reflection`
- **AND** retrieval uses the same keyword memory path as every other memory

#### Scenario: Historical reflection migration completes

- **GIVEN** a historical `agent_reflections` row passes owner/content/provenance validation
- **WHEN** the migration finishes
- **THEN** an equivalent ordinary memory row exists with preserved metadata
- **AND** the old table and independent Reflection surface are removed

### Requirement: Source vocabulary and retired write log are explicit

The system MUST restrict memory `source` values to `extraction`, `ad_hoc`, `proposal`, `consolidation`, `reflection` and `manual`. The obsolete `memory_write_logs` table, model, repository and production call sites MUST be removed; write-job state and artifact provenance MUST replace it.

#### Scenario: Unknown source is rejected

- **GIVEN** a write request supplies a source outside the fixed vocabulary
- **WHEN** the worker validates the job
- **THEN** the job is rejected with a validation error
- **AND** no SQL memory row is created

#### Scenario: Retired write log is absent after migration

- **GIVEN** the schema migration and code cleanup have completed
- **WHEN** repositories and runtime wiring are inspected
- **THEN** `memory_write_logs` is absent and no production dependency references it

## Verification: 验收

- [ ] SQL schema and Go models represent one-row atomic memories, versioned artifacts, write jobs, usage fields and fixed sources.
- [ ] Agent-facing reads no longer use `DurableFileStore`; keyword ES score order and SQL scope hydration are tested.
- [ ] Queue failure is fail-open to successful runs; SQL-to-ES outbox retry and p95 freshness instrumentation exist.
- [ ] Citation strip, line-level tolerance, owner validation and per-run deduplicated usage are tested.
- [ ] Usage-driven 30-day/256-entry lifecycle and consolidated protection are tested without quality scoring.
- [ ] Reflection migration and deletion of old table/API/index/worker plus `memory_write_logs` cleanup are validated.
