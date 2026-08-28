# Task 1 Brief — Correlation context

Implement only Task 1 of `observability-correlation-tracing`.

Read this brief first; it is the exact task contract. Define a small immutable correlation value object and context helpers in `internal/pkg/observability/correlation.go`, with tests in `correlation_test.go`.

Required API:

```go
type Correlation struct {
    RequestID      string
    OwnerID        int64
    ConversationID int64
    RunID          int64
    TurnID         int64
    ParentRunID    *int64
    StepIndex      int
    ToolCallID     string
}

func WithCorrelation(context.Context, Correlation) context.Context
func CorrelationFromContext(context.Context) (Correlation, bool)
```

Required methods return derived copies without mutating the receiver: `WithRequestID`, `WithOwnerID`, `WithConversationID`, `WithRunID`, `WithTurnID`, `WithParentRunID`, `WithStepIndex`, and `WithToolCallID`.

Tests must cover: full round trip; nil context returns zero value and `ok=false` without panic; optional `ParentRunID=nil` and `StepIndex=0` remain unchanged; context presence (`ok=true`) is independent from owner validity (`OwnerID > 0`); and derived values do not mutate the original.

ID types must match the existing domain/runtime contracts: `ConversationID`, `RunID`, and `TurnID` are `int64`, `ParentRunID` is `*int64`, while `RequestID` and `ToolCallID` are strings. `CorrelationFromContext` semantics: `ok` means a Correlation value is present in the context, not that fields are valid. Do not synthesize IDs or normalize zero values. A nil input context to `WithCorrelation` must be handled safely (use a background context).

TDD is mandatory: write tests first, run the focused test and capture a meaningful RED failure, then implement the minimum code, run GREEN and refactor while green. Do not modify other packages or artifacts except this brief/report if needed.

Report path: `openspec/changes/observability-correlation-tracing/task-1-report.md`. Include changed files, RED/GREEN/REFACTOR commands and outcomes, self-review, and concerns. Do not commit; controller runtime policy is `auto_commit: false`.
