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
	"agentcanvas/internal/infrastructure/llm"
	agenterrors "agentcanvas/internal/pkg/errors"
	runtimeagent "agentcanvas/internal/runtime/agent"
	agentruntime "agentcanvas/internal/runtime/agentruntime"
	"agentcanvas/internal/runtime/toolruntime"
)

func TestManagedDefinitionOwnsInternalDefaults(t *testing.T) {
	temperature := 0.4
	definition := ManagedDefinition(AgentEditableSettings{
		ProviderID: 8, Model: "  gpt-test  ", SystemPrompt: "  be useful  ",
		KnowledgeIDs: []int64{4, 4, 2}, Temperature: &temperature,
	})

	if definition.Mode != "react" || !definition.MemoryEnabled || !definition.ReflectionEnabled || !definition.AllowSubagents {
		t.Fatalf("managed runtime defaults are incomplete: %+v", definition)
	}
	if definition.MaxIterations != 8 || definition.MaxToolCalls != 16 || definition.MaxExecutionTimeMS != 120000 ||
		definition.MaxToolTimeoutMS != 30000 || definition.MaxToolOutputBytes != 512*1024 ||
		definition.MaxParallelSubAgents != 4 || definition.MaxSubagentDepth != 3 || definition.OutputMode != "final_answer" {
		t.Fatalf("unexpected managed limits: %+v", definition)
	}
	if definition.Model != "gpt-test" || definition.SystemPrompt != "be useful" || len(definition.KnowledgeIDs) != 2 ||
		definition.KnowledgeIDs[0] != 4 || definition.KnowledgeIDs[1] != 2 {
		t.Fatalf("editable settings were not normalized: %+v", definition)
	}
}

func TestCreateAgentValidatesResourcesAndPublishesFirstRelease(t *testing.T) {
	agents := newSettingsAgentRepo()
	service := NewService(agents, nil, nil, nil, nil, nil, nil, nil, nil)
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
	service := NewService(agents, turns, conversations, messages, nil, nil, nil, nil, nil)

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
	definition := agentdomain.Definition{ProviderID: 1, Model: "test-model", SystemPrompt: "test", Mode: "react"}
	definitionJSON, checksum, err := definition.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	agents := &settingsAgentRepo{releases: []agentdomain.Release{{ID: releaseID, OwnerID: 3, AgentID: agentID, DefinitionJSON: definitionJSON, Checksum: checksum, RuleHash: "rules"}}}
	service := NewService(agents, turns, conversations, nil, nil, nil, nil, nil, nil)

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

func TestCancelRunCancelsQueuedTurn(t *testing.T) {
	runID := int64(401)
	turn := &agentdomain.Turn{ID: 201, OwnerID: 3, RunID: &runID, Status: agentdomain.TurnStatusQueued}
	turns := &settingsTurnRepo{byRun: map[int64]*agentdomain.Turn{runID: turn}}
	runs := &settingsRunRepo{item: &agentdomain.Run{ID: runID, OwnerID: 3, Status: agentdomain.RunStatusQueued}}
	service := NewService(nil, turns, nil, nil, runs, nil, nil, nil, nil)

	if err := service.CancelRun(context.Background(), 3, runID); err != nil {
		t.Fatalf("CancelRun returned error: %v", err)
	}
	if turns.updated == nil || turns.updated.Status != agentdomain.TurnStatusCancelled || turns.updated.FinishedAt == nil {
		t.Fatalf("queued turn was not cancelled with the run: %+v", turns.updated)
	}
}

func TestRunSubagentPersistsParentAndDelegationMetadata(t *testing.T) {
	parent := &agentdomain.Run{ID: 41, OwnerID: 3, AgentID: 10, RunType: agentdomain.RunTypeTurn, DelegationDepth: 0}
	runs := &settingsRunRepo{items: map[int64]*agentdomain.Run{parent.ID: parent}, nextID: 42}
	runtime := &settingsAgentRuntime{executeResult: &agentruntime.RunResult{Output: agentruntime.RunOutput{"final_answer": "done"}}}
	service := NewService(nil, nil, nil, nil, runs, nil, nil, nil, runtime)
	conversationID := int64(20)

	result, err := service.RunSubagent(context.Background(), toolruntime.SubagentRequest{
		OwnerID: 3, ParentRunID: parent.ID, AgentID: parent.AgentID, ConversationID: &conversationID,
		DelegationDepth: 0, MaxDepth: 3,
		Definition: toolruntime.SubagentDefinition{Task: " research ", SystemPrompt: "be precise", MaxDepth: 3},
	})
	if err != nil {
		t.Fatalf("RunSubagent returned error: %v", err)
	}
	child := runs.items[result.RunID]
	if child == nil || child.RunType != agentdomain.RunTypeSubagent || child.ParentRunID == nil || *child.ParentRunID != parent.ID ||
		child.DelegationDepth != 1 || child.Status != agentdomain.RunStatusSucceeded {
		t.Fatalf("subagent relationship was not persisted: %+v", child)
	}
	if child.FinishedAt == nil || runtime.executeReq.RunID != child.ID || runtime.executeReq.ParentRunID == nil ||
		*runtime.executeReq.ParentRunID != parent.ID || runtime.executeReq.DelegationDepth != 1 || runtime.executeReq.Task != "research" {
		t.Fatalf("runtime request did not preserve delegation context: run=%+v request=%+v", child, runtime.executeReq)
	}
}

func TestRunSubagentRejectsDepthAndParentContextMismatch(t *testing.T) {
	parent := &agentdomain.Run{ID: 41, OwnerID: 3, AgentID: 10, DelegationDepth: 2}
	runs := &settingsRunRepo{items: map[int64]*agentdomain.Run{parent.ID: parent}, nextID: 42}
	service := NewService(nil, nil, nil, nil, runs, nil, nil, nil, &settingsAgentRuntime{})
	base := toolruntime.SubagentRequest{OwnerID: 3, ParentRunID: parent.ID, AgentID: parent.AgentID,
		DelegationDepth: 2, MaxDepth: 2, Definition: toolruntime.SubagentDefinition{Task: "research"}}

	if _, err := service.RunSubagent(context.Background(), base); !errors.Is(err, agenterrors.ErrForbidden) {
		t.Fatalf("maximum delegation depth must be rejected, got %v", err)
	}
	base.MaxDepth = 3
	base.AgentID = 99
	if _, err := service.RunSubagent(context.Background(), base); !errors.Is(err, agenterrors.ErrForbidden) {
		t.Fatalf("mismatched parent Agent context must be rejected, got %v", err)
	}
	if len(runs.items) != 1 {
		t.Fatalf("rejected delegation must not create a child run: %+v", runs.items)
	}
}

func TestRunSubagentLeavesPausedRunOpenAndPersistsCheckpoint(t *testing.T) {
	parent := &agentdomain.Run{ID: 41, OwnerID: 3, AgentID: 10, DelegationDepth: 0}
	runs := &settingsRunRepo{items: map[int64]*agentdomain.Run{parent.ID: parent}, nextID: 42}
	approvals := &settingsApprovalRepo{}
	runtime := &settingsAgentRuntime{executeResult: &agentruntime.RunResult{Output: agentruntime.RunOutput{
		"stop_reason": runtimeagent.StopReasonPaused,
		"checkpoint":  runtimeagent.Checkpoint{MessagesSummary: "paused state"},
	}}}
	service := NewService(nil, nil, nil, nil, runs, nil, nil, approvals, runtime)

	result, err := service.RunSubagent(context.Background(), toolruntime.SubagentRequest{OwnerID: 3,
		ParentRunID: parent.ID, AgentID: parent.AgentID, MaxDepth: 2,
		Definition: toolruntime.SubagentDefinition{Task: "pause here"},
	})
	if err != nil {
		t.Fatalf("RunSubagent returned error: %v", err)
	}
	child := runs.items[result.RunID]
	if child == nil || child.Status != agentdomain.RunStatusPaused || child.FinishedAt != nil || approvals.createdCheckpoint == nil {
		t.Fatalf("paused subagent was finalized or lost its checkpoint: run=%+v checkpoint=%+v", child, approvals.createdCheckpoint)
	}
}

func TestPersistCheckpointRoundTripsAgentState(t *testing.T) {
	approvals := &settingsApprovalRepo{}
	service := NewService(nil, nil, nil, nil, nil, nil, nil, approvals, nil)
	run := &agentdomain.Run{ID: 42, OwnerID: 3, AgentID: 10}
	checkpoint := runtimeagent.Checkpoint{
		SnapshotVersion: 2,
		BaseMessages:    []llm.ChatMessage{{Role: conversation.RoleSystem, Content: "system"}},
		Messages:        []llm.ChatMessage{{Role: conversation.RoleUser, Content: "continue"}},
		Interaction:     &runtimeagent.Interaction{ID: "approval-42", Kind: "tool_approval", ToolCallID: "call-1"},
		RuleHash:        "rules-hash",
		Metadata: map[string]any{
			"tool_registry_hash": "registry-hash",
			"tool_policy_hash":   "policy-hash",
		},
	}
	output := agentruntime.RunOutput{
		"approval":   runtimeagent.Approval{ToolCallID: "call-1", ToolName: "shell", RiskLevel: "high", Reason: "requires review"},
		"checkpoint": checkpoint,
	}

	if err := service.persistCheckpoint(context.Background(), run, output, agentdomain.RunStatusWaitingHuman); err != nil {
		t.Fatalf("persistCheckpoint returned error: %v", err)
	}
	if approvals.createdApproval == nil || approvals.createdApproval.InteractionID != "approval-42" {
		t.Fatalf("approval interaction was not persisted: %+v", approvals.createdApproval)
	}
	stored := approvals.createdCheckpoint
	if stored == nil || stored.SnapshotVersion != 2 || stored.InteractionID != "approval-42" ||
		stored.ToolRegistryHash != "registry-hash" || stored.ToolPolicyHash != "policy-hash" || len(stored.RuntimeCheckpointJSON) == 0 {
		t.Fatalf("checkpoint state was not persisted: %+v", stored)
	}
	decoded, err := decodeCheckpoint(stored)
	if err != nil {
		t.Fatalf("decodeCheckpoint returned error: %v", err)
	}
	if decoded.Interaction == nil || decoded.Interaction.ID != "approval-42" || len(decoded.BaseMessages) != 1 || decoded.RuleHash != "rules-hash" {
		t.Fatalf("checkpoint did not round trip: %+v", decoded)
	}
}

func TestResumeByIDPropagatesPendingApprovalStorageFailure(t *testing.T) {
	run := &agentdomain.Run{ID: 42, OwnerID: 3, AgentID: 10, Status: agentdomain.RunStatusPaused}
	runs := &settingsRunRepo{items: map[int64]*agentdomain.Run{run.ID: run}}
	storageErr := errors.New("approval storage unavailable")
	service := NewService(nil, nil, nil, nil, runs, nil, nil, &settingsApprovalRepo{pendingErr: storageErr}, nil)

	if _, err := service.ResumeByID(context.Background(), 3, run.ID); !errors.Is(err, storageErr) {
		t.Fatalf("unexpected approval storage errors must propagate, got %v", err)
	}
}

func TestDecideApprovalResumesSubagentWithoutTurn(t *testing.T) {
	_, definitionJSON, err := subagentRuntimeDefinition(toolruntime.SubagentDefinition{Task: "continue", SystemPrompt: "be precise"})
	if err != nil {
		t.Fatal(err)
	}
	run := &agentdomain.Run{ID: 42, OwnerID: 3, AgentID: 10, RunType: agentdomain.RunTypeSubagent,
		Status: agentdomain.RunStatusWaitingHuman, DefinitionJSON: definitionJSON, DefinitionHash: hashJSON(definitionJSON), InputJSON: json.RawMessage(`{"query":"continue"}`)}
	runs := &settingsRunRepo{items: map[int64]*agentdomain.Run{run.ID: run}}
	request := &agentdomain.ApprovalRequest{ID: 7, OwnerID: 3, RunID: run.ID, Status: agentdomain.ApprovalStatusPending}
	checkpoint := &agentdomain.RunCheckpoint{ID: 8, OwnerID: 3, RunID: run.ID, MessagesJSON: json.RawMessage(`[]`), PendingToolCallJSON: json.RawMessage(`null`), ContextJSON: json.RawMessage(`{}`)}
	approvals := &settingsApprovalRepo{request: request, checkpoint: checkpoint}
	runtime := &settingsAgentRuntime{resumeResult: &agentruntime.RunResult{Output: agentruntime.RunOutput{"final_answer": "approved"}}}
	service := NewService(nil, nil, nil, nil, runs, nil, nil, approvals, runtime)

	resumed, err := service.DecideApprovalRequest(context.Background(), 3, request.ID, true, "ok")
	if err != nil {
		t.Fatalf("DecideApprovalRequest returned error: %v", err)
	}
	if !approvals.decided || resumed.Status != agentdomain.RunStatusSucceeded || resumed.FinishedAt == nil || runtime.resumeReq.RunID != run.ID {
		t.Fatalf("subagent approval did not resume directly: run=%+v decided=%v request=%+v", resumed, approvals.decided, runtime.resumeReq)
	}
}

type settingsRunRepo struct {
	agentdomain.RunRepository
	item   *agentdomain.Run
	items  map[int64]*agentdomain.Run
	nextID int64
}

func (r *settingsRunRepo) FindByID(_ context.Context, ownerID, id int64) (*agentdomain.Run, error) {
	item := r.item
	if r.items != nil {
		item = r.items[id]
	}
	if item == nil || item.OwnerID != ownerID || item.ID != id {
		return nil, agenterrors.ErrNotFound
	}
	return item, nil
}

func (r *settingsRunRepo) Create(_ context.Context, item *agentdomain.Run) error {
	if r.items == nil {
		r.items = map[int64]*agentdomain.Run{}
	}
	if r.nextID == 0 {
		r.nextID = 1
	}
	item.ID = r.nextID
	r.nextID++
	r.items[item.ID] = item
	return nil
}

func (r *settingsRunRepo) Update(_ context.Context, item *agentdomain.Run) error {
	r.item = item
	if r.items != nil {
		r.items[item.ID] = item
	}
	return nil
}

func (r *settingsRunRepo) ListByParent(_ context.Context, ownerID, parentRunID int64) ([]agentdomain.Run, error) {
	items := make([]agentdomain.Run, 0)
	for _, item := range r.items {
		if item != nil && item.OwnerID == ownerID && item.ParentRunID != nil && *item.ParentRunID == parentRunID {
			items = append(items, *item)
		}
	}
	return items, nil
}

type settingsAgentRuntime struct {
	executeReq    agentruntime.RunRequest
	executeResult *agentruntime.RunResult
	executeErr    error
	resumeReq     agentruntime.ResumeRequest
	resumeResult  *agentruntime.RunResult
	resumeErr     error
}

func (r *settingsAgentRuntime) Execute(_ context.Context, request agentruntime.RunRequest, _ agentruntime.EventEmitter) (*agentruntime.RunResult, error) {
	r.executeReq = request
	return r.executeResult, r.executeErr
}

func (r *settingsAgentRuntime) Resume(_ context.Context, request agentruntime.ResumeRequest, _ agentruntime.EventEmitter) (*agentruntime.RunResult, error) {
	r.resumeReq = request
	return r.resumeResult, r.resumeErr
}

type settingsApprovalRepo struct {
	agentdomain.ApprovalRepository
	pending           *agentdomain.ApprovalRequest
	pendingErr        error
	request           *agentdomain.ApprovalRequest
	checkpoint        *agentdomain.RunCheckpoint
	createdApproval   *agentdomain.ApprovalRequest
	createdCheckpoint *agentdomain.RunCheckpoint
	decided           bool
}

func (r *settingsApprovalRepo) FindApprovalRequestByID(_ context.Context, ownerID, id int64) (*agentdomain.ApprovalRequest, error) {
	if r.request == nil || r.request.OwnerID != ownerID || r.request.ID != id {
		return nil, agenterrors.ErrNotFound
	}
	return r.request, nil
}

func (r *settingsApprovalRepo) FindPendingApprovalByRun(context.Context, int64, int64) (*agentdomain.ApprovalRequest, error) {
	return r.pending, r.pendingErr
}

func (r *settingsApprovalRepo) CreateApprovalRequest(_ context.Context, item *agentdomain.ApprovalRequest) error {
	r.createdApproval = item
	return nil
}

func (r *settingsApprovalRepo) FindLatestCheckpointByRun(_ context.Context, ownerID, runID int64) (*agentdomain.RunCheckpoint, error) {
	if r.checkpoint == nil || r.checkpoint.OwnerID != ownerID || r.checkpoint.RunID != runID {
		return nil, agenterrors.ErrNotFound
	}
	return r.checkpoint, nil
}

func (r *settingsApprovalRepo) CreateCheckpoint(_ context.Context, item *agentdomain.RunCheckpoint) error {
	r.createdCheckpoint = item
	return nil
}

func (r *settingsApprovalRepo) DecideApprovalAndClaimResume(_ context.Context, item *agentdomain.ApprovalRequest) error {
	r.decided = true
	r.request = item
	return nil
}

func (*settingsApprovalRepo) ClaimResume(context.Context, int64, int64) error { return nil }

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

func (r *settingsAgentRepo) FindReleaseByID(_ context.Context, ownerID, id int64) (*agentdomain.Release, error) {
	for index := range r.releases {
		if r.releases[index].ID == id && r.releases[index].OwnerID == ownerID {
			return &r.releases[index], nil
		}
	}
	return nil, agenterrors.ErrNotFound
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

func (*settingsTurnRepo) CreateWithArtifacts(_ context.Context, turn *agentdomain.Turn, message *conversation.Message, run *agentdomain.Run) error {
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
