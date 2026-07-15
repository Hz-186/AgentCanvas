package agent_usecase

import "testing"

func TestDecodeLifecycleOutcomeFindsNestedStructuredOutput(t *testing.T) {
	outcome := decodeLifecycleOutcome(map[string]any{"node": map[string]any{"structured_output": `{"action":"replace","output_patch":{"final_answer":"safe"}}`}})
	if outcome.Action != "" {
		// Unknown wrapper keys are deliberately ignored; only stable runtime
		// output envelope keys are traversed.
		t.Fatalf("unexpected contract from unknown wrapper: %+v", outcome)
	}
	outcome = decodeLifecycleOutcome(map[string]any{"output": map[string]any{"structured_output": `{"action":"replace","output_patch":{"final_answer":"safe"}}`}})
	if outcome.Action != "replace" || outcome.OutputPatch["final_answer"] != "safe" {
		t.Fatalf("unexpected lifecycle outcome: %+v", outcome)
	}
}

func TestDecodeLifecycleOutcomeDefaultsMaps(t *testing.T) {
	outcome := decodeLifecycleOutcome(map[string]any{"action": "continue"})
	if outcome.Action != "continue" || outcome.ContextPatch == nil || outcome.OutputPatch == nil || outcome.Metadata == nil {
		t.Fatalf("outcome was not normalized: %+v", outcome)
	}
}
