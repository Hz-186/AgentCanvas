# Task 2 Brief — HTTP correlation middleware and diagnostics

Implement only Task 2 of `observability-correlation-tracing`. Read this brief first; it is the exact task contract. Task 1 now provides `observability.Correlation` with domain IDs as `int64`/`*int64` and immutable context helpers.

Files in scope:

- Modify `internal/interface/http/middleware/request_id.go`, `auth.go`, `recovery.go`, and `internal/interface/http/router.go`.
- Create `internal/interface/http/middleware/access_log.go`, `request_id_test.go`, `access_log_test.go`, `recovery_test.go`, and `internal/interface/http/router_observability_test.go`.

Behavior:

1. Request ID middleware reuses a non-empty `X-Request-ID` or generates a non-empty ID when missing. It must write the same ID to Gin context, standard `context.Context` via Task 1, and response header; incoming IDs are not rewritten.
2. Auth middleware must put authenticated owner identity into the standard correlation context when authentication succeeds. On unauthenticated requests, leave owner absent and preserve existing scope-denial behavior. Tests should construct the real `authusecase.Service` with fake token/repository dependencies; do not require injecting a mock into its concrete pointer signature. Never log authorization/API keys/full request bodies.
3. Add context-aware access logging. A downstream 201 response must emit `http.access` with `phase=http`, `result=ok`, route, status=201, latency_ms >= 0, and correlation request ID. Use the project logger conventions and privacy-safe attributes.
4. Recovery middleware must convert downstream panic to HTTP 500, emit `http.error` with `phase=http`, `result=error`, request/owner correlation, and prevent downstream continuation after panic.
5. Wire router middleware in this order: RequestID → AccessLog → Recovery (while preserving existing middleware behavior). Add a router test proving the wiring/order and that a panic yields status 500 plus request_id in the emitted event.

TDD is mandatory: write focused tests first and observe expected RED failures, implement minimal production changes, then GREEN and refactor while green. Preserve existing auth and router tests. Run the focused command from tasks plus package tests and go vet as available. Do not modify application/runtime packages outside the listed files. Do not git add/commit (`auto_commit=false`).

Report path: `openspec/changes/observability-correlation-tracing/task-2-report.md`. Include changed files, RED/GREEN/REFACTOR commands/outcomes, self-review, and concerns. If project facts conflict with this contract, stop and report `REVERSE_SYNC_REQUIRED` rather than silently changing the API.
