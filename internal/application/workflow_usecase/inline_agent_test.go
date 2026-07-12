package workflow_usecase

import (
	"encoding/json"
	"testing"

	"agentcanvas/internal/runtime/toolruntime"
)

func TestInlineAgentDSLPreservesDefaultAgentPolicy(t *testing.T) {
	definition := toolruntime.InlineAgentDefinition{
		Name: "researcher", SystemPrompt: "inspect carefully", Task: "inspect repository",
		ProviderID: 4, Model: "model", ToolIDs: []int64{1}, MCPServerIDs: []int64{2},
		MaxIterations: 8, MaxToolCalls: 16, MaxExecutionTimeMS: 120000, MaxParallelChildren: 4,
		MaxDepth:               3,
		RequireApprovalForRisk: []string{toolruntime.RiskHigh}, MaxToolTimeoutMS: 30000,
		MaxToolOutputBytes: 4096, AllowedHosts: []string{"api.example.com"}, CodeExecutionEnabled: true,
	}
	dsl := inlineAgentDSL(definition, true)
	if len(dsl.Nodes) != 2 || dsl.Nodes[1].Type != "agent_loop" {
		t.Fatalf("unexpected inline agent DSL: %+v", dsl)
	}
	var config map[string]any
	if err := json.Unmarshal(dsl.Nodes[1].Config, &config); err != nil {
		t.Fatal(err)
	}
	if config["allow_inline_agents"] != true || config["code_execution_enabled"] != true {
		t.Fatalf("capabilities were not preserved: %+v", config)
	}
	if config["disable_profile_defaults"] != true {
		t.Fatalf("inline agents must not expand capabilities from workflow profile: %+v", config)
	}
	if config["max_parallel_sub_agents"] != float64(4) || config["max_tool_timeout_ms"] != float64(30000) {
		t.Fatalf("limits were not preserved: %+v", config)
	}
	if config["max_workflow_call_depth"] != float64(3) {
		t.Fatalf("delegation depth was not preserved: %+v", config)
	}
}
