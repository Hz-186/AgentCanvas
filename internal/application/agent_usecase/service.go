package agent_usecase

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	agentdomain "agentcanvas/internal/domain/agent"
	"agentcanvas/internal/domain/conversation"
	"agentcanvas/internal/domain/knowledge"
	"agentcanvas/internal/domain/provider"
	"agentcanvas/internal/domain/workflow"
	"agentcanvas/internal/infrastructure/llm"
	agenterrors "agentcanvas/internal/pkg/errors"
	runtimeagent "agentcanvas/internal/runtime/agent"
	"agentcanvas/internal/runtime/engine"
	runtimeevent "agentcanvas/internal/runtime/event"
	"agentcanvas/internal/runtime/harness/rules"
	runtimenode "agentcanvas/internal/runtime/node"
	"agentcanvas/internal/runtime/toolruntime"
)

type Service struct {
	agents        agentdomain.Repository
	turns         agentdomain.TurnRepository
	conversations conversation.AgentRepository
	messages      conversation.MessageRepository
	runs          workflow.RunRepository
	events        workflow.RunEventRepository
	steps         workflow.RunStepRepository
	approvals     workflow.ApprovalRepository
	improvement   TurnReviewEnqueuer
	sessionSearch conversation.MessageSearchIndex
	runtime       runtimenode.AgentRuntime
	lifecycle     LifecycleWorkflowRuntime
	providers     provider.Repository
	knowledge     knowledge.BaseRepository
	cancelMu      sync.Mutex
	cancels       map[int64]context.CancelFunc
	leaseDuration time.Duration
}

type LifecycleWorkflowRuntime interface {
	toolruntime.WorkflowCaller
	ValidateLifecycleWorkflow(ctx context.Context, ownerID, workflowID, versionID int64) error
}

func NewService(
	agents agentdomain.Repository,
	turns agentdomain.TurnRepository,
	conversations conversation.AgentRepository,
	messages conversation.MessageRepository,
	runs workflow.RunRepository,
	events workflow.RunEventRepository,
	steps workflow.RunStepRepository,
	approvals workflow.ApprovalRepository,
	lifecycle LifecycleWorkflowRuntime,
	runtime runtimenode.AgentRuntime,
) *Service {
	return &Service{agents: agents, turns: turns, conversations: conversations, messages: messages, runs: runs,
		events: events, steps: steps, approvals: approvals, lifecycle: lifecycle, runtime: runtime, cancels: map[int64]context.CancelFunc{}, leaseDuration: 30 * time.Second}
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
	ProviderID   int64    `json:"provider_id"`
	Model        string   `json:"model"`
	SystemPrompt string   `json:"system_prompt"`
	KnowledgeIDs []int64  `json:"knowledge_ids"`
	Temperature  *float64 `json:"temperature,omitempty"`
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
		Mode: "react", KnowledgeIDs: normalizeIDs(settings.KnowledgeIDs), KnowledgeTopK: 5, KnowledgeMode: "hybrid",
		SkillLoadingMode: "auto", MemoryEnabled: true, ReflectionEnabled: true, AllowInlineAgents: true,
		MaxIterations: 8, MaxToolCalls: 16, MaxExecutionTimeMS: 120000, MaxToolTimeoutMS: 30000,
		MaxToolOutputBytes: 512 * 1024, MaxParallelSubAgents: 4, MaxWorkflowCallDepth: 3, OutputMode: "final_answer",
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
		KnowledgeIDs: append([]int64(nil), definition.KnowledgeIDs...), Temperature: definition.Temperature}
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
	item := &agentdomain.Agent{OwnerID: ownerID, Name: strings.TrimSpace(req.Name), Description: strings.TrimSpace(req.Description),
		AvatarURL: strings.TrimSpace(req.AvatarURL), Status: agentdomain.StatusDraft, DraftDefinition: definition}
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
	for _, binding := range lifecycleBindings(item.DraftDefinition) {
		if s.lifecycle == nil {
			result.Valid = false
			result.Errors = append(result.Errors, binding.event+" lifecycle workflow runtime is not configured")
			continue
		}
		if err := s.lifecycle.ValidateLifecycleWorkflow(ctx, ownerID, binding.workflowID, binding.versionID); err != nil {
			result.Valid = false
			result.Errors = append(result.Errors, binding.event+": "+err.Error())
		}
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
		ResourceVersions: resources, RuleSetHash: ruleHash, ToolSchemaHash: toolHash, CreatedBy: ownerID}
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
		"knowledge_bases": len(d.KnowledgeIDs), "mcp_servers": len(d.MCPServerIDs),
		"callable_agents": len(d.CallableAgentIDs), "callable_workflows": len(d.CallableWorkflowIDs),
		"memory": d.MemoryEnabled, "reflection": d.ReflectionEnabled,
	}, nil
}

type CreateConversationRequest struct {
	Title string `json:"title"`
	Mode  string `json:"mode"`
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
	item := &conversation.Conversation{OwnerID: ownerID, AgentID: &agentID, AgentReleaseID: agentItem.CurrentReleaseID,
		Title: title, Name: title, Source: conversation.SourceAgent, AgentMode: mode}
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
	fork := &conversation.Conversation{OwnerID: ownerID, AgentID: &agentID, AgentReleaseID: releaseID,
		ParentConversationID: &source.ID, Title: title, Name: title, Source: conversation.SourceAgent, AgentMode: source.AgentMode}
	if err := s.conversations.Create(ctx, fork); err != nil {
		return nil, err
	}
	messages, err := s.messages.ListByConversation(ctx, ownerID, source.ID)
	if err != nil {
		return nil, err
	}
	for _, message := range messages {
		copyMessage := &conversation.Message{OwnerID: ownerID, ConversationID: fork.ID, Role: message.Role,
			Content: message.Content, ContentType: message.ContentType, TokenCount: message.TokenCount, MetadataJSON: message.MetadataJSON}
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
	Run         *workflow.Run         `json:"run"`
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
		var run *workflow.Run
		if existing.RunID != nil {
			run, _ = s.runs.FindByID(ctx, ownerID, *existing.RunID)
		}
		return &TurnAccepted{Turn: existing, Run: run, UserMessage: s.userMessageForTurn(ctx, ownerID, existing)}, nil
	}
	if conv.AgentReleaseID == nil || *conv.AgentReleaseID <= 0 {
		return nil, agenterrors.ErrInvalidInput
	}
	userMessage := &conversation.Message{OwnerID: ownerID, ConversationID: conversationID, Role: conversation.RoleUser,
		Content: content, ContentType: conversation.ContentTypeText, MetadataJSON: "{}"}
	now := time.Now().UTC()
	mode, err := normalizeAgentMode(conv.AgentMode)
	if err != nil {
		return nil, err
	}
	inputJSON, _ := json.Marshal(map[string]any{"query": content, "mode": mode})
	run := &workflow.Run{OwnerID: ownerID, RunKind: workflow.RunKindAgent, AgentID: &agentID,
		AgentReleaseID: conv.AgentReleaseID, ConversationID: &conversationID, Status: workflow.RunStatusQueued,
		InputJSON: inputJSON, StartedAt: now, CreatedAt: now, UpdatedAt: now}
	turn := &agentdomain.Turn{OwnerID: ownerID, AgentID: agentID, AgentReleaseID: *conv.AgentReleaseID,
		ConversationID: conversationID, IdempotencyKey: key,
		Status: agentdomain.TurnStatusQueued, InputJSON: inputJSON}
	if err := s.turns.CreateWithArtifacts(ctx, turn, userMessage, run); err != nil {
		if existing, existingErr := s.turns.FindByIdempotencyKey(ctx, ownerID, conversationID, key); existingErr == nil {
			var existingRun *workflow.Run
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

func (s *Service) executeTurn(ctx context.Context, ownerID, turnID int64) {
	turn, err := s.turns.FindByID(ctx, ownerID, turnID)
	if err != nil {
		return
	}
	s.executeTurnOwned(ctx, turn)
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
	if run.Status == workflow.RunStatusCancelled {
		finished := time.Now().UTC()
		turn.Status, turn.FinishedAt = agentdomain.TurnStatusCancelled, &finished
		_ = s.turns.Update(ctx, turn)
		return
	}
	run.Status, run.ErrorMessage = workflow.RunStatusRunning, ""
	_ = s.runs.Update(ctx, run)
	execCtx, cancel := context.WithCancel(ctx)
	s.registerCancel(run.ID, cancel)
	defer func() { cancel(); s.unregisterCancel(run.ID) }()
	if turn.LeaseToken != "" {
		go s.heartbeatLease(execCtx, turn.ID, turn.LeaseToken, cancel)
	}
	ctx = execCtx
	release, err := s.agents.FindReleaseByID(ctx, turn.OwnerID, turn.AgentReleaseID)
	if err != nil {
		s.failTurn(ctx, turn, run, err)
		return
	}
	definition, err := runtimenode.DecodeAgentRuntimeDefinition(release.DefinitionJSON)
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
	emitter := &runEventEmitter{repo: s.events, ownerID: turn.OwnerID, runID: run.ID}
	contextBlocks := []runtimeagent.ContextBlock{}
	pre, err := s.runLifecycle(ctx, "pre_turn", release.Definition, turn, run, task, nil, emitter)
	if err != nil {
		s.failTurn(ctx, turn, run, err)
		return
	}
	if pre != nil {
		if pre.Action == "block" {
			s.failTurn(ctx, turn, run, fmt.Errorf("pre-turn workflow blocked the turn: %s", pre.Reason))
			return
		}
		if query, ok := pre.ContextPatch["query"].(string); ok && strings.TrimSpace(query) != "" {
			task = strings.TrimSpace(query)
		}
		if len(pre.ContextPatch) > 0 {
			raw, _ := json.Marshal(pre.ContextPatch)
			contextBlocks = append(contextBlocks, runtimeagent.ContextBlock{Name: "pre_turn_context", Role: "system", Content: string(raw), Pinned: true})
		}
	}
	result, execErr := s.runtime.Execute(ctx, runtimenode.AgentRunRequest{OwnerID: turn.OwnerID, AgentID: turn.AgentID,
		AgentReleaseID: turn.AgentReleaseID, RunID: run.ID, ConversationID: &turn.ConversationID, Task: task,
		Definition: definition, StepRecorder: &runStepRecorder{repo: s.steps}, ContextBlocks: contextBlocks}, emitter)
	if execErr != nil {
		s.failTurn(ctx, turn, run, execErr)
		return
	}
	post, postErr := s.runLifecycle(ctx, "post_turn", release.Definition, turn, run, task, map[string]any(result.Output), emitter)
	if postErr != nil {
		_ = emitter.Emit(ctx, runtimeevent.Event{Type: runtimeevent.LifecycleFailed, Payload: map[string]any{"event": "post_turn", "warning": postErr.Error()}})
	} else if post != nil && post.Action == "replace" {
		if value, ok := post.OutputPatch["final_answer"].(string); ok {
			result.Output["final_answer"], result.Output["content"] = value, value
		}
		if value, ok := post.OutputPatch["content"].(string); ok {
			result.Output["content"], result.Output["final_answer"] = value, value
		}
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
					run.Status, run.ErrorMessage = workflow.RunStatusPaused, reason
					_ = s.runs.Update(ctx, run)
				}
			}
			continue
		}
		retryAt := time.Now().UTC().Add(time.Duration(turn.AttemptCount+1) * time.Second)
		_ = s.turns.RequeueExpired(ctx, turn.ID, retryAt, "requeued after expired worker lease before any tool call")
		if turn.RunID != nil {
			if run, findErr := s.runs.FindByID(ctx, turn.OwnerID, *turn.RunID); findErr == nil {
				run.Status, run.ErrorMessage = workflow.RunStatusQueued, ""
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

func (s *Service) failTurn(ctx context.Context, turn *agentdomain.Turn, run *workflow.Run, cause error) {
	if run != nil {
		if current, err := s.runs.FindByID(ctx, turn.OwnerID, run.ID); err == nil && current.Status == workflow.RunStatusCancelled {
			now := time.Now().UTC()
			turn.Status, turn.FinishedAt = agentdomain.TurnStatusCancelled, &now
			_ = s.turns.Update(ctx, turn)
			return
		}
	}
	now := time.Now().UTC()
	turn.Status, turn.ErrorMessage, turn.FinishedAt = agentdomain.TurnStatusFailed, cause.Error(), &now
	_ = s.turns.Update(ctx, turn)
	if run != nil {
		run.Status, run.ErrorMessage, run.FinishedAt = workflow.RunStatusFailed, cause.Error(), &now
		run.LatencyMS = int(now.Sub(run.StartedAt).Milliseconds())
		_ = s.runs.Update(ctx, run)
	}
}

func (s *Service) completeTurn(ctx context.Context, turn *agentdomain.Turn, run *workflow.Run, result *runtimenode.AgentRunResult) {
	if current, err := s.runs.FindByID(ctx, turn.OwnerID, run.ID); err == nil && current.Status == workflow.RunStatusCancelled {
		now := time.Now().UTC()
		turn.Status, turn.FinishedAt = agentdomain.TurnStatusCancelled, &now
		_ = s.turns.Update(ctx, turn)
		return
	}
	stopReason, _ := result.Output["stop_reason"].(string)
	if stopReason == runtimeagent.StopReasonWaitingHuman || stopReason == runtimeagent.StopReasonPaused {
		if stopReason == runtimeagent.StopReasonWaitingHuman {
			turn.Status, run.Status = agentdomain.TurnStatusWaitingHuman, workflow.RunStatusWaitingHuman
		} else {
			turn.Status, run.Status = agentdomain.TurnStatusPaused, workflow.RunStatusPaused
		}
		output, _ := json.Marshal(result.Output)
		turn.OutputJSON, run.OutputJSON = output, output
		if err := s.persistCheckpoint(ctx, run, result.Output, run.Status); err != nil {
			s.failTurn(ctx, turn, run, err)
			return
		}
		_ = s.turns.Update(ctx, turn)
		_ = s.runs.Update(ctx, run)
		return
	}
	content, _ := result.Output["final_answer"].(string)
	totalTokens, _ := result.Output["total_tokens"].(int)
	assistant := &conversation.Message{OwnerID: turn.OwnerID, ConversationID: turn.ConversationID, Role: conversation.RoleAssistant,
		Content: content, ContentType: conversation.ContentTypeText, RunID: &run.ID, TokenCount: totalTokens, MetadataJSON: "{}"}
	now := time.Now().UTC()
	output, _ := json.Marshal(result.Output)
	turn.Status, turn.AssistantMessageID, turn.OutputJSON, turn.FinishedAt = agentdomain.TurnStatusSucceeded, &assistant.ID, output, &now
	run.Status, run.OutputJSON, run.FinishedAt = workflow.RunStatusSucceeded, output, &now
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
}

func (s *Service) persistCheckpoint(ctx context.Context, run *workflow.Run, output engine.NodeOutput, status string) error {
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
	if hasApproval {
		raw, _ := json.Marshal(approval)
		if err := s.approvals.CreateApprovalRequest(ctx, &workflow.ApprovalRequest{OwnerID: run.OwnerID, WorkflowID: 0,
			RunID: run.ID, NodeID: "agent", ToolCallID: approval.ToolCallID, ToolName: approval.ToolName,
			RiskLevel: approval.RiskLevel, Reason: approval.Reason, RequestJSON: raw, Status: workflow.ApprovalStatusPending}); err != nil {
			return err
		}
	}
	if hasCheckpoint {
		messages, _ := json.Marshal(checkpoint.Messages)
		pending, _ := json.Marshal(checkpoint.PendingToolCall)
		contextJSON, _ := json.Marshal(checkpoint.Context)
		stepsJSON, _ := json.Marshal(output["steps"])
		return s.approvals.CreateCheckpoint(ctx, &workflow.WorkflowCheckpoint{OwnerID: run.OwnerID, WorkflowID: 0, RunID: run.ID,
			NodeID: "agent", Status: status, MessagesJSON: messages, MessagesSummary: checkpoint.MessagesSummary,
			StepsJSON: stepsJSON, PendingToolCallJSON: pending, ContextJSON: contextJSON})
	}
	return nil
}

func (s *Service) ResumeIndependentRun(ctx context.Context, run *workflow.Run, stored *workflow.WorkflowCheckpoint, decision *workflow.ApprovalRequest) (*workflow.Run, error) {
	if run == nil || stored == nil || run.AgentID == nil || run.AgentReleaseID == nil {
		return nil, agenterrors.ErrInvalidInput
	}
	turn, err := s.turns.FindByRunID(ctx, run.OwnerID, run.ID)
	if err != nil {
		return nil, mapNotFound(err)
	}
	release, err := s.agents.FindReleaseByID(ctx, run.OwnerID, *run.AgentReleaseID)
	if err != nil {
		return nil, mapNotFound(err)
	}
	definition, err := runtimenode.DecodeAgentRuntimeDefinition(release.DefinitionJSON)
	if err != nil {
		return nil, err
	}
	checkpoint, err := decodeCheckpoint(stored)
	if err != nil {
		return nil, err
	}
	approved := decision != nil && decision.Status == workflow.ApprovalStatusApproved
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
	turn.Status = agentdomain.TurnStatusRunning
	_ = s.turns.Update(ctx, turn)
	execCtx, cancel := context.WithCancel(ctx)
	s.registerCancel(run.ID, cancel)
	defer func() { cancel(); s.unregisterCancel(run.ID) }()
	result, execErr := s.runtime.Resume(execCtx, runtimenode.AgentResumeRequest{AgentRunRequest: runtimenode.AgentRunRequest{
		OwnerID: run.OwnerID, AgentID: *run.AgentID, AgentReleaseID: *run.AgentReleaseID, RunID: run.ID,
		ConversationID: run.ConversationID, Task: task, Definition: definition, StepRecorder: &runStepRecorder{repo: s.steps}},
		Checkpoint: checkpoint, Approved: approved, RejectionNote: note}, &runEventEmitter{repo: s.events, ownerID: run.OwnerID, runID: run.ID})
	if execErr != nil {
		s.failTurn(ctx, turn, run, execErr)
		return run, execErr
	}
	s.completeTurn(ctx, turn, run, result)
	return s.runs.FindByID(ctx, run.OwnerID, run.ID)
}

func decodeCheckpoint(stored *workflow.WorkflowCheckpoint) (*runtimeagent.Checkpoint, error) {
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
		Context: trace, Metadata: map[string]any{"node_id": "agent", "checkpoint_id": stored.ID,
			"tool_registry_hash": stored.ToolRegistryHash, "tool_policy_hash": stored.ToolPolicyHash}}, nil
}

var _ workflow.IndependentRunResumer = (*Service)(nil)
var _ workflow.IndependentRunCanceller = (*Service)(nil)

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
func (s *Service) CancelIndependentRun(ctx context.Context, ownerID, runID int64) error {
	turn, err := s.turns.FindByRunID(ctx, ownerID, runID)
	if err != nil {
		return mapNotFound(err)
	}
	if turn.Status == agentdomain.TurnStatusQueued || turn.Status == agentdomain.TurnStatusRunning || turn.Status == agentdomain.TurnStatusRetryWait {
		finished := time.Now().UTC()
		turn.Status, turn.FinishedAt = agentdomain.TurnStatusCancelled, &finished
		if err := s.turns.Update(ctx, turn); err != nil {
			return err
		}
	}
	s.cancelMu.Lock()
	cancel := s.cancels[runID]
	s.cancelMu.Unlock()
	if cancel != nil {
		cancel()
	}
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

func (s *Service) GetRun(ctx context.Context, ownerID, runID int64) (*workflow.Run, error) {
	item, err := s.runs.FindByID(ctx, ownerID, runID)
	return item, mapNotFound(err)
}

func (s *Service) ListRunEvents(ctx context.Context, ownerID, runID, afterID int64) ([]workflow.RunEvent, error) {
	if _, err := s.GetRun(ctx, ownerID, runID); err != nil {
		return nil, err
	}
	items, err := s.events.ListByRun(ctx, ownerID, runID)
	if err != nil || afterID <= 0 {
		return items, err
	}
	filtered := make([]workflow.RunEvent, 0, len(items))
	for _, item := range items {
		if item.ID > afterID {
			filtered = append(filtered, item)
		}
	}
	return filtered, nil
}

func (s *Service) CallAgent(ctx context.Context, req toolruntime.AgentCallRequest) (*toolruntime.AgentCallResult, error) {
	maxDepth := req.MaxDepth
	if maxDepth <= 0 {
		maxDepth = 3
	}
	if req.OwnerID <= 0 || req.ParentRunID <= 0 || req.CallerAgentID <= 0 || req.AgentID <= 0 || strings.TrimSpace(req.Task) == "" || req.CallDepth >= maxDepth {
		return nil, fmt.Errorf("%w: independent agent call is not allowed", agenterrors.ErrForbidden)
	}
	parent, err := s.runs.FindByID(ctx, req.OwnerID, req.ParentRunID)
	if err != nil {
		return nil, mapNotFound(err)
	}
	if parent.AgentID == nil || *parent.AgentID != req.CallerAgentID || parent.CallDepth != req.CallDepth {
		return nil, fmt.Errorf("%w: agent parent run context does not match", agenterrors.ErrForbidden)
	}
	child, err := s.agents.FindByID(ctx, req.OwnerID, req.AgentID)
	if err != nil {
		return nil, mapNotFound(err)
	}
	if child.Status != agentdomain.StatusActive || child.CurrentReleaseID == nil {
		return nil, fmt.Errorf("%w: called agent has no active release", agenterrors.ErrInvalidInput)
	}
	release, err := s.agents.FindReleaseByID(ctx, req.OwnerID, *child.CurrentReleaseID)
	if err != nil {
		return nil, mapNotFound(err)
	}
	definition, err := runtimenode.DecodeAgentRuntimeDefinition(release.DefinitionJSON)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	inputJSON, _ := json.Marshal(map[string]any{"query": strings.TrimSpace(req.Task)})
	run := &workflow.Run{OwnerID: req.OwnerID, RunKind: workflow.RunKindAgent, AgentID: &child.ID, AgentReleaseID: &release.ID,
		ParentRunID: &req.ParentRunID, CallerNodeID: "call_agent", CallDepth: req.CallDepth + 1,
		Status: workflow.RunStatusRunning, InputJSON: inputJSON, StartedAt: now}
	if err := s.runs.Create(ctx, run); err != nil {
		return nil, err
	}
	result, execErr := s.runtime.Execute(ctx, runtimenode.AgentRunRequest{OwnerID: req.OwnerID, AgentID: child.ID,
		AgentReleaseID: release.ID, RunID: run.ID, Task: strings.TrimSpace(req.Task), Definition: definition,
		StepRecorder: &runStepRecorder{repo: s.steps}}, &runEventEmitter{repo: s.events, ownerID: req.OwnerID, runID: run.ID})
	finished := time.Now().UTC()
	run.FinishedAt, run.LatencyMS = &finished, int(finished.Sub(run.StartedAt).Milliseconds())
	var output map[string]any
	if result != nil {
		output = map[string]any(result.Output)
		run.OutputJSON, _ = json.Marshal(output)
	}
	if execErr != nil {
		run.Status, run.ErrorMessage = workflow.RunStatusFailed, execErr.Error()
	} else {
		run.Status = workflow.RunStatusSucceeded
	}
	_ = s.runs.Update(ctx, run)
	return &toolruntime.AgentCallResult{RunID: run.ID, AgentID: child.ID, AgentReleaseID: release.ID,
		Status: run.Status, Output: output, Error: run.ErrorMessage, LatencyMS: run.LatencyMS}, execErr
}

var _ toolruntime.AgentCaller = (*Service)(nil)

type runEventEmitter struct {
	repo           workflow.RunEventRepository
	ownerID, runID int64
}

func (e *runEventEmitter) Emit(ctx context.Context, event runtimeevent.Event) error {
	if event.RunID == 0 {
		event.RunID = e.runID
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}
	payload, _ := json.Marshal(event.Payload)
	return e.repo.Create(ctx, &workflow.RunEvent{OwnerID: e.ownerID, RunID: event.RunID, EventType: event.Type,
		NodeID: event.NodeID, NodeType: event.NodeType, PayloadJSON: payload, CreatedAt: event.CreatedAt})
}

type runStepRecorder struct{ repo workflow.RunStepRepository }

func (r *runStepRecorder) RecordAgentStep(ctx context.Context, rc *engine.RunContext, step engine.AgentStepRecord) error {
	return r.repo.Create(ctx, &workflow.RunStep{OwnerID: rc.OwnerID, RunID: rc.RunID, NodeID: step.NodeID,
		StepIndex: step.StepIndex, StepType: step.StepType, Role: step.Role, Content: step.Content,
		ToolCallID: step.ToolCallID, ToolName: step.ToolName, ArgumentsJSON: step.ArgumentsJSON, OutputJSON: step.OutputJSON,
		Compressed: step.Compressed, ErrorMessage: step.ErrorMessage, TokenCount: step.TokenCount, LatencyMS: step.LatencyMS, CreatedAt: time.Now().UTC()})
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
