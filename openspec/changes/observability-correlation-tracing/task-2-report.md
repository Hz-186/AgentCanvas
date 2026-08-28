# Task 2 report — HTTP correlation middleware and diagnostics

## Changed files

- `internal/interface/http/middleware/request_id.go`
- `internal/interface/http/middleware/auth.go`
- `internal/interface/http/middleware/recovery.go`
- `internal/interface/http/middleware/access_log.go`
- `internal/interface/http/middleware/request_id_test.go`
- `internal/interface/http/middleware/access_log_test.go`
- `internal/interface/http/middleware/recovery_test.go`
- `internal/interface/http/router.go`
- `internal/interface/http/router_observability_test.go`

## RED

Added focused behavior tests first for request ID propagation/generation, unauthenticated auth behavior using a real `authusecase.Service` and fake dependencies, access event metadata, recovery event metadata, and router panic/event ordering.

Command:

```powershell
go test ./internal/interface/http/middleware -run 'Test(RequestID|AccessLog|Recovery|Auth)' -count=1
```

Initial outcome: blocked before compilation because PowerShell reported `go : The term 'go' is not recognized` and `where.exe go` found no executable. After locating the bundled toolchain at `D:\Users\hongze01.zhang\AppData\Local\Temp\agentcanvas-go122full\go\bin\go.exe`, the focused middleware tests initially failed on an assertion expecting `int` status while `slog.Any` correctly returned `int64`; the test assertion was corrected, then the focused middleware suite passed.

## GREEN implementation

- Request IDs now remain unmodified and are written to Gin context, the response header, and `observability.Correlation` in the request's standard context.
- Successful authentication updates the same standard context with `OwnerID`; unauthenticated paths return through their pre-existing abort behavior without adding an owner.
- `AccessLog` emits one body-free `http.access` event after downstream processing with `phase`, `result`, `route`, `status`, `latency_ms`, and available correlation fields.
- `Recovery` produces a body-free `http.error` event with `phase=http`, `result=error`, error class, and available request/owner IDs, then retains the existing JSON 500 abort behavior.
- Global router order is `RequestID -> AccessLog -> Recovery -> CORS`.

## REFACTOR / self-review

- Kept correlation extraction at the middleware boundary and only log allowlisted request metadata; no authorization header, API/API bearer token, or request body is logged.
- Used nil-safe logger fallbacks to retain router compatibility with tests/build paths that omit a logger.
- The router test verifies that recovery emits before access (the expected nesting consequence of the declared middleware order) and both events retain the client request ID.
- `git diff --check` completed successfully; it reports only pre-existing/unrelated worktree-file line-ending warnings, not whitespace errors.

## Verification

```powershell
D:\Users\hongze01.zhang\AppData\Local\Temp\agentcanvas-go122full\go\bin\go.exe test ./internal/interface/http/middleware -run 'Test(RequestID|AccessLog|Recovery|Auth)' -count=1
# PASS (1.5s)

D:\Users\hongze01.zhang\AppData\Local\Temp\agentcanvas-go122full\go\bin\go.exe test ./internal/interface/http/middleware -count=1
# PASS (1.8s)
```

The combined focused command and all `internal/interface/http` tests, plus vet, reach unrelated Windows compilation failures in existing packages:

```text
internal/application/workspace_usecase: undefined syscall.Kill, syscall.Flock, syscall.LOCK_EX, syscall.LOCK_UN
internal/runtime/toolruntime: undefined syscall.Flock, syscall.LOCK_EX, syscall.LOCK_UN
```

No Task 2 files are implicated by these errors.

## Required verification still blocked

The following commands cannot complete until the existing Windows `syscall` portability errors are resolved (or a compatible build environment is used):

```powershell
go test ./internal/interface/http/middleware ./internal/interface/http -run 'Test(RequestID|AccessLog|Recovery|Auth|RouterObservability)' -count=1
go test ./internal/interface/http/middleware ./internal/interface/http -count=1
D:\Users\hongze01.zhang\AppData\Local\Temp\agentcanvas-go122full\go\bin\go.exe vet ./internal/interface/http/middleware ./internal/interface/http
```

## Concerns

1. `internal/interface/http` package tests and vet are blocked by pre-existing Windows `syscall` compile errors in workspace/toolruntime packages.
2. No files were staged or committed (`auto_commit=false`).

## Fix round 1 evidence

Per `task-2-fix-brief.md`, regression assertions were added first for explicit `event` attributes and recovery `route/status/latency_ms` fields.

RED command:

```powershell
D:\Users\hongze01.zhang\AppData\Local\Temp\agentcanvas-go122full\go\bin\go.exe test ./internal/interface/http/middleware -run 'Test(RequestID|AccessLog|Recovery|Auth)' -count=1
# FAIL: access/recovery records lacked the newly asserted event and route/status/latency fields
```

GREEN/refactor commands:

```powershell
D:\Users\hongze01.zhang\AppData\Local\Temp\agentcanvas-go122full\go\bin\gofmt.exe -w internal/interface/http/middleware/access_log.go internal/interface/http/middleware/recovery.go internal/interface/http/middleware/access_log_test.go internal/interface/http/middleware/recovery_test.go internal/interface/http/router_observability_test.go
D:\Users\hongze01.zhang\AppData\Local\Temp\agentcanvas-go122full\go\bin\go.exe test ./internal/interface/http/middleware -run 'Test(RequestID|AccessLog|Recovery|Auth)' -count=1
# PASS
D:\Users\hongze01.zhang\AppData\Local\Temp\agentcanvas-go122full\go\bin\go.exe test ./internal/interface/http/middleware -count=1
# PASS
D:\Users\hongze01.zhang\AppData\Local\Temp\agentcanvas-go122full\go\bin\go.exe vet ./internal/interface/http/middleware
# PASS
```

Implementation now emits `event=http.access` and `event=http.error`; recovery records `phase`, `result`, `route`, final status, non-negative latency, error class, and available correlation IDs. Router order and privacy behavior are unchanged. Successful-auth owner propagation remains covered by the existing real-Service fixture path in `request_id_test.go` only for unauthenticated fallback; a dedicated successful JWT fixture was deferred because the current task's real auth fixture requires additional unrelated repository setup and is not needed to implement the reviewed findings.

Linux compile exit=0; vet exit=0 (cross-compile evidence).

## Review fix round 1 requested

Spec reviewer found that `http.access`/`http.error` must include an explicit `event` attribute, and recovery events must include route, status, and latency_ms. The original implementation used only the slog message for the event name and omitted these recovery fields. Fix is delegated to the Task 2 implementer with regression tests.
