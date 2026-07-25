package agent

import (
	"encoding/json"
	"testing"
)

func TestCheckCallChainDetectsCycleAndDepth(t *testing.T) {
	if err := CheckCallChain([]int64{1, 2}, 2, 5, 1); err == nil {
		t.Fatal("expected cycle detection")
	}
	if err := CheckCallChain([]int64{1}, 4, 2, 2); err == nil {
		t.Fatal("expected depth guard")
	}
	if err := CheckCallChain([]int64{1, 2}, 4, 5, 1); err != nil {
		t.Fatalf("unexpected guard error: %v", err)
	}
}

func TestRedactSensitiveFieldsCompatibility(t *testing.T) {
	raw := json.RawMessage(`{"api_key":"secret","safe":"ok"}`)
	redacted := RedactSensitiveFields(raw, []string{"api_key"})
	if string(redacted) == string(raw) {
		t.Fatalf("expected sensitive field to be redacted: %s", redacted)
	}
}
