package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentcanvas/internal/domain/memory"
	gitinfra "agentcanvas/internal/infrastructure/git"
	runtimeagent "agentcanvas/internal/runtime/agent"
	runtimeevent "agentcanvas/internal/runtime/event"
	"agentcanvas/internal/runtime/toolruntime"
)

func TestCoordinatorExtraTokensIncludesPreparedBlocks(t *testing.T) {
	base := coordinatorExtraTokens("openai_compatible", "gpt-4o", "system", "task", 50, nil, nil)
	withMemory := coordinatorExtraTokens("openai_compatible", "gpt-4o", "system", "task", 50, nil,
		[]runtimeagent.ContextBlock{{Name: "memory_recall", Content: strings.Repeat("project fact ", 100)}})
	if withMemory <= base {
		t.Fatalf("prepared context blocks were not included in coordinator budget: base=%d with_memory=%d", base, withMemory)
	}
}

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

func TestDecodeDefinitionAcceptsNestedRuntimeConfig(t *testing.T) {
	raw := json.RawMessage(`{"model_config":{"provider_id":2,"model":"m","mode":"react"},"prompt_config":{"system_prompt":"base"},"tool_config":{"tool_pack_ids":[3],"allow_subagents":true,"max_subagent_depth":3},"memory_policy":{"memory_enabled":false}}`)
	definition, err := DecodeDefinition(raw)
	if err != nil {
		t.Fatal(err)
	}
	cfg := agentRuntimeConfig(definition)
	if cfg.ProviderID != 2 || cfg.Model != "m" || cfg.Mode != "react" || cfg.SystemPrompt != "base" || len(cfg.ToolPackIDs) != 1 || !cfg.AllowSubagents || cfg.MemoryEnabled {
		t.Fatalf("nested runtime definition was not decoded: %+v", cfg)
	}
}

func TestWorkspaceCodingContextTreatsCommitsBeyondWorktreeBaseAsUnpushed(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	gitService := gitinfra.NewService(gitinfra.Config{GitUserName: "AgentCanvas Test", GitUserEmail: "test@example.com"})
	if _, err := gitService.EnsureRepository(ctx, root, true); err != nil {
		t.Fatal(err)
	}
	base, err := gitService.Head(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "change.txt"), []byte("change\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := gitService.Commit(ctx, root, "feat: add change", []string{"change.txt"}); err != nil {
		t.Fatal(err)
	}
	block := (runtimeCore{coreWorkspace: coreWorkspace{Git: gitService}}).workspaceCodingContext(ctx, &RunContext{Workspace: &toolruntime.WorkspaceContext{
		Kind: "worktree", RepositoryRoot: root, WorkspacePath: root, BranchName: gitService.CurrentBranch(ctx, root), BaseSHA: base, GitEnabled: true,
	}})
	if block == nil || !strings.Contains(block.Content, "unpushed=true") {
		t.Fatalf("coding context lost worktree base status: %#v", block)
	}
}

type configuredMemoryRepository struct {
	memory.Repository
}

func TestAgentRuntimeMemoryRequiresUnifiedContextIndex(t *testing.T) {
	n := runtimeCore{coreRepositories: coreRepositories{Memories: configuredMemoryRepository{}}}
	_, err := n.loadTools(context.Background(), 1, agentRuntimeConfig{RuntimeMemoryPolicy: RuntimeMemoryPolicy{MemoryEnabled: true}}, nil)
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
