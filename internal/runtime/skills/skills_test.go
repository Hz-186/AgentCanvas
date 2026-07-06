package skills

import (
	"context"
	"encoding/json"
	"testing"

	"agentcanvas/internal/runtime/toolruntime"
)

func TestDeterministicSkillDoesNotInvokeModel(t *testing.T) {
	skill := DeterministicSkill{
		Desc: Descriptor{Name: "json_validate", Description: "validate json"},
		Run: func(ctx context.Context, input json.RawMessage) (any, error) {
			var value any
			if err := json.Unmarshal(input, &value); err != nil {
				return nil, err
			}
			return map[string]any{"valid": true}, nil
		},
	}
	registry := NewRegistry(skill)
	result, err := registry.Invoke(context.Background(), Invocation{Name: "json_validate", InputJSON: json.RawMessage(`{"ok":true}`)})
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	if result.ModelInvoked || result.ModelTokenCost != 0 || !result.Deterministic {
		t.Fatalf("expected zero-token deterministic result, got %+v", result)
	}
	if !skill.Descriptor().DisableModelInvocation {
		t.Fatalf("expected disable-model-invocation descriptor, got %+v", skill.Descriptor())
	}
}

func TestRuntimeToolSkillBuildsDescriptorAndInvokesTool(t *testing.T) {
	tool := fakeSkillTool{name: "search_knowledge"}
	skill := NewRuntimeToolSkill(tool, toolruntime.ToolRunContext{OwnerID: 7})
	desc := skill.Descriptor()
	if desc.Name != "search_knowledge" || !desc.RequiresModel || desc.Deterministic {
		t.Fatalf("unexpected descriptor: %+v", desc)
	}
	if desc.RiskLevel != toolruntime.RiskMedium || desc.SideEffect != toolruntime.SideEffectRead {
		t.Fatalf("metadata not mapped to descriptor: %+v", desc)
	}
	result, err := skill.Invoke(context.Background(), json.RawMessage(`{"query":"rag"}`))
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	if !result.ModelInvoked || result.Deterministic || result.OutputText == "" {
		t.Fatalf("unexpected runtime tool skill result: %+v", result)
	}
}

type fakeSkillTool struct {
	name string
}

func (t fakeSkillTool) Name() string { return t.name }

func (t fakeSkillTool) Description() string { return "search knowledge base" }

func (t fakeSkillTool) Parameters() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }

func (t fakeSkillTool) Metadata() toolruntime.ToolMetadata {
	return toolruntime.ToolMetadata{RiskLevel: toolruntime.RiskMedium, SideEffect: toolruntime.SideEffectRead}
}

func (t fakeSkillTool) Execute(ctx context.Context, rc toolruntime.ToolRunContext, input json.RawMessage) (*toolruntime.ToolResult, error) {
	return toolruntime.ResultFromValue(map[string]any{"owner_id": rc.OwnerID, "input": string(input)})
}
