package agent_usecase

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	workspaceusecase "agentcanvas/internal/application/workspace_usecase"
	agentdomain "agentcanvas/internal/domain/agent"
	"agentcanvas/internal/domain/conversation"
	"agentcanvas/internal/domain/knowledge"
	"agentcanvas/internal/domain/provider"
	workspacedomain "agentcanvas/internal/domain/workspace"
	agenterrors "agentcanvas/internal/pkg/errors"
	agentruntime "agentcanvas/internal/runtime/agentruntime"
	"agentcanvas/internal/runtime/harness/rules"
	"agentcanvas/internal/runtime/toolruntime"
)

type Service struct {
	agents        agentdomain.Repository
	turns         agentdomain.TurnRepository
	conversations conversation.AgentRepository
	messages      conversation.MessageRepository
	runs          agentdomain.RunRepository
	events        agentdomain.RunEventRepository
	steps         agentdomain.RunStepRepository
	approvals     agentdomain.ApprovalRepository
	improvement   TurnReviewEnqueuer
	sessionSearch conversation.MessageSearchIndex
	runtime       agentruntime.Runtime
	providers     provider.Repository
	knowledge     knowledge.BaseRepository
	cancelMu      sync.Mutex
	cancels       map[int64]context.CancelFunc
	leaseDuration time.Duration
	streamHub     runStreamHub
	workspace     *workspaceusecase.Service
}

func runtimeWorkspaceContext(item *workspacedomain.Context) *toolruntime.WorkspaceContext {
	if item == nil {
		return nil
	}
	return &toolruntime.WorkspaceContext{
		ID: item.ID, ProjectID: item.ProjectID, RunID: item.RunID, Kind: item.Kind,
		RepositoryRoot: item.RepositoryRoot, WorkspacePath: item.WorkspacePath,
		BranchName: item.BranchName, BaseSHA: item.BaseSHA, HeadSHA: item.HeadSHA, Dirty: item.Dirty, Unpushed: item.Unpushed,
		FileWriteEnabled: item.FileWriteEnabled, GitEnabled: item.GitEnabled, ExecEnabled: item.ExecEnabled,
	}
}

func workspaceEventPayload(item *workspacedomain.Workspace, err error) map[string]any {
	payload := map[string]any{"workspace_id": int64(0), "project_id": int64(0), "run_id": int64(0), "kind": "", "repo_root": "", "path": "", "branch": "", "base_sha": "", "head_sha": "", "dirty": false, "unpushed": false, "status": "", "locked": false, "lock_reason": "", "cleanup_reason": "", "error": ""}
	if item != nil {
		payload["workspace_id"], payload["project_id"], payload["run_id"] = item.ID, item.ProjectID, item.RunID
		payload["kind"], payload["repo_root"], payload["path"] = item.Kind, item.RepositoryRoot, item.WorkspacePath
		payload["branch"], payload["base_sha"], payload["head_sha"] = item.BranchName, item.BaseSHA, item.HeadSHA
		payload["dirty"], payload["unpushed"] = item.Dirty, item.Unpushed
		payload["status"], payload["locked"], payload["lock_reason"], payload["cleanup_reason"] = item.Status, item.Locked, item.LockReason, item.CleanupReason
		if item.ErrorMessage != "" {
			payload["error"] = item.ErrorMessage
		}
	}
	if err != nil {
		payload["error"] = err.Error()
	}
	return payload
}

func NewService(
	agents agentdomain.Repository,
	turns agentdomain.TurnRepository,
	conversations conversation.AgentRepository,
	messages conversation.MessageRepository,
	runs agentdomain.RunRepository,
	events agentdomain.RunEventRepository,
	steps agentdomain.RunStepRepository,
	approvals agentdomain.ApprovalRepository,
	runtime agentruntime.Runtime,
) *Service {
	return &Service{agents: agents, turns: turns, conversations: conversations, messages: messages, runs: runs,
		events: events, steps: steps, approvals: approvals, runtime: runtime, cancels: map[int64]context.CancelFunc{}, leaseDuration: 30 * time.Second}
}

type CreateAgentRequest struct {
	Name        string                `json:"name"`
	Description string                `json:"description"`
	AvatarURL   string                `json:"avatar_url"`
	Settings    AgentEditableSettings `json:"settings"`
}

type UpdateAgentRequest struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`
	AvatarURL   *string `json:"avatar_url"`
}

type AgentEditableSettings struct {
	ProviderID      int64    `json:"provider_id"`
	Model           string   `json:"model"`
	SystemPrompt    string   `json:"system_prompt"`
	KnowledgeIDs    []int64  `json:"knowledge_ids"`
	PythonToolNames []string `json:"python_tool_names,omitempty"`
	Temperature     *float64 `json:"temperature,omitempty"`
}

type AgentView struct {
	ID               int64                 `json:"id"`
	OwnerID          int64                 `json:"owner_id"`
	Name             string                `json:"name"`
	Description      string                `json:"description"`
	AvatarURL        string                `json:"avatar_url"`
	Status           string                `json:"status"`
	Settings         AgentEditableSettings `json:"settings"`
	CurrentReleaseID *int64                `json:"current_release_id,omitempty"`
	CreatedAt        time.Time             `json:"created_at"`
	UpdatedAt        time.Time             `json:"updated_at"`
}

const defaultAgentSystemPrompt = "You are a capable, careful AI agent. Use tools when they materially help, and return a concise final answer."

func ManagedDefinition(settings AgentEditableSettings) agentdomain.Definition {
	return agentdomain.Definition{
		ModelConfig:  agentdomain.ModelConfig{ProviderID: settings.ProviderID, Model: strings.TrimSpace(settings.Model), Temperature: settings.Temperature},
		PromptConfig: agentdomain.PromptConfig{SystemPrompt: strings.TrimSpace(settings.SystemPrompt), OutputMode: "final_answer"},
		ToolConfig:   agentdomain.ToolConfig{PythonToolNames: append([]string(nil), settings.PythonToolNames...)},
		ResourceRefs: agentdomain.ResourceRefs{KnowledgeIDs: normalizeIDs(settings.KnowledgeIDs), KnowledgeTopK: 5, KnowledgeMode: "hybrid", SkillLoadingMode: "auto"},
		MemoryPolicy: agentdomain.MemoryPolicy{MemoryEnabled: true, ReflectionEnabled: true},
		ExecutionLimits: agentdomain.ExecutionLimits{
			Mode: "react", AllowSubagents: true, MaxIterations: 8, MaxToolCalls: 16, MaxExecutionTimeMS: 120000,
			MaxToolTimeoutMS: 30000, MaxToolOutputBytes: 512 * 1024, MaxParallelSubAgents: 4, MaxSubagentDepth: 3,
		},
	}.Normalize()
}

func normalizeIDs(ids []int64) []int64 {
	seen := make(map[int64]struct{}, len(ids))
	result := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id <= 0 {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		result = append(result, id)
	}
	return result
}

func settingsFromDefinition(definition agentdomain.Definition) AgentEditableSettings {
	return AgentEditableSettings{ProviderID: definition.ProviderID, Model: definition.Model, SystemPrompt: definition.SystemPrompt,
		KnowledgeIDs: append([]int64(nil), definition.KnowledgeIDs...), PythonToolNames: append([]string(nil), definition.PythonToolNames...), Temperature: definition.Temperature}
}

func viewAgent(item *agentdomain.Agent) *AgentView {
	if item == nil {
		return nil
	}
	return &AgentView{ID: item.ID, OwnerID: item.OwnerID, Name: item.Name, Description: item.Description, AvatarURL: item.AvatarURL,
		Status: item.Status, Settings: settingsFromDefinition(item.DraftDefinition), CurrentReleaseID: item.CurrentReleaseID,
		CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt}
}

func (s *Service) ConfigureEditableResources(providers provider.Repository, knowledgeBases knowledge.BaseRepository) {
	s.providers, s.knowledge = providers, knowledgeBases
}

func (s *Service) validateEditableSettings(ctx context.Context, ownerID int64, settings AgentEditableSettings) error {
	definition := ManagedDefinition(settings)
	if definition.SystemPrompt == "" {
		definition.SystemPrompt = defaultAgentSystemPrompt
	}
	if err := definition.Validate(); err != nil {
		return fmt.Errorf("%w: %v", agenterrors.ErrInvalidInput, err)
	}
	if s.providers == nil || s.knowledge == nil {
		return errors.New("agent editable resource validation is not configured")
	}
	providerItem, err := s.providers.FindByID(ctx, ownerID, settings.ProviderID)
	if err != nil || providerItem.Status != provider.StatusActive {
		return agenterrors.ErrInvalidInput
	}
	for _, id := range definition.KnowledgeIDs {
		item, findErr := s.knowledge.FindByID(ctx, ownerID, id)
		if findErr != nil || item.Status != knowledge.KnowledgeBaseStatusActive {
			return agenterrors.ErrInvalidInput
		}
	}
	return nil
}

func (s *Service) CreateAgent(ctx context.Context, ownerID int64, req CreateAgentRequest) (*AgentView, error) {
	if ownerID <= 0 || strings.TrimSpace(req.Name) == "" {
		return nil, agenterrors.ErrInvalidInput
	}
	if err := s.validateEditableSettings(ctx, ownerID, req.Settings); err != nil {
		return nil, err
	}
	definition := ManagedDefinition(req.Settings)
	if definition.SystemPrompt == "" {
		definition.SystemPrompt = defaultAgentSystemPrompt
	}
	item := &agentdomain.Agent{
		OwnerID:         ownerID,
		Name:            strings.TrimSpace(req.Name),
		Description:     strings.TrimSpace(req.Description),
		AvatarURL:       strings.TrimSpace(req.AvatarURL),
		Status:          agentdomain.StatusDraft,
		DraftDefinition: definition,
	}
	if err := s.agents.Create(ctx, item); err != nil {
		return nil, err
	}
	if _, err := s.Publish(ctx, ownerID, item.ID); err != nil {
		return nil, err
	}
	return s.GetAgent(ctx, ownerID, item.ID)
}

func (s *Service) ListAgents(ctx context.Context, ownerID int64) ([]AgentView, error) {
	items, err := s.agents.ListByOwner(ctx, ownerID)
	if err != nil {
		return nil, err
	}
	result := make([]AgentView, 0, len(items))
	for index := range items {
		result = append(result, *viewAgent(&items[index]))
	}
	return result, nil
}

func (s *Service) getAgent(ctx context.Context, ownerID, id int64) (*agentdomain.Agent, error) {
	item, err := s.agents.FindByID(ctx, ownerID, id)
	return item, mapNotFound(err)
}

func (s *Service) GetAgent(ctx context.Context, ownerID, id int64) (*AgentView, error) {
	item, err := s.getAgent(ctx, ownerID, id)
	return viewAgent(item), err
}

func (s *Service) UpdateAgent(ctx context.Context, ownerID, id int64, req UpdateAgentRequest) (*AgentView, error) {
	item, err := s.getAgent(ctx, ownerID, id)
	if err != nil {
		return nil, err
	}
	if req.Name != nil {
		if strings.TrimSpace(*req.Name) == "" {
			return nil, agenterrors.ErrInvalidInput
		}
		item.Name = strings.TrimSpace(*req.Name)
	}
	if req.Description != nil {
		item.Description = strings.TrimSpace(*req.Description)
	}
	if req.AvatarURL != nil {
		item.AvatarURL = strings.TrimSpace(*req.AvatarURL)
	}
	if err := s.agents.Update(ctx, item); err != nil {
		return nil, err
	}
	return viewAgent(item), nil
}

func (s *Service) UpdateAgentSettings(ctx context.Context, ownerID, id int64, settings AgentEditableSettings) (*AgentView, error) {
	if err := s.validateEditableSettings(ctx, ownerID, settings); err != nil {
		return nil, err
	}
	item, err := s.getAgent(ctx, ownerID, id)
	if err != nil {
		return nil, err
	}
	item.DraftDefinition = ManagedDefinition(settings)
	if item.DraftDefinition.SystemPrompt == "" {
		item.DraftDefinition.SystemPrompt = defaultAgentSystemPrompt
	}
	if err := s.agents.Update(ctx, item); err != nil {
		return nil, err
	}
	if _, err := s.Publish(ctx, ownerID, id); err != nil {
		return nil, err
	}
	return s.GetAgent(ctx, ownerID, id)
}

func (s *Service) DeleteAgent(ctx context.Context, ownerID, id int64) error {
	if _, err := s.getAgent(ctx, ownerID, id); err != nil {
		return err
	}
	return s.agents.SoftDelete(ctx, ownerID, id)
}

type ValidationResult struct {
	Valid    bool     `json:"valid"`
	Errors   []string `json:"errors"`
	Warnings []string `json:"warnings"`
	Checksum string   `json:"checksum,omitempty"`
}

func (s *Service) ValidateAgent(ctx context.Context, ownerID, id int64) (*ValidationResult, error) {
	item, err := s.getAgent(ctx, ownerID, id)
	if err != nil {
		return nil, err
	}
	result := &ValidationResult{Valid: true, Errors: []string{}, Warnings: []string{}}
	if err := validateDefinitionRules(item.DraftDefinition); err != nil {
		result.Valid = false
		result.Errors = append(result.Errors, err.Error())
	}
	_, checksum, validateErr := item.DraftDefinition.Snapshot()
	if validateErr != nil {
		result.Valid = false
		result.Errors = append(result.Errors, validateErr.Error())
		return result, nil
	}
	result.Checksum = checksum
	return result, nil
}

func (s *Service) Publish(ctx context.Context, ownerID, id int64) (*agentdomain.Release, error) {
	item, err := s.getAgent(ctx, ownerID, id)
	if err != nil {
		return nil, err
	}
	if err := item.DraftDefinition.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", agenterrors.ErrInvalidInput, err)
	}
	validation, err := s.ValidateAgent(ctx, ownerID, id)
	if err != nil {
		return nil, err
	}
	if !validation.Valid {
		return nil, fmt.Errorf("%w: %s", agenterrors.ErrInvalidInput, strings.Join(validation.Errors, "; "))
	}
	version, err := s.agents.NextReleaseVersion(ctx, ownerID, id)
	if err != nil {
		return nil, err
	}
	resources, ruleHash, toolHash, err := item.DraftDefinition.ResourceSnapshot()
	if err != nil {
		return nil, err
	}
	release := &agentdomain.Release{OwnerID: ownerID, AgentID: id, VersionNo: version, Definition: item.DraftDefinition,
		ResourceVersions: resources, RuleHash: ruleHash, ToolSchemaHash: toolHash, CreatedBy: ownerID}
	if err := s.agents.CreateRelease(ctx, release); err != nil {
		return nil, err
	}
	if err := s.agents.SetCurrentRelease(ctx, ownerID, id, release.ID); err != nil {
		return nil, err
	}
	return release, nil
}

func validateDefinitionRules(definition agentdomain.Definition) error {
	if len(definition.RulesJSON) == 0 || string(definition.RulesJSON) == "null" {
		return nil
	}
	var items []rules.Rule
	if err := json.Unmarshal(definition.RulesJSON, &items); err != nil {
		return fmt.Errorf("rules_json is invalid: %w", err)
	}
	if _, err := rules.ValidateRules(items); err != nil {
		return fmt.Errorf("rules_json is invalid: %w", err)
	}
	return nil
}

func (s *Service) ListReleases(ctx context.Context, ownerID, agentID int64) ([]agentdomain.Release, error) {
	if _, err := s.getAgent(ctx, ownerID, agentID); err != nil {
		return nil, err
	}
	return s.agents.ListReleases(ctx, ownerID, agentID)
}

func (s *Service) GetRelease(ctx context.Context, ownerID, id int64) (*agentdomain.Release, error) {
	item, err := s.agents.FindReleaseByID(ctx, ownerID, id)
	return item, mapNotFound(err)
}

func (s *Service) Capabilities(ctx context.Context, ownerID, releaseID int64) (map[string]any, error) {
	release, err := s.GetRelease(ctx, ownerID, releaseID)
	if err != nil {
		return nil, err
	}
	d := release.Definition
	return map[string]any{
		"release_id": release.ID, "checksum": release.Checksum, "mode": d.Mode,
		"tools": len(d.ToolIDs), "tool_packs": len(d.ToolPackIDs), "skills": len(d.SkillIDs),
		"python_tools":    len(d.PythonToolNames),
		"knowledge_bases": len(d.KnowledgeIDs), "mcp_servers": len(d.MCPServerIDs),
		"dynamic_subagents": d.AllowSubagents,
		"memory":            d.MemoryEnabled, "reflection": d.ReflectionEnabled,
	}, nil
}

type CreateConversationRequest struct {
	Title         string `json:"title"`
	Mode          string `json:"mode"`
	ProjectID     *int64 `json:"project_id,omitempty"`
	WorkspaceMode string `json:"workspace_mode,omitempty"`
}

func normalizeAgentMode(mode string) (string, error) {
	mode = strings.TrimSpace(mode)
	if mode == "" {
		return "react", nil
	}
	if mode != "react" && mode != "plan_execute" {
		return "", agenterrors.ErrInvalidInput
	}
	return mode, nil
}

func decodeInputJSON(raw json.RawMessage) (map[string]any, error) {
	input := make(map[string]any)
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return input, nil
	}
	if err := json.Unmarshal(trimmed, &input); err != nil {
		return nil, err
	}
	if input == nil {
		input = make(map[string]any)
	}
	return input, nil
}

func (s *Service) CreateConversation(ctx context.Context, ownerID, agentID int64, req CreateConversationRequest) (*conversation.Conversation, error) {
	agentItem, err := s.getAgent(ctx, ownerID, agentID)
	if err != nil {
		return nil, err
	}
	if agentItem.CurrentReleaseID == nil || *agentItem.CurrentReleaseID <= 0 {
		return nil, fmt.Errorf("%w: agent must be published before starting a conversation", agenterrors.ErrInvalidInput)
	}
	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = "New conversation"
	}
	mode, err := normalizeAgentMode(req.Mode)
	if err != nil {
		return nil, err
	}
	workspaceMode, err := workspaceusecase.NormalizeMode(req.WorkspaceMode)
	if err != nil {
		return nil, err
	}
	if req.ProjectID != nil {
		if s.workspace == nil {
			return nil, agenterrors.ErrInvalidInput
		}
		project, err := s.workspace.GetProject(ctx, ownerID, *req.ProjectID)
		if err != nil {
			return nil, err
		}
		if project.Archived {
			return nil, fmt.Errorf("%w: project is archived", agenterrors.ErrForbidden)
		}
	}
	item := &conversation.Conversation{OwnerID: ownerID, AgentID: &agentID, AgentReleaseID: agentItem.CurrentReleaseID,
		ProjectID: req.ProjectID, WorkspaceMode: workspaceMode, Title: title, Name: title, Source: conversation.SourceAgent, AgentMode: mode}
	if err := s.conversations.Create(ctx, item); err != nil {
		return nil, err
	}
	return item, nil
}

type UpdateConversationModeRequest struct {
	Mode string `json:"mode"`
}

func (s *Service) UpdateConversationMode(ctx context.Context, ownerID, agentID, conversationID int64, req UpdateConversationModeRequest) (*conversation.Conversation, error) {
	item, err := s.GetConversation(ctx, ownerID, agentID, conversationID)
	if err != nil {
		return nil, err
	}
	latest, latestErr := s.turns.FindLatestByConversation(ctx, ownerID, agentID, conversationID)
	if latestErr != nil && !errors.Is(latestErr, agentdomain.ErrNoTurnAvailable) && !errors.Is(mapNotFound(latestErr), agenterrors.ErrNotFound) {
		return nil, latestErr
	}
	if latestErr == nil && (latest.Status == agentdomain.TurnStatusQueued || latest.Status == agentdomain.TurnStatusRetryWait ||
		latest.Status == agentdomain.TurnStatusRunning || latest.Status == agentdomain.TurnStatusWaitingHuman || latest.Status == agentdomain.TurnStatusPaused) {
		return nil, fmt.Errorf("%w: cannot change mode while a turn is active", agenterrors.ErrInvalidInput)
	}
	mode, err := normalizeAgentMode(req.Mode)
	if err != nil {
		return nil, err
	}
	if err := s.conversations.UpdateAgentMode(ctx, ownerID, conversationID, mode); err != nil {
		return nil, err
	}
	item.AgentMode = mode
	return item, nil
}

func (s *Service) ListConversations(ctx context.Context, ownerID, agentID int64) ([]conversation.Conversation, error) {
	if _, err := s.getAgent(ctx, ownerID, agentID); err != nil {
		return nil, err
	}
	return s.conversations.ListByAgent(ctx, ownerID, agentID)
}

func (s *Service) GetConversation(ctx context.Context, ownerID, agentID, conversationID int64) (*conversation.Conversation, error) {
	item, err := s.conversations.FindByID(ctx, ownerID, conversationID)
	if err != nil {
		return nil, mapNotFound(err)
	}
	if item.AgentID == nil || *item.AgentID != agentID || item.Source != conversation.SourceAgent {
		return nil, agenterrors.ErrNotFound
	}
	return item, nil
}

func (s *Service) ListMessages(ctx context.Context, ownerID, agentID, conversationID int64) ([]conversation.Message, error) {
	if _, err := s.GetConversation(ctx, ownerID, agentID, conversationID); err != nil {
		return nil, err
	}
	return s.messages.ListByConversation(ctx, ownerID, conversationID)
}

func (s *Service) DeleteConversation(ctx context.Context, ownerID, agentID, conversationID int64) error {
	if _, err := s.GetConversation(ctx, ownerID, agentID, conversationID); err != nil {
		return err
	}
	if err := s.conversations.SoftDelete(ctx, ownerID, conversationID); err != nil {
		return err
	}
	if s.sessionSearch != nil {
		_ = s.sessionSearch.DeleteConversation(ctx, ownerID, agentID, conversationID)
	}
	return nil
}

func (s *Service) ForkConversation(ctx context.Context, ownerID, agentID, conversationID int64, useCurrentRelease bool) (*conversation.Conversation, error) {
	source, err := s.GetConversation(ctx, ownerID, agentID, conversationID)
	if err != nil {
		return nil, err
	}
	releaseID := source.AgentReleaseID
	if useCurrentRelease {
		agentItem, getErr := s.getAgent(ctx, ownerID, agentID)
		if getErr != nil {
			return nil, getErr
		}
		if agentItem.CurrentReleaseID == nil {
			return nil, agenterrors.ErrInvalidInput
		}
		releaseID = agentItem.CurrentReleaseID
	}
	title := source.Title + " (fork)"
	fork := &conversation.Conversation{
		OwnerID:              ownerID,
		AgentID:              &agentID,
		AgentReleaseID:       releaseID,
		ParentConversationID: &source.ID,
		Title:                title,
		Name:                 title,
		Source:               conversation.SourceAgent,
		AgentMode:            source.AgentMode,
		ProjectID:            source.ProjectID,
		WorkspaceMode:        source.WorkspaceMode,
	}
	if err := s.conversations.Create(ctx, fork); err != nil {
		return nil, err
	}
	messages, err := s.messages.ListByConversation(ctx, ownerID, source.ID)
	if err != nil {
		return nil, err
	}
	for _, message := range messages {
		copyMessage := &conversation.Message{
			OwnerID:        ownerID,
			ConversationID: fork.ID,
			Role:           message.Role,
			Content:        message.Content,
			ContentType:    message.ContentType,
			TokenCount:     message.TokenCount,
			MetadataJSON:   message.MetadataJSON,
		}
		if err := s.messages.Create(ctx, copyMessage); err != nil {
			return nil, err
		}
		if s.sessionSearch != nil {
			_ = s.sessionSearch.IndexMessage(ctx, ownerID, agentID, copyMessage)
		}
	}
	return fork, nil
}

type CreateTurnRequest struct {
	Content string `json:"content" binding:"required"`
}

type TurnAccepted struct {
	Turn        *agentdomain.Turn     `json:"turn"`
	Run         *agentdomain.Run      `json:"run"`
	UserMessage *conversation.Message `json:"user_message"`
}

func (s *Service) userMessageForTurn(ctx context.Context, ownerID int64, turn *agentdomain.Turn) *conversation.Message {
	if turn == nil || turn.UserMessageID <= 0 {
		return nil
	}
	items, err := s.messages.ListByConversation(ctx, ownerID, turn.ConversationID)
	if err != nil {
		return nil
	}
	for index := range items {
		if items[index].ID == turn.UserMessageID {
			return &items[index]
		}
	}
	return nil
}

func (s *Service) StartTurn(ctx context.Context, ownerID, agentID, conversationID int64, idempotencyKey string, req CreateTurnRequest) (*TurnAccepted, error) {
	content := strings.TrimSpace(req.Content)
	key := strings.TrimSpace(idempotencyKey)
	if content == "" || key == "" || len(key) > 128 {
		return nil, agenterrors.ErrInvalidInput
	}
	conv, err := s.GetConversation(ctx, ownerID, agentID, conversationID)
	if err != nil {
		return nil, err
	}
	if existing, existingErr := s.turns.FindByIdempotencyKey(ctx, ownerID, conversationID, key); existingErr == nil {
		var run *agentdomain.Run
		if existing.RunID != nil {
			run, _ = s.runs.FindByID(ctx, ownerID, *existing.RunID)
		}
		return &TurnAccepted{Turn: existing, Run: run, UserMessage: s.userMessageForTurn(ctx, ownerID, existing)}, nil
	}
	if conv.AgentReleaseID == nil || *conv.AgentReleaseID <= 0 {
		return nil, agenterrors.ErrInvalidInput
	}
	release, err := s.agents.FindReleaseByID(ctx, ownerID, *conv.AgentReleaseID)
	if err != nil {
		return nil, mapNotFound(err)
	}
	if len(release.DefinitionJSON) == 0 || strings.TrimSpace(release.Checksum) == "" {
		return nil, fmt.Errorf("%w: agent release snapshot is incomplete", agenterrors.ErrInvalidInput)
	}
	userMessage := &conversation.Message{
		OwnerID:        ownerID,
		ConversationID: conversationID,
		Role:           conversation.RoleUser,
		Content:        content,
		ContentType:    conversation.ContentTypeText,
		MetadataJSON:   "{}",
	}
	now := time.Now().UTC()
	mode, err := normalizeAgentMode(conv.AgentMode)
	if err != nil {
		return nil, err
	}
	inputJSON, _ := json.Marshal(map[string]any{"query": content, "mode": mode})
	run := &agentdomain.Run{
		OwnerID:        ownerID,
		RunType:        agentdomain.RunTypeTurn,
		AgentID:        agentID,
		AgentReleaseID: conv.AgentReleaseID,
		ConversationID: &conversationID,
		Status:         agentdomain.RunStatusQueued,
		DefinitionJSON: append(json.RawMessage(nil), release.DefinitionJSON...),
		DefinitionHash: release.Checksum,
		RuleHash:       release.RuleHash,
		InputJSON:      inputJSON,
		StartedAt:      now,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	turn := &agentdomain.Turn{
		OwnerID:        ownerID,
		AgentID:        agentID,
		AgentReleaseID: *conv.AgentReleaseID,
		ConversationID: conversationID,
		IdempotencyKey: key,
		Status:         agentdomain.TurnStatusQueued,
		InputJSON:      inputJSON,
	}
	if err := s.turns.CreateWithArtifacts(ctx, turn, userMessage, run); err != nil {
		if existing, existingErr := s.turns.FindByIdempotencyKey(ctx, ownerID, conversationID, key); existingErr == nil {
			var existingRun *agentdomain.Run
			if existing.RunID != nil {
				existingRun, _ = s.runs.FindByID(ctx, ownerID, *existing.RunID)
			}
			return &TurnAccepted{Turn: existing, Run: existingRun, UserMessage: s.userMessageForTurn(ctx, ownerID, existing)}, nil
		}
		return nil, err
	}
	if s.sessionSearch != nil {
		_ = s.sessionSearch.IndexMessage(ctx, ownerID, agentID, userMessage)
	}
	return &TurnAccepted{Turn: turn, Run: run, UserMessage: userMessage}, nil
}

func (s *Service) SearchSessions(ctx context.Context, ownerID, agentID int64, request conversation.MessageSearchRequest) ([]conversation.MessageSearchResult, error) {
	if _, err := s.getAgent(ctx, ownerID, agentID); err != nil {
		return nil, err
	}
	if s.sessionSearch == nil {
		return nil, fmt.Errorf("session search is not configured")
	}
	request.OwnerID, request.AgentID = ownerID, agentID
	return s.sessionSearch.SearchMessages(ctx, request)
}

func (s *Service) executeTurnOwned(ctx context.Context, turn *agentdomain.Turn) {
	turnWorker{Service: s}.execute(ctx, turn)
}

func mapNotFound(err error) error {
	if err == nil {
		return nil
	}
	if strings.Contains(strings.ToLower(err.Error()), "record not found") || errors.Is(err, agenterrors.ErrNotFound) {
		return agenterrors.ErrNotFound
	}
	return err
}
