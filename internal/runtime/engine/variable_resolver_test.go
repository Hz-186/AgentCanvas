package engine_test

import (
	"testing"

	"agentcanvas/internal/runtime/engine"
)

func TestResolveTemplateSupportsLegacyAndNormalizedNodeIDs(t *testing.T) {
	rc := &engine.RunContext{
		Input: map[string]any{"query": "问题"},
		NodeOutputs: map[string]engine.NodeOutput{
			"retrieval":   {"context": "知识库上下文"},
			"memory read": {"memory_context": "历史记忆"},
		},
	}

	got := engine.ResolveTemplate("{{retrieve.context}} / {{memory_read.memory_context}} / {{ sys.query }}", rc)
	want := "知识库上下文 / 历史记忆 / 问题"
	if got != want {
		t.Fatalf("ResolveTemplate() = %q, want %q", got, want)
	}
}
