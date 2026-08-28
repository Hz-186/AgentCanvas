# Task 1 Report: Correlation context

## Changed files

- `internal/pkg/observability/correlation_test.go` — behavior-first tests for round trip, nil contexts, presence semantics, optional fields, and immutable derivation.
- `internal/pkg/observability/correlation.go` — `Correlation` value object, context helpers, and derived-copy methods.

## TDD evidence

### RED

Command:

```text
go test ./internal/pkg/observability
```

Using the bundled Go toolchain, the focused test failed before implementation with `undefined: Correlation`, `undefined: CorrelationFromContext`, and `undefined: WithCorrelation` compiler errors. This confirms the tests exercised the missing API.

### GREEN

Commands:

```text
gofmt -w internal/pkg/observability/correlation.go internal/pkg/observability/correlation_test.go
go test ./internal/pkg/observability
go vet ./internal/pkg/observability
```

Result: focused tests passed (`ok agentcanvas/internal/pkg/observability`) and `go vet` exited 0.

### REFACTOR

Kept the implementation minimal after green: a private context key, nil-safe context handling, value receivers for derived copies, and defensive copying of `ParentRunID`. Re-ran the focused test and vet after formatting; both remain green.

## Self-review

- `CorrelationFromContext` reports presence only; zero/invalid fields are returned unchanged.
- Nil input to either context helper is safe.
- All `With*` methods copy the receiver; `WithParentRunID` also copies the pointed-to string.
- No production packages outside `internal/pkg/observability` were changed.

## Concerns

None. The Go executable is not on PATH in this environment; evidence uses the repository's bundled Go 1.22 toolchain under the local temp directory.

## Reverse Sync correction (2026-08-28)

Artifacts were reconciled to existing domain/runtime ID contracts: `ConversationID`, `RunID`, and `TurnID` are `int64`, and `ParentRunID` is `*int64`. Tests were updated first and the focused RED run then failed with the expected type-mismatch compiler errors against the prior string implementation. Production and tests were corrected, formatted, and re-run:

```text
gofmt -w internal/pkg/observability/correlation.go internal/pkg/observability/correlation_test.go
go test ./internal/pkg/observability
go vet ./internal/pkg/observability
```

Both commands exited 0; all focused tests remain green. The implementation still preserves nil/zero values and immutable derived-copy semantics.

## Review fix: ParentRunID boundary copies (2026-08-28)

Added regression tests for mutating the source pointer after `WithCorrelation` and mutating the pointer returned by `CorrelationFromContext`. The focused RED run failed both tests, demonstrating aliasing. `WithCorrelation` and `CorrelationFromContext` now defensively clone `ParentRunID` at their boundaries.

```text
go test ./internal/pkg/observability -run 'TestWithCorrelationCopiesParentRunID|TestCorrelationFromContextCopiesParentRunID'  # RED: both tests failed before fix
gofmt -w internal/pkg/observability/correlation.go internal/pkg/observability/correlation_test.go
go test ./internal/pkg/observability  # PASS
go vet ./internal/pkg/observability  # PASS
```
