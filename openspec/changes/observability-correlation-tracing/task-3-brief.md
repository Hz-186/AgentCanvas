# Task 3 Brief — Persist and restore async turn correlation metadata

Implement only Task 3 of `observability-correlation-tracing`. Task 1 provides `observability.Correlation` with `ConversationID`, `RunID`, `TurnID` as `int64`, `ParentRunID *int64`; Task 2 propagates request/owner correlation through HTTP context. Read this brief first; it is the exact contract.

Files in scope:

- Modify `internal/application/agent_usecase/service.go` and `turn_worker.go`.
- Create `internal/application/agent_usecase/service_test.go` and `turn_worker_test.go` only if those files do not already exist; preserve existing tests if they do.

Behavior and schema:

1. `StartTurn` must persist additive `InputJSON.observability` metadata on both Run and Turn. Use versioned object `{"version":1,"request_id":"..."}` and preserve existing `query`, `mode`, `manual_compaction`, and other fields. Include available owner/conversation/run/turn correlation IDs according to existing model shapes without overwriting unrelated keys.
2. Idempotent existing turns must return the existing object and perform zero create calls; existing metadata must not be overwritten.
3. `turnWorker` must load queued Run/Turn, decode supported metadata, restore a fresh standard context with request/owner/conversation/run/turn/parent IDs before runtime execution, and not depend on the original HTTP context.
4. Legacy input with no observability metadata must still execute using persisted owner/run/turn IDs, with empty request ID and no fabricated request ID.
5. Malformed metadata (non-object or unsupported version) must be ignored safely; preserve business input fields, continue runtime execution, and emit only a bounded parse diagnostic without changing turn business outcome.
6. Run load errors must prevent runtime execution and enter the existing failure/retry path; diagnostics should include run/turn identifiers without altering semantics.
7. Emit `turn.started`, `turn.finished`, and `turn.failed` diagnostics at existing lifecycle transitions with event/phase/result/latency and correlation metadata, while preserving durable state ordering.

Use the actual repository interfaces and current `RunIdentity`/runtime request structures; do not invent mocks for concrete services. TDD is mandatory: write focused tests first, observe RED, implement minimal code, GREEN, refactor. Do not modify logger/runtime packages in this task. Do not stage/commit (`auto_commit=false`).

Verification: `internal/application/agent_usecase` cannot compile natively on this Windows host because of pre-existing Unix-only syscalls in `workspace_usecase`/`toolruntime` (reproduced on base commit ff9eaf9; NOT yours to fix). Set up a scratch `go test -overlay` harness outside the repository that stubs the three flock/kill call sites, then run focused `go test ./internal/application/agent_usecase -run 'Test(StartTurnCorrelation|TurnWorkerCorrelation|TurnLifecycleDiagnostics)' -count=1` and package tests natively; additionally run `GOOS=linux GOARCH=amd64 go test -c ./internal/application/agent_usecase` and `GOOS=linux GOARCH=amd64 go vet ./internal/application/agent_usecase` as the cross-compile gate. Overlay stub files must never enter the working tree. Record all evidence in `openspec/changes/observability-correlation-tracing/task-3-report.md`.

If code facts conflict with this contract, stop and report `REVERSE_SYNC_REQUIRED` before changing artifacts or implementation.
