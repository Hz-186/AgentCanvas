package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDefinitionSnapshotIsNormalizedAndDeterministic(t *testing.T) {
	definition := Definition{ModelConfig: ModelConfig{ProviderID: 7}, PromptConfig: PromptConfig{SystemPrompt: "help"}, ExecutionLimits: ExecutionLimits{Mode: "react"}}
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
	err := (Definition{ModelConfig: ModelConfig{ProviderID: 1}, ExecutionLimits: ExecutionLimits{Mode: "react", MaxIterations: 51}}).Validate()
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
		{name: "tool calls", definition: Definition{ExecutionLimits: ExecutionLimits{MaxToolCalls: 101}}, field: "max_tool_calls"},
		{name: "parallel sub agents", definition: Definition{ExecutionLimits: ExecutionLimits{MaxParallelSubAgents: 65}}, field: "max_parallel_sub_agents"},
		{name: "subagent depth", definition: Definition{ExecutionLimits: ExecutionLimits{MaxSubagentDepth: 6}}, field: "max_subagent_depth"},
		{name: "tool timeout", definition: Definition{ExecutionLimits: ExecutionLimits{MaxToolTimeoutMS: 600001}}, field: "max_tool_timeout_ms"},
		{name: "tool output", definition: Definition{ExecutionLimits: ExecutionLimits{MaxToolOutputBytes: 2*1024*1024 + 1}}, field: "max_tool_output_bytes"},
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
	first, _, firstToolHash, err := (Definition{ToolConfig: ToolConfig{ToolIDs: []int64{1}}}).ResourceSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	_, _, secondToolHash, err := (Definition{ToolConfig: ToolConfig{ToolIDs: []int64{2}}}).ResourceSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(first) == 0 || firstToolHash == secondToolHash {
		t.Fatal("tool schema hash must change with pinned tool IDs")
	}
}

func TestDefinitionWireFormatStaysFlatAndAcceptsNestedInput(t *testing.T) {
	legacy := definitionFlat{ProviderID: 7, Model: "gpt-test", SystemPrompt: "help", Mode: "react", ToolIDs: []int64{3}, MaxIterations: 8}
	want, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	got, err := json.Marshal(definitionFromFlat(legacy))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) || strings.Contains(string(got), "model_config") {
		t.Fatalf("definition wire format changed:\nwant %s\n got %s", want, got)
	}
	var nested Definition
	if err := json.Unmarshal([]byte(`{"model_config":{"provider_id":9,"model":"nested"},"prompt_config":{"system_prompt":"prompt"},"execution_limits":{"mode":"react","max_iterations":4}}`), &nested); err != nil {
		t.Fatal(err)
	}
	if nested.ProviderID != 9 || nested.Model != "nested" || nested.SystemPrompt != "prompt" || nested.Mode != "default" || nested.MaxIterations != 4 {
		t.Fatalf("nested definition was not decoded: %+v", nested)
	}
}
