package observability

import (
	"context"
	"reflect"
	"testing"
)

func TestCorrelationRoundTripPreservesAllFields(t *testing.T) {
	parent := int64(7)
	want := Correlation{RequestID: "req-1", OwnerID: 42, ConversationID: 11, RunID: 12, TurnID: 13, ParentRunID: &parent, StepIndex: 3, ToolCallID: "tool-1"}

	got, ok := CorrelationFromContext(WithCorrelation(context.Background(), want))
	if !ok {
		t.Fatal("expected correlation value to be present")
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip mismatch: got %#v, want %#v", got, want)
	}
}

func TestCorrelationFromContextNilReturnsZeroAndFalse(t *testing.T) {
	got, ok := CorrelationFromContext(nil)
	if ok || got != (Correlation{}) {
		t.Fatalf("nil context result: got %#v, ok=%v", got, ok)
	}
}

func TestWithCorrelationNilContextIsSafe(t *testing.T) {
	want := Correlation{StepIndex: 0}
	got, ok := CorrelationFromContext(WithCorrelation(nil, want))
	if !ok || got != want {
		t.Fatalf("nil context storage result: got %#v, ok=%v", got, ok)
	}
}

func TestCorrelationPresenceIsIndependentFromOwnerValidity(t *testing.T) {
	got, ok := CorrelationFromContext(WithCorrelation(context.Background(), Correlation{OwnerID: 0}))
	if !ok || got.OwnerID != 0 {
		t.Fatalf("expected present zero-owner correlation: got %#v, ok=%v", got, ok)
	}
}

func TestCorrelationDerivedValuesDoNotMutateOriginal(t *testing.T) {
	parent := int64(5)
	original := Correlation{RequestID: "req", OwnerID: 1, ParentRunID: &parent, StepIndex: 2}
	newParent := int64(8)
	derived := original.WithRequestID("req-2").WithOwnerID(9).WithConversationID(2).WithRunID(3).WithTurnID(4).WithParentRunID(&newParent).WithStepIndex(7).WithToolCallID("tool")

	if !reflect.DeepEqual(original, Correlation{RequestID: "req", OwnerID: 1, ParentRunID: &parent, StepIndex: 2}) {
		t.Fatalf("original was mutated: %#v", original)
	}
	if derived.RequestID != "req-2" || derived.OwnerID != 9 || derived.ConversationID != 2 || derived.RunID != 3 || derived.TurnID != 4 || derived.ParentRunID == nil || *derived.ParentRunID != 8 || derived.StepIndex != 7 || derived.ToolCallID != "tool" {
		t.Fatalf("derived fields incorrect: %#v", derived)
	}
}

func TestCorrelationOptionalFieldsRemainZeroWhenUnset(t *testing.T) {
	got, ok := CorrelationFromContext(WithCorrelation(context.Background(), Correlation{}))
	if !ok || got.ParentRunID != nil || got.StepIndex != 0 {
		t.Fatalf("optional fields changed: got %#v, ok=%v", got, ok)
	}
}

func TestWithCorrelationCopiesParentRunID(t *testing.T) {
	parent := int64(21)
	ctx := WithCorrelation(context.Background(), Correlation{ParentRunID: &parent})
	parent = 99
	got, _ := CorrelationFromContext(ctx)
	if got.ParentRunID == nil || *got.ParentRunID != 21 {
		t.Fatalf("context value changed through input pointer: %#v", got.ParentRunID)
	}
}

func TestCorrelationFromContextCopiesParentRunID(t *testing.T) {
	parent := int64(22)
	ctx := WithCorrelation(context.Background(), Correlation{ParentRunID: &parent})
	got, _ := CorrelationFromContext(ctx)
	*got.ParentRunID = 100
	again, _ := CorrelationFromContext(ctx)
	if again.ParentRunID == nil || *again.ParentRunID != 22 {
		t.Fatalf("context value changed through returned pointer: %#v", again.ParentRunID)
	}
}
