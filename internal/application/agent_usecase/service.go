package agent_usecase

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"agentcanvas/internal/domain/agent"
	"agentcanvas/internal/domain/conversation"
	"agentcanvas/internal/domain/flow"
	"agentcanvas/internal/domain/memory"
	providerdomain "agentcanvas/internal/domain/provider"
	"agentcanvas/internal/domain/retrieval"
	"agentcanvas/internal/domain/tool"
	cryptoinfra "agentcanvas/internal/infrastructure/crypto"
	"agentcanvas/internal/infrastructure/llm"
	"agentcanvas/internal/runtime/engine"
	runtimeevent "agentcanvas/internal/runtime/event"
	runtimenode "agentcanvas/internal/runtime/node"

	agenterrors "agentcanvas/internal/pkg/errors"

	"gorm.io/gorm"
)

type Service struct {
	agents          agent.Repository
	versions        agent.FlowVersionRepository
	runs            agent.RunRepository
	events          agent.RunEventRepository
	nodeLogs        agent.NodeLogRepository
	memories        memory.Repository
	memoryLogs      memory.WriteLogRepository
	tools           tool.DefinitionRepository
	toolInvocations tool.InvocationRepository
	providers       providerdomain.Repository
	messages        conversation.MessageRepository
	retriever       retrieval.Retriever
	llm             llm.ChatClient
	secrets         *cryptoinfra.SecretBox
	executor        *engine.Executor
	validator       *flow.Validator
}

func NewService(agents agent.Repository, versions agent.FlowVersionRepository, runs agent.RunRepository, events agent.RunEventRepository, nodeLogs agent.NodeLogRepository, memories memory.Repository, memoryLogs memory.WriteLogRepository, tools tool.DefinitionRepository, toolInvocations tool.InvocationRepository, providers providerdomain.Repository, messages conversation.MessageRepository, retriever retrieval.Retriever, llmClient llm.ChatClient, secrets *cryptoinfra.SecretBox) *Service {
	s := &Service{agents: agents, versions: versions, runs: runs, events: events, nodeLogs: nodeLogs, memories: memories, memoryLogs: memoryLogs, tools: tools, toolInvocations: toolInvocations, providers: providers, messages: messages, retriever: retriever, llm: llmClient, secrets: secrets}
	s.executor = engine.NewExecutor(runtimenode.DefaultNodes(runtimenode.Deps{Retriever: retriever, LLM: llmClient, Providers: s, Messages: s, Memories: memories, MemoryWriteLogs: memoryLogs, Tools: tools, ToolInvocations: toolInvocations}))
	s.validator = flow.NewValidator(s.executor)
	return s
}

type CreateAgentRequest struct {
	Name        string `json:"name" binding:"required"`
	Description string `json:"description"`
	AvatarURL   string `json:"avatar_url"`
}

type UpdateAgentRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	AvatarURL   string `json:"avatar_url"`
	Status      *int   `json:"status"`
}

type CreateFlowVersionRequest struct {
	DSLJSON     json.RawMessage `json:"dsl_json" binding:"required"`
	Description string          `json:"description"`
}

type RunAgentRequest struct {
	FlowVersionID  int64          `json:"flow_version_id"`
	ConversationID *int64         `json:"conversation_id"`
	Input          map[string]any `json:"input" binding:"required"`
}

func (s *Service) CreateAgent(ctx context.Context, ownerID int64, req CreateAgentRequest) (*agent.Agent, error) {
	name := strings.TrimSpace(req.Name)
	if ownerID <= 0 || name == "" {
		return nil, agenterrors.ErrInvalidInput
	}
	item := &agent.Agent{
		OwnerID:     ownerID,
		Name:        name,
		Description: strings.TrimSpace(req.Description),
		AvatarURL:   strings.TrimSpace(req.AvatarURL),
		Status:      agent.StatusActive,
	}
	if err := s.agents.Create(ctx, item); err != nil {
		return nil, err
	}
	return item, nil
}

func (s *Service) ListAgents(ctx context.Context, ownerID int64) ([]agent.Agent, error) {
	return s.agents.ListByOwner(ctx, ownerID)
}

func (s *Service) GetAgent(ctx context.Context, ownerID, id int64) (*agent.Agent, error) {
	item, err := s.agents.FindByID(ctx, ownerID, id)
	return item, mapNotFound(err)
}

func (s *Service) UpdateAgent(ctx context.Context, ownerID, id int64, req UpdateAgentRequest) (*agent.Agent, error) {
	item, err := s.GetAgent(ctx, ownerID, id)
	if err != nil {
		return nil, err
	}
	if name := strings.TrimSpace(req.Name); name != "" {
		item.Name = name
	}
	item.Description = strings.TrimSpace(req.Description)
	item.AvatarURL = strings.TrimSpace(req.AvatarURL)
	if req.Status != nil {
		item.Status = *req.Status
	}
	if err := s.agents.Update(ctx, item); err != nil {
		return nil, err
	}
	return item, nil
}

func (s *Service) DeleteAgent(ctx context.Context, ownerID, id int64) error {
	if _, err := s.GetAgent(ctx, ownerID, id); err != nil {
		return err
	}
	return s.agents.SoftDelete(ctx, ownerID, id)
}

func (s *Service) CreateFlowVersion(ctx context.Context, ownerID, agentID int64, req CreateFlowVersionRequest) (*agent.FlowVersion, error) {
	if _, err := s.GetAgent(ctx, ownerID, agentID); err != nil {
		return nil, err
	}
	dsl, err := flow.ParseDSL(req.DSLJSON)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid dsl_json", agenterrors.ErrInvalidInput)
	}
	if err := s.validator.Validate(dsl); err != nil {
		return nil, err
	}
	if existing, err := s.findEquivalentFlowVersion(ctx, ownerID, agentID, dsl); err != nil {
		return nil, err
	} else if existing != nil {
		return existing, nil
	}
	versionNo, err := s.versions.NextVersionNo(ctx, ownerID, agentID)
	if err != nil {
		return nil, err
	}
	item := &agent.FlowVersion{
		OwnerID:     ownerID,
		AgentID:     agentID,
		VersionNo:   versionNo,
		DSLJSON:     req.DSLJSON,
		Description: strings.TrimSpace(req.Description),
		IsDraft:     true,
	}
	if err := s.versions.Create(ctx, item); err != nil {
		return nil, err
	}
	return item, nil
}

func (s *Service) findEquivalentFlowVersion(ctx context.Context, ownerID, agentID int64, dsl *flow.DSL) (*agent.FlowVersion, error) {
	candidates := make([]*agent.FlowVersion, 0, 2)
	latest, err := s.versions.FindLatestByAgent(ctx, ownerID, agentID)
	if err != nil && err != gorm.ErrRecordNotFound {
		return nil, err
	}
	if latest != nil {
		candidates = append(candidates, latest)
	}
	current, err := s.versions.FindCurrentByAgent(ctx, ownerID, agentID)
	if err != nil && err != gorm.ErrRecordNotFound {
		return nil, err
	}
	if current != nil && (latest == nil || current.ID != latest.ID) {
		candidates = append(candidates, current)
	}
	for _, candidate := range candidates {
		candidateDSL, err := flow.ParseDSL(candidate.DSLJSON)
		if err != nil {
			return nil, fmt.Errorf("%w: invalid saved dsl_json", agenterrors.ErrInvalidInput)
		}
		equal, err := flow.EqualCanvasDSL(candidateDSL, dsl)
		if err != nil {
			return nil, fmt.Errorf("%w: invalid saved dsl_json", agenterrors.ErrInvalidInput)
		}
		if equal {
			return candidate, nil
		}
	}
	return nil, nil
}

func (s *Service) ListFlowVersions(ctx context.Context, ownerID, agentID int64) ([]agent.FlowVersion, error) {
	if _, err := s.GetAgent(ctx, ownerID, agentID); err != nil {
		return nil, err
	}
	return s.versions.ListByAgent(ctx, ownerID, agentID)
}

func (s *Service) GetFlowVersion(ctx context.Context, ownerID, id int64) (*agent.FlowVersion, error) {
	item, err := s.versions.FindByID(ctx, ownerID, id)
	return item, mapNotFound(err)
}

func (s *Service) ValidateFlowVersion(ctx context.Context, ownerID, id int64) error {
	item, err := s.GetFlowVersion(ctx, ownerID, id)
	if err != nil {
		return err
	}
	dsl, err := flow.ParseDSL(item.DSLJSON)
	if err != nil {
		return fmt.Errorf("%w: invalid dsl_json", agenterrors.ErrInvalidInput)
	}
	return s.validator.Validate(dsl)
}

func (s *Service) PublishFlowVersion(ctx context.Context, ownerID, id int64) (*agent.FlowVersion, error) {
	item, err := s.GetFlowVersion(ctx, ownerID, id)
	if err != nil {
		return nil, err
	}
	if err := s.ValidateFlowVersion(ctx, ownerID, id); err != nil {
		return nil, err
	}
	if err := s.versions.Publish(ctx, ownerID, item.AgentID, id); err != nil {
		return nil, err
	}
	return s.GetFlowVersion(ctx, ownerID, id)
}

func (s *Service) RunAgent(ctx context.Context, ownerID, agentID int64, req RunAgentRequest) (*agent.Run, engine.NodeOutput, error) {
	item, output, err := s.run(ctx, ownerID, agentID, req, nil)
	return item, output, err
}

func (s *Service) StreamRunAgent(
	ctx context.Context,
	ownerID, agentID int64,
	req RunAgentRequest,
	emit func(runtimeevent.Event) error,
) (*agent.Run, engine.NodeOutput, error) {
	return s.run(ctx, ownerID, agentID, req, emit)
}

func (s *Service) GetRun(ctx context.Context, ownerID, id int64) (*agent.Run, error) {
	item, err := s.runs.FindByID(ctx, ownerID, id)
	return item, mapNotFound(err)
}

func (s *Service) ListRunEvents(ctx context.Context, ownerID, runID int64) ([]agent.RunEvent, error) {
	if _, err := s.GetRun(ctx, ownerID, runID); err != nil {
		return nil, err
	}
	return s.events.ListByRun(ctx, ownerID, runID)
}

func (s *Service) ListNodeLogs(ctx context.Context, ownerID, runID int64) ([]agent.NodeLog, error) {
	if _, err := s.GetRun(ctx, ownerID, runID); err != nil {
		return nil, err
	}
	return s.nodeLogs.ListByRun(ctx, ownerID, runID)
}

func (s *Service) ListMemoryWriteLogs(ctx context.Context, ownerID, runID int64) ([]memory.WriteLog, error) {
	if _, err := s.GetRun(ctx, ownerID, runID); err != nil {
		return nil, err
	}
	return s.memoryLogs.ListByRun(ctx, ownerID, runID)
}

func (s *Service) ListToolInvocations(ctx context.Context, ownerID, runID int64) ([]tool.Invocation, error) {
	if _, err := s.GetRun(ctx, ownerID, runID); err != nil {
		return nil, err
	}
	return s.toolInvocations.ListByRun(ctx, ownerID, runID)
}

func (s *Service) CancelRun(ctx context.Context, ownerID, id int64) (*agent.Run, error) {
	item, err := s.GetRun(ctx, ownerID, id)
	if err != nil {
		return nil, err
	}
	if item.Status == agent.RunStatusRunning {
		now := time.Now().UTC()
		item.Status = agent.RunStatusCancelled
		item.FinishedAt = &now
		item.LatencyMS = int(now.Sub(item.StartedAt).Milliseconds())
		if err := s.runs.Update(ctx, item); err != nil {
			return nil, err
		}
	}
	return item, nil
}

func (s *Service) LoadChatProviderConfig(ctx context.Context, ownerID, providerID int64, model string) (*runtimenode.LoadedProvider, error) {
	provider, err := s.providers.FindByID(ctx, ownerID, providerID)
	if err != nil {
		return nil, mapNotFound(err)
	}
	if provider.Status != providerdomain.StatusActive {
		return nil, agenterrors.ErrInvalidInput
	}
	apiKey, err := s.secrets.Decrypt(provider.EncryptedAPIKey)
	if err != nil {
		return nil, err
	}
	selectedModel := strings.TrimSpace(model)
	if selectedModel == "" {
		selectedModel = provider.DefaultChatModel
	}
	if selectedModel == "" {
		return nil, fmt.Errorf("%w: model is required", agenterrors.ErrInvalidInput)
	}
	return &runtimenode.LoadedProvider{
		ProviderID: provider.ID,
		Model:      selectedModel,
		Config: llm.ChatProviderConfig{
			ProviderType: provider.ProviderType,
			BaseURL:      provider.BaseURL,
			APIKey:       apiKey,
		},
	}, nil
}

func (s *Service) WriteAssistantMessage(ctx context.Context, ownerID int64, conversationID *int64, runID int64, content string, tokenCount int) (int64, error) {
	if conversationID == nil || *conversationID <= 0 {
		return 0, nil
	}
	message := &conversation.Message{
		OwnerID:        ownerID,
		ConversationID: *conversationID,
		Role:           conversation.RoleAssistant,
		Content:        content,
		ContentType:    conversation.ContentTypeText,
		RunID:          &runID,
		TokenCount:     tokenCount,
	}
	if err := s.messages.Create(ctx, message); err != nil {
		return 0, err
	}
	return message.ID, nil
}

func (s *Service) run(ctx context.Context, ownerID, agentID int64, req RunAgentRequest, stream func(runtimeevent.Event) error) (*agent.Run, engine.NodeOutput, error) {
	if req.Input == nil {
		return nil, nil, agenterrors.ErrInvalidInput
	}
	if _, err := s.GetAgent(ctx, ownerID, agentID); err != nil {
		return nil, nil, err
	}
	version, err := s.loadRunVersion(ctx, ownerID, agentID, req.FlowVersionID)
	if err != nil {
		return nil, nil, err
	}
	dsl, err := flow.ParseDSL(version.DSLJSON)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: invalid dsl_json", agenterrors.ErrInvalidInput)
	}
	if err := s.validator.Validate(dsl); err != nil {
		return nil, nil, err
	}
	inputJSON, _ := json.Marshal(req.Input)
	now := time.Now().UTC()
	run := &agent.Run{
		OwnerID:        ownerID,
		AgentID:        agentID,
		FlowVersionID:  version.ID,
		ConversationID: req.ConversationID,
		Status:         agent.RunStatusRunning,
		InputJSON:      inputJSON,
		StartedAt:      now,
	}
	if err := s.runs.Create(ctx, run); err != nil {
		return nil, nil, err
	}
	rc := &engine.RunContext{
		OwnerID:        ownerID,
		AgentID:        agentID,
		FlowVersionID:  version.ID,
		RunID:          run.ID,
		ConversationID: req.ConversationID,
		Input:          req.Input,
		Events: &eventEmitter{
			repo:    s.events,
			ownerID: ownerID,
			runID:   run.ID,
			stream:  stream,
		},
	}
	output, execErr := s.executor.Execute(ctx, rc, dsl)
	finished := time.Now().UTC()
	run.FinishedAt = &finished
	run.LatencyMS = int(finished.Sub(run.StartedAt).Milliseconds())
	if output != nil {
		run.OutputJSON, _ = json.Marshal(output)
	}
	if execErr != nil {
		run.Status = agent.RunStatusFailed
		run.ErrorMessage = execErr.Error()
	} else {
		run.Status = agent.RunStatusSucceeded
	}
	if err := s.runs.Update(ctx, run); err != nil {
		return run, output, err
	}
	_ = s.writeNodeLogs(ctx, ownerID, run.ID, dsl, rc)
	return run, output, execErr
}

func (s *Service) loadRunVersion(ctx context.Context, ownerID, agentID, versionID int64) (*agent.FlowVersion, error) {
	if versionID > 0 {
		version, err := s.GetFlowVersion(ctx, ownerID, versionID)
		if err != nil {
			return nil, err
		}
		if version.AgentID != agentID {
			return nil, agenterrors.ErrNotFound
		}
		return version, nil
	}
	version, err := s.versions.FindCurrentByAgent(ctx, ownerID, agentID)
	return version, mapNotFound(err)
}

func (s *Service) writeNodeLogs(ctx context.Context, ownerID, runID int64, dsl *flow.DSL, rc *engine.RunContext) error {
	for _, spec := range dsl.Nodes {
		if rc.ExecutedNodes != nil && !rc.ExecutedNodes[spec.ID] {
			continue
		}
		inputJSON, _ := json.Marshal(rc.NodeInputs[spec.ID])
		outputJSON, _ := json.Marshal(rc.NodeOutputs[spec.ID])
		now := time.Now().UTC()
		log := &agent.NodeLog{
			OwnerID:    ownerID,
			RunID:      runID,
			NodeID:     spec.ID,
			NodeType:   spec.Type,
			Status:     agent.NodeLogStatusSucceeded,
			InputJSON:  inputJSON,
			OutputJSON: outputJSON,
			LatencyMS:  rc.NodeLatencies[spec.ID],
			StartedAt:  now,
			FinishedAt: &now,
			CreatedAt:  now,
		}
		if _, ok := rc.NodeOutputs[spec.ID]; !ok {
			log.Status = agent.NodeLogStatusFailed
			log.ErrorMessage = rc.NodeErrors[spec.ID]
		}
		if err := s.nodeLogs.Create(ctx, log); err != nil {
			return err
		}
	}
	return nil
}

type eventEmitter struct {
	repo    agent.RunEventRepository
	ownerID int64
	runID   int64
	stream  func(runtimeevent.Event) error
}

func (e *eventEmitter) Emit(ctx context.Context, ev runtimeevent.Event) error {
	if ev.RunID == 0 {
		ev.RunID = e.runID
	}
	if ev.CreatedAt.IsZero() {
		ev.CreatedAt = time.Now().UTC()
	}
	payload, _ := json.Marshal(ev.Payload)
	if err := e.repo.Create(ctx, &agent.RunEvent{
		OwnerID:     e.ownerID,
		RunID:       ev.RunID,
		EventType:   ev.Type,
		NodeID:      ev.NodeID,
		NodeType:    ev.NodeType,
		PayloadJSON: payload,
		CreatedAt:   ev.CreatedAt,
	}); err != nil {
		return err
	}
	if e.stream != nil {
		return e.stream(ev)
	}
	return nil
}

func mapNotFound(err error) error {
	if err == nil {
		return nil
	}
	if err == gorm.ErrRecordNotFound {
		return agenterrors.ErrNotFound
	}
	return err
}
