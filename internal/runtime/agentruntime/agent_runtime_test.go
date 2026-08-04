package agentruntime

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"agentcanvas/internal/domain/memory"
)

func TestDecodeDefinitionBuildsIdentityAndCapabilities(t *testing.T) {
	raw := json.RawMessage(`{"provider_id":2,"model":"m","mode":"react","system_prompt":"base","role":"researcher","goal":"verify","tool_pack_ids":[3],"allow_subagents":true,"max_subagent_depth":3}`)
	definition, err := DecodeDefinition(raw)
	if err != nil {
		t.Fatal(err)
	}
	cfg := agentRuntimeConfig(definition)
	if !strings.Contains(cfg.SystemPrompt, "ROLE: researcher") || !strings.Contains(cfg.SystemPrompt, "GOAL: verify") {
		t.Fatalf("identity was not assembled: %q", cfg.SystemPrompt)
	}
	if len(cfg.ToolPackIDs) != 1 || !cfg.AllowSubagents || cfg.MaxSubagentDepth != 3 {
		t.Fatalf("capabilities were not decoded: %+v", cfg)
	}
}

type configuredMemoryRepository struct {
	memory.Repository
}

func TestAgentRuntimeMemoryRequiresUnifiedContextIndex(t *testing.T) {
	n := runtimeCore{Memories: configuredMemoryRepository{}}
	_, err := n.loadTools(context.Background(), 1, agentRuntimeConfig{MemoryEnabled: true}, nil)
	if err == nil || !strings.Contains(err.Error(), "unified context index is not configured") {
		t.Fatalf("expected unified context index configuration error, got %v", err)
	}
}
