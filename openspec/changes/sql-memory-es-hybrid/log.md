# Log — sql-memory-es-hybrid

## 2026-08-27 apply resume

### Skills Loaded: using-superpowers, vsdd-workflow-router, vsdd-workflow-apply, vsdd-workflow-reverse-sync, vsdd-workflow-git-discipline, vsdd-workflow-implement-task, vsdd-workflow-review-implementation, test-driven-development, subagent-driven-development, using-git-worktrees

- Restored the serial apply checkpoint on branch `refactor/memory-usecase-cleanup`; Task 1 implementation exists in the working tree and is pending independent spec/code-quality review plus an executable build gate.
- `auto_commit: false`; the workflow will not stage, commit, push, or interrupt between tasks for commit handling.

### Review Evidence Task 1
- Stage: spec
- Subagent ID / turn: `/root/task1_spec_reviewer`
- Verdict: BLOCKED
- Findings: Critical source constraint compatibility; Important lease equality and stale integration SQL; repository idempotency/ordering coverage incomplete. Reflection `recall_count` finding was adjudicated non-applicable because it targets `agent_reflections`, which migration 000016 does not rename.

### Review Evidence Task 1
- Stage: code-quality
- Subagent ID / turn: `/root/task1_quality_reviewer`
- Verdict: BLOCKED
- Findings: source CHECK/default can break existing rows and writes; lease eligibility/order mismatch; repository tests and write validation incomplete; mutable canonical slices are a minor issue.

## 2026-08-27 explore

| 事件 | 说明 |
| --- | --- |
| Skills Loaded | brainstorming, vsdd-workflow-router, openspec-explore；explore 阶段完成决策树穷举 |
| Handoff | `openspec/changes/sql-memory-es-hybrid/exploration-handoff.md` 已落盘 |
| User decisions | 独立完整 change；SQL 原子记忆分条 + artifacts；Reflection 全量吸收并删除旧表/API；复用统一 Context；永久纯关键词；读入口 SQL summary / ES keyword + SQL hydration；citation strip/owner 校验/行级容错；统一 `memory_write_jobs`；p95 ≤5s；保持 1200/5/20/6000；30 天/256 usage 生命周期；迁移后删除旧文件；命名与 source 枚举已固定；skills 归 skill 子系统；退役 `memory_write_logs` |

## 2026-08-27 propose

| 事件 | 说明 |
| --- | --- |
| OpenSpec | `openspec new change` 因 Explore handoff 预先占用目录而重复拒绝；按 propose fallback 创建 `.openspec.yaml`，随后执行 `openspec status/instructions` 获取正式输出约束 |
| Reverse Sync | 已将附件 R2 的 0.7/0.3 与用户最终纯关键词 B 决策标为显式覆盖；未发现需阻塞的代码事实冲突 |
| Draft artifacts | proposal/specs/design/tasks 已生成，待 standard design review |
| Dependency graph | Task 1 → Tasks 2/4/6 → Tasks 3/5/7 → Task 8；共享 SQL/runtime wiring 的任务保持串行 |
| Mode | `complexity_mode=standard`，`execution_mode=serial`，`active_review_stage=design_review` |
| Design review round 1 | PASS；按 reviewer 建议补充 design.md 的 memory_id tie-break 与 citation warning 语义 |
| Design review round 2 | PASS；proposal/specs/design/tasks 一致，无 Must Fix；standard 门禁满足 |
| Verification | `openspec validate sql-memory-es-hybrid --type change --strict --json` PASS；8 tasks / 40 RED entries / dependency section verified；`git diff --check` clean |
| Design review correction | Round 2 targeted review found `manual` source omitted from unified producer contract；回写 proposal/spec/design/tasks，补充 `manual` RED/ASSERT/DoD，等待定向复核 |
| Design review targeted re-review | PASS；`manual` 已在 proposal/spec/design/tasks 闭合为第六类 `memory_write_jobs` producer，全部一致 |

### Review Evidence Task 1 (re-review round 1)
- Stage: spec (scoped re-review of fix round 1)
- Subagent ID: agent_797754d2-b329-4c36-a5a6-78446328749e
- Verdict: BLOCKED (findings remain open)
- Findings: F1 agent_tool source ADDRESSED; F2 lease equality/ordering ADDRESSED; F3 integration-test maps inverted (NOT ADDRESSED, new Important breakage at agent_runtime_integration_test.go:576,601 — harness only applies 000001-000010, so maps must keep 000008-era names); F4 repository idempotency/claim-ordering tests absent; spec gap: 000016 not wired into migration harness.

### Review Evidence Task 1 (re-review round 1)
- Stage: code-quality (scoped re-review of fix round 1)
- Subagent ID: agent_5aad10b8-33fb-4f34-9960-04e57452d79f
- Verdict: BLOCKED (findings remain open)
- Findings: F1 source CHECK/normalization ADDRESSED; F2 lease boundary/ordering/argument validation ADDRESSED; F3 repo create/update validation ADDRESSED in code; F4 mutable canonical slices NOT ADDRESSED (Minor, parked); new Important breakage same as spec review (agent_runtime_integration_test.go:576,601 inverted swap fails harness at 000008 checkpoints); out-of-scope minors (schema_contract_test.go:45 key-rule isolation, cosmetic double space runtime_service_test.go:121).

### Task 1 close-out (2026-08-27)
- Fix round 2 landed: integration-test maps restored to 000008-era names; 000016 up/down wired into the migration harness with state assertions; sqlmock repository tests added (5 tests) + go-sqlmock v1.5.2; owner-fixture minor folded in.
- Controller verification: `go test -count=1 ./internal/domain/memory/...` ok; `GOOS=linux GOARCH=amd64 go test -c ./internal/infrastructure/mysql/` exit 0; vet/gofmt clean.
- Round-2 scoped re-review waived by user directive; round-1 re-review findings 1/2 addressed, findings 3/4 covered by fix round 2 with probe-run evidence (identical sqlmock tests pass natively in throwaway module, 5/5).
- auto_commit: false — no commits made.

### Task 2 close-out (2026-08-27)
- Implemented `internal/application/memory_usecase/memory_write_pipeline.go`: unified envelope (`WriteJobRequest`), fail-open `Enqueue`, `ProcessNext` claim-one/retry-backoff(1min..6h, cap 8 attempts)/DLQ, `SQLMemoryWriter` (single repo transaction commit boundary; dedup key = job idempotency key), `AdHocWriteJobAdapter` (source ad_hoc, key `ad_hoc:<runID>`), worker seam `WriteJobWorker`.
- Wired: `internal/bootstrap/app.go` (AdHocNotes/AdHocMemoryNoteWriter → job adapter; file reads stay on DurableFileStore), `cmd/worker/main.go` (drain loop after durable worker).
- RED: compile-fail list (undefined NewMemoryWritePipeline/WriteJobRequest/NewAdHocWriteJobAdapter). GREEN: `go test ./internal/application/memory_usecase` ok (6/6 pipeline tests). Cross-compile exit 0: agentruntime, agent_usecase, cmd/worker, bootstrap.
- Review: lightweight controller review PASS (user-directed lighter process) — fail-open verified at execution.go:451-456; MemoryRepository.Create verified transactional (memory row + context outbox in one transaction, OnConflict DoNothing on deduplication_key); nil-writer bootstrap path never dereferences writer.
- auto_commit: false — no commits made.

### Task 3 close-out (2026-08-27)
- `consolidation_projection.go`: protected-input selection, version-diff-first single consolidation call, two versioned artifacts (handbook/summary) with checksum+source refs, retryable failures, no file fallback. Phase-2 file writes removed; `DurableFileStore` reduced to a read-only seam (read rewiring deferred to Tasks 4/5 by design).
- RED: 5 failing projection tests (missing service). GREEN: `go test ./internal/application/memory_usecase` ok. `GOOS=linux go test -c ./cmd/worker` exit 0. Grep: zero production writes of MEMORY.md/memory_summary.md/raw_memories.md.
- Review: lightweight controller review PASS (user-directed lighter process). Concerns parked: removal-only diffs deferred to Task 7; identity-based diff misses content edits (final-review note); artifact pair writes converge on retry.
- auto_commit: false — no commits made.

### Task 4 close-out (2026-08-27)
- `readKeywordMemory` in runtime_service.go is the sole Agent-facing detail read path: ES keyword → SQL hydration, score order with numeric ID tie-break, owner/scope/status/conflict/expiry re-checks, 6000-char truncation, limit 5/20. Vector/archival fallbacks removed; `Retriever` retained for write-path conflict detection only; `ContextBackendIndex` hybrid mode retained for conversation-message recall (out of scope).
- RED: 5 failing read tests (hybrid path fired vector; ES error degraded to full-table scan). GREEN: `go test ./internal/domain/memory ./internal/infrastructure/retrieval/...` ok. `GOOS=linux go test -c ./internal/runtime/toolruntime` exit 0; full `GOOS=linux go build ./...` exit 0.
- REVERSE_SYNC adjudicated: pre-existing stack contradicted the approved pure-keyword design; removal is spec-compliant, no artifact change needed.
- Review: lightweight controller review PASS (user-directed lighter process).
- auto_commit: false — no commits made.

### Task 5 close-out (2026-08-27)
- Automatic summary block reads SQL `summary` artifact via `MemoryArtifactRepository.Latest` (1200-token bound, delegated-run zero-call gate, nil block on failure, no file fallback); `MemoryFiles`/`DurableFileStore` removed from Agent-facing reader wiring and bootstrap. Skills: new `skill.Retriever` + skill_usecase retriever (owner-scoped, limit 3/5); `read_memory` no longer searches skills.
- RED: 12 compile errors (missing MemoryArtifacts/SkillRetriever fields). GREEN: agentruntime/toolruntime/bootstrap linux `go test -c` exit 0; full `GOOS=linux go build ./...` exit 0; behavioral proof via throwaway module 7/7 PASS; native skill_usecase/domain tests ok.
- Review: lightweight controller review PASS. Concern benign: `skill_search` output drops `score` key — no production consumer found.
- auto_commit: false — no commits made.

### Task 6 close-out (2026-08-27)
- `internal/domain/memory/citation.go`: line-tolerant parser + `AccountCitations` (strip → owner-validate → per-run dedupe → one bulk `MarkUsed`; per-line warnings, never fails the run); `StripCitations` pure export. Finalizer wired in `execution.go` (strips FinalAnswer + StepTypeFinalAnswer + StepTypeLLMResponse contents).
- Fix round 1 (spec gap found by implementer): runner persisted assistant transcript rows with raw citations before finalization — now `persistTranscriptEntries` sanitizes assistant rows at every persist site; `result.FinalAnswer` stays raw so accounting still sees the block; in-memory transcript/checkpoint stay raw (internal state); live streaming remains transient.
- RED: 5 compile-fail tests. GREEN: `go test ./internal/domain/memory` ok (6/6); `GOOS=linux go test -c ./internal/runtime/agent ./internal/runtime/agentruntime` exit 0; full `GOOS=linux go build ./...` exit 0.
- Review: lightweight controller review PASS (user-directed lighter process).
- auto_commit: false — no commits made.

### Task 7 close-out (2026-08-27)
- `internal/application/memory_usecase/lifecycle.go`: usage-then-recency deterministic selection (SQL: usage_count DESC, COALESCE(last_used_at, updated_at) DESC, id ASC), 30-day cold exclusion, consolidated-protection retention, 256 cap, zero LLM scoring; prune returns deleted IDs for surgical consolidation cleanup (`RemovePrunedSources`). Config defaults `lifecycle_cold_window_days=30`, `lifecycle_selection_cap=256`.
- Fix round 1 (canonical mapping adjudicated): `SourceRefsJSON.source_id` = `memories.id` — consolidation inputs now read memory rows via `ListBySources` (source ad_hoc/extraction), job-ID-based input paths removed. Extraction evidence without a memory row excluded until Task 2/Task 8 wires the extraction producer.
- RED: 5 lifecycle tests compile-fail. GREEN: domain/memory + memory_usecase ok (26 tests); `GOOS=linux go test -c ./internal/infrastructure/mysql` exit 0; full `GOOS=linux go build ./...` exit 0; linux vet exit 0.
- Review: lightweight controller review PASS (user-directed lighter process).
- auto_commit: false — no commits made.

### Task 8 close-out (2026-08-27)
- Migration importer (`memory_usecase/migration.go`): deterministic import of MEMORY.md / memory_summary.md / raw_memories.md / rollout_summaries/ / extensions/ad_hoc/ into the five artifact kinds via `MemoryArtifactRepository`; skill files hand off to the skill subsystem (no memory artifact); sha256 checksum + owner tenancy gate deletion (aborts before any write, reports exact offending path); rerunnable via `FileProgressStore` persisted outside the durable root.
- Reflection conversion (`reflection_usecase/conversion.go`): historical validated/disputed rows → ordinary memories, source `reflection`, status mapping active/revoked/superseded, metadata preserved; `mysql/legacy_reflection_reader.go` is the only remaining `agent_reflections` reader (migration-only).
- Producer rewiring: `finalizeReflection` now enqueues a normal write job (source `reflection`, key `reflection:run:<runID>`) via `TerminalReflectionWriter`; approved improvement proposals enqueue source `proposal` jobs (`ProposalMemoryWriter`, aliases keep call sites unchanged); inline reflection remains runtime-only.
- Cleanup stage (`memory_usecase/cleanup.go`): destructive retirement runs only after migration validation + ES backfill validation (`ValidateContextBackfill`); each action exactly once via progress store; failure ⇒ zero DROP/delete and sources stay rerunnable.
- Retired surfaces: reflection Service/Worker/QueueRuntime, reflection mysql repos + event sink + NATS transport + HTTP handler/routes, ReflectionQueue config, ReflectionSystem health check, ReflectionSemanticIndex, `memory_write_logs` model/repository, v1 ES memory store (`memory_retrieval.go`), `TypeReflection` context resource kind. Migration 000017 drops 6 tables with valid .down.
- RED: 5 TDD tests (compile-fail evidence captured). GREEN: `go test ./internal/application/reflection_usecase ./internal/application/memory_usecase` both ok (5/5 targeted); domain/memory, domain/reflection, observability, health_usecase native ok; `GOOS=linux go test -c` exit 0 for mysql/http/agent_usecase/agentruntime/toolruntime/bootstrap/pkg/config/runtime/agent (pre-existing Flock block on Windows); full `GOOS=linux go build ./...` exit 0; `go vet ./...` exit 0.
- Controller verification (lightweight): native tests re-run ok (reflection_usecase/memory_usecase/domain/memory), full linux build exit 0, cleanup gate + checksum gate read-verified, diffstat 65 files +1566/−5645, retired-surface scan clean (only migration seams + inline-only metrics/step types retained).
- Review: lightweight controller review PASS (user-directed lighter process).
- Notes: 2 pre-existing Windows-native pkg/config test failures (non-absolute `/workspaces` paths; pass under linux); stray `go fmt` line-ending normalization during verification fully restored (diff unchanged).
- auto_commit: false — no commits made.
