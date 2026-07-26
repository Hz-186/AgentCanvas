package agent_usecase

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	agentdomain "agentcanvas/internal/domain/agent"
	"agentcanvas/internal/domain/conversation"
	"agentcanvas/internal/domain/knowledge"
	"agentcanvas/internal/domain/provider"
	"agentcanvas/internal/domain/workflow"
	agenterrors "agentcanvas/internal/pkg/errors"
)

func TestManagedDefinitionOwnsInternalDefaults(t *testing.T) {
	temperature := 0.4
	definition := ManagedDefinition(AgentEditableSettings{
		ProviderID: 8, Model: "  gpt-test  ", SystemPrompt: "  be useful  ",
		KnowledgeIDs: []int64{4, 4, 2}, Temperature: &temperature,
	})

	if definition.Mode != "react" || !definition.MemoryEnabled || !definition.ReflectionEnabled || !definition.AllowInlineAgents {
		t.Fatalf("managed runtime defaults are incomplete: %+v", definition)
	}
	if definition.MaxIterations != 8 || definition.MaxToolCalls != 16 || definition.MaxExecutionTimeMS != 120000 ||
		definition.MaxToolTimeoutMS != 30000 || definition.MaxToolOutputBytes != 512*1024 ||
		definition.MaxParallelSubAgents != 4 || definition.MaxWorkflowCallDepth != 3 || definition.OutputMode != "final_answer" {
		t.Fatalf("unexpected managed limits: %+v", definition)
	}
	if definition.Model != "gpt-test" || definition.SystemPrompt != "be useful" || len(definition.KnowledgeIDs) != 2 ||
		definition.KnowledgeIDs[0] != 4 || definition.KnowledgeIDs[1] != 2 {
		t.Fatalf("editable settings were not normalized: %+v", definition)
	}
}

func TestCreateAgentValidatesResourcesAndPublishesFirstRelease(t *testing.T) {
	agents := newSettingsAgentRepo()
	service := NewService(agents, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	service.ConfigureEditableResources(
		&settingsProviderRepo{items: map[int64]*provider.ModelProvider{7: {ID: 7, OwnerID: 3, Status: provider.StatusActive}}},
		&settingsKnowledgeRepo{items: map[int64]*knowledge.KnowledgeBase{9: {ID: 9, OwnerID: 3, Status: knowledge.KnowledgeBaseStatusActive}}},
	)

	view, err := service.CreateAgent(context.Background(), 3, CreateAgentRequest{
		Name: " Planner ", Settings: AgentEditableSettings{ProviderID: 7, KnowledgeIDs: []int64{9}},
	})
	if err != nil {
		t.Fatalf("CreateAgent returned error: %v", err)
	}
	if view.Name != "Planner" || view.Status != agentdomain.StatusActive || view.CurrentReleaseID == nil || *view.CurrentReleaseID != 100 {
		t.Fatalf("agent was not created and published atomically from the caller perspective: %+v", view)
	}
	if len(agents.releases) != 1 || agents.releases[0].Definition.Mode != "react" || agents.releases[0].Definition.SystemPrompt != defaultAgentSystemPrompt {
		t.Fatalf("unexpected first release: %+v", agents.releases)
	}

	_, err = service.CreateAgent(context.Background(), 4, CreateAgentRequest{
		Name: "foreign-provider", Settings: AgentEditableSettings{ProviderID: 7},
	})
	if !errors.Is(err, agenterrors.ErrInvalidInput) {
		t.Fatalf("foreign provider must be rejected, got %v", err)
	}
}

func TestConversationModeLifecycleAndForkInheritance(t *testing.T) {
	agentID, releaseID := int64(10), int64(100)
	agents := newSettingsAgentRepo()
	agents.items[agentID] = &agentdomain.Agent{ID: agentID, OwnerID: 3, Status: agentdomain.StatusActive, CurrentReleaseID: &releaseID}
	conversations := &settingsConversationRepo{items: map[int64]*conversation.Conversation{}, nextID: 20}
	turns := &settingsTurnRepo{latestErr: agentdomain.ErrNoTurnAvailable}
	messages := &settingsMessageRepo{byConversation: map[int64][]conversation.Message{}}
	service := NewService(agents, turns, conversations, messages, nil, nil, nil, nil, nil, nil)

	created, err := service.CreateConversation(context.Background(), 3, agentID, CreateConversationRequest{})
	if err != nil || created.AgentMode != "react" {
		t.Fatalf("new conversation must default to react: item=%+v err=%v", created, err)
	}
	if _, err = service.UpdateConversationMode(context.Background(), 3, agentID, created.ID, UpdateConversationModeRequest{Mode: "invalid"}); !errors.Is(err, agenterrors.ErrInvalidInput) {
		t.Fatalf("invalid mode must be rejected, got %v", err)
	}
	updated, err := service.UpdateConversationMode(context.Background(), 3, agentID, created.ID, UpdateConversationModeRequest{Mode: "plan_execute"})
	if err != nil || updated.AgentMode != "plan_execute" {
		t.Fatalf("mode was not persisted: item=%+v err=%v", updated, err)
	}

	turns.latest = &agentdomain.Turn{Status: agentdomain.TurnStatusRunning}
	turns.latestErr = nil
	if _, err = service.UpdateConversationMode(context.Background(), 3, agentID, created.ID, UpdateConversationModeRequest{Mode: "react"}); !errors.Is(err, agenterrors.ErrInvalidInput) {
		t.Fatalf("active turn must block mode changes, got %v", err)
	}
	turns.latest = nil
	turns.latestErr = agentdomain.ErrNoTurnAvailable
	forked, err := service.ForkConversation(context.Background(), 3, agentID, created.ID, false)
	if err != nil || forked.AgentMode != "plan_execute" || forked.ParentConversationID == nil || *forked.ParentConversationID != created.ID {
		t.Fatalf("fork did not inherit mode: item=%+v err=%v", forked, err)
	}

	turns.latestErr = errors.New("turn storage unavailable")
	if _, err = service.UpdateConversationMode(context.Background(), 3, agentID, created.ID, UpdateConversationModeRequest{Mode: "react"}); err == nil || err.Error() != "turn storage unavailable" {
		t.Fatalf("unexpected repository errors must propagate, got %v", err)
	}
}

func TestStartTurnSnapshotsModeAndReturnsPersistentUserMessage(t *testing.T) {
	agentID, releaseID := int64(10), int64(100)
	conv := &conversation.Conversation{ID: 20, OwnerID: 3, AgentID: &agentID, AgentReleaseID: &releaseID, Source: conversation.SourceAgent, AgentMode: "plan_execute"}
	conversations := &settingsConversationRepo{items: map[int64]*conversation.Conversation{20: conv}}
	turns := &settingsTurnRepo{latestErr: agentdomain.ErrNoTurnAvailable}
	service := NewService(nil, turns, conversations, nil, nil, nil, nil, nil, nil, nil)

	accepted, err := service.StartTurn(context.Background(), 3, agentID, 20, "request-1", CreateTurnRequest{Content: " investigate "})
	if err != nil {
		t.Fatalf("StartTurn returned error: %v", err)
	}
	if accepted.UserMessage == nil || accepted.UserMessage.ID != 301 || accepted.UserMessage.Content != "investigate" || accepted.Turn.UserMessageID != 301 {
		t.Fatalf("persistent user message was not returned: %+v", accepted)
	}
	var turnInput, runInput map[string]any
	if err = json.Unmarshal(accepted.Turn.InputJSON, &turnInput); err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(accepted.Run.InputJSON, &runInput); err != nil {
		t.Fatal(err)
	}
	if turnInput["mode"] != "plan_execute" || runInput["mode"] != "plan_execute" || turnInput["query"] != "investigate" {
		t.Fatalf("turn mode was not snapshotted: turn=%v run=%v", turnInput, runInput)
	}
}

func TestCancelIndependentRunCancelsQueuedTurn(t *testing.T) {
	runID := int64(401)
	turn := &agentdomain.Turn{ID: 201, OwnerID: 3, RunID: &runID, Status: agentdomain.TurnStatusQueued}
	turns := &settingsTurnRepo{byRun: map[int64]*agentdomain.Turn{runID: turn}}
	service := NewService(nil, turns, nil, nil, nil, nil, nil, nil, nil, nil)

	if err := service.CancelIndependentRun(context.Background(), 3, runID); err != nil {
		t.Fatalf("CancelIndependentRun returned error: %v", err)
	}
	if turns.updated == nil || turns.updated.Status != agentdomain.TurnStatusCancelled || turns.updated.FinishedAt == nil {
		t.Fatalf("queued turn was not cancelled with the run: %+v", turns.updated)
	}
}

type settingsAgentRepo struct {
	agentdomain.Repository
	items    map[int64]*agentdomain.Agent
	releases []agentdomain.Release
	nextID   int64
}

func newSettingsAgentRepo() *settingsAgentRepo {
	return &settingsAgentRepo{items: map[int64]*agentdomain.Agent{}, nextID: 10}
}

func (r *settingsAgentRepo) Create(_ context.Context, item *agentdomain.Agent) error {
	item.ID = r.nextID
	r.nextID++
	r.items[item.ID] = item
	return nil
}

func (r *settingsAgentRepo) FindByID(_ context.Context, ownerID, id int64) (*agentdomain.Agent, error) {
	item := r.items[id]
	if item == nil || item.OwnerID != ownerID {
		return nil, agenterrors.ErrNotFound
	}
	return item, nil
}

func (r *settingsAgentRepo) Update(_ context.Context, item *agentdomain.Agent) error {
	r.items[item.ID] = item
	return nil
}

func (r *settingsAgentRepo) NextReleaseVersion(context.Context, int64, int64) (int, error) {
	return len(r.releases) + 1, nil
}

func (r *settingsAgentRepo) CreateRelease(_ context.Context, item *agentdomain.Release) error {
	item.ID = int64(100 + len(r.releases))
	r.releases = append(r.releases, *item)
	return nil
}

func (r *settingsAgentRepo) SetCurrentRelease(_ context.Context, ownerID, agentID, releaseID int64) error {
	item, err := r.FindByID(context.Background(), ownerID, agentID)
	if err != nil {
		return err
	}
	item.CurrentReleaseID = &releaseID
	item.Status = agentdomain.StatusActive
	return nil
}

type settingsProviderRepo struct {
	provider.Repository
	items map[int64]*provider.ModelProvider
}

func (r *settingsProviderRepo) FindByID(_ context.Context, ownerID, id int64) (*provider.ModelProvider, error) {
	item := r.items[id]
	if item == nil || item.OwnerID != ownerID {
		return nil, agenterrors.ErrNotFound
	}
	return item, nil
}

type settingsKnowledgeRepo struct {
	knowledge.BaseRepository
	items map[int64]*knowledge.KnowledgeBase
}

func (r *settingsKnowledgeRepo) FindByID(_ context.Context, ownerID, id int64) (*knowledge.KnowledgeBase, error) {
	item := r.items[id]
	if item == nil || item.OwnerID != ownerID {
		return nil, agenterrors.ErrNotFound
	}
	return item, nil
}

type settingsConversationRepo struct {
	conversation.AgentRepository
	items  map[int64]*conversation.Conversation
	nextID int64
}

func (r *settingsConversationRepo) Create(_ context.Context, item *conversation.Conversation) error {
	if r.nextID == 0 {
		r.nextID = 20
	}
	item.ID = r.nextID
	r.nextID++
	r.items[item.ID] = item
	return nil
}

func (r *settingsConversationRepo) FindByID(_ context.Context, ownerID, id int64) (*conversation.Conversation, error) {
	item := r.items[id]
	if item == nil || item.OwnerID != ownerID {
		return nil, agenterrors.ErrNotFound
	}
	return item, nil
}

func (r *settingsConversationRepo) UpdateAgentMode(_ context.Context, ownerID, id int64, mode string) error {
	item, err := r.FindByID(context.Background(), ownerID, id)
	if err != nil {
		return err
	}
	item.AgentMode = mode
	return nil
}

type settingsMessageRepo struct {
	conversation.MessageRepository
	byConversation map[int64][]conversation.Message
}

func (r *settingsMessageRepo) ListByConversation(_ context.Context, _ int64, conversationID int64) ([]conversation.Message, error) {
	return append([]conversation.Message(nil), r.byConversation[conversationID]...), nil
}

func (r *settingsMessageRepo) Create(_ context.Context, message *conversation.Message) error {
	message.ID = int64(400 + len(r.byConversation[message.ConversationID]))
	r.byConversation[message.ConversationID] = append(r.byConversation[message.ConversationID], *message)
	return nil
}

type settingsTurnRepo struct {
	agentdomain.TurnRepository
	latest    *agentdomain.Turn
	latestErr error
	byRun     map[int64]*agentdomain.Turn
	updated   *agentdomain.Turn
}

func (r *settingsTurnRepo) FindLatestByConversation(context.Context, int64, int64, int64) (*agentdomain.Turn, error) {
	return r.latest, r.latestErr
}

func (*settingsTurnRepo) FindByIdempotencyKey(context.Context, int64, int64, string) (*agentdomain.Turn, error) {
	return nil, agentdomain.ErrNoTurnAvailable
}

func (*settingsTurnRepo) CreateWithArtifacts(_ context.Context, turn *agentdomain.Turn, message *conversation.Message, run *workflow.Run) error {
	turn.ID = 201
	message.ID = 301
	run.ID = 401
	turn.UserMessageID = message.ID
	turn.RunID = &run.ID
	return nil
}

func (r *settingsTurnRepo) FindByRunID(_ context.Context, ownerID, runID int64) (*agentdomain.Turn, error) {
	item := r.byRun[runID]
	if item == nil || item.OwnerID != ownerID {
		return nil, agentdomain.ErrNoTurnAvailable
	}
	return item, nil
}

func (r *settingsTurnRepo) Update(_ context.Context, item *agentdomain.Turn) error {
	r.updated = item
	return nil
}
