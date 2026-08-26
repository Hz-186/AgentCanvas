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
	"agentcanvas/internal/domain"
	agentdomain "agentcanvas/internal/domain/agent"
	"agentcanvas/internal/domain/conversation"
	"agentcanvas/internal/domain/goal"
	"agentcanvas/internal/domain/knowledge"
	"agentcanvas/internal/domain/provider"
	workspacedomain "agentcanvas/internal/domain/workspace"
	agenterrors "agentcanvas/internal/pkg/errors"
	agentruntime "agentcanvas/internal/runtime/agentruntime"
	"agentcanvas/internal/runtime/harness/rules"
	"agentcanvas/internal/runtime/toolruntime"
)

type Service struct {
	agents                 agentdomain.Repository
	turns                  agentdomain.TurnRepository
	conversations          conversation.AgentRepository
	messages               conversation.MessageRepository
	runs                   agentdomain.RunRepository
	events                 agentdomain.RunEventRepository
	steps                  agentdomain.RunStepRepository
	approvals              agentdomain.ApprovalRepository
	improvement            TurnReviewEnqueuer
	sessionSearch          conversation.MessageSearchIndex
	runtime                agentruntime.Runtime
	providers              provider.Repository
	knowledge              knowledge.BaseRepository
	cancelMu               sync.Mutex
	cancels                map[int64]context.CancelFunc
	steeringMu             sync.Mutex
	steeringByRun          map[int64][]string
	leaseDuration          time.Duration
	streamHub              runStreamHub
	workspace              *workspaceusecase.Service
	goals                  goal.Repository
	goalTokenBudgetCeiling *int64
	goalContinuationMu     sync.Mutex
	goalContinuations      map[int64]struct{}
	goalUsageMu            sync.Mutex
	goalUsageByRun         map[int64]goalUsageState
	goalStreams            *goalStreamHub
}

func (s *Service) ConfigureGoalRepository(repository goal.Repository) { s.goals = repository }

func (s *Service) ConfigureGoalTokenBudgetCeiling(ceiling *int64) {
	if ceiling == nil {
		s.goalTokenBudgetCeiling = nil
		return
	}
	value := *ceiling
	s.goalTokenBudgetCeiling = &value
}

func runtimeWorkspaceContext(item *workspacedomain.Context) *toolruntime.WorkspaceContext {
	if item == nil {
		return nil
	}
	return &toolruntime.WorkspaceContext{
		ID:                 item.ID,
		ProjectID:          item.ProjectID,
		RunID:              item.RunID,
		Kind:               item.Kind,
		RepositoryRoot:     item.RepositoryRoot,
		WorkspacePath:      item.WorkspacePath,
		BranchName:         item.BranchName,
		BaseSHA:            item.BaseSHA,
		HeadSHA:            item.HeadSHA,
		Dirty:              item.Dirty,
		HasUnpushedCommits: item.HasUnpushedCommits,
		FileWriteEnabled:   item.FileWriteEnabled,
		GitEnabled:         item.GitEnabled,
		ExecEnabled:        item.ExecEnabled,
	}
}

func workspaceEventPayload(item *workspacedomain.Workspace, err error) map[string]any {
	payload := map[string]any{
		"workspace_id":         int64(0),
		"project_id":           int64(0),
		"run_id":               int64(0),
		"kind":                 "",
		"repository_root":      "",
		"workspace_path":       "",
		"branch_name":          "",
		"base_sha":             "",
		"head_sha":             "",
		"dirty":                false,
		"has_unpushed_commits": false,
		"status":               "",
		"locked":               false,
		"lock_reason":          "",
		"cleanup_reason":       "",
		"error_message":        "",
	}
	if item != nil {
		payload["workspace_id"], payload["project_id"], payload["run_id"] = item.ID, item.ProjectID, item.RunID
		payload["kind"], payload["repository_root"], payload["workspace_path"] = item.Kind, item.RepositoryRoot, item.WorkspacePath
		payload["branch_name"], payload["base_sha"], payload["head_sha"] = item.BranchName, item.BaseSHA, item.HeadSHA
		payload["dirty"], payload["has_unpushed_commits"] = item.Dirty, item.HasUnpushedCommits
		payload["status"], payload["locked"], payload["lock_reason"], payload["cleanup_reason"] = item.Status, item.Locked, item.LockReason, item.CleanupReason
		if item.ErrorMessage != "" {
			payload["error_message"] = item.ErrorMessage
		}
	}
	if err != nil {
		payload["error_message"] = err.Error()
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
	return &Service{
		agents:            agents,
		turns:             turns,
		conversations:     conversations,
		messages:          messages,
		runs:              runs,
		events:            events,
		steps:             steps,
		approvals:         approvals,
		runtime:           runtime,
		cancels:           map[int64]context.CancelFunc{},
		steeringByRun:     map[int64][]string{},
		goalContinuations: map[int64]struct{}{},
		goalUsageByRun:    map[int64]goalUsageState{},
		goalStreams:       newGoalStreamHub(),
		leaseDuration:     30 * time.Second,
	}
}

func (s *Service) queueSteering(runID int64, content string) bool {
	content = strings.TrimSpace(content)
	if runID <= 0 || content == "" {
		return false
	}
	s.steeringMu.Lock()
	defer s.steeringMu.Unlock()
	if s.steeringByRun == nil {
		s.steeringByRun = make(map[int64][]string)
	}
	s.steeringByRun[runID] = append(s.steeringByRun[runID], content)
	return true
}

func (s *Service) consumeSteering(runID int64) []string {
	s.steeringMu.Lock()
	defer s.steeringMu.Unlock()
	items := append([]string(nil), s.steeringByRun[runID]...)
	delete(s.steeringByRun, runID)
	return items
}

func (s *Service) clearSteering(runID int64) {
	s.steeringMu.Lock()
	delete(s.steeringByRun, runID)
	s.steeringMu.Unlock()
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
	ProviderID       int64    `json:"provider_id"`
	Model            string   `json:"model"`
	SystemPrompt     string   `json:"system_prompt"`
	KnowledgeBaseIDs []int64  `json:"knowledge_base_ids"`
	Temperature      *float64 `json:"temperature,omitempty"`
}

type AgentView struct {
	ID          int64                 `json:"id"`
	OwnerID     int64                 `json:"owner_id"`
	Name        string                `json:"name"`
	Description string                `json:"description"`
	AvatarURL   string                `json:"avatar_url"`
	Status      string                `json:"status"`
	Settings    AgentEditableSettings `json:"settings"`
	CreatedAt   time.Time             `json:"created_at"`
	UpdatedAt   time.Time             `json:"updated_at"`
}

const defaultAgentSystemPrompt = "You are a capable, careful AI agent. Use tools when they materially help, and return a concise final answer."

func ManagedDefinition(settings AgentEditableSettings) agentdomain.Definition {
	return agentdomain.Definition{
		ModelConfig:  agentdomain.ModelConfig{ProviderID: settings.ProviderID, Model: strings.TrimSpace(settings.Model), Temperature: settings.Temperature},
		PromptConfig: agentdomain.PromptConfig{SystemPrompt: strings.TrimSpace(settings.SystemPrompt), OutputMode: "final_answer"},
		ResourceRefs: agentdomain.ResourceRefs{KnowledgeBaseIDs: normalizeIDs(settings.KnowledgeBaseIDs), KnowledgeTopK: 5, KnowledgeMode: "hybrid", SkillLoadingMode: "auto"},
		MemoryPolicy: agentdomain.MemoryPolicy{MemoryEnabled: true, ReflectionEnabled: true},
		ExecutionLimits: agentdomain.ExecutionLimits{
			Mode: conversation.ModeDefault, AllowSubagents: true, MaxIterations: 8, MaxToolCalls: 16, MaxExecutionTimeMS: 120000,
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
		KnowledgeBaseIDs: append([]int64(nil), definition.KnowledgeBaseIDs...), Temperature: definition.Temperature}
}

func viewAgent(item *agentdomain.Agent) *AgentView {
	if item == nil {
		return nil
	}
	return &AgentView{ID: item.ID, OwnerID: item.OwnerID, Name: item.Name, Description: item.Description, AvatarURL: item.AvatarURL,
		Status: item.Status, Settings: settingsFromDefinition(item.DraftDefinition),
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
	if err != nil || !providerItem.Enabled {
		return agenterrors.ErrInvalidInput
	}
	for _, id := range definition.KnowledgeBaseIDs {
		item, findErr := s.knowledge.FindByID(ctx, ownerID, id)
		if findErr != nil || !item.Enabled {
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
		SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{OwnerID: ownerID}},
		Name:            strings.TrimSpace(req.Name),
		Description:     strings.TrimSpace(req.Description),
		AvatarURL:       strings.TrimSpace(req.AvatarURL),
		Status:          agentdomain.StatusActive,
		DraftDefinition: definition,
	}
	if err := s.agents.Create(ctx, item); err != nil {
		return nil, err
	}
	return viewAgent(item), nil
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
	return viewAgent(item), nil
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

type CreateConversationRequest struct {
	Title         string `json:"title"`
	Mode          string `json:"mode"`
	ProjectID     *int64 `json:"project_id,omitempty"`
	WorkspaceMode string `json:"workspace_mode,omitempty"`
}

func normalizeAgentMode(mode string) (string, error) {
	normalized, err := conversation.NormalizeMode(mode)
	if err != nil {
		return "", agenterrors.ErrInvalidInput
	}
	return normalized, nil
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
	if _, err := s.getAgent(ctx, ownerID, agentID); err != nil {
		return nil, err
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
	item := &conversation.Conversation{
		SoftDeleteModel: domain.SoftDeleteModel{
			BaseModel: domain.BaseModel{OwnerID: ownerID},
		},
		AgentID:       &agentID,
		ProjectID:     req.ProjectID,
		WorkspaceMode: workspaceMode,
		Title:         title,
		AgentMode:     mode,
	}
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
	items, err := s.conversations.ListByAgent(ctx, ownerID, agentID)
	for index := range items {
		if mode, modeErr := normalizeAgentMode(items[index].AgentMode); modeErr == nil {
			items[index].AgentMode = mode
		}
	}
	return items, err
}

func (s *Service) GetConversation(ctx context.Context, ownerID, agentID, conversationID int64) (*conversation.Conversation, error) {
	item, err := s.conversations.FindByID(ctx, ownerID, conversationID)
	if err != nil {
		return nil, mapNotFound(err)
	}
	if item.AgentID == nil || *item.AgentID != agentID {
		return nil, agenterrors.ErrNotFound
	}
	if mode, modeErr := normalizeAgentMode(item.AgentMode); modeErr == nil {
		item.AgentMode = mode
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

func (s *Service) ForkConversation(ctx context.Context, ownerID, agentID, conversationID int64) (*conversation.Conversation, error) {
	return s.ForkConversationWithOptions(ctx, ownerID, agentID, conversationID, false)
}

func (s *Service) ForkConversationWithOptions(ctx context.Context, ownerID, agentID, conversationID int64, deferGoalContinuation bool) (*conversation.Conversation, error) {
	source, err := s.GetConversation(ctx, ownerID, agentID, conversationID)
	if err != nil {
		return nil, err
	}
	title := source.Title + " (fork)"
	fork := &conversation.Conversation{
		SoftDeleteModel:      domain.SoftDeleteModel{BaseModel: domain.BaseModel{OwnerID: ownerID}},
		AgentID:              &agentID,
		ParentConversationID: &source.ID,
		Title:                title,
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
			ImmutableModel: domain.ImmutableModel{OwnerID: ownerID},
			ConversationID: fork.ID,
			Role:           message.Role,
			Content:        message.Content,
			TokenCount:     message.TokenCount,
			ContentType:    message.ContentType,
			MetadataJSON:   append(json.RawMessage(nil), message.MetadataJSON...),
		}
		if err := s.messages.Create(ctx, copyMessage); err != nil {
			return nil, err
		}
		// Only text rows carry searchable prose; tool entries and echoes stay
		// out of the session index.
		if s.sessionSearch != nil && copyMessage.ContentType == conversation.ContentTypeText {
			_ = s.sessionSearch.IndexMessage(ctx, ownerID, agentID, copyMessage)
		}
	}
	if deferGoalContinuation && s.goals != nil {
		if sourceGoal, goalErr := s.goals.Get(ctx, ownerID, source.ID); goalErr == nil && sourceGoal != nil {
			if flushErr := s.flushGoalProgressForFork(ctx, ownerID, source, sourceGoal); flushErr != nil {
				return nil, flushErr
			}
			// Re-read after the flush so the fork copies the authoritative usage
			// and budget-limited status, not a stale pre-flush snapshot.
			if refreshed, refreshErr := s.goals.Get(ctx, ownerID, source.ID); refreshErr == nil && refreshed != nil {
				sourceGoal = refreshed
			}
			copyGoal := *sourceGoal
			copyGoal.ID, copyGoal.GoalID, copyGoal.ConversationID = 0, "", fork.ID
			copyGoal.OwnerID = ownerID
			if err := s.goals.Create(ctx, &copyGoal); err != nil {
				return nil, err
			}
			if err := s.goals.SetDeferral(ctx, ownerID, fork.ID, true); err != nil {
				return nil, err
			}
		}
	}
	return fork, nil
}

func (s *Service) flushGoalProgressForFork(ctx context.Context, ownerID int64, source *conversation.Conversation, current *goal.ThreadGoal) error {
	if s.goals == nil || source == nil || current == nil || current.Status != goal.StatusActive || s.turns == nil || s.runs == nil {
		return nil
	}
	agentID := int64(0)
	if source.AgentID != nil {
		agentID = *source.AgentID
	}
	turn, err := s.turns.FindLatestByConversation(ctx, ownerID, agentID, source.ID)
	if err != nil || turn == nil || turn.RunID == nil {
		return nil
	}
	run, err := s.runs.FindByID(ctx, ownerID, *turn.RunID)
	if err != nil || run == nil {
		return nil
	}
	switch run.Status {
	case agentdomain.RunStatusRunning, agentdomain.RunStatusResuming, agentdomain.RunStatusWaitingHuman, agentdomain.RunStatusPaused:
	default:
		return nil
	}
	s.goalUsageMu.Lock()
	state := s.goalUsageByRun[run.ID]
	if state.GoalID != current.GoalID {
		state = goalUsageState{GoalID: current.GoalID}
	}
	elapsed := int64(time.Since(run.StartedAt).Seconds())
	if elapsed < 0 {
		elapsed = 0
	}
	seconds := elapsed - state.SecondsAccounted
	if seconds < 0 {
		seconds = 0
	}
	tokens := int64(run.TotalTokens) - state.TokensAccounted
	if tokens < 0 {
		tokens = 0
	}
	expected := state.GoalID
	s.goalUsageMu.Unlock()
	if seconds == 0 && tokens == 0 {
		return nil
	}
	if _, err := s.accountGoal(ctx, ownerID, source.ID, seconds, tokens, "active_only", expected); err != nil && !errors.Is(err, goal.ErrNotFound) {
		return err
	}
	s.goalUsageMu.Lock()
	state.SecondsAccounted += seconds
	state.TokensAccounted += tokens
	s.goalUsageByRun[run.ID] = state
	s.goalUsageMu.Unlock()
	return nil
}

type CreateTurnRequest struct {
	Content          string `json:"content" binding:"required"`
	ManualCompaction bool   `json:"manual_compaction,omitempty"`
	GoalContinuation bool   `json:"goal_continuation,omitempty"`
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
	if !req.GoalContinuation && s.goals != nil {
		_ = s.goals.SetDeferral(ctx, ownerID, conversationID, false)
	}
	if existing, existingErr := s.turns.FindByIdempotencyKey(ctx, ownerID, conversationID, key); existingErr == nil {
		var run *agentdomain.Run
		if existing.RunID != nil {
			run, _ = s.runs.FindByID(ctx, ownerID, *existing.RunID)
		}
		return &TurnAccepted{Turn: existing, Run: run, UserMessage: s.userMessageForTurn(ctx, ownerID, existing)}, nil
	}
	agentItem, err := s.getAgent(ctx, ownerID, agentID)
	if err != nil {
		return nil, err
	}
	definition := agentItem.DraftDefinition.Normalize()
	if err := validateDefinitionRules(definition); err != nil {
		return nil, fmt.Errorf("%w: %v", agenterrors.ErrInvalidInput, err)
	}
	definitionJSON, definitionHash, err := definition.Snapshot()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", agenterrors.ErrInvalidInput, err)
	}
	_, ruleHash, _, err := definition.ResourceSnapshot()
	if err != nil {
		return nil, err
	}
	userMessage := &conversation.Message{
		ImmutableModel: domain.ImmutableModel{
			OwnerID: ownerID,
		},
		ConversationID: conversationID,
		Role:           conversation.RoleUser,
		Content:        content,
	}
	if req.GoalContinuation {
		userMessage.Role = conversation.RoleDeveloper
	}
	if req.ManualCompaction {
		// /compact is an operator echo, not real user input: tag it so
		// compaction skips it and the search index never sees it.
		userMessage.ContentType = conversation.ContentTypeSystemEcho
	}
	now := time.Now().UTC()
	mode, err := normalizeAgentMode(conv.AgentMode)
	if err != nil {
		return nil, err
	}
	inputJSON, _ := json.Marshal(map[string]any{"query": content, "mode": mode, "manual_compaction": req.ManualCompaction})
	run := &agentdomain.Run{
		BaseModel: domain.BaseModel{
			OwnerID:   ownerID,
			CreatedAt: now,
			UpdatedAt: now,
		},
		RunType:        agentdomain.RunTypeTurn,
		AgentID:        agentID,
		ConversationID: &conversationID,
		Status:         agentdomain.RunStatusQueued,
		DefinitionJSON: definitionJSON,
		DefinitionHash: definitionHash,
		RuleHash:       ruleHash,
		InputJSON:      inputJSON,
		StartedAt:      now,
	}
	turn := &agentdomain.Turn{
		BaseModel:      domain.BaseModel{OwnerID: ownerID},
		AgentID:        agentID,
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
			return &TurnAccepted{
				Turn:        existing,
				Run:         existingRun,
				UserMessage: s.userMessageForTurn(ctx, ownerID, existing),
			}, nil
		}
		return nil, err
	}
	if s.sessionSearch != nil && userMessage.ContentType == conversation.ContentTypeText {
		_ = s.sessionSearch.IndexMessage(ctx, ownerID, agentID, userMessage)
	}
	return &TurnAccepted{
		Turn:        turn,
		Run:         run,
		UserMessage: userMessage,
	}, nil
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
