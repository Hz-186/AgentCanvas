package agent_usecase

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"agentcanvas/internal/domain"
	agentdomain "agentcanvas/internal/domain/agent"
	"agentcanvas/internal/domain/conversation"
	agentruntime "agentcanvas/internal/runtime/agentruntime"
)

func completedTurnFixture(assistantMessageID any) (*agentdomain.Run, *agentdomain.Turn, *settingsAgentRuntime) {
	definitionJSON, _, _ := (agentdomain.Definition{
		ModelConfig:  agentdomain.ModelConfig{ProviderID: 1, Model: "test-model"},
		PromptConfig: agentdomain.PromptConfig{SystemPrompt: "test"},
	}).Snapshot()
	runID := int64(502)
	run := &agentdomain.Run{BaseModel: domain.BaseModel{ID: runID, OwnerID: 3}, AgentID: 10, Status: agentdomain.RunStatusQueued,
		DefinitionJSON: definitionJSON, InputJSON: json.RawMessage(`{"query":"hi"}`), StartedAt: time.Now().UTC()}
	turn := &agentdomain.Turn{BaseModel: domain.BaseModel{ID: 602, OwnerID: 3}, AgentID: 10, RunID: &runID,
		Status: agentdomain.TurnStatusRunning, InputJSON: json.RawMessage(`{"query":"hi"}`)}
	output := agentruntime.RunOutput{"final_answer": "done", "total_tokens": 5}
	if assistantMessageID != nil {
		output["assistant_message_id"] = assistantMessageID
	}
	runtime := &settingsAgentRuntime{executeResult: &agentruntime.RunResult{Output: output}}
	return run, turn, runtime
}

func TestCompleteTurnReusesRealtimeAssistantMessage(t *testing.T) {
	run, turn, runtime := completedTurnFixture(int64(42))
	turns := &settingsTurnRepo{}
	NewService(nil, turns, nil, nil, &settingsRunRepo{items: map[int64]*agentdomain.Run{run.ID: run}}, nil, nil, nil, runtime).
		executeTurnOwned(context.Background(), turn)
	if len(turns.completedMessages) != 0 {
		t.Fatalf("existing assistant message must be referenced, not re-created: %+v", turns.completedMessages)
	}
	if turn.AssistantMessageID == nil || *turn.AssistantMessageID != 42 {
		t.Fatalf("turn must reference the realtime-written row: %+v", turn.AssistantMessageID)
	}
	if turn.Status != agentdomain.TurnStatusSucceeded {
		t.Fatalf("turn must complete: %s", turn.Status)
	}
}

func TestCompleteTurnFallsBackToCreatingAssistantMessage(t *testing.T) {
	run, turn, runtime := completedTurnFixture(nil)
	turns := &settingsTurnRepo{}
	NewService(nil, turns, nil, nil, &settingsRunRepo{items: map[int64]*agentdomain.Run{run.ID: run}}, nil, nil, nil, runtime).
		executeTurnOwned(context.Background(), turn)
	if len(turns.completedMessages) != 1 {
		t.Fatalf("missing assistant_message_id must fall back to row creation exactly once: %+v", turns.completedMessages)
	}
	if turns.completedMessages[0].Content != "done" || turns.completedMessages[0].ContentType != conversation.ContentTypeText {
		t.Fatalf("fallback row must be the plain assistant text: %+v", turns.completedMessages[0])
	}
}

func TestCompleteTurnTagsManualCompactionEcho(t *testing.T) {
	definitionJSON, _, _ := (agentdomain.Definition{
		ModelConfig:  agentdomain.ModelConfig{ProviderID: 1, Model: "test-model"},
		PromptConfig: agentdomain.PromptConfig{SystemPrompt: "test"},
	}).Snapshot()
	runID := int64(503)
	run := &agentdomain.Run{BaseModel: domain.BaseModel{ID: runID, OwnerID: 3}, AgentID: 10, Status: agentdomain.RunStatusQueued,
		DefinitionJSON: definitionJSON, InputJSON: json.RawMessage(`{"query":"/compact","manual_compaction":true}`), StartedAt: time.Now().UTC()}
	turn := &agentdomain.Turn{BaseModel: domain.BaseModel{ID: 603, OwnerID: 3}, AgentID: 10, RunID: &runID,
		Status: agentdomain.TurnStatusRunning, InputJSON: json.RawMessage(`{"query":"/compact","manual_compaction":true}`)}
	runtime := &settingsAgentRuntime{executeResult: &agentruntime.RunResult{Output: agentruntime.RunOutput{"final_answer": "Context compacted."}}}
	turns := &settingsTurnRepo{}
	NewService(nil, turns, nil, nil, &settingsRunRepo{items: map[int64]*agentdomain.Run{run.ID: run}}, nil, nil, nil, runtime).
		executeTurnOwned(context.Background(), turn)
	if len(turns.completedMessages) != 1 || turns.completedMessages[0].ContentType != conversation.ContentTypeSystemEcho {
		t.Fatalf("manual compaction ack must be tagged system_echo: %+v", turns.completedMessages)
	}
}
