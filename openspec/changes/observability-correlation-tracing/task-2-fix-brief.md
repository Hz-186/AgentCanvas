# Task 2 Fix Round 1 Brief

Fix only the open Task 2 review findings in the existing HTTP observability implementation.

Required changes:

1. `http.access` and `http.error` slog records must include an explicit `event` attribute with the event name, in addition to the slog message if retained.
2. Recovery-generated `http.error` must include `phase=http`, `result=error`, `route`, final `status` (500), `latency_ms >= 0`, `error_class`, and available request/owner correlation fields.
3. Add/adjust regression tests to assert these attributes. Add a focused successful-auth test if feasible within existing real `authusecase.Service` fixture patterns, proving owner_id reaches standard correlation context; otherwise document why it is deferred as Minor.

Preserve router order, response schema, privacy, and existing behavior. TDD remains mandatory: add/adjust tests first and capture RED, then implement and run GREEN plus gofmt. Do not change files outside Task 2 scope. Do not stage or commit. Append fix evidence to `openspec/changes/observability-correlation-tracing/task-2-report.md`.
