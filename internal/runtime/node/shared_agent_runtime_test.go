package node

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDecodeAgentRuntimeDefinitionBuildsIdentityAndCapabilities(t *testing.T) {
	raw := json.RawMessage(`{"provider_id":2,"model":"m","mode":"react","system_prompt":"base","role":"researcher","goal":"verify","tool_pack_ids":[3],"callable_agent_ids":[8],"call_workflow_ids":[9]}`)
	definition, err := DecodeAgentRuntimeDefinition(raw)
	if err != nil {
		t.Fatal(err)
	}
	cfg := agentRuntimeConfig(definition)
	if !strings.Contains(cfg.SystemPrompt, "ROLE: researcher") || !strings.Contains(cfg.SystemPrompt, "GOAL: verify") {
		t.Fatalf("identity was not assembled: %q", cfg.SystemPrompt)
	}
	if len(cfg.ToolPackIDs) != 1 || len(cfg.CallAgentIDs) != 1 || len(cfg.CallWorkflowIDs) != 1 {
		t.Fatalf("capabilities were not decoded: %+v", cfg)
	}
}
