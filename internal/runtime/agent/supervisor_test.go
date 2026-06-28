package agent

import (
	"encoding/json"
	"testing"
)

func TestCheckCallChainDetectsCycle(t *testing.T) {
	chain := []int64{1, 2, 3}
	err := CheckCallChain(chain, 2, 5, 1)
	if err == nil {
		t.Fatal("expected cycle detection error")
	}
}

func TestCheckCallChainAllowsNewAgent(t *testing.T) {
	chain := []int64{1, 2, 3}
	err := CheckCallChain(chain, 4, 5, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCheckCallChainMaxDepthExceeded(t *testing.T) {
	chain := []int64{1}
	err := CheckCallChain(chain, 4, 2, 2)
	if err == nil {
		t.Fatal("expected max depth exceeded error")
	}
}

func TestCheckCallChainMaxDepthZero(t *testing.T) {
	chain := []int64{1}
	err := CheckCallChain(chain, 5, 0, 100)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSupervisorBuildPrompt(t *testing.T) {
	s := SupervisorRuntime{}
	members := []TeamMemberInfo{
		{WorkflowID: 1, Role: "researcher", Description: "Searches knowledge bases"},
		{WorkflowID: 2, Role: "writer", Description: "Writes final answers"},
	}
	prompt := s.BuildSupervisorPrompt(members)
	if prompt == "" {
		t.Fatal("expected non-empty supervisor prompt")
	}
	if len(prompt) == 0 {
		t.Fatal("unexpected empty prompt")
	}
}

func TestCompactToolOutput(t *testing.T) {
	result := CompactToolOutput("short output", 100)
	if result != "short output" {
		t.Fatalf("expected unchanged, got %s", result)
	}
	result = CompactToolOutput("long output here that exceeds limit", 10)
	if result != "long outpu...[compressed]" {
		t.Fatalf("expected compressed, got %s", result)
	}
	result = CompactToolOutput("exact size", 10)
	if result != "exact size" {
		t.Fatalf("expected unchanged, got %s", result)
	}
}

func TestCompactToolOutputZeroBytes(t *testing.T) {
	result := CompactToolOutput("anything", 0)
	if result != "anything" {
		t.Fatalf("expected unchanged, got %s", result)
	}
}

func TestRedactSensitiveFields(t *testing.T) {
	raw := json.RawMessage(`{"api_key":"secret123","name":"test","password":"hidden"}`)
	redacted := RedactSensitiveFields(raw, []string{"api_key", "password"})
	var m map[string]any
	json.Unmarshal(redacted, &m)
	if m["api_key"] != "[REDACTED]" {
		t.Fatalf("expected redacted, got %v", m["api_key"])
	}
	if m["name"] != "test" {
		t.Fatalf("expected unchanged, got %v", m["name"])
	}
	if m["password"] != "[REDACTED]" {
		t.Fatalf("expected redacted, got %v", m["password"])
	}
}

func TestRedactSensitiveFieldsEmpty(t *testing.T) {
	raw := json.RawMessage(`{"key":"value"}`)
	redacted := RedactSensitiveFields(raw, []string{})
	if string(redacted) != `{"key":"value"}` {
		t.Fatalf("expected unchanged, got %s", redacted)
	}
	redacted = RedactSensitiveFields(json.RawMessage{}, []string{"key"})
	if len(redacted) != 0 {
		t.Fatalf("expected empty, got %s", redacted)
	}
}
