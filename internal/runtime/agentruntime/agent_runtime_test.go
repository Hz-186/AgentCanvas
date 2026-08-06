package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"agentcanvas/internal/domain/memory"
	runtimeagent "agentcanvas/internal/runtime/agent"
	runtimeevent "agentcanvas/internal/runtime/event"
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

type capturedRuntimeEvents struct {
	events []runtimeevent.Event
}

func (e *capturedRuntimeEvents) Emit(_ context.Context, event runtimeevent.Event) error {
	e.events = append(e.events, event)
	return nil
}

func TestEmitAgentResultEventReflectsFinalOutcome(t *testing.T) {
	tests := []struct {
		name      string
		runErr    error
		wantType  string
		wantError string
	}{
		{name: "success", wantType: runtimeevent.AgentFinished},
		{name: "failure", runErr: errors.New("structured output validation failed"), wantType: runtimeevent.AgentFailed, wantError: "structured output validation failed"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			emitter := &capturedRuntimeEvents{}
			rc := &RunContext{RunID: 42, Events: emitter}
			result := &runtimeagent.RunResult{StopReason: runtimeagent.StopReasonFinalAnswer, Iterations: 2, ToolCalls: 3}
			result.Usage.TotalTokens = 17

			emitAgentResultEvent(context.Background(), rc, result, test.runErr)

			if len(emitter.events) != 1 || emitter.events[0].Type != test.wantType {
				t.Fatalf("events = %+v, want one %q event", emitter.events, test.wantType)
			}
			if emitter.events[0].RunID != rc.RunID || emitter.events[0].Payload["total_tokens"] != 17 {
				t.Fatalf("event payload does not reflect finalized result: %+v", emitter.events[0])
			}
			if got, _ := emitter.events[0].Payload["error"].(string); got != test.wantError {
				t.Fatalf("error payload = %q, want %q", got, test.wantError)
			}
		})
	}
}
