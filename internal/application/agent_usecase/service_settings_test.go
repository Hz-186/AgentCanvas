package agent_usecase

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	workspaceusecase "agentcanvas/internal/application/workspace_usecase"
	"agentcanvas/internal/domain"
	agentdomain "agentcanvas/internal/domain/agent"
	"agentcanvas/internal/domain/conversation"
	"agentcanvas/internal/domain/knowledge"
	projectdomain "agentcanvas/internal/domain/project"
	"agentcanvas/internal/domain/provider"
	workspacedomain "agentcanvas/internal/domain/workspace"
	gitinfra "agentcanvas/internal/infrastructure/git"
	"agentcanvas/internal/infrastructure/llm"
	agenterrors "agentcanvas/internal/pkg/errors"
	"agentcanvas/internal/pkg/jsonutil"
	runtimeagent "agentcanvas/internal/runtime/agent"
	agentruntime "agentcanvas/internal/runtime/agentruntime"
	runtimeevent "agentcanvas/internal/runtime/event"
	"agentcanvas/internal/runtime/toolruntime"
)

func TestManagedDefinitionOwnsInternalDefaults(t *testing.T) {
	temperature := 0.4
	definition := ManagedDefinition(AgentEditableSettings{
		ProviderID: 8, Model: "  gpt-test  ", SystemPrompt: "  be useful  ",
		KnowledgeBaseIDs: []int64{4, 4, 2}, PythonToolNames: []string{" python_text_stats ", "python_text_stats"}, Temperature: &temperature,
	})

	if definition.Mode != "react" || !definition.MemoryEnabled || !definition.ReflectionEnabled || !definition.AllowSubagents {
		t.Fatalf("managed runtime defaults are incomplete: %+v", definition)
	}
	if definition.MaxIterations != 8 || definition.MaxToolCalls != 16 || definition.MaxExecutionTimeMS != 120000 ||
		definition.MaxToolTimeoutMS != 30000 || definition.MaxToolOutputBytes != 512*1024 ||
		definition.MaxParallelSubAgents != 4 || definition.MaxSubagentDepth != 3 || definition.OutputMode != "final_answer" {
		t.Fatalf("unexpected managed limits: %+v", definition)
	}
	if definition.Model != "gpt-test" || definition.SystemPrompt != "be useful" || len(definition.KnowledgeBaseIDs) != 2 ||
		definition.KnowledgeBaseIDs[0] != 4 || definition.KnowledgeBaseIDs[1] != 2 || len(definition.PythonToolNames) != 1 || definition.PythonToolNames[0] != "python_text_stats" {
		t.Fatalf("editable settings were not normalized: %+v", definition)
	}
}

func TestCreateAgentValidatesResourcesAndPublishesFirstRelease(t *testing.T) {
	agents := newSettingsAgentRepo()
	service := NewService(agents, nil, nil, nil, nil, nil, nil, nil, nil)
	service.ConfigureEditableResources(
		&settingsProviderRepo{items: map[int64]*provider.ModelProvider{7: {SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 7, OwnerID: 3}}, Enabled: provider.ProviderEnabled}}},
		&settingsKnowledgeRepo{items: map[int64]*knowledge.KnowledgeBase{9: {SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 9, OwnerID: 3}}, Enabled: knowledge.KnowledgeBaseEnabled}}},
	)

	view, err := service.CreateAgent(context.Background(), 3, CreateAgentRequest{
		Name: " Planner ", Settings: AgentEditableSettings{ProviderID: 7, KnowledgeBaseIDs: []int64{9}},
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
	agents.items[agentID] = &agentdomain.Agent{SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: agentID, OwnerID: 3}}, Status: agentdomain.StatusActive, CurrentReleaseID: &releaseID}
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

func TestCreateConversationRejectsArchivedProject(t *testing.T) {
	agentID, releaseID, projectID := int64(10), int64(100), int64(11)
	agents := newSettingsAgentRepo()
	agents.items[agentID] = &agentdomain.Agent{SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: agentID, OwnerID: 3}}, Status: agentdomain.StatusActive, CurrentReleaseID: &releaseID}
	conversations := &settingsConversationRepo{items: map[int64]*conversation.Conversation{}, nextID: 20}
	service := NewService(agents, nil, conversations, nil, nil, nil, nil, nil, nil)
	service.ConfigureWorkspace(workspaceusecase.NewService(
		&staticLifecycleProjectRepo{item: projectdomain.Project{BaseModel: domain.BaseModel{ID: projectID, OwnerID: 3}, Archived: true}},
		nil,
		gitinfra.NewService(gitinfra.Config{}),
		workspaceusecase.Config{Enabled: true},
	))

	_, err := service.CreateConversation(context.Background(), 3, agentID, CreateConversationRequest{ProjectID: &projectID})
	if !errors.Is(err, agenterrors.ErrForbidden) {
		t.Fatalf("archived project must reject new conversations, got %v", err)
	}
	if len(conversations.items) != 0 {
		t.Fatalf("conversation was persisted for archived project: %#v", conversations.items)
	}
}

func TestStartTurnSnapshotsModeAndReturnsPersistentUserMessage(t *testing.T) {
	agentID, releaseID := int64(10), int64(100)
	conv := &conversation.Conversation{SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 20, OwnerID: 3}}, AgentID: &agentID, AgentReleaseID: &releaseID, AgentMode: "plan_execute"}
	conversations := &settingsConversationRepo{items: map[int64]*conversation.Conversation{20: conv}}
	turns := &settingsTurnRepo{latestErr: agentdomain.ErrNoTurnAvailable}
	definition := agentdomain.Definition{ModelConfig: agentdomain.ModelConfig{ProviderID: 1, Model: "test-model"}, PromptConfig: agentdomain.PromptConfig{SystemPrompt: "test"}, ExecutionLimits: agentdomain.ExecutionLimits{Mode: "react"}}
	definitionJSON, checksum, err := definition.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	agents := &settingsAgentRepo{releases: []agentdomain.Release{{ImmutableModel: domain.ImmutableModel{ID: releaseID, OwnerID: 3}, AgentID: agentID, DefinitionJSON: definitionJSON, Checksum: checksum, RuleHash: "rules"}}}
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

func TestExecuteTurnAcceptsEmptyHistoricalInputAndFailsNilRuntimeResult(t *testing.T) {
	definitionJSON, _, err := (agentdomain.Definition{
		ModelConfig:  agentdomain.ModelConfig{ProviderID: 1, Model: "test-model"},
		PromptConfig: agentdomain.PromptConfig{SystemPrompt: "test"},
	}).Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	runID := int64(401)
	run := &agentdomain.Run{BaseModel: domain.BaseModel{ID: runID, OwnerID: 3}, AgentID: 10, Status: agentdomain.RunStatusQueued, DefinitionJSON: definitionJSON, StartedAt: time.Now().UTC()}
	turn := &agentdomain.Turn{BaseModel: domain.BaseModel{ID: 201, OwnerID: 3}, AgentID: 10, RunID: &runID, Status: agentdomain.TurnStatusRunning}
	runs := &settingsRunRepo{items: map[int64]*agentdomain.Run{runID: run}}
	turns := &settingsTurnRepo{}
	runtime := &settingsAgentRuntime{}

	NewService(nil, turns, nil, nil, runs, nil, nil, nil, runtime).executeTurnOwned(context.Background(), turn)

	if runtime.executeReq.RunID != runID || turn.Status != agentdomain.TurnStatusFailed || run.Status != agentdomain.RunStatusFailed || !strings.Contains(run.ErrorMessage, "returned no result") {
		t.Fatalf("empty input or nil result handling mismatch: request=%+v turn=%+v run=%+v", runtime.executeReq, turn, run)
	}
}

func TestProjectTurnPassesProjectIDWithoutWorkspace(t *testing.T) {
	definitionJSON, _, err := (agentdomain.Definition{
		ModelConfig:  agentdomain.ModelConfig{ProviderID: 1, Model: "test-model"},
		PromptConfig: agentdomain.PromptConfig{SystemPrompt: "test"},
	}).Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	conversationID, projectID, runID, agentID := int64(20), int64(11), int64(401), int64(10)
	run := &agentdomain.Run{BaseModel: domain.BaseModel{ID: runID, OwnerID: 3}, AgentID: agentID, ConversationID: &conversationID, Status: agentdomain.RunStatusQueued, DefinitionJSON: definitionJSON, StartedAt: time.Now().UTC()}
	turn := &agentdomain.Turn{BaseModel: domain.BaseModel{ID: 201, OwnerID: 3}, AgentID: agentID, ConversationID: conversationID, RunID: &runID, Status: agentdomain.TurnStatusQueued}
	runs := &settingsRunRepo{items: map[int64]*agentdomain.Run{runID: run}}
	conversations := &settingsConversationRepo{items: map[int64]*conversation.Conversation{
		conversationID: {SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: conversationID, OwnerID: 3}}, AgentID: &agentID, ProjectID: &projectID},
	}}
	runtime := &settingsAgentRuntime{}
	service := NewService(nil, &settingsTurnRepo{}, conversations, nil, runs, nil, nil, nil, runtime)
	service.ConfigureWorkspace(workspaceusecase.NewService(nil, nil, nil, workspaceusecase.Config{Enabled: false}))

	service.executeTurnOwned(context.Background(), turn)

	if runtime.executeReq.ProjectID != projectID || runtime.executeReq.Workspace != nil {
		t.Fatalf("project context depends on workspace: %+v", runtime.executeReq)
	}
}

func TestExecuteTurnFailsInvalidPersistedInputBeforeRuntime(t *testing.T) {
	definitionJSON, _, err := (agentdomain.Definition{
		ModelConfig:  agentdomain.ModelConfig{ProviderID: 1, Model: "test-model"},
		PromptConfig: agentdomain.PromptConfig{SystemPrompt: "test"},
	}).Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	runID := int64(402)
	run := &agentdomain.Run{BaseModel: domain.BaseModel{ID: runID, OwnerID: 3}, AgentID: 10, Status: agentdomain.RunStatusQueued, DefinitionJSON: definitionJSON, StartedAt: time.Now().UTC()}
	turn := &agentdomain.Turn{BaseModel: domain.BaseModel{ID: 202, OwnerID: 3}, AgentID: 10, RunID: &runID, Status: agentdomain.TurnStatusRunning, InputJSON: json.RawMessage(`{"query":`)}
	runs := &settingsRunRepo{items: map[int64]*agentdomain.Run{runID: run}}
	runtime := &settingsAgentRuntime{executeResult: &agentruntime.RunResult{Output: agentruntime.RunOutput{"final_answer": "must not execute"}}}

	NewService(nil, &settingsTurnRepo{}, nil, nil, runs, nil, nil, nil, runtime).executeTurnOwned(context.Background(), turn)

	if runtime.executeReq.RunID != 0 || turn.Status != agentdomain.TurnStatusFailed || run.Status != agentdomain.RunStatusFailed || !strings.Contains(run.ErrorMessage, "decode turn input") {
		t.Fatalf("invalid persisted input was not rejected: request=%+v turn=%+v run=%+v", runtime.executeReq, turn, run)
	}
}

func TestRootRunNeverEntersRunningWhenWorkspacePreparationFails(t *testing.T) {
	definition := agentdomain.Definition{ModelConfig: agentdomain.ModelConfig{ProviderID: 1, Model: "test-model"}, PromptConfig: agentdomain.PromptConfig{SystemPrompt: "test"}, ExecutionLimits: agentdomain.ExecutionLimits{Mode: "react"}}
	definitionJSON, _, err := definition.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	conversationID, projectID, runID, agentID := int64(20), int64(11), int64(401), int64(10)
	run := &agentdomain.Run{BaseModel: domain.BaseModel{ID: runID, OwnerID: 3}, AgentID: agentID, ConversationID: &conversationID,
		Status: agentdomain.RunStatusQueued, DefinitionJSON: definitionJSON,
		InputJSON: json.RawMessage(`{"query":"edit README","mode":"react"}`), StartedAt: time.Now().UTC(),
	}
	runs := &workspaceLifecycleRunRepo{settingsRunRepo: settingsRunRepo{items: map[int64]*agentdomain.Run{runID: run}}}
	turns := &settingsTurnRepo{}
	turn := &agentdomain.Turn{BaseModel: domain.BaseModel{ID: 201, OwnerID: 3}, AgentID: agentID, ConversationID: conversationID, RunID: &runID, InputJSON: run.InputJSON, Status: agentdomain.TurnStatusQueued}
	conversations := &settingsConversationRepo{items: map[int64]*conversation.Conversation{
		conversationID: {SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: conversationID, OwnerID: 3}}, AgentID: &agentID, ProjectID: &projectID, WorkspaceMode: "worktree"},
	}}
	events := &workspaceLifecycleEventRepo{}
	runtime := &settingsAgentRuntime{executeResult: &agentruntime.RunResult{Output: agentruntime.RunOutput{"final_answer": "should not run"}}}
	service := NewService(nil, turns, conversations, nil, runs, events, nil, nil, runtime)
	service.ConfigureWorkspace(workspaceusecase.NewService(
		&missingLifecycleProjectRepo{}, nil, gitinfra.NewService(gitinfra.Config{}),
		workspaceusecase.Config{Enabled: true, AllowedRoots: []string{"/workspaces"}},
	))

	service.executeTurnOwned(context.Background(), turn)
	if run.Status != agentdomain.RunStatusFailed {
		t.Fatalf("Run status = %q, want failed", run.Status)
	}
	for _, status := range runs.statuses {
		if status == agentdomain.RunStatusRunning {
			t.Fatalf("Run entered running before workspace readiness: %v", runs.statuses)
		}
	}
	if runtime.executeReq.RunID != 0 {
		t.Fatalf("runtime executed despite workspace failure: %#v", runtime.executeReq)
	}
	if len(events.items) != 1 || events.items[0].EventType != runtimeevent.WorkspaceFailed {
		t.Fatalf("workspace failure event missing: %#v", events.items)
	}
	var payload map[string]any
	if err := json.Unmarshal(events.items[0].PayloadJSON, &payload); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"workspace_id", "project_id", "run_id", "repository_root", "workspace_path", "branch_name", "base_sha", "head_sha", "dirty", "has_unpushed_commits", "status", "locked", "error_message"} {
		if _, ok := payload[key]; !ok {
			t.Fatalf("workspace.failed payload is missing %q: %#v", key, payload)
		}
	}
}

func TestRootRunDoesNotExecuteWhenWorkspaceLinkPersistenceFails(t *testing.T) {
	definition := agentdomain.Definition{ModelConfig: agentdomain.ModelConfig{ProviderID: 1, Model: "test-model"}, PromptConfig: agentdomain.PromptConfig{SystemPrompt: "test"}, ExecutionLimits: agentdomain.ExecutionLimits{Mode: "react"}}
	definitionJSON, _, err := definition.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	conversationID, projectID, runID, agentID := int64(20), int64(11), int64(402), int64(10)
	run := &agentdomain.Run{BaseModel: domain.BaseModel{ID: runID, OwnerID: 3}, AgentID: agentID, ConversationID: &conversationID,
		Status: agentdomain.RunStatusQueued, DefinitionJSON: definitionJSON,
		InputJSON: json.RawMessage(`{"query":"edit README","mode":"react"}`), StartedAt: time.Now().UTC(),
	}
	linkErr := errors.New("run workspace link unavailable")
	runs := &workspaceLifecycleRunRepo{settingsRunRepo: settingsRunRepo{items: map[int64]*agentdomain.Run{runID: run}}}
	turns := &settingsTurnRepo{ownedUpdateErr: linkErr}
	turn := &agentdomain.Turn{BaseModel: domain.BaseModel{ID: 202, OwnerID: 3}, AgentID: agentID, ConversationID: conversationID, RunID: &runID, InputJSON: run.InputJSON, Status: agentdomain.TurnStatusQueued}
	conversations := &settingsConversationRepo{items: map[int64]*conversation.Conversation{
		conversationID: {SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: conversationID, OwnerID: 3}}, AgentID: &agentID, ProjectID: &projectID, WorkspaceMode: workspacedomain.KindWorktree},
	}}
	events := &workspaceLifecycleEventRepo{}
	runtime := &settingsAgentRuntime{executeResult: &agentruntime.RunResult{Output: agentruntime.RunOutput{"final_answer": "should not run"}}}
	workspaceRepository := &lifecycleWorkspaceRepository{items: make(map[int64]workspacedomain.Workspace)}
	gitService := gitinfra.NewService(gitinfra.Config{GitUserName: "AgentCanvas Test", GitUserEmail: "agentcanvas@example.test"})
	service := NewService(nil, turns, conversations, nil, runs, events, nil, nil, runtime)
	service.ConfigureWorkspace(workspaceusecase.NewService(
		&staticLifecycleProjectRepo{item: projectdomain.Project{BaseModel: domain.BaseModel{ID: projectID, OwnerID: 3}, Slug: "demo", Name: "Demo", RepositoryRoot: root}},
		workspaceRepository, gitService,
		workspaceusecase.Config{Enabled: true, AllowedRoots: []string{root}, WorktreeDirName: ".worktrees", AutoInitRepository: true},
	))

	service.executeTurnOwned(context.Background(), turn)
	if !turns.ownedUpdateFailed || run.Status != agentdomain.RunStatusFailed || runtime.executeReq.RunID != 0 {
		t.Fatalf("workspace link failure did not stop execution: run=%+v runtime=%+v", run, runtime.executeReq)
	}
	if run.ErrorMessage != linkErr.Error() {
		t.Fatalf("unexpected Run error: %q", run.ErrorMessage)
	}
	if len(events.items) != 1 || events.items[0].EventType != runtimeevent.WorkspaceFailed {
		t.Fatalf("workspace link failure event missing: %#v", events.items)
	}
	workspaces, err := gitService.ListWorktrees(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range workspaces {
		if filepath.Clean(item.Path) != filepath.Clean(root) && item.Locked {
			t.Fatalf("failed Run left its worktree locked: %#v", item)
		}
	}
}

func TestSubagentDoesNotExecuteWhenWorkspaceLinkPersistenceFails(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	projectID, parentRunID := int64(11), int64(41)
	workspaceRepository := &lifecycleWorkspaceRepository{items: make(map[int64]workspacedomain.Workspace)}
	gitService := gitinfra.NewService(gitinfra.Config{GitUserName: "AgentCanvas Test", GitUserEmail: "agentcanvas@example.test"})
	workspaceService := workspaceusecase.NewService(
		&staticLifecycleProjectRepo{item: projectdomain.Project{BaseModel: domain.BaseModel{ID: projectID, OwnerID: 3}, Slug: "demo", Name: "Demo", RepositoryRoot: root}},
		workspaceRepository, gitService,
		workspaceusecase.Config{Enabled: true, AllowedRoots: []string{root}, WorktreeDirName: ".worktrees", AutoInitRepository: true},
	)
	parentWorkspace, err := workspaceService.PrepareRunWorkspace(ctx, 3, projectID, parentRunID, workspacedomain.KindWorktree, "demo", "parent task", nil)
	if err != nil {
		t.Fatal(err)
	}
	parent := &agentdomain.Run{BaseModel: domain.BaseModel{ID: parentRunID, OwnerID: 3}, AgentID: 10, RunType: agentdomain.RunTypeTurn, DelegationDepth: 0, Status: agentdomain.RunStatusRunning, WorkspaceID: &parentWorkspace.ID}
	linkErr := errors.New("child workspace link unavailable")
	runs := &workspaceLifecycleRunRepo{
		settingsRunRepo:  settingsRunRepo{items: map[int64]*agentdomain.Run{parent.ID: parent}, nextID: parentRunID + 1},
		workspaceLinkErr: linkErr,
	}
	events := &workspaceLifecycleEventRepo{}
	runtime := &settingsAgentRuntime{executeResult: &agentruntime.RunResult{Output: agentruntime.RunOutput{"final_answer": "should not run"}}}
	service := NewService(nil, nil, nil, nil, runs, events, nil, nil, runtime)
	service.ConfigureWorkspace(workspaceService)

	_, err = service.RunSubagent(ctx, toolruntime.SubagentRequest{
		OwnerID: 3, ParentRunID: parentRunID, AgentID: parent.AgentID, DelegationDepth: 0, MaxDepth: 3,
		Definition: toolruntime.SubagentDefinition{Task: "edit child file", WorkspaceMode: workspacedomain.KindWorktree},
	})
	if !errors.Is(err, linkErr) {
		t.Fatalf("RunSubagent returned %v, want %v", err, linkErr)
	}
	child := runs.items[parentRunID+1]
	if child == nil || child.Status != agentdomain.RunStatusFailed || child.ErrorMessage != linkErr.Error() || runtime.executeReq.RunID != 0 {
		t.Fatalf("workspace link failure did not stop the child Run: child=%+v runtime=%+v", child, runtime.executeReq)
	}
	if len(events.items) != 1 || events.items[0].EventType != runtimeevent.WorkspaceFailed {
		t.Fatalf("child workspace link failure event missing: %#v", events.items)
	}
	childWorkspace, err := workspaceRepository.FindByRunID(ctx, 3, child.ID)
	if err != nil {
		t.Fatal(err)
	}
	if childWorkspace.Locked || childWorkspace.LockReason != "" {
		t.Fatalf("failed child Run left its sibling worktree locked: %#v", childWorkspace)
	}
}

func TestRootRunEmitsFinalWorkspaceStatusAfterRuntimeMutation(t *testing.T) {
	definition := agentdomain.Definition{ModelConfig: agentdomain.ModelConfig{ProviderID: 1, Model: "test-model"}, PromptConfig: agentdomain.PromptConfig{SystemPrompt: "test"}, ExecutionLimits: agentdomain.ExecutionLimits{Mode: "react"}}
	definitionJSON, _, err := definition.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	conversationID, projectID, runID, agentID := int64(20), int64(11), int64(404), int64(10)
	run := &agentdomain.Run{BaseModel: domain.BaseModel{ID: runID, OwnerID: 3}, AgentID: agentID, ConversationID: &conversationID,
		Status: agentdomain.RunStatusQueued, DefinitionJSON: definitionJSON,
		InputJSON: json.RawMessage(`{"query":"edit README","mode":"react"}`), StartedAt: time.Now().UTC(),
	}
	runs := &workspaceLifecycleRunRepo{settingsRunRepo: settingsRunRepo{items: map[int64]*agentdomain.Run{runID: run}}}
	turns := &settingsTurnRepo{}
	turn := &agentdomain.Turn{BaseModel: domain.BaseModel{ID: 204, OwnerID: 3}, AgentID: agentID, ConversationID: conversationID, RunID: &runID, InputJSON: run.InputJSON, Status: agentdomain.TurnStatusQueued}
	conversations := &settingsConversationRepo{items: map[int64]*conversation.Conversation{
		conversationID: {SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: conversationID, OwnerID: 3}}, AgentID: &agentID, ProjectID: &projectID, WorkspaceMode: workspacedomain.KindShared},
	}}
	events := &workspaceLifecycleEventRepo{}
	runtime := &settingsAgentRuntime{
		executeResult: &agentruntime.RunResult{Output: agentruntime.RunOutput{"final_answer": "done"}},
		executeHook: func(request agentruntime.RunRequest) {
			if request.Workspace == nil {
				t.Fatal("runtime request is missing workspace context")
			}
			if writeErr := os.WriteFile(filepath.Join(request.Workspace.WorkspacePath, "agent.txt"), []byte("runtime change\n"), 0o644); writeErr != nil {
				t.Fatal(writeErr)
			}
		},
	}
	workspaceRepository := &lifecycleWorkspaceRepository{items: make(map[int64]workspacedomain.Workspace)}
	workspaceService := workspaceusecase.NewService(
		&staticLifecycleProjectRepo{item: projectdomain.Project{BaseModel: domain.BaseModel{ID: projectID, OwnerID: 3}, Slug: "demo", Name: "Demo", RepositoryRoot: root}},
		workspaceRepository,
		gitinfra.NewService(gitinfra.Config{GitUserName: "AgentCanvas Test", GitUserEmail: "agentcanvas@example.test"}),
		workspaceusecase.Config{Enabled: true, AllowedRoots: []string{root}, AutoInitRepository: true},
	)
	service := NewService(nil, turns, conversations, nil, runs, events, nil, nil, runtime)
	service.ConfigureWorkspace(workspaceService)

	service.executeTurnOwned(context.Background(), turn)
	var finalPayload map[string]any
	for index := range events.items {
		if events.items[index].EventType == runtimeevent.WorkspaceStatusChanged {
			if err := json.Unmarshal(events.items[index].PayloadJSON, &finalPayload); err != nil {
				t.Fatal(err)
			}
		}
	}
	if finalPayload == nil || finalPayload["dirty"] != true || finalPayload["run_id"] != float64(runID) || finalPayload["status"] != workspacedomain.StatusReady {
		t.Fatalf("final workspace.status_changed event did not describe the runtime mutation: %#v", finalPayload)
	}
}

func TestRefreshWorkspaceSnapshotPersistsRuntimeFileChanges(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	projectID := int64(11)
	workspaceRepository := &lifecycleWorkspaceRepository{items: make(map[int64]workspacedomain.Workspace)}
	gitService := gitinfra.NewService(gitinfra.Config{GitUserName: "AgentCanvas Test", GitUserEmail: "agentcanvas@example.test"})
	workspaceService := workspaceusecase.NewService(
		&staticLifecycleProjectRepo{item: projectdomain.Project{BaseModel: domain.BaseModel{ID: projectID, OwnerID: 3}, Slug: "demo", Name: "Demo", RepositoryRoot: root}},
		workspaceRepository, gitService,
		workspaceusecase.Config{Enabled: true, AllowedRoots: []string{root}, AutoInitRepository: true},
	)
	item, err := workspaceService.PrepareRunWorkspace(ctx, 3, projectID, 403, workspacedomain.KindShared, "demo", "runtime-write", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "agent.txt"), []byte("runtime change\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	service := NewService(nil, nil, nil, nil, nil, nil, nil, nil, nil)
	service.ConfigureWorkspace(workspaceService)
	service.refreshWorkspaceSnapshot(ctx, 3, item.ID)

	stored, err := workspaceRepository.FindByID(ctx, 3, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !stored.Dirty || stored.LastCheckedAt == nil || stored.ErrorMessage != "" {
		t.Fatalf("runtime file change was not persisted in the workspace snapshot: %#v", stored)
	}
}

func TestCancelRunCancelsQueuedTurn(t *testing.T) {
	runID := int64(401)
	turn := &agentdomain.Turn{BaseModel: domain.BaseModel{ID: 201, OwnerID: 3}, RunID: &runID, Status: agentdomain.TurnStatusQueued}
	turns := &settingsTurnRepo{byRun: map[int64]*agentdomain.Turn{runID: turn}}
	runs := &settingsRunRepo{item: &agentdomain.Run{BaseModel: domain.BaseModel{ID: runID, OwnerID: 3}, Status: agentdomain.RunStatusQueued}}
	service := NewService(nil, turns, nil, nil, runs, nil, nil, nil, nil)

	if err := service.CancelRun(context.Background(), 3, runID); err != nil {
		t.Fatalf("CancelRun returned error: %v", err)
	}
	if turns.updated == nil || turns.updated.Status != agentdomain.TurnStatusCancelled || turns.updated.FinishedAt == nil {
		t.Fatalf("queued turn was not cancelled with the run: %+v", turns.updated)
	}
}

func TestCancelRunReturnsChildCancellationError(t *testing.T) {
	parentID, childID := int64(401), int64(402)
	childErr := errors.New("child cancel failed")
	runs := &settingsRunRepo{
		items: map[int64]*agentdomain.Run{
			parentID: {BaseModel: domain.BaseModel{ID: parentID, OwnerID: 3}, Status: agentdomain.RunStatusRunning},
			childID:  {BaseModel: domain.BaseModel{ID: childID, OwnerID: 3}, ParentRunID: &parentID, Status: agentdomain.RunStatusRunning},
		},
		cancelErrors: map[int64]error{childID: childErr},
	}
	turns := &settingsTurnRepo{byRun: map[int64]*agentdomain.Turn{parentID: {BaseModel: domain.BaseModel{ID: 201, OwnerID: 3}, RunID: &parentID, Status: agentdomain.TurnStatusRunning}}}
	service := NewService(nil, turns, nil, nil, runs, nil, nil, nil, nil)

	err := service.CancelRun(context.Background(), 3, parentID)
	if !errors.Is(err, childErr) || !strings.Contains(err.Error(), "cancel child run 402") {
		t.Fatalf("CancelRun() error = %v", err)
	}
	if runs.items[parentID].Status != agentdomain.RunStatusCancelled {
		t.Fatalf("parent status = %q, want cancelled", runs.items[parentID].Status)
	}
}

func TestRunSubagentPersistsParentAndDelegationMetadata(t *testing.T) {
	parent := &agentdomain.Run{BaseModel: domain.BaseModel{ID: 41, OwnerID: 3}, AgentID: 10, RunType: agentdomain.RunTypeTurn, DelegationDepth: 0}
	runs := &settingsRunRepo{items: map[int64]*agentdomain.Run{parent.ID: parent}, nextID: 42}
	runtime := &settingsAgentRuntime{executeResult: &agentruntime.RunResult{Output: agentruntime.RunOutput{"final_answer": "done"}}}
	service := NewService(nil, nil, nil, nil, runs, nil, nil, nil, runtime)
	conversationID := int64(20)

	result, err := service.RunSubagent(context.Background(), toolruntime.SubagentRequest{
		OwnerID: 3, ParentRunID: parent.ID, AgentID: parent.AgentID, ConversationID: &conversationID,
		ProjectID: 11, DelegationDepth: 0, MaxDepth: 3,
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
		*runtime.executeReq.ParentRunID != parent.ID || runtime.executeReq.ProjectID != 11 || runtime.executeReq.DelegationDepth != 1 || runtime.executeReq.Task != "research" {
		t.Fatalf("runtime request did not preserve delegation context: run=%+v request=%+v", child, runtime.executeReq)
	}
}

func TestRunSubagentRejectsDepthAndParentContextMismatch(t *testing.T) {
	parent := &agentdomain.Run{BaseModel: domain.BaseModel{ID: 41, OwnerID: 3}, AgentID: 10, DelegationDepth: 2}
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
	parent := &agentdomain.Run{BaseModel: domain.BaseModel{ID: 41, OwnerID: 3}, AgentID: 10, DelegationDepth: 0}
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
	run := &agentdomain.Run{BaseModel: domain.BaseModel{ID: 42, OwnerID: 3}, AgentID: 10}
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
	if stored == nil || len(stored.CheckpointJSON) == 0 {
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
	run := &agentdomain.Run{BaseModel: domain.BaseModel{ID: 42, OwnerID: 3}, AgentID: 10, Status: agentdomain.RunStatusPaused}
	runs := &settingsRunRepo{items: map[int64]*agentdomain.Run{run.ID: run}}
	storageErr := errors.New("approval storage unavailable")
	service := NewService(nil, nil, nil, nil, runs, nil, nil, &settingsApprovalRepo{pendingErr: storageErr}, nil)

	if _, err := service.ResumeByID(context.Background(), 3, run.ID); !errors.Is(err, storageErr) {
		t.Fatalf("unexpected approval storage errors must propagate, got %v", err)
	}
}

func TestResumeByIDQueuesTurnInsideApprovalTransaction(t *testing.T) {
	run := &agentdomain.Run{BaseModel: domain.BaseModel{ID: 42, OwnerID: 3}, AgentID: 10, RunType: agentdomain.RunTypeTurn, Status: agentdomain.RunStatusPaused}
	runs := &settingsRunRepo{items: map[int64]*agentdomain.Run{run.ID: run}}
	turns := &settingsTurnRepo{byRun: map[int64]*agentdomain.Turn{run.ID: {BaseModel: domain.BaseModel{ID: 9, OwnerID: 3}, RunID: &run.ID, Status: agentdomain.TurnStatusPaused, InputJSON: json.RawMessage(`{"query":"continue"}`)}}}
	approvals := &settingsApprovalRepo{checkpoint: &agentdomain.RunCheckpoint{ImmutableModel: domain.ImmutableModel{ID: 8, OwnerID: 3}, RunID: run.ID}}
	service := NewService(nil, turns, nil, nil, runs, nil, nil, approvals, nil)

	if _, err := service.ResumeByID(context.Background(), 3, run.ID); err != nil {
		t.Fatalf("ResumeByID returned error: %v", err)
	}
	var input map[string]any
	if err := json.Unmarshal(approvals.resumeInput, &input); err != nil {
		t.Fatalf("resume input was not passed to the transaction: %v", err)
	}
	if input["query"] != "continue" || input["resume_approved"] != true || turns.updated != nil {
		t.Fatalf("resume was not queued atomically: input=%v separately_updated=%+v", input, turns.updated)
	}
}

func TestDecideApprovalQueuesSubagentResumeWithoutHTTPRuntimeCall(t *testing.T) {
	_, definitionJSON, err := subagentRuntimeDefinition(toolruntime.SubagentDefinition{Task: "continue", SystemPrompt: "be precise"})
	if err != nil {
		t.Fatal(err)
	}
	run := &agentdomain.Run{BaseModel: domain.BaseModel{ID: 42, OwnerID: 3}, AgentID: 10, RunType: agentdomain.RunTypeSubagent,
		Status: agentdomain.RunStatusWaitingHuman, DefinitionJSON: definitionJSON, DefinitionHash: jsonutil.Hash(definitionJSON), InputJSON: json.RawMessage(`{"query":"continue"}`)}
	runs := &settingsRunRepo{items: map[int64]*agentdomain.Run{run.ID: run}}
	request := &agentdomain.ApprovalRequest{BaseModel: domain.BaseModel{ID: 7, OwnerID: 3}, RunID: run.ID, Status: agentdomain.ApprovalStatusPending}
	checkpoint := &agentdomain.RunCheckpoint{ImmutableModel: domain.ImmutableModel{ID: 8, OwnerID: 3}, RunID: run.ID, CheckpointJSON: json.RawMessage(`{}`)}
	approvals := &settingsApprovalRepo{request: request, checkpoint: checkpoint}
	runtime := &settingsAgentRuntime{resumeResult: &agentruntime.RunResult{Output: agentruntime.RunOutput{"final_answer": "approved"}}}
	service := NewService(nil, nil, nil, nil, runs, nil, nil, approvals, runtime)

	resumed, err := service.DecideApprovalRequest(context.Background(), 3, request.ID, true, "ok")
	if err != nil {
		t.Fatalf("DecideApprovalRequest returned error: %v", err)
	}
	var input map[string]any
	if err := json.Unmarshal(approvals.resumeInput, &input); err != nil {
		t.Fatal(err)
	}
	if !approvals.decided || resumed.Status != agentdomain.RunStatusResuming || runtime.resumeReq.RunID != 0 || input["resume_approved"] != true {
		t.Fatalf("subagent approval was not queued: run=%+v decided=%v request=%+v input=%v", resumed, approvals.decided, runtime.resumeReq, input)
	}
}

func TestWorkerResumesSubagentThroughClaimedTurn(t *testing.T) {
	_, definitionJSON, err := subagentRuntimeDefinition(toolruntime.SubagentDefinition{Task: "continue", SystemPrompt: "be precise"})
	if err != nil {
		t.Fatal(err)
	}
	runID, conversationID, projectID := int64(43), int64(20), int64(11)
	run := &agentdomain.Run{BaseModel: domain.BaseModel{ID: runID, OwnerID: 3}, AgentID: 10, RunType: agentdomain.RunTypeSubagent,
		Status: agentdomain.RunStatusResuming, DefinitionJSON: definitionJSON, DefinitionHash: jsonutil.Hash(definitionJSON),
		ConversationID: &conversationID, InputJSON: json.RawMessage(`{"query":"continue"}`), StartedAt: time.Now().UTC()}
	turn := &agentdomain.Turn{BaseModel: domain.BaseModel{ID: 9, OwnerID: 3}, AgentID: 10, ConversationID: conversationID, RunID: &runID, Status: agentdomain.TurnStatusRunning,
		LeaseToken: "owned", InputJSON: json.RawMessage(`{"query":"continue","resume_approved":true}`)}
	turns := &settingsTurnRepo{byRun: map[int64]*agentdomain.Turn{runID: turn}}
	runs := &settingsRunRepo{items: map[int64]*agentdomain.Run{runID: run}}
	approvals := &settingsApprovalRepo{checkpoint: &agentdomain.RunCheckpoint{ImmutableModel: domain.ImmutableModel{ID: 8, OwnerID: 3}, RunID: runID, CheckpointJSON: json.RawMessage(`{}`)}}
	runtime := &settingsAgentRuntime{resumeResult: &agentruntime.RunResult{Output: agentruntime.RunOutput{"final_answer": "approved"}}}
	conversations := &settingsConversationRepo{items: map[int64]*conversation.Conversation{
		conversationID: {SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: conversationID, OwnerID: 3}}, ProjectID: &projectID},
	}}

	NewService(nil, turns, conversations, nil, runs, nil, nil, approvals, runtime).executeTurnOwned(context.Background(), turn)

	if runtime.resumeReq.RunID != runID || runtime.resumeReq.ProjectID != projectID || runtime.resumeReq.Workspace != nil || turn.Status != agentdomain.TurnStatusSucceeded || run.Status != agentdomain.RunStatusSucceeded || turns.completedRun != run {
		t.Fatalf("claimed subagent resume did not complete under the Turn lease: request=%+v turn=%+v run=%+v", runtime.resumeReq, turn, run)
	}
}

func TestResumeRunEmitsFinalWorkspaceStatusAfterRuntimeMutation(t *testing.T) {
	ctx := context.Background()
	_, definitionJSON, err := subagentRuntimeDefinition(toolruntime.SubagentDefinition{Task: "continue", SystemPrompt: "be precise"})
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	projectID, runID := int64(11), int64(43)
	workspaceRepository := &lifecycleWorkspaceRepository{items: make(map[int64]workspacedomain.Workspace)}
	workspaceService := workspaceusecase.NewService(
		&staticLifecycleProjectRepo{item: projectdomain.Project{BaseModel: domain.BaseModel{ID: projectID, OwnerID: 3}, Slug: "demo", Name: "Demo", RepositoryRoot: root}},
		workspaceRepository,
		gitinfra.NewService(gitinfra.Config{GitUserName: "AgentCanvas Test", GitUserEmail: "agentcanvas@example.test"}),
		workspaceusecase.Config{Enabled: true, AllowedRoots: []string{root}, AutoInitRepository: true},
	)
	workspace, err := workspaceService.PrepareRunWorkspace(ctx, 3, projectID, runID, workspacedomain.KindShared, "demo", "resume mutation", nil)
	if err != nil {
		t.Fatal(err)
	}
	run := &agentdomain.Run{BaseModel: domain.BaseModel{ID: runID, OwnerID: 3}, AgentID: 10, RunType: agentdomain.RunTypeSubagent,
		Status: agentdomain.RunStatusWaitingHuman, DefinitionJSON: definitionJSON, DefinitionHash: jsonutil.Hash(definitionJSON),
		InputJSON: json.RawMessage(`{"query":"continue"}`), WorkspaceID: &workspace.ID, StartedAt: time.Now().UTC(),
	}
	runs := &settingsRunRepo{items: map[int64]*agentdomain.Run{run.ID: run}}
	events := &workspaceLifecycleEventRepo{}
	runtime := &settingsAgentRuntime{
		resumeResult: &agentruntime.RunResult{Output: agentruntime.RunOutput{"final_answer": "approved"}},
		resumeHook: func(request agentruntime.ResumeRequest) {
			if request.Workspace == nil {
				t.Fatal("resume request is missing workspace context")
			}
			if writeErr := os.WriteFile(filepath.Join(request.Workspace.WorkspacePath, "resumed.txt"), []byte("resume change\n"), 0o644); writeErr != nil {
				t.Fatal(writeErr)
			}
		},
	}
	service := NewService(nil, nil, nil, nil, runs, events, nil, nil, runtime)
	service.ConfigureWorkspace(workspaceService)
	checkpoint := &agentdomain.RunCheckpoint{ImmutableModel: domain.ImmutableModel{ID: 8, OwnerID: 3}, RunID: run.ID, CheckpointJSON: json.RawMessage(`{}`)}

	resumed, err := service.ResumeRun(ctx, run, checkpoint, &agentdomain.ApprovalRequest{Status: agentdomain.ApprovalStatusApproved})
	if err != nil {
		t.Fatalf("ResumeRun returned error: %v", err)
	}
	if resumed.Status != agentdomain.RunStatusSucceeded || runtime.resumeReq.Workspace == nil {
		t.Fatalf("Run did not resume with its workspace: run=%+v request=%+v", resumed, runtime.resumeReq)
	}
	stored, err := workspaceRepository.FindByID(ctx, 3, workspace.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !stored.Dirty || stored.LastCheckedAt == nil {
		t.Fatalf("resume mutation was not persisted: %#v", stored)
	}
	var finalPayload map[string]any
	for index := range events.items {
		if events.items[index].EventType == runtimeevent.WorkspaceStatusChanged {
			if err := json.Unmarshal(events.items[index].PayloadJSON, &finalPayload); err != nil {
				t.Fatal(err)
			}
		}
	}
	if finalPayload == nil || finalPayload["dirty"] != true || finalPayload["run_id"] != float64(runID) {
		t.Fatalf("resume workspace.status_changed event mismatch: %#v", finalPayload)
	}
}

type settingsRunRepo struct {
	agentdomain.RunRepository
	item         *agentdomain.Run
	items        map[int64]*agentdomain.Run
	nextID       int64
	cancelErrors map[int64]error
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

func (r *settingsRunRepo) CancelActive(_ context.Context, item *agentdomain.Run, finishedAt time.Time) (bool, error) {
	if err := r.cancelErrors[item.ID]; err != nil {
		return false, err
	}
	if !agentdomain.IsActiveRunStatus(item.Status) {
		return false, nil
	}
	item.Status, item.FinishedAt = agentdomain.RunStatusCancelled, &finishedAt
	return true, nil
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
	executeHook   func(agentruntime.RunRequest)
	resumeReq     agentruntime.ResumeRequest
	resumeResult  *agentruntime.RunResult
	resumeErr     error
	resumeHook    func(agentruntime.ResumeRequest)
}

type missingLifecycleProjectRepo struct{ projectdomain.Repository }

func (*missingLifecycleProjectRepo) FindByID(context.Context, int64, int64) (*projectdomain.Project, error) {
	return nil, agenterrors.ErrNotFound
}

type staticLifecycleProjectRepo struct {
	projectdomain.Repository
	item projectdomain.Project
}

func (r *staticLifecycleProjectRepo) FindByID(_ context.Context, ownerID, projectID int64) (*projectdomain.Project, error) {
	if r.item.OwnerID != ownerID || r.item.ID != projectID {
		return nil, agenterrors.ErrNotFound
	}
	item := r.item
	return &item, nil
}

type workspaceLifecycleRunRepo struct {
	settingsRunRepo
	statuses            []string
	workspaceLinkErr    error
	workspaceLinkFailed bool
}

func (r *workspaceLifecycleRunRepo) Update(ctx context.Context, item *agentdomain.Run) error {
	r.statuses = append(r.statuses, item.Status)
	if r.workspaceLinkErr != nil && item.WorkspaceID != nil && !r.workspaceLinkFailed {
		r.workspaceLinkFailed = true
		return r.workspaceLinkErr
	}
	return r.settingsRunRepo.Update(ctx, item)
}

type lifecycleWorkspaceRepository struct {
	workspacedomain.Repository
	nextID int64
	items  map[int64]workspacedomain.Workspace
}

func (r *lifecycleWorkspaceRepository) Create(_ context.Context, item *workspacedomain.Workspace) error {
	r.nextID++
	item.ID = r.nextID
	r.items[item.ID] = *item
	return nil
}

func (r *lifecycleWorkspaceRepository) FindByID(_ context.Context, ownerID, id int64) (*workspacedomain.Workspace, error) {
	item, ok := r.items[id]
	if !ok || item.OwnerID != ownerID {
		return nil, agenterrors.ErrNotFound
	}
	return &item, nil
}

func (r *lifecycleWorkspaceRepository) FindByRunID(_ context.Context, ownerID, runID int64) (*workspacedomain.Workspace, error) {
	for _, item := range r.items {
		if item.OwnerID == ownerID && item.RunID == runID {
			copy := item
			return &copy, nil
		}
	}
	return nil, agenterrors.ErrNotFound
}

func (r *lifecycleWorkspaceRepository) ListByProject(_ context.Context, ownerID, projectID int64) ([]workspacedomain.Workspace, error) {
	items := make([]workspacedomain.Workspace, 0)
	for _, item := range r.items {
		if item.OwnerID == ownerID && item.ProjectID == projectID {
			items = append(items, item)
		}
	}
	return items, nil
}

func (r *lifecycleWorkspaceRepository) Update(_ context.Context, item *workspacedomain.Workspace) error {
	r.items[item.ID] = *item
	return nil
}

type workspaceLifecycleEventRepo struct {
	agentdomain.RunEventRepository
	items []agentdomain.RunEvent
}

func (r *workspaceLifecycleEventRepo) Create(_ context.Context, item *agentdomain.RunEvent) error {
	item.ID = int64(len(r.items) + 1)
	r.items = append(r.items, *item)
	return nil
}

func (r *workspaceLifecycleEventRepo) ListByRun(_ context.Context, ownerID, runID int64) ([]agentdomain.RunEvent, error) {
	items := make([]agentdomain.RunEvent, 0)
	for _, item := range r.items {
		if item.OwnerID == ownerID && item.RunID == runID {
			items = append(items, item)
		}
	}
	return items, nil
}

func (r *settingsAgentRuntime) Execute(_ context.Context, request agentruntime.RunRequest, _ agentruntime.EventEmitter) (*agentruntime.RunResult, error) {
	r.executeReq = request
	if r.executeHook != nil {
		r.executeHook(request)
	}
	return r.executeResult, r.executeErr
}

func (r *settingsAgentRuntime) Resume(_ context.Context, request agentruntime.ResumeRequest, _ agentruntime.EventEmitter) (*agentruntime.RunResult, error) {
	r.resumeReq = request
	if r.resumeHook != nil {
		r.resumeHook(request)
	}
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
	resumeInput       []byte
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

func (r *settingsApprovalRepo) DecideApprovalAndClaimResume(_ context.Context, item *agentdomain.ApprovalRequest, input []byte) error {
	r.decided = true
	r.request = item
	r.resumeInput = append([]byte(nil), input...)
	return nil
}

func (r *settingsApprovalRepo) ClaimResume(_ context.Context, _, _ int64, input []byte) error {
	r.resumeInput = append([]byte(nil), input...)
	return nil
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
	latest            *agentdomain.Turn
	latestErr         error
	byRun             map[int64]*agentdomain.Turn
	updated           *agentdomain.Turn
	completedRun      *agentdomain.Run
	ownedUpdateErr    error
	ownedUpdateFailed bool
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

func (r *settingsTurnRepo) CancelByRun(_ context.Context, ownerID, runID int64, finishedAt time.Time) (*agentdomain.Turn, error) {
	item := r.byRun[runID]
	if item == nil || item.OwnerID != ownerID {
		return nil, agentdomain.ErrNoTurnAvailable
	}
	item.Status, item.FinishedAt = agentdomain.TurnStatusCancelled, &finishedAt
	r.updated = item
	return item, nil
}

func (r *settingsTurnRepo) UpdateRunOwned(_ context.Context, turn *agentdomain.Turn, run *agentdomain.Run, _ bool) error {
	if r.ownedUpdateErr != nil && !r.ownedUpdateFailed {
		r.ownedUpdateFailed = true
		return r.ownedUpdateErr
	}
	r.updated = turn
	if turn.Status == agentdomain.TurnStatusSucceeded {
		r.completedRun = run
	}
	return nil
}

func (r *settingsTurnRepo) CompleteWithMessage(_ context.Context, turn *agentdomain.Turn, message *conversation.Message, _ *agentdomain.Run) error {
	message.ID = 302
	turn.AssistantMessageID = &message.ID
	r.updated = turn
	return nil
}
