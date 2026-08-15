package agent

import (
	"strings"
	"testing"
)

func TestDefinitionSnapshotIsNormalizedAndDeterministic(t *testing.T) {
	definition := Definition{ProviderID: 7, Mode: "react", SystemPrompt: "help"}
	first, firstHash, err := definition.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	second, secondHash, err := definition.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) || firstHash == "" || firstHash != secondHash {
		t.Fatalf("snapshot must be deterministic: %s %s", firstHash, secondHash)
	}
	if definition.Normalize().MaxIterations != 8 || definition.Normalize().MaxToolCalls != 16 {
		t.Fatalf("expected safe defaults: %+v", definition.Normalize())
	}
}

func TestDefinitionRejectsInvalidLimits(t *testing.T) {
	err := (Definition{ProviderID: 1, Mode: "react", MaxIterations: 51}).Validate()
	if err == nil {
		t.Fatal("expected max_iterations validation error")
	}
}

func TestDefinitionLimitsMatchSharedRuntime(t *testing.T) {
	tests := []struct {
		name       string
		definition Definition
		field      string
	}{
		{name: "tool calls", definition: Definition{MaxToolCalls: 101}, field: "max_tool_calls"},
		{name: "parallel sub agents", definition: Definition{MaxParallelSubAgents: 65}, field: "max_parallel_sub_agents"},
		{name: "subagent depth", definition: Definition{MaxSubagentDepth: 6}, field: "max_subagent_depth"},
		{name: "tool timeout", definition: Definition{MaxToolTimeoutMS: 600001}, field: "max_tool_timeout_ms"},
		{name: "tool output", definition: Definition{MaxToolOutputBytes: 2*1024*1024 + 1}, field: "max_tool_output_bytes"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			test.definition.ProviderID, test.definition.Mode = 1, "react"
			if err := test.definition.Validate(); err == nil || !strings.Contains(err.Error(), test.field) {
				t.Fatalf("expected %s validation error, got %v", test.field, err)
			}
		})
	}
}

func TestDefinitionResourceSnapshotHashesCapabilities(t *testing.T) {
	first, _, firstToolHash, err := (Definition{ToolIDs: []int64{1}}).ResourceSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	_, _, secondToolHash, err := (Definition{ToolIDs: []int64{2}}).ResourceSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(first) == 0 || firstToolHash == secondToolHash {
		t.Fatal("tool schema hash must change with pinned tool IDs")
	}
	_, _, pythonToolHash, err := (Definition{ToolIDs: []int64{1}, PythonToolNames: []string{"python_text_stats"}}).ResourceSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if pythonToolHash == firstToolHash {
		t.Fatal("tool schema hash must change with pinned Python tools")
	}
}
