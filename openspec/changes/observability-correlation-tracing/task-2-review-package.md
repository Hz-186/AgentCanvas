# Task 2 Review Package

Requirements: `task-2-brief.md`; implementation evidence: `task-2-report.md`; governing spec/design: `specs/correlated-agent-observability/spec.md` and `design.md`.

Review only these production/test changes:

- `internal/interface/http/middleware/request_id.go`
- `internal/interface/http/middleware/auth.go`
- `internal/interface/http/middleware/recovery.go`
- `internal/interface/http/middleware/access_log.go`
- `internal/interface/http/middleware/request_id_test.go`
- `internal/interface/http/middleware/access_log_test.go`
- `internal/interface/http/middleware/recovery_test.go`
- `internal/interface/http/router.go`
- `internal/interface/http/router_observability_test.go`
- Task 1 dependency: `internal/pkg/observability/correlation.go`

Verification evidence:

- Native Windows `go test ./internal/interface/http/middleware -count=1` PASS.
- `GOOS=linux GOARCH=amd64 go test -c ./internal/interface/http` exit 0.
- `GOOS=linux GOARCH=amd64 go vet ./internal/interface/http/middleware ./internal/interface/http` exit 0.
- Native Windows execution of the HTTP package is blocked by pre-existing Unix-only `syscall.Kill/Flock` in unrelated transitive packages.

Reviewers must inspect the files directly. Do not edit code. Return PASS only when no Critical, Important, or DESIGN_ISSUE remains; cite file:line for findings.
