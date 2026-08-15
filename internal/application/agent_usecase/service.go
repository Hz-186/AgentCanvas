package agent_usecase

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	workspaceusecase "agentcanvas/internal/application/workspace_usecase"
	agentdomain "agentcanvas/internal/domain/agent"
	"agentcanvas/internal/domain/conversation"
	"agentcanvas/internal/domain/knowledge"
	"agentcanvas/internal/domain/provider"
	workspacedomain "agentcanvas/internal/domain/workspace"
	"agentcanvas/internal/infrastructure/llm"
	agenterrors "agentcanvas/internal/pkg/errors"
	runtimeagent "agentcanvas/internal/runtime/agent"
	agentruntime "agentcanvas/internal/runtime/agentruntime"
	runtimeevent "agentcanvas/internal/runtime/event"
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
		ProviderID: settings.ProviderID, Model: strings.TrimSpace(settings.Model),
		SystemPrompt: strings.TrimSpace(settings.SystemPrompt), Temperature: settings.Temperature,
		PythonToolNames: append([]string(nil), settings.PythonToolNames...),
		Mode:            "react", KnowledgeIDs: normalizeIDs(settings.KnowledgeIDs), KnowledgeTopK: 5, KnowledgeMode: "hybrid",
		SkillLoadingMode: "auto", MemoryEnabled: true, ReflectionEnabled: true, AllowSubagents: true,
		MaxIterations: 8, MaxToolCalls: 16, MaxExecutionTimeMS: 120000, MaxToolTimeoutMS: 30000,
		MaxToolOutputBytes: 512 * 1024, MaxParallelSubAgents: 4, MaxSubagentDepth: 3, OutputMode: "final_answer",
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
	started := time.Now().UTC()
	turn.Status, turn.StartedAt = agentdomain.TurnStatusRunning, &started
	_ = s.turns.Update(ctx, turn)
	run, err := s.runs.FindByID(ctx, turn.OwnerID, *turn.RunID)
	if err != nil {
		s.failTurn(ctx, turn, nil, err)
		return
	}
	if run.Status == agentdomain.RunStatusCancelled {
		finished := time.Now().UTC()
		turn.Status, turn.FinishedAt = agentdomain.TurnStatusCancelled, &finished
		_ = s.turns.Update(ctx, turn)
		return
	}
	run.ErrorMessage = ""
	execCtx, cancel := context.WithCancel(ctx)
	s.registerCancel(run.ID, cancel)
	defer func() { cancel(); s.unregisterCancel(run.ID) }()
	if turn.LeaseToken != "" {
		go s.heartbeatLease(execCtx, turn.ID, turn.LeaseToken, cancel)
	}
	ctx = execCtx
	definition, err := agentruntime.DecodeDefinition(run.DefinitionJSON)
	if err != nil {
		s.failTurn(ctx, turn, run, err)
		return
	}
	var input map[string]any
	_ = json.Unmarshal(turn.InputJSON, &input)
	task, _ := input["query"].(string)
	if mode, modeErr := normalizeAgentMode(fmt.Sprint(input["mode"])); modeErr == nil {
		definition.Mode = mode
	}
	emitter := s.newRunEventEmitter(turn.OwnerID, run.ID, &turn.ConversationID)
	var runtimeWorkspace *toolruntime.WorkspaceContext
	var preparedWorkspace *workspacedomain.Workspace
	if s.workspace != nil && run.ConversationID != nil {
		conv, convErr := s.conversations.FindByID(ctx, turn.OwnerID, *run.ConversationID)
		if convErr != nil {
			s.failTurn(ctx, turn, run, convErr)
			return
		}
		if conv.ProjectID != nil {
			project, projectErr := s.workspace.GetProject(ctx, turn.OwnerID, *conv.ProjectID)
			if projectErr != nil {
				_ = emitter.Emit(ctx, runtimeevent.Event{Type: runtimeevent.WorkspaceFailed, RunID: run.ID, Payload: workspaceEventPayload(&workspacedomain.Workspace{ProjectID: *conv.ProjectID, RunID: run.ID}, projectErr)})
				s.failTurn(ctx, turn, run, projectErr)
				return
			}
			ws, wsErr := s.workspace.PrepareRunWorkspace(ctx, turn.OwnerID, *conv.ProjectID, run.ID, conv.WorkspaceMode, project.Slug, task, nil)
			if wsErr != nil {
				failedWorkspace := ws
				if failedWorkspace == nil {
					failedWorkspace = &workspacedomain.Workspace{ProjectID: project.ID, RunID: run.ID, RepositoryRoot: project.PrimaryPath}
				} else {
					run.WorkspaceID = &failedWorkspace.ID
					_ = s.runs.Update(ctx, run)
				}
				_ = emitter.Emit(ctx, runtimeevent.Event{Type: runtimeevent.WorkspaceFailed, RunID: run.ID, Payload: workspaceEventPayload(failedWorkspace, wsErr)})
				s.failTurn(ctx, turn, run, wsErr)
				return
			}
			preparedWorkspace = ws
			defer func() { _ = s.workspace.ReleaseRunWorkspaceLock(context.Background(), turn.OwnerID, run.ID) }()
			run.WorkspaceID = &ws.ID
			if updateErr := s.runs.Update(ctx, run); updateErr != nil {
				_ = emitter.Emit(ctx, runtimeevent.Event{Type: runtimeevent.WorkspaceFailed, RunID: run.ID, Payload: workspaceEventPayload(ws, updateErr)})
				s.failTurn(ctx, turn, run, updateErr)
				return
			}
			runtimeWorkspace = runtimeWorkspaceContext(s.workspace.WorkspaceContext(ws))
			_ = emitter.Emit(ctx, runtimeevent.Event{Type: runtimeevent.WorkspaceCreated, RunID: run.ID, Payload: workspaceEventPayload(ws, nil)})
		}
	}
	if runtimeWorkspace != nil {
		_ = emitter.Emit(ctx, runtimeevent.Event{Type: runtimeevent.WorkspaceReady, RunID: run.ID, Payload: workspaceEventPayload(preparedWorkspace, nil)})
	}
	if err := run.TransitionStatus(agentdomain.RunStatusRunning); err != nil {
		s.failTurn(ctx, turn, run, err)
		return
	}
	if err := s.runs.Update(ctx, run); err != nil {
		s.failTurn(ctx, turn, run, err)
		return
	}
	result, execErr := s.runtime.Execute(ctx,
		agentruntime.RunRequest{
			OwnerID:        turn.OwnerID,
			AgentID:        turn.AgentID,
			AgentReleaseID: turn.AgentReleaseID,
			RunID:          run.ID,
			ConversationID: &turn.ConversationID,
			Task:           task,
			RuleHash:       run.RuleHash,
			Definition:     definition,
			StepRecorder:   &runStepRecorder{repo: s.steps},
			Workspace:      runtimeWorkspace,
		}, emitter)
	if preparedWorkspace != nil {
		if refreshed := s.refreshWorkspaceSnapshot(context.WithoutCancel(ctx), turn.OwnerID, preparedWorkspace.ID); refreshed != nil {
			_ = emitter.Emit(context.WithoutCancel(ctx), runtimeevent.Event{Type: runtimeevent.WorkspaceStatusChanged, RunID: run.ID, Payload: workspaceEventPayload(refreshed, nil)})
		}
	}
	if execErr != nil {
		s.failTurn(ctx, turn, run, execErr)
		return
	}
	s.completeTurn(ctx, turn, run, result)
}

func (s *Service) ConfigureWorker(leaseDuration time.Duration) {
	if leaseDuration >= 10*time.Second {
		s.leaseDuration = leaseDuration
	}
}

func (s *Service) ConfigureImprovement(enqueuer TurnReviewEnqueuer) {
	s.improvement = enqueuer
}

func (s *Service) ConfigureSessionSearch(index conversation.MessageSearchIndex) {
	s.sessionSearch = index
}

func (s *Service) ConfigureWorkspace(service *workspaceusecase.Service) {
	s.workspace = service
}

func (s *Service) refreshWorkspaceSnapshot(ctx context.Context, ownerID, workspaceID int64) *workspacedomain.Workspace {
	if s.workspace == nil || ownerID <= 0 || workspaceID <= 0 {
		return nil
	}
	item, err := s.workspace.GetWorkspace(ctx, ownerID, workspaceID)
	if err == nil {
		var refreshed *workspacedomain.Workspace
		refreshed, err = s.workspace.RefreshGitStatus(ctx, item)
		if refreshed != nil {
			item = refreshed
		}
	}
	if err != nil {
		slog.Default().Error("refresh run workspace status failed", "owner_id", ownerID, "workspace_id", workspaceID, "error", err)
	}
	return item
}

// EmitWorkspaceEvent lets HTTP lifecycle actions use the same durable Run
// Event/SSE lane as runtime tool actions.
func (s *Service) EmitWorkspaceEvent(ctx context.Context, ownerID, runID int64, eventType string, payload map[string]any) error {
	if ownerID <= 0 || runID <= 0 {
		return agenterrors.ErrInvalidInput
	}
	if s.events == nil {
		return nil
	}
	return s.newRunEventEmitter(ownerID, runID, nil).Emit(ctx, runtimeevent.Event{Type: eventType, RunID: runID, Payload: payload})
}

func (s *Service) effectiveLeaseDuration() time.Duration {
	if s.leaseDuration < 10*time.Second {
		return 30 * time.Second
	}
	return s.leaseDuration
}

func (s *Service) RunWorker(ctx context.Context, workerID string, concurrency int) {
	if concurrency <= 0 {
		concurrency = 1
	}
	for i := 0; i < concurrency; i++ {
		go s.workerLoop(ctx, fmt.Sprintf("%s-%d", workerID, i+1))
	}
	go s.recoveryLoop(ctx)
}

func (s *Service) workerLoop(ctx context.Context, workerID string) {
	ticker := time.NewTicker(300 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		token := newLeaseToken()
		turn, err := s.turns.ClaimNext(ctx, workerID, token, time.Now().UTC().Add(s.effectiveLeaseDuration()))
		if errors.Is(err, agentdomain.ErrNoTurnAvailable) {
			continue
		}
		if err != nil {
			slog.Default().Error("claim agent turn failed", "worker_id", workerID, "error", err)
			continue
		}
		s.executeTurnOwned(ctx, turn)
	}
}

func (s *Service) heartbeatLease(ctx context.Context, turnID int64, token string, cancel context.CancelFunc) {
	leaseDuration := s.effectiveLeaseDuration()
	ticker := time.NewTicker(leaseDuration / 3)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.turns.RenewLease(ctx, turnID, token, time.Now().UTC().Add(leaseDuration)); err != nil {
				cancel()
				return
			}
		}
	}
}

func (s *Service) recoveryLoop(ctx context.Context) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	_ = s.recoverExpired(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = s.recoverExpired(ctx)
		}
	}
}

func (s *Service) recoverExpired(ctx context.Context) error {
	items, err := s.turns.ListExpiredRunning(ctx, time.Now().UTC(), 100)
	if err != nil {
		return err
	}
	for i := range items {
		turn := &items[i]
		hasToolSideEffect := false
		if turn.RunID != nil && s.steps != nil {
			steps, listErr := s.steps.ListByRun(ctx, turn.OwnerID, *turn.RunID)
			if listErr == nil {
				for _, step := range steps {
					if step.StepType == runtimeagent.StepTypeToolCall || step.StepType == runtimeagent.StepTypeToolResult {
						hasToolSideEffect = true
						break
					}
				}
			}
		}
		if hasToolSideEffect || turn.AttemptCount >= turn.MaxAttempts {
			reason := "expired worker lease requires checkpoint/manual review to avoid replaying tool side effects"
			_ = s.turns.PauseExpired(ctx, turn.ID, reason)
			if turn.RunID != nil {
				if run, findErr := s.runs.FindByID(ctx, turn.OwnerID, *turn.RunID); findErr == nil {
					if err := run.TransitionStatus(agentdomain.RunStatusPaused); err != nil {
						run.ErrorMessage = err.Error()
					} else {
						run.ErrorMessage = reason
					}
					_ = s.runs.Update(ctx, run)
				}
			}
			continue
		}
		retryAt := time.Now().UTC().Add(time.Duration(turn.AttemptCount+1) * time.Second)
		_ = s.turns.RequeueExpired(ctx, turn.ID, retryAt, "requeued after expired worker lease before any tool call")
		if turn.RunID != nil {
			if run, findErr := s.runs.FindByID(ctx, turn.OwnerID, *turn.RunID); findErr == nil {
				if err := run.TransitionStatus(agentdomain.RunStatusQueued); err != nil {
					run.ErrorMessage = err.Error()
				} else {
					run.ErrorMessage = ""
				}
				_ = s.runs.Update(ctx, run)
			}
		}
	}
	return nil
}

func newLeaseToken() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return fmt.Sprintf("lease-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(value[:])
}

func (s *Service) failTurn(ctx context.Context, turn *agentdomain.Turn, run *agentdomain.Run, cause error) {
	if run != nil {
		if current, err := s.runs.FindByID(ctx, turn.OwnerID, run.ID); err == nil && current.Status == agentdomain.RunStatusCancelled {
			now := time.Now().UTC()
			turn.Status, turn.FinishedAt = agentdomain.TurnStatusCancelled, &now
			if err := s.turns.Update(ctx, turn); err == nil {
				s.publishRunSnapshot(current, turn, nil, llm.Usage{})
			}
			return
		}
	}
	now := time.Now().UTC()
	turn.Status, turn.ErrorMessage, turn.FinishedAt = agentdomain.TurnStatusFailed, cause.Error(), &now
	turnErr := s.turns.Update(ctx, turn)
	if run != nil {
		if err := run.TransitionStatus(agentdomain.RunStatusFailed); err != nil {
			run.ErrorMessage = err.Error()
		} else {
			run.ErrorMessage = cause.Error()
			run.FinishedAt = &now
		}
		run.LatencyMS = int(now.Sub(run.StartedAt).Milliseconds())
		if runErr := s.runs.Update(ctx, run); turnErr == nil && runErr == nil {
			s.publishRunSnapshot(run, turn, nil, llm.Usage{})
		}
	}
}

func (s *Service) completeTurn(ctx context.Context, turn *agentdomain.Turn, run *agentdomain.Run, result *agentruntime.RunResult) {
	if current, err := s.runs.FindByID(ctx, turn.OwnerID, run.ID); err == nil && current.Status == agentdomain.RunStatusCancelled {
		now := time.Now().UTC()
		turn.Status, turn.FinishedAt = agentdomain.TurnStatusCancelled, &now
		if err := s.turns.Update(ctx, turn); err == nil {
			s.publishRunSnapshot(current, turn, nil, llm.Usage{})
		}
		return
	}
	stopReason, _ := result.Output["stop_reason"].(string)
	if stopReason == runtimeagent.StopReasonWaitingHuman || stopReason == runtimeagent.StopReasonPaused {
		if stopReason == runtimeagent.StopReasonWaitingHuman {
			turn.Status = agentdomain.TurnStatusWaitingHuman
			if err := run.TransitionStatus(agentdomain.RunStatusWaitingHuman); err != nil {
				s.failTurn(ctx, turn, run, err)
				return
			}
		} else {
			turn.Status = agentdomain.TurnStatusPaused
			if err := run.TransitionStatus(agentdomain.RunStatusPaused); err != nil {
				s.failTurn(ctx, turn, run, err)
				return
			}
		}
		output, _ := json.Marshal(result.Output)
		turn.OutputJSON, run.OutputJSON = output, output
		if err := s.persistCheckpoint(ctx, run, result.Output, run.Status); err != nil {
			s.failTurn(ctx, turn, run, err)
			return
		}
		if err := s.turns.Update(ctx, turn); err != nil {
			s.failTurn(ctx, turn, run, err)
			return
		}
		if err := s.runs.Update(ctx, run); err != nil {
			s.failTurn(ctx, turn, run, err)
			return
		}
		if run.Status == agentdomain.RunStatusWaitingHuman {
			if approval, err := s.approvals.FindPendingApprovalByRun(ctx, run.OwnerID, run.ID); err == nil {
				s.publishApprovalRequired(run, approval)
			}
		}
		s.publishRunSnapshot(run, turn, nil, usageFromOutput(result.Output))
		return
	}
	content, _ := result.Output["final_answer"].(string)
	totalTokens, _ := result.Output["total_tokens"].(int)
	assistant := &conversation.Message{
		OwnerID:        turn.OwnerID,
		ConversationID: turn.ConversationID,
		Role:           conversation.RoleAssistant,
		Content:        content,
		ContentType:    conversation.ContentTypeText,
		RunID:          &run.ID,
		TokenCount:     totalTokens, MetadataJSON: "{}",
	}
	now := time.Now().UTC()
	output, _ := json.Marshal(result.Output)
	turn.Status, turn.AssistantMessageID, turn.OutputJSON, turn.FinishedAt = agentdomain.TurnStatusSucceeded, &assistant.ID, output, &now
	if err := run.TransitionStatus(agentdomain.RunStatusSucceeded); err != nil {
		s.failTurn(ctx, turn, run, err)
		return
	}
	run.OutputJSON, run.FinishedAt = output, &now
	if value, ok := result.Output["total_tokens"].(int); ok {
		run.TotalTokens = value
	}
	run.LatencyMS = int(now.Sub(run.StartedAt).Milliseconds())
	if err := s.turns.CompleteWithMessage(ctx, turn, assistant, run); err != nil {
		if errors.Is(err, agentdomain.ErrLeaseLost) {
			return
		}
		s.failTurn(ctx, turn, run, err)
		return
	}
	if s.improvement != nil {
		if release, err := s.agents.FindReleaseByID(ctx, turn.OwnerID, turn.AgentReleaseID); err == nil {
			_ = s.improvement.EnqueueTurnReview(ctx, turn, release)
		}
	}
	if s.sessionSearch != nil {
		_ = s.sessionSearch.IndexMessage(ctx, turn.OwnerID, turn.AgentID, assistant)
	}
	s.publishRunSnapshot(run, turn, assistant, usageFromOutput(result.Output))
}

func (s *Service) persistCheckpoint(ctx context.Context, run *agentdomain.Run, output agentruntime.RunOutput, status string) error {
	if s.approvals == nil {
		return fmt.Errorf("approval repository is not configured")
	}
	decode := func(key string, target any) bool {
		value, ok := output[key]
		if !ok {
			return false
		}
		raw, _ := json.Marshal(value)
		return json.Unmarshal(raw, target) == nil
	}
	var approval runtimeagent.Approval
	var checkpoint runtimeagent.Checkpoint
	hasApproval, hasCheckpoint := decode("approval", &approval), decode("checkpoint", &checkpoint)
	interactionID := ""
	if checkpoint.Interaction != nil {
		interactionID = checkpoint.Interaction.ID
	}
	if hasApproval {
		raw, _ := json.Marshal(approval)
		if err := s.approvals.CreateApprovalRequest(ctx, &agentdomain.ApprovalRequest{
			OwnerID:       run.OwnerID,
			RunID:         run.ID,
			ToolCallID:    approval.ToolCallID,
			InteractionID: interactionID,
			ToolName:      approval.ToolName,
			RiskLevel:     approval.RiskLevel,
			Reason:        approval.Reason,
			RequestJSON:   raw,
			Status:        agentdomain.ApprovalStatusPending,
		}); err != nil {
			return err
		}
	}
	if hasCheckpoint {
		runtimeCheckpoint, _ := json.Marshal(checkpoint)
		messages, _ := json.Marshal(checkpoint.Messages)
		pending, _ := json.Marshal(checkpoint.PendingToolCall)
		contextJSON, _ := json.Marshal(checkpoint.Context)
		stepsJSON, _ := json.Marshal(checkpoint.Steps)
		toolRegistryHash, _ := checkpoint.Metadata["tool_registry_hash"].(string)
		toolPolicyHash, _ := checkpoint.Metadata["tool_policy_hash"].(string)
		return s.approvals.CreateCheckpoint(ctx,
			&agentdomain.RunCheckpoint{
				OwnerID:               run.OwnerID,
				RunID:                 run.ID,
				Status:                status,
				SnapshotVersion:       checkpoint.SnapshotVersion,
				InteractionID:         interactionID,
				RuntimeCheckpointJSON: runtimeCheckpoint,
				MessagesJSON:          messages,
				MessagesSummary:       checkpoint.MessagesSummary,
				StepsJSON:             stepsJSON,
				PendingToolCallJSON:   pending,
				ContextJSON:           contextJSON,
				ToolRegistryHash:      toolRegistryHash,
				ToolPolicyHash:        toolPolicyHash,
			})
	}
	return nil
}

func (s *Service) ResumeRun(ctx context.Context, run *agentdomain.Run, stored *agentdomain.RunCheckpoint, decision *agentdomain.ApprovalRequest) (*agentdomain.Run, error) {
	if run == nil || stored == nil || run.AgentID <= 0 {
		return nil, agenterrors.ErrInvalidInput
	}
	var turn *agentdomain.Turn
	if len(run.DefinitionJSON) == 0 || strings.TrimSpace(run.DefinitionHash) == "" {
		return nil, agenterrors.ErrInvalidInput
	}
	if run.RunType != agentdomain.RunTypeSubagent {
		loadedTurn, err := s.turns.FindByRunID(ctx, run.OwnerID, run.ID)
		if err != nil {
			return nil, mapNotFound(err)
		}
		turn = loadedTurn
	}
	definition, err := agentruntime.DecodeDefinition(run.DefinitionJSON)
	if err != nil {
		return nil, err
	}
	checkpoint, err := decodeCheckpoint(stored)
	if err != nil {
		return nil, err
	}
	approved := decision != nil && decision.Status == agentdomain.ApprovalStatusApproved
	note := ""
	if decision != nil {
		note = decision.DecisionNote
	}
	var input map[string]any
	_ = json.Unmarshal(run.InputJSON, &input)
	task, _ := input["query"].(string)
	if mode, modeErr := normalizeAgentMode(fmt.Sprint(input["mode"])); modeErr == nil {
		definition.Mode = mode
	}
	if err := run.TransitionStatus(agentdomain.RunStatusResuming); err != nil {
		return nil, err
	}
	if err := s.runs.Update(ctx, run); err != nil {
		return nil, err
	}
	if turn != nil {
		turn.Status = agentdomain.TurnStatusRunning
		_ = s.turns.Update(ctx, turn)
	}
	failResumePreparation := func(cause error) (*agentdomain.Run, error) {
		if turn != nil {
			s.failTurn(ctx, turn, run, cause)
			return run, cause
		}
		now := time.Now().UTC()
		if transitionErr := run.TransitionStatus(agentdomain.RunStatusFailed); transitionErr != nil {
			run.ErrorMessage = transitionErr.Error()
		} else {
			run.ErrorMessage, run.FinishedAt = cause.Error(), &now
		}
		run.LatencyMS = int(now.Sub(run.StartedAt).Milliseconds())
		_ = s.runs.Update(ctx, run)
		s.publishRunSnapshot(run, nil, nil, llm.Usage{})
		return run, cause
	}
	releaseID := int64(0)
	if run.AgentReleaseID != nil {
		releaseID = *run.AgentReleaseID
	}
	var runtimeWorkspace *toolruntime.WorkspaceContext
	var resolvedWorkspace *workspacedomain.Workspace
	if s.workspace != nil && run.WorkspaceID != nil {
		var item *workspacedomain.Workspace
		var workspaceErr error
		if run.WorkspaceID != nil {
			item, workspaceErr = s.workspace.GetWorkspace(ctx, run.OwnerID, *run.WorkspaceID)
		} else {
			item, workspaceErr = s.workspace.GetRunWorkspace(ctx, run.OwnerID, run.ID)
		}
		if workspaceErr != nil {
			return failResumePreparation(workspaceErr)
		}
		resolved, resolveErr := s.workspace.ResolveExistingWorkspace(ctx, item)
		if resolveErr != nil {
			return failResumePreparation(resolveErr)
		}
		if lockErr := s.workspace.AcquireRunWorkspaceLock(ctx, resolved, run.ID); lockErr != nil {
			return failResumePreparation(lockErr)
		}
		defer func() { _ = s.workspace.ReleaseRunWorkspaceLock(context.Background(), run.OwnerID, run.ID) }()
		runtimeWorkspace = runtimeWorkspaceContext(s.workspace.WorkspaceContext(resolved))
		if runtimeWorkspace == nil {
			return failResumePreparation(fmt.Errorf("run workspace is not ready"))
		}
		runtimeWorkspace.RunID = run.ID
		resolvedWorkspace = resolved
	}
	execCtx, cancel := context.WithCancel(ctx)
	s.registerCancel(run.ID, cancel)
	defer func() { cancel(); s.unregisterCancel(run.ID) }()
	emitter := s.newRunEventEmitter(run.OwnerID, run.ID, run.ConversationID)
	if runtimeWorkspace != nil {
		_ = emitter.Emit(ctx, runtimeevent.Event{Type: runtimeevent.WorkspaceReady, RunID: run.ID, Payload: workspaceEventPayload(resolvedWorkspace, nil)})
	}
	result, execErr := s.runtime.Resume(execCtx,
		agentruntime.ResumeRequest{
			RunRequest: agentruntime.RunRequest{
				OwnerID:         run.OwnerID,
				AgentID:         run.AgentID,
				AgentReleaseID:  releaseID,
				RunID:           run.ID,
				ParentRunID:     run.ParentRunID,
				DelegationDepth: run.DelegationDepth,
				ConversationID:  run.ConversationID,
				RuleHash:        run.RuleHash,
				Task:            task,
				Definition:      definition,
				StepRecorder:    &runStepRecorder{repo: s.steps},
				Workspace:       runtimeWorkspace,
			},
			Checkpoint:    checkpoint,
			Approved:      approved,
			RejectionNote: note,
		}, emitter)
	if resolvedWorkspace != nil {
		if refreshed := s.refreshWorkspaceSnapshot(context.WithoutCancel(ctx), run.OwnerID, resolvedWorkspace.ID); refreshed != nil {
			if refreshed.Kind == workspacedomain.KindShared && refreshed.RunID != run.ID {
				view := *refreshed
				view.RunID = run.ID
				refreshed = &view
			}
			_ = emitter.Emit(context.WithoutCancel(ctx), runtimeevent.Event{Type: runtimeevent.WorkspaceStatusChanged, RunID: run.ID, Payload: workspaceEventPayload(refreshed, nil)})
		}
	}
	if execErr != nil {
		if turn != nil {
			s.failTurn(ctx, turn, run, execErr)
		} else {
			now := time.Now().UTC()
			if transitionErr := run.TransitionStatus(agentdomain.RunStatusFailed); transitionErr != nil {
				run.ErrorMessage = transitionErr.Error()
			} else {
				run.ErrorMessage, run.FinishedAt = execErr.Error(), &now
			}
			run.LatencyMS = int(now.Sub(run.StartedAt).Milliseconds())
			_ = s.runs.Update(ctx, run)
		}
		return run, execErr
	}
	if turn != nil {
		s.completeTurn(ctx, turn, run, result)
	} else {
		s.completeSubagentRun(ctx, run, result)
	}
	return s.runs.FindByID(ctx, run.OwnerID, run.ID)
}

func (s *Service) completeSubagentRun(ctx context.Context, run *agentdomain.Run, result *agentruntime.RunResult) {
	if run == nil || result == nil {
		return
	}
	output, _ := json.Marshal(result.Output)
	run.OutputJSON = output
	stopReason, _ := result.Output["stop_reason"].(string)
	switch stopReason {
	case runtimeagent.StopReasonWaitingHuman:
		if err := run.TransitionStatus(agentdomain.RunStatusWaitingHuman); err != nil {
			run.ErrorMessage = err.Error()
		}
	case runtimeagent.StopReasonPaused:
		if err := run.TransitionStatus(agentdomain.RunStatusPaused); err != nil {
			run.ErrorMessage = err.Error()
		}
	default:
		if err := run.TransitionStatus(agentdomain.RunStatusSucceeded); err != nil {
			run.ErrorMessage = err.Error()
		}
	}
	if run.Status == agentdomain.RunStatusWaitingHuman || run.Status == agentdomain.RunStatusPaused {
		run.FinishedAt = nil
		if err := s.persistCheckpoint(ctx, run, result.Output, run.Status); err != nil {
			if transitionErr := run.TransitionStatus(agentdomain.RunStatusFailed); transitionErr != nil {
				run.ErrorMessage = transitionErr.Error()
			} else {
				run.ErrorMessage = err.Error()
			}
		}
	} else {
		now := time.Now().UTC()
		run.FinishedAt = &now
		run.LatencyMS = int(now.Sub(run.StartedAt).Milliseconds())
	}
	_ = s.runs.Update(ctx, run)
	s.publishRunSnapshot(run, nil, nil, usageFromOutput(result.Output))
}

func decodeCheckpoint(stored *agentdomain.RunCheckpoint) (*runtimeagent.Checkpoint, error) {
	if len(stored.RuntimeCheckpointJSON) > 0 && string(stored.RuntimeCheckpointJSON) != "null" {
		var checkpoint runtimeagent.Checkpoint
		if err := json.Unmarshal(stored.RuntimeCheckpointJSON, &checkpoint); err != nil {
			return nil, fmt.Errorf("decode runtime checkpoint: %w", err)
		}
		if checkpoint.Metadata == nil {
			checkpoint.Metadata = map[string]any{}
		}
		checkpoint.Metadata["checkpoint_id"] = stored.ID
		checkpoint.Metadata["tool_registry_hash"] = stored.ToolRegistryHash
		checkpoint.Metadata["tool_policy_hash"] = stored.ToolPolicyHash
		return &checkpoint, nil
	}
	var messages []llm.ChatMessage
	if err := json.Unmarshal(stored.MessagesJSON, &messages); err != nil {
		return nil, fmt.Errorf("decode checkpoint messages: %w", err)
	}
	var pending *llm.ToolCall
	if len(stored.PendingToolCallJSON) > 0 && string(stored.PendingToolCallJSON) != "null" {
		var value llm.ToolCall
		if err := json.Unmarshal(stored.PendingToolCallJSON, &value); err != nil {
			return nil, err
		}
		pending = &value
	}
	var trace runtimeagent.ContextTrace
	_ = json.Unmarshal(stored.ContextJSON, &trace)
	return &runtimeagent.Checkpoint{Messages: messages, MessagesSummary: stored.MessagesSummary, PendingToolCall: pending,
		Context: trace, Metadata: map[string]any{"checkpoint_id": stored.ID,
			"tool_registry_hash": stored.ToolRegistryHash, "tool_policy_hash": stored.ToolPolicyHash}}, nil
}

func (s *Service) registerCancel(runID int64, cancel context.CancelFunc) {
	s.cancelMu.Lock()
	defer s.cancelMu.Unlock()
	s.cancels[runID] = cancel
}
func (s *Service) unregisterCancel(runID int64) {
	s.cancelMu.Lock()
	defer s.cancelMu.Unlock()
	delete(s.cancels, runID)
}
func (s *Service) CancelRun(ctx context.Context, ownerID, runID int64) error {
	run, err := s.runs.FindByID(ctx, ownerID, runID)
	if err != nil {
		return mapNotFound(err)
	}
	if run.Status == agentdomain.RunStatusQueued ||
		run.Status == agentdomain.RunStatusRunning ||
		run.Status == agentdomain.RunStatusResuming ||
		run.Status == agentdomain.RunStatusWaitingHuman ||
		run.Status == agentdomain.RunStatusPaused {
		finished := time.Now().UTC()
		if err := run.TransitionStatus(agentdomain.RunStatusCancelled); err != nil {
			return err
		}
		run.FinishedAt = &finished
		if err := s.runs.Update(ctx, run); err != nil {
			return err
		}
	}
	if children, listErr := s.runs.ListByParent(ctx, ownerID, runID); listErr == nil {
		for index := range children {
			switch children[index].Status {
			case agentdomain.RunStatusQueued, agentdomain.RunStatusRunning, agentdomain.RunStatusResuming, agentdomain.RunStatusWaitingHuman, agentdomain.RunStatusPaused:
				_ = s.CancelRun(ctx, ownerID, children[index].ID)
			}
		}
	}
	var snapshotTurn *agentdomain.Turn
	if turn, findErr := s.turns.FindByRunID(ctx, ownerID, runID); findErr == nil {
		if turn.Status == agentdomain.TurnStatusQueued || turn.Status == agentdomain.TurnStatusRunning || turn.Status == agentdomain.TurnStatusRetryWait {
			finished := time.Now().UTC()
			turn.Status, turn.FinishedAt = agentdomain.TurnStatusCancelled, &finished
			if err := s.turns.Update(ctx, turn); err == nil {
				snapshotTurn = turn
			}
		}
	}
	s.cancelMu.Lock()
	cancel := s.cancels[runID]
	s.cancelMu.Unlock()
	if cancel != nil {
		cancel()
	}
	s.publishRunSnapshot(run, snapshotTurn, nil, llm.Usage{})
	return nil
}

func (s *Service) ExecuteAcceptedTurn(ctx context.Context, ownerID, turnID int64) error {
	turn, err := s.turns.FindByID(ctx, ownerID, turnID)
	if err != nil {
		return mapNotFound(err)
	}
	s.executeTurnOwned(ctx, turn)
	return nil
}

func (s *Service) GetTurn(ctx context.Context, ownerID, turnID int64) (*agentdomain.Turn, error) {
	item, err := s.turns.FindByID(ctx, ownerID, turnID)
	return item, mapNotFound(err)
}

func (s *Service) GetLatestTurn(ctx context.Context, ownerID, agentID, conversationID int64) (*agentdomain.Turn, error) {
	item, err := s.turns.FindLatestByConversation(ctx, ownerID, agentID, conversationID)
	return item, mapNotFound(err)
}

func (s *Service) GetRun(ctx context.Context, ownerID, runID int64) (*agentdomain.Run, error) {
	item, err := s.runs.FindByID(ctx, ownerID, runID)
	return item, mapNotFound(err)
}

func (s *Service) ListRunEvents(ctx context.Context, ownerID, runID, afterID int64) ([]agentdomain.RunEvent, error) {
	if _, err := s.GetRun(ctx, ownerID, runID); err != nil {
		return nil, err
	}
	items, err := s.events.ListByRun(ctx, ownerID, runID)
	if err != nil || afterID <= 0 {
		return items, err
	}
	filtered := make([]agentdomain.RunEvent, 0, len(items))
	for _, item := range items {
		if item.ID > afterID {
			filtered = append(filtered, item)
		}
	}
	return filtered, nil
}

func (s *Service) ListChildRuns(ctx context.Context, ownerID, runID int64) ([]agentdomain.Run, error) {
	if _, err := s.GetRun(ctx, ownerID, runID); err != nil {
		return nil, err
	}
	return s.runs.ListByParent(ctx, ownerID, runID)
}

func (s *Service) ListRunSteps(ctx context.Context, ownerID, runID int64) ([]agentdomain.RunStep, error) {
	if _, err := s.GetRun(ctx, ownerID, runID); err != nil {
		return nil, err
	}
	return s.steps.ListByRun(ctx, ownerID, runID)
}

func (s *Service) ListApprovalRequests(ctx context.Context, ownerID int64, status string) ([]agentdomain.ApprovalRequest, error) {
	if ownerID <= 0 || s.approvals == nil {
		return nil, agenterrors.ErrInvalidInput
	}
	return s.approvals.ListApprovalRequests(ctx, ownerID, strings.TrimSpace(status))
}

func (s *Service) DecideApprovalRequest(ctx context.Context, ownerID, requestID int64, approved bool, note string) (*agentdomain.Run, error) {
	if ownerID <= 0 || requestID <= 0 || s.approvals == nil {
		return nil, agenterrors.ErrInvalidInput
	}
	request, err := s.approvals.FindApprovalRequestByID(ctx, ownerID, requestID)
	if err != nil {
		return nil, mapNotFound(err)
	}
	if request.Status != agentdomain.ApprovalStatusPending {
		return nil, agenterrors.ErrConflict
	}
	run, err := s.GetRun(ctx, ownerID, request.RunID)
	if err != nil {
		return nil, err
	}
	checkpoint, err := s.approvals.FindLatestCheckpointByRun(ctx, ownerID, run.ID)
	if err != nil {
		return nil, mapNotFound(err)
	}
	decidedAt := time.Now().UTC()
	request.DecidedAt, request.DecisionNote = &decidedAt, strings.TrimSpace(note)
	if approved {
		request.Status = agentdomain.ApprovalStatusApproved
	} else {
		request.Status = agentdomain.ApprovalStatusRejected
	}
	if err := s.approvals.DecideApprovalAndClaimResume(ctx, request); err != nil {
		return nil, agenterrors.ErrConflict
	}
	return s.ResumeRun(ctx, run, checkpoint, request)
}

func (s *Service) ResumeByID(ctx context.Context, ownerID, runID int64) (*agentdomain.Run, error) {
	if s.approvals == nil {
		return nil, agenterrors.ErrInvalidInput
	}
	run, err := s.GetRun(ctx, ownerID, runID)
	if err != nil {
		return nil, err
	}
	if run.Status != agentdomain.RunStatusPaused {
		return nil, agenterrors.ErrConflict
	}
	pending, pendingErr := s.approvals.FindPendingApprovalByRun(ctx, ownerID, runID)
	if pendingErr == nil && pending != nil {
		return nil, agenterrors.ErrForbidden
	}
	if pendingErr != nil && !errors.Is(mapNotFound(pendingErr), agenterrors.ErrNotFound) {
		return nil, pendingErr
	}
	checkpoint, err := s.approvals.FindLatestCheckpointByRun(ctx, ownerID, runID)
	if err != nil {
		return nil, mapNotFound(err)
	}
	if err := s.approvals.ClaimResume(ctx, ownerID, runID); err != nil {
		return nil, agenterrors.ErrConflict
	}
	return s.ResumeRun(ctx, run, checkpoint, nil)
}

func (s *Service) RunSubagent(ctx context.Context, req toolruntime.SubagentRequest) (*toolruntime.SubagentResult, error) {
	maxDepth := req.MaxDepth
	if maxDepth <= 0 {
		maxDepth = 2
	}
	if req.OwnerID <= 0 || req.ParentRunID <= 0 || req.AgentID <= 0 || strings.TrimSpace(req.Definition.Task) == "" || req.DelegationDepth >= maxDepth {
		return nil, fmt.Errorf("%w: subagent call is not allowed", agenterrors.ErrForbidden)
	}
	parent, err := s.runs.FindByID(ctx, req.OwnerID, req.ParentRunID)
	if err != nil {
		return nil, mapNotFound(err)
	}
	if parent.AgentID != req.AgentID || parent.DelegationDepth != req.DelegationDepth {
		return nil, fmt.Errorf("%w: agent parent run context does not match", agenterrors.ErrForbidden)
	}
	if parent.Status == agentdomain.RunStatusCancelled || parent.Status == agentdomain.RunStatusFailed {
		return nil, fmt.Errorf("%w: parent run is not active", agenterrors.ErrForbidden)
	}
	if req.Definition.MaxParallelChildren > 0 {
		children, listErr := s.runs.ListByParent(ctx, req.OwnerID, req.ParentRunID)
		if listErr != nil {
			return nil, listErr
		}
		active := 0
		for index := range children {
			switch children[index].Status {
			case agentdomain.RunStatusQueued, agentdomain.RunStatusRunning, agentdomain.RunStatusWaitingHuman, agentdomain.RunStatusPaused, agentdomain.RunStatusResuming:
				active++
			}
		}
		if active >= req.Definition.MaxParallelChildren {
			return nil, fmt.Errorf("%w: subagent parallel limit reached", agenterrors.ErrForbidden)
		}
	}
	definition, definitionJSON, err := subagentRuntimeDefinition(req.Definition)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	inputJSON, _ := json.Marshal(map[string]any{"query": strings.TrimSpace(req.Definition.Task)})
	run := &agentdomain.Run{
		OwnerID:         req.OwnerID,
		RunType:         agentdomain.RunTypeSubagent,
		AgentID:         parent.AgentID,
		ConversationID:  req.ConversationID,
		ParentRunID:     &req.ParentRunID,
		DelegationDepth: req.DelegationDepth + 1,
		DefinitionJSON:  definitionJSON,
		DefinitionHash:  hashJSON(definitionJSON),
		Status:          agentdomain.RunStatusQueued,
		InputJSON:       inputJSON,
		StartedAt:       now,
	}
	if err := s.runs.Create(ctx, run); err != nil {
		return nil, err
	}
	var runtimeWorkspace *toolruntime.WorkspaceContext
	emitter := s.newRunEventEmitter(req.OwnerID, run.ID, req.ConversationID)
	if s.workspace != nil && parent.WorkspaceID != nil {
		var parentWorkspace *workspacedomain.Workspace
		var workspaceErr error
		parentWorkspace, workspaceErr = s.workspace.GetWorkspace(ctx, req.OwnerID, *parent.WorkspaceID)
		if workspaceErr != nil {
			_ = emitter.Emit(ctx, runtimeevent.Event{Type: runtimeevent.WorkspaceFailed, RunID: run.ID, Payload: workspaceEventPayload(&workspacedomain.Workspace{RunID: run.ID}, workspaceErr)})
			_ = run.TransitionStatus(agentdomain.RunStatusFailed)
			run.ErrorMessage = workspaceErr.Error()
			_ = s.runs.Update(ctx, run)
			return nil, workspaceErr
		}
		project, projectErr := s.workspace.GetProject(ctx, req.OwnerID, parentWorkspace.ProjectID)
		if projectErr != nil {
			_ = emitter.Emit(ctx, runtimeevent.Event{Type: runtimeevent.WorkspaceFailed, RunID: run.ID, Payload: workspaceEventPayload(&workspacedomain.Workspace{ProjectID: parentWorkspace.ProjectID, RunID: run.ID, RepositoryRoot: parentWorkspace.RepositoryRoot}, projectErr)})
			_ = run.TransitionStatus(agentdomain.RunStatusFailed)
			run.ErrorMessage = projectErr.Error()
			_ = s.runs.Update(ctx, run)
			return nil, projectErr
		}
		childWorkspace, childErr := s.workspace.PrepareChildWorkspace(ctx, req.OwnerID, parentWorkspace.ProjectID, run.ID, req.Definition.WorkspaceMode, project.Slug, req.Definition.Task, parentWorkspace)
		if childErr != nil {
			failedWorkspace := childWorkspace
			if failedWorkspace == nil {
				failedWorkspace = &workspacedomain.Workspace{ProjectID: parentWorkspace.ProjectID, RunID: run.ID, RepositoryRoot: parentWorkspace.RepositoryRoot}
			} else {
				run.WorkspaceID = &failedWorkspace.ID
				_ = s.runs.Update(ctx, run)
			}
			_ = emitter.Emit(ctx, runtimeevent.Event{Type: runtimeevent.WorkspaceFailed, RunID: run.ID, Payload: workspaceEventPayload(failedWorkspace, childErr)})
			_ = run.TransitionStatus(agentdomain.RunStatusFailed)
			run.ErrorMessage = childErr.Error()
			_ = s.runs.Update(ctx, run)
			return nil, childErr
		}
		defer func() { _ = s.workspace.ReleaseRunWorkspaceLock(context.Background(), req.OwnerID, run.ID) }()
		run.WorkspaceID = &childWorkspace.ID
		if updateErr := s.runs.Update(ctx, run); updateErr != nil {
			_ = emitter.Emit(ctx, runtimeevent.Event{Type: runtimeevent.WorkspaceFailed, RunID: run.ID, Payload: workspaceEventPayload(childWorkspace, updateErr)})
			_ = run.TransitionStatus(agentdomain.RunStatusFailed)
			run.ErrorMessage = updateErr.Error()
			_ = s.runs.Update(ctx, run)
			return nil, updateErr
		}
		runtimeWorkspace = runtimeWorkspaceContext(s.workspace.WorkspaceContext(childWorkspace))
		_ = emitter.Emit(ctx, runtimeevent.Event{Type: runtimeevent.WorkspaceCreated, RunID: run.ID, Payload: workspaceEventPayload(childWorkspace, nil)})
		_ = emitter.Emit(ctx, runtimeevent.Event{Type: runtimeevent.WorkspaceReady, RunID: run.ID, Payload: workspaceEventPayload(childWorkspace, nil)})
	}
	if err := run.TransitionStatus(agentdomain.RunStatusRunning); err != nil {
		run.ErrorMessage = err.Error()
		_ = s.runs.Update(ctx, run)
		return nil, err
	}
	if err := s.runs.Update(ctx, run); err != nil {
		return nil, err
	}
	result, execErr := s.runtime.Execute(ctx,
		agentruntime.RunRequest{
			OwnerID:         req.OwnerID,
			AgentID:         parent.AgentID,
			RunID:           run.ID,
			ParentRunID:     &req.ParentRunID,
			DelegationDepth: run.DelegationDepth,
			ConversationID:  req.ConversationID,
			Task:            strings.TrimSpace(req.Definition.Task),
			Definition:      definition,
			StepRecorder:    &runStepRecorder{repo: s.steps},
			Workspace:       runtimeWorkspace,
		}, emitter)
	if run.WorkspaceID != nil {
		if refreshed := s.refreshWorkspaceSnapshot(context.WithoutCancel(ctx), req.OwnerID, *run.WorkspaceID); refreshed != nil {
			if refreshed.Kind == workspacedomain.KindShared && refreshed.RunID != run.ID {
				view := *refreshed
				view.RunID = run.ID
				refreshed = &view
			}
			_ = emitter.Emit(context.WithoutCancel(ctx), runtimeevent.Event{Type: runtimeevent.WorkspaceStatusChanged, RunID: run.ID, Payload: workspaceEventPayload(refreshed, nil)})
		}
	}
	var output map[string]any
	if result != nil {
		output = map[string]any(result.Output)
	}
	if execErr != nil {
		finished := time.Now().UTC()
		if transitionErr := run.TransitionStatus(agentdomain.RunStatusFailed); transitionErr != nil {
			run.ErrorMessage = transitionErr.Error()
		} else {
			run.ErrorMessage = execErr.Error()
		}
		run.FinishedAt, run.LatencyMS = &finished, int(finished.Sub(run.StartedAt).Milliseconds())
		if updateErr := s.runs.Update(ctx, run); updateErr == nil {
			s.publishRunSnapshot(run, nil, nil, llm.Usage{})
		}
	} else if result == nil {
		execErr = fmt.Errorf("agent runtime returned no result")
		finished := time.Now().UTC()
		if transitionErr := run.TransitionStatus(agentdomain.RunStatusFailed); transitionErr != nil {
			run.ErrorMessage = transitionErr.Error()
		} else {
			run.ErrorMessage = execErr.Error()
		}
		run.FinishedAt, run.LatencyMS = &finished, int(finished.Sub(run.StartedAt).Milliseconds())
		if updateErr := s.runs.Update(ctx, run); updateErr == nil {
			s.publishRunSnapshot(run, nil, nil, llm.Usage{})
		}
	} else {
		s.completeSubagentRun(ctx, run, result)
	}
	return &toolruntime.SubagentResult{RunID: run.ID, Status: run.Status, Output: output,
		Error: run.ErrorMessage, LatencyMS: run.LatencyMS}, execErr
}

func subagentRuntimeDefinition(source toolruntime.SubagentDefinition) (agentruntime.Definition, json.RawMessage, error) {
	raw, err := json.Marshal(map[string]any{
		"provider_id": source.ProviderID, "model": source.Model, "mode": source.Mode,
		"workspace_mode": source.WorkspaceMode,
		"system_prompt":  source.SystemPrompt, "tool_ids": source.ToolIDs, "skill_ids": source.SkillIDs,
		"knowledge_ids": source.KnowledgeIDs, "mcp_server_ids": source.MCPServerIDs,
		"max_iterations": source.MaxIterations, "max_tool_calls": source.MaxToolCalls,
		"max_execution_time_ms": source.MaxExecutionTimeMS, "max_parallel_sub_agents": source.MaxParallelChildren,
		"allow_subagents": true, "max_subagent_depth": source.MaxDepth,
		"require_approval_for_risk": source.RequireApprovalForRisk, "max_tool_timeout_ms": source.MaxToolTimeoutMS,
		"max_tool_output_bytes": source.MaxToolOutputBytes, "allowed_hosts": source.AllowedHosts,
		"code_execution_enabled": source.CodeExecutionEnabled,
	})
	if err != nil {
		return agentruntime.Definition{}, nil, err
	}
	definition, err := agentruntime.DecodeDefinition(raw)
	return definition, raw, err
}

func hashJSON(raw json.RawMessage) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

var _ toolruntime.SubagentDispatcher = (*Service)(nil)

type runStepRecorder struct{ repo agentdomain.RunStepRepository }

func (r *runStepRecorder) RecordAgentStep(ctx context.Context, rc *agentruntime.RunContext, step agentruntime.AgentStepRecord) error {
	return r.repo.Create(ctx, &agentdomain.RunStep{
		OwnerID:       rc.OwnerID,
		RunID:         rc.RunID,
		StepIndex:     step.StepIndex,
		StepType:      step.StepType,
		Role:          step.Role,
		Content:       step.Content,
		ToolCallID:    step.ToolCallID,
		ToolName:      step.ToolName,
		ArgumentsJSON: step.ArgumentsJSON,
		OutputJSON:    step.OutputJSON,
		Compressed:    step.Compressed,
		ErrorMessage:  step.ErrorMessage,
		TokenCount:    step.TokenCount,
		LatencyMS:     step.LatencyMS,
		ProviderID:    step.ProviderID,
		Model:         step.Model,
		CreatedAt:     time.Now().UTC(),
	})
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
