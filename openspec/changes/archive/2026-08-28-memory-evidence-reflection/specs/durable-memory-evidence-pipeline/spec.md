# Capability: durable-memory-evidence-pipeline

## Purpose

Defines the durable evidence contract for memory extraction: what counts as extractable evidence, how scheduling debounces per conversation, how long conversations are fully covered, and how extracted candidates pass deterministic gates into the unified write pipeline.

## ADDED Requirements

### Requirement: Tool error state is durably persisted

The system MUST persist `is_error` and `error_code` for every newly written `function_call_output` message row inside `metadata_json`. Rows written before this capability MUST be treated as `unknown` error state; the system MUST NOT infer success or failure from output text.

#### Scenario: Failed tool result is persisted with structured error state

- **GIVEN** a run step produces a tool result with `IsError=true` and error code `invalid_arguments`
- **WHEN** the message sink persists the `function_call_output` row
- **THEN** `metadata_json` contains `is_error=true` and `error_code="invalid_arguments"`

#### Scenario: Legacy row without error state

- **GIVEN** a `function_call_output` row whose `metadata_json` has no `is_error` key
- **WHEN** the evidence renderer reads it
- **THEN** the exchange unit carries error state `unknown`
- **AND** no text heuristics are applied to guess success or failure

#### Scenario: Transcript replay rebuilds identical metadata

- **GIVEN** a `function_call_output` row already persisted with `is_error`/`error_code` metadata under a `transcript_entry_id`
- **WHEN** the same transcript entries are persisted again for that entry id (checkpoint-resume replay)
- **THEN** the rebuilt row's `metadata_json` is byte-equal to the existing row (no transcript conflict)
- **AND** when no matching run step exists for a tool call id, no error keys are added and the row stays byte-compatible with legacy rows

### Requirement: Durable extraction reads complete conversation evidence

The durable extraction window read MUST include archived messages and MUST render tool call arguments and outputs. `reasoning`, `system_echo`, and developer/system injected content MUST be excluded. All evidence text, arguments, and outputs MUST be secret-redacted before entering the extraction prompt.

#### Scenario: Compacted history still reaches the extractor

- **GIVEN** a conversation where compaction archived messages 100–400 and the extraction window spans 100–600
- **WHEN** the durable worker reads the window
- **THEN** the worker's window read (`messagesThrough`) uses the archive-inclusive path
- **AND** archived rows 100–400 reach the evidence renderer and can be referenced by candidates
- **AND** the active-only read paths used by context building remain unchanged

#### Scenario: Secrets never reach the extraction model

- **GIVEN** evidence containing `api_key = "abcd1234efgh5678"`
- **WHEN** the prompt is assembled
- **THEN** the raw value is replaced by the existing redaction placeholder in the prompt input

### Requirement: Scheduling debounces per conversation

The system MUST maintain at most one active (pending) durable extraction job per conversation. A new run completion MUST refresh the pending row's `through_message_id` and `due_at` in place. If the latest job is running, the system MUST create exactly one successor keyed `durable:<owner>:<conversation>:after-job:<running-job-id>`. If the latest job is terminal or absent, a new row is created. Queue wakeup MUST be published only when a new row is created, never on `due_at` refresh.

#### Scenario: Active conversation keeps a single pending row

- **GIVEN** a conversation with a pending durable job
- **WHEN** three more runs complete within the idle window
- **THEN** the same job row is updated three times with advancing `through_message_id` and `due_at`
- **AND** no additional job rows are created
- **AND** no additional queue events are published

#### Scenario: New messages while extraction runs create one successor

- **GIVEN** the latest durable job is running
- **WHEN** two runs complete during the run
- **THEN** exactly one successor row exists for that running job id
- **AND** the running job's boundary is not modified

#### Scenario: Worker claims before the scheduler's locking read

- **GIVEN** a pending job that a worker claims and commits to `running` before the scheduler's locking read acquires the row
- **WHEN** the scheduler's transactional read observes the latest job
- **THEN** it observes `running` and creates exactly one successor keyed after that running job
- **AND** the unique idempotency key prevents duplicate successors

#### Scenario: Defensive fallback for implementations without locking reads

- **GIVEN** a repository implementation whose conditional pending-refresh update affects zero rows
- **WHEN** the scheduler observes the zero-row result
- **THEN** it falls back to successor creation instead of failing

### Requirement: Scheduling whitelist is explicit

Durable scheduling MUST trigger only for root runs ending with `final_answer`, `max_iterations_exceeded`, `max_tool_calls_exceeded`, or `timeout`. Runs ending `waiting_human`, `paused`, `cancelled`, `llm_error`, `tool_name_not_found`, `reflection_failed`, `context_overflow`, or `clarification_required`, and all sub-agent runs, MUST NOT schedule.

#### Scenario: Budget-exhausted run schedules

- **GIVEN** a root run ending with `max_iterations_exceeded` and memory enabled
- **WHEN** the run finalizes
- **THEN** `ScheduleDurableBoundary` is invoked exactly once

#### Scenario: Paused run does not schedule

- **GIVEN** a root run ending with `waiting_human`
- **WHEN** the run finalizes
- **THEN** no scheduling call occurs

### Requirement: Long conversations are extracted without losing the middle

Evidence MUST be chunked at whole evidence-unit boundaries under the existing 120000-byte single-chunk cap. A single oversized tool output MUST be sliced into consecutive fragments carrying `part_index`/`part_count` without dropping the middle. Adjacent chunks MUST overlap by two evidence units. Each chunk's candidates MUST be persisted into `result_json` as they complete so retries skip completed chunks. Multi-chunk jobs MUST run one lightweight merge pass over candidates and per-chunk summaries using the same extraction model.

#### Scenario: Failure evidence in the middle survives

- **GIVEN** a conversation whose failure loop sits in the middle third of a 3-chunk window
- **WHEN** extraction completes
- **THEN** candidates referencing the middle failure exist
- **AND** chunk 1 produced no duplicate of chunk 2's candidates after gating and dedup

#### Scenario: Retry after crash skips completed chunks

- **GIVEN** chunk 1 candidates already persisted in `result_json`
- **WHEN** the job is reclaimed after a crash
- **THEN** chunk 1 is not sent to the model again

### Requirement: Candidates pass deterministic quality gates

Each candidate MUST, before write: have non-blank `title` and `content`; `confidence >= 0.7`; `importance >= 0.5`; carry message-range or tool-call evidence references; have finite scores within `[0,1]`; and retain valid content after redaction. Candidates failing any gate MUST be dropped with a recorded reason. When all candidates are dropped or the model returns none, the job MUST complete normally with `ResultJSON.outcome = "no_output"` and write no memories. No new job status enum value is introduced.

#### Scenario: Chit-chat produces no_output

- **GIVEN** a window containing only greetings and one-off Q&A
- **WHEN** extraction and gating complete
- **THEN** zero memory rows are written
- **AND** `result_json` records `outcome="no_output"` and the job status is `completed`

#### Scenario: Verified failure lesson is written

- **GIVEN** a user correction plus a tool-verified failure and recovery in the window
- **WHEN** the model emits a candidate with confidence 0.85, importance 0.6, and tool-call evidence
- **THEN** the candidate passes all gates and is enqueued for writing

### Requirement: Extraction writes flow through unified write jobs

Gated candidates MUST be written through `memory_write_jobs` with `source=extraction` and idempotency key `extraction:<job-id>:<index>`. The memory `DeduplicationKey` for extraction-produced rows MUST be `hex(sha256(memory_type + "\n" + normalize(content)))`, reusing the existing unique `(owner_id, deduplication_key)` index for cross-job deduplication. Other sources (`ad_hoc`, `reflection`, `proposal`, `consolidation`, `manual`) MUST keep their existing dedup semantics.

#### Scenario: Same content extracted twice yields one memory

- **GIVEN** two different extraction jobs produce candidates with identical type and normalized content
- **WHEN** both write jobs commit
- **THEN** exactly one `memories` row exists for that owner and deduplication key

#### Scenario: Ad-hoc dedup semantics unchanged

- **GIVEN** an `ad_hoc` write job
- **WHEN** it writes a memory
- **THEN** its `DeduplicationKey` still defaults to the job idempotency key

### Requirement: Missing extraction model fails instead of dumping text

When no extraction model is configured, durable extraction MUST fail the job into the existing backoff/retry path. The system MUST NOT store redacted conversation text as `RawMemory` or a truncated summary as `RolloutSummary`.

#### Scenario: Unconfigured model

- **GIVEN** `durable_memory.enabled=true` with empty model config
- **WHEN** a durable job is processed
- **THEN** the job returns an error, attempt count increments, and backoff applies
- **AND** no `raw_memory` text dump is produced

### Requirement: Window start derives from the latest completed durable job

The extraction window start MUST be determined by a targeted lookup of the latest `completed` durable job for the conversation, replacing any bounded-scan shadowing. Windows MUST be read through the archive-inclusive read path.

#### Scenario: Earlier completed job bounds the window

- **GIVEN** the conversation's latest completed durable job has `through_message_id=500` and 250 unrelated completed jobs exist
- **WHEN** a new job extracts
- **THEN** the window starts after message 500 regardless of the unrelated jobs
