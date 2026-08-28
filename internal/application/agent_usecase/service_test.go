package agent_usecase

import (
	"context"
	"encoding/json"
	"testing"

	"agentcanvas/internal/domain"
	agentdomain "agentcanvas/internal/domain/agent"
	"agentcanvas/internal/domain/conversation"
	"agentcanvas/internal/pkg/observability"
)

// correlationTurnRepo extends the settings turn fake with an idempotency hit
// and a create call counter so idempotent replays can be asserted precisely.
type correlationTurnRepo struct {
	settingsTurnRepo
	existing    *agentdomain.Turn
	createCalls int
}

func (r *correlationTurnRepo) FindByIdempotencyKey(context.Context, int64, int64, string) (*agentdomain.Turn, error) {
	if r.existing != nil {
		return r.existing, nil
	}
	return nil, agentdomain.ErrNoTurnAvailable
}

func (r *correlationTurnRepo) CreateWithArtifacts(ctx context.Context, turn *agentdomain.Turn, message *conversation.Message, run *agentdomain.Run) error {
	r.createCalls++
	return r.settingsTurnRepo.CreateWithArtifacts(ctx, turn, message, run)
}

func startTurnCorrelationService(t *testing.T, turns *correlationTurnRepo, runs *settingsRunRepo) *Service {
	t.Helper()
	agentID := int64(10)
	conv := &conversation.Conversation{SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 20, OwnerID: 3}}, AgentID: &agentID, AgentMode: "plan_execute"}
	conversations := &settingsConversationRepo{items: map[int64]*conversation.Conversation{20: conv}}
	definition := agentdomain.Definition{
		ModelConfig:     agentdomain.ModelConfig{ProviderID: 1, Model: "test-model"},
		PromptConfig:    agentdomain.PromptConfig{SystemPrompt: "test"},
		ExecutionLimits: agentdomain.ExecutionLimits{Mode: "react"},
	}
	agents := newSettingsAgentRepo()
	agents.items[agentID] = &agentdomain.Agent{SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: agentID, OwnerID: 3}}, Status: agentdomain.StatusActive, DraftDefinition: definition}
	return NewService(agents, turns, conversations, nil, runs, nil, nil, nil, nil)
}

func TestStartTurnCorrelationPersistsRequestMetadata(t *testing.T) {
	turns := &correlationTurnRepo{}
	service := startTurnCorrelationService(t, turns, nil)

	ctx := observability.WithCorrelation(context.Background(), observability.Correlation{RequestID: "rid-start-1", OwnerID: 3})
	accepted, err := service.StartTurn(ctx, 3, 10, 20, "request-obs-1", CreateTurnRequest{Content: " investigate "})
	if err != nil {
		t.Fatalf("StartTurn returned error: %v", err)
	}
	if accepted.Run == nil || accepted.Turn == nil {
		t.Fatalf("StartTurn must return both artifacts: %+v", accepted)
	}
	for name, raw := range map[string]json.RawMessage{"run": accepted.Run.InputJSON, "turn": accepted.Turn.InputJSON} {
		var input map[string]any
		if err := json.Unmarshal(raw, &input); err != nil {
			t.Fatalf("decode %s input: %v", name, err)
		}
		if input["query"] != "investigate" || input["mode"] != "plan" || input["manual_compaction"] != false {
			t.Fatalf("%s input lost business fields: %v", name, input)
		}
		metadata, _ := input["observability"].(map[string]any)
		if metadata == nil {
			t.Fatalf("%s input has no observability namespace: %v", name, input)
		}
		if metadata["version"] != float64(1) {
			t.Fatalf("%s observability version = %v, want 1", name, metadata["version"])
		}
		if metadata["request_id"] != "rid-start-1" || metadata["owner_id"] != float64(3) || metadata["conversation_id"] != float64(20) {
			t.Fatalf("%s observability correlation incomplete: %v", name, metadata)
		}
	}
}

func TestStartTurnCorrelationKeepsIdempotentExistingTurnMetadata(t *testing.T) {
	existingRunID := int64(91)
	existingInput := json.RawMessage(`{"query":"original","mode":"react","manual_compaction":false,"observability":{"version":1,"request_id":"rid-original","owner_id":3,"conversation_id":20}}`)
	existingTurn := &agentdomain.Turn{BaseModel: domain.BaseModel{ID: 90, OwnerID: 3}, AgentID: 10, ConversationID: 20, RunID: &existingRunID, Status: agentdomain.TurnStatusQueued, InputJSON: existingInput}
	existingRun := &agentdomain.Run{BaseModel: domain.BaseModel{ID: existingRunID, OwnerID: 3}, Status: agentdomain.RunStatusQueued, InputJSON: existingInput}
	turns := &correlationTurnRepo{existing: existingTurn}
	runs := &settingsRunRepo{items: map[int64]*agentdomain.Run{existingRunID: existingRun}}
	service := startTurnCorrelationService(t, turns, runs)

	ctx := observability.WithCorrelation(context.Background(), observability.Correlation{RequestID: "rid-duplicate", OwnerID: 3})
	accepted, err := service.StartTurn(ctx, 3, 10, 20, "request-dup", CreateTurnRequest{Content: "second attempt"})
	if err != nil {
		t.Fatalf("StartTurn returned error: %v", err)
	}
	if turns.createCalls != 0 {
		t.Fatalf("idempotent replay must not create artifacts, got %d create calls", turns.createCalls)
	}
	if accepted.Turn != existingTurn || accepted.Run != existingRun {
		t.Fatalf("idempotent replay must return the persisted objects: turn=%p run=%p", accepted.Turn, accepted.Run)
	}
	for name, raw := range map[string]json.RawMessage{"run": accepted.Run.InputJSON, "turn": accepted.Turn.InputJSON} {
		var input map[string]any
		if err := json.Unmarshal(raw, &input); err != nil {
			t.Fatalf("decode %s input: %v", name, err)
		}
		metadata, _ := input["observability"].(map[string]any)
		if metadata == nil || metadata["request_id"] != "rid-original" {
			t.Fatalf("%s metadata was overwritten by the duplicate request: %v", name, input)
		}
	}
}
