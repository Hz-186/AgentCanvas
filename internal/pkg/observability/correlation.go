package observability

import "context"

// Correlation carries identifiers used to connect work across request, turn,
// run, and tool boundaries. Values are treated as immutable; With* methods
// return a copy with one field changed.
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

type correlationContextKey struct{}

// WithCorrelation stores correlation in ctx. A nil context is replaced with
// context.Background so callers can safely use this helper at boundaries.
func WithCorrelation(ctx context.Context, correlation Correlation) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	correlation = correlation.WithParentRunID(correlation.ParentRunID)
	return context.WithValue(ctx, correlationContextKey{}, correlation)
}

// CorrelationFromContext returns the stored value and whether one was present.
// Presence is independent of field validity (including OwnerID == 0).
func CorrelationFromContext(ctx context.Context) (Correlation, bool) {
	if ctx == nil {
		return Correlation{}, false
	}
	correlation, ok := ctx.Value(correlationContextKey{}).(Correlation)
	if ok {
		correlation = correlation.WithParentRunID(correlation.ParentRunID)
	}
	return correlation, ok
}

func (c Correlation) WithRequestID(value string) Correlation     { c.RequestID = value; return c }
func (c Correlation) WithOwnerID(value int64) Correlation        { c.OwnerID = value; return c }
func (c Correlation) WithConversationID(value int64) Correlation { c.ConversationID = value; return c }
func (c Correlation) WithRunID(value int64) Correlation          { c.RunID = value; return c }
func (c Correlation) WithTurnID(value int64) Correlation         { c.TurnID = value; return c }
func (c Correlation) WithParentRunID(value *int64) Correlation {
	if value == nil {
		c.ParentRunID = nil
		return c
	}
	copyValue := *value
	c.ParentRunID = &copyValue
	return c
}
func (c Correlation) WithStepIndex(value int) Correlation     { c.StepIndex = value; return c }
func (c Correlation) WithToolCallID(value string) Correlation { c.ToolCallID = value; return c }
