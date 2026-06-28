package agent_usecase

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
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
	runtimeagent "agentcanvas/internal/runtime/agent"
	"agentcanvas/internal/runtime/engine"
	runtimeevent "agentcanvas/internal/runtime/event"
	runtimenode "agentcanvas/internal/runtime/node"
	"agentcanvas/internal/runtime/toolruntime"

	agenterrors "agentcanvas/internal/pkg/errors"

	"gorm.io/gorm"
)

type Service struct {
	agents          agent.Repository
	profiles        agent.ProfileRepository
	versions        agent.FlowVersionRepository
	runs            agent.RunRepository
	events          agent.RunEventRepository
	nodeLogs        agent.NodeLogRepository
	runSteps        agent.RunStepRepository
	evals           agent.EvalDatasetRepository
	approvals       agent.ApprovalRepository
	teams           agent.TeamRepository
	memories        memory.Repository
	memoryLogs      memory.WriteLogRepository
	tools           tool.DefinitionRepository
	toolPacks       tool.PackRepository
	mcpServers      tool.MCPRepository
	toolRegistry    toolruntime.Registry
	toolInvocations tool.InvocationRepository
	providers       providerdomain.Repository
	messages        conversation.MessageRepository
	retriever       retrieval.Retriever
	llm             llm.ChatClient
	secrets         *cryptoinfra.SecretBox
	executor        *engine.Executor
	validator       *flow.Validator
	runCancels      *runCancelRegistry
}

func NewService(agents agent.Repository, profiles agent.ProfileRepository, versions agent.FlowVersionRepository, runs agent.RunRepository, events agent.RunEventRepository, nodeLogs agent.NodeLogRepository, runSteps agent.RunStepRepository, evals agent.EvalDatasetRepository, approvals agent.ApprovalRepository, teams agent.TeamRepository, memories memory.Repository, memoryLogs memory.WriteLogRepository, tools tool.DefinitionRepository, toolPacks tool.PackRepository, mcpServers tool.MCPRepository, toolInvocations tool.InvocationRepository, providers providerdomain.Repository, messages conversation.MessageRepository, retriever retrieval.Retriever, llmClient llm.ChatClient, secrets *cryptoinfra.SecretBox) *Service {
	s := &Service{agents: agents, profiles: profiles, versions: versions, runs: runs, events: events, nodeLogs: nodeLogs, runSteps: runSteps, evals: evals, approvals: approvals, teams: teams, memories: memories, memoryLogs: memoryLogs, tools: tools, toolPacks: toolPacks, mcpServers: mcpServers, toolInvocations: toolInvocations, providers: providers, messages: messages, retriever: retriever, llm: llmClient, secrets: secrets}
	s.executor = engine.NewExecutor(runtimenode.DefaultNodes(runtimenode.Deps{Retriever: retriever, LLM: llmClient, Providers: s, Messages: s, MessageHistory: messages, Memories: memories, MemoryWriteLogs: memoryLogs, Tools: tools, ToolPacks: toolPacks, MCPServers: mcpServers, ToolInvocations: toolInvocations, AgentCaller: s, Profiles: s, Teams: teams}))
	s.validator = flow.NewValidator(s.executor)
	s.runCancels = newRunCancelRegistry()
	return s
}

type runOptions struct {
	ParentRunID  *int64
	CallerNodeID string
	CallDepth    int
	CallChain    []int64
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

type UpdateAgentProfileRequest struct {
	Role                     *string          `json:"role"`
	Goal                     *string          `json:"goal"`
	Backstory                *string          `json:"backstory"`
	SystemPrompt             *string          `json:"system_prompt"`
	DefaultProviderID        *int64           `json:"default_provider_id"`
	DefaultModel             *string          `json:"default_model"`
	MaxIterations            *int             `json:"max_iterations"`
	MaxExecutionTimeMS       *int             `json:"max_execution_time_ms"`
	MemoryEnabled            *bool            `json:"memory_enabled"`
	PlanningEnabled          *bool            `json:"planning_enabled"`
	AllowDelegation          *bool            `json:"allow_delegation"`
	AllowCodeExecution       *bool            `json:"allow_code_execution"`
	DefaultToolPackIDs       *[]int64         `json:"default_tool_pack_ids"`
	DefaultToolIDs           *[]int64         `json:"default_tool_ids"`
	DefaultMCPServerIDs      *[]int64         `json:"default_mcp_server_ids"`
	DefaultKnowledgeIDs      *[]int64         `json:"default_knowledge_ids"`
	DefaultKnowledgeTopK     *int             `json:"default_knowledge_top_k"`
	DefaultKnowledgeMode     *string          `json:"default_knowledge_mode"`
	DefaultCallAgentIDs      *[]int64         `json:"default_call_agent_ids"`
	DefaultMaxAgentCallDepth *int             `json:"default_max_agent_call_depth"`
	OutputSchemaJSON         *json.RawMessage `json:"output_schema_json"`
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

type CreateEvalDatasetRequest struct {
	Name        string `json:"name" binding:"required"`
	Description string `json:"description"`
}

type CreateEvalCaseRequest struct {
	Name              string          `json:"name" binding:"required"`
	InputJSON         json.RawMessage `json:"input_json" binding:"required"`
	ExpectedJSON      json.RawMessage `json:"expected_json"`
	TagsJSON          json.RawMessage `json:"tags_json"`
	RequiredToolsJSON json.RawMessage `json:"required_tools_json"`
}

type RunEvalDatasetRequest struct {
	FlowVersionID int64 `json:"flow_version_id"`
}

type CreateTeamRequest struct {
	Name              string `json:"name" binding:"required"`
	SupervisorAgentID int64  `json:"supervisor_agent_id" binding:"required"`
	HandoffStrategy   string `json:"handoff_strategy"`
	MaxDepth          int    `json:"max_depth"`
}

type AddTeamMemberRequest struct {
	AgentID int64  `json:"agent_id" binding:"required"`
	Role    string `json:"role"`
}

type DecideApprovalRequest struct {
	Note string `json:"note"`
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
	if s.profiles != nil {
		_ = s.profiles.Create(ctx, defaultAgentProfile(ownerID, item.ID, item.Name, item.Description))
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

func (s *Service) GetAgentProfile(ctx context.Context, ownerID, agentID int64) (*agent.Profile, error) {
	item, err := s.GetAgent(ctx, ownerID, agentID)
	if err != nil {
		return nil, err
	}
	if s.profiles == nil {
		return defaultAgentProfile(ownerID, agentID, item.Name, item.Description), nil
	}
	profile, err := s.profiles.FindByAgent(ctx, ownerID, agentID)
	if err == nil {
		return profile, nil
	}
	if err != gorm.ErrRecordNotFound {
		return nil, err
	}
	profile = defaultAgentProfile(ownerID, agentID, item.Name, item.Description)
	if err := s.profiles.Create(ctx, profile); err != nil {
		return nil, err
	}
	return profile, nil
}

func (s *Service) UpdateAgentProfile(ctx context.Context, ownerID, agentID int64, req UpdateAgentProfileRequest) (*agent.Profile, error) {
	profile, err := s.GetAgentProfile(ctx, ownerID, agentID)
	if err != nil {
		return nil, err
	}
	if req.Role != nil {
		profile.Role = strings.TrimSpace(*req.Role)
	}
	if req.Goal != nil {
		profile.Goal = strings.TrimSpace(*req.Goal)
	}
	if req.Backstory != nil {
		profile.Backstory = strings.TrimSpace(*req.Backstory)
	}
	if req.SystemPrompt != nil {
		profile.SystemPrompt = strings.TrimSpace(*req.SystemPrompt)
	}
	if req.DefaultProviderID != nil {
		if *req.DefaultProviderID > 0 {
			profile.DefaultProviderID = req.DefaultProviderID
		} else {
			profile.DefaultProviderID = nil
		}
	}
	if req.DefaultModel != nil {
		profile.DefaultModel = strings.TrimSpace(*req.DefaultModel)
	}
	if req.MaxIterations != nil {
		if *req.MaxIterations <= 0 || *req.MaxIterations > 50 {
			return nil, fmt.Errorf("%w: max_iterations must be 1..50", agenterrors.ErrInvalidInput)
		}
		profile.MaxIterations = *req.MaxIterations
	}
	if req.MaxExecutionTimeMS != nil {
		if *req.MaxExecutionTimeMS <= 0 || *req.MaxExecutionTimeMS > 600000 {
			return nil, fmt.Errorf("%w: max_execution_time_ms must be 1..600000", agenterrors.ErrInvalidInput)
		}
		profile.MaxExecutionTimeMS = *req.MaxExecutionTimeMS
	}
	if req.MemoryEnabled != nil {
		profile.MemoryEnabled = *req.MemoryEnabled
	}
	if req.PlanningEnabled != nil {
		profile.PlanningEnabled = *req.PlanningEnabled
	}
	if req.AllowDelegation != nil {
		profile.AllowDelegation = *req.AllowDelegation
	}
	if req.AllowCodeExecution != nil {
		profile.AllowCodeExecution = *req.AllowCodeExecution
	}
	if req.DefaultToolPackIDs != nil {
		profile.DefaultToolPackIDs = mustMarshalJSON(normalizePositiveIDs(*req.DefaultToolPackIDs))
	}
	if req.DefaultToolIDs != nil {
		profile.DefaultToolIDs = mustMarshalJSON(normalizePositiveIDs(*req.DefaultToolIDs))
	}
	if req.DefaultMCPServerIDs != nil {
		profile.DefaultMCPServerIDs = mustMarshalJSON(normalizePositiveIDs(*req.DefaultMCPServerIDs))
	}
	if req.DefaultKnowledgeIDs != nil {
		profile.DefaultKnowledgeIDs = mustMarshalJSON(normalizePositiveIDs(*req.DefaultKnowledgeIDs))
	}
	if req.DefaultKnowledgeTopK != nil {
		if *req.DefaultKnowledgeTopK < 0 || *req.DefaultKnowledgeTopK > 20 {
			return nil, fmt.Errorf("%w: default_knowledge_top_k must be 0..20", agenterrors.ErrInvalidInput)
		}
		profile.DefaultKnowledgeTopK = *req.DefaultKnowledgeTopK
	}
	if req.DefaultKnowledgeMode != nil {
		mode := strings.TrimSpace(*req.DefaultKnowledgeMode)
		if mode != "" && mode != string(retrieval.ModeKeyword) && mode != string(retrieval.ModeVector) && mode != string(retrieval.ModeHybrid) {
			return nil, fmt.Errorf("%w: unsupported default_knowledge_mode", agenterrors.ErrInvalidInput)
		}
		profile.DefaultKnowledgeMode = mode
	}
	if req.DefaultCallAgentIDs != nil {
		profile.DefaultCallAgentIDs = mustMarshalJSON(normalizePositiveIDs(*req.DefaultCallAgentIDs))
	}
	if req.DefaultMaxAgentCallDepth != nil {
		if *req.DefaultMaxAgentCallDepth < 0 || *req.DefaultMaxAgentCallDepth > 5 {
			return nil, fmt.Errorf("%w: default_max_agent_call_depth must be 0..5", agenterrors.ErrInvalidInput)
		}
		profile.DefaultMaxAgentCallDepth = *req.DefaultMaxAgentCallDepth
	}
	if req.OutputSchemaJSON != nil {
		outputSchema, err := normalizeOptionalRawJSON(*req.OutputSchemaJSON, "output_schema_json")
		if err != nil {
			return nil, err
		}
		profile.OutputSchemaJSON = outputSchema
	}
	if strings.TrimSpace(profile.Role) == "" || strings.TrimSpace(profile.Goal) == "" {
		return nil, fmt.Errorf("%w: role and goal are required", agenterrors.ErrInvalidInput)
	}
	if s.profiles == nil {
		return profile, nil
	}
	if err := s.profiles.Update(ctx, profile); err != nil {
		return nil, err
	}
	return profile, nil
}

func (s *Service) CreateEvalDataset(ctx context.Context, ownerID, agentID int64, req CreateEvalDatasetRequest) (*agent.EvalDataset, error) {
	if s.evals == nil {
		return nil, fmt.Errorf("%w: eval repository is not configured", agenterrors.ErrInvalidInput)
	}
	if _, err := s.GetAgent(ctx, ownerID, agentID); err != nil {
		return nil, err
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, fmt.Errorf("%w: eval dataset name is required", agenterrors.ErrInvalidInput)
	}
	item := &agent.EvalDataset{
		OwnerID:     ownerID,
		AgentID:     agentID,
		Name:        name,
		Description: strings.TrimSpace(req.Description),
		Status:      agent.EvalDatasetStatusActive,
	}
	if err := s.evals.CreateDataset(ctx, item); err != nil {
		return nil, err
	}
	return item, nil
}

func (s *Service) ListEvalDatasets(ctx context.Context, ownerID, agentID int64) ([]agent.EvalDataset, error) {
	if s.evals == nil {
		return nil, fmt.Errorf("%w: eval repository is not configured", agenterrors.ErrInvalidInput)
	}
	if _, err := s.GetAgent(ctx, ownerID, agentID); err != nil {
		return nil, err
	}
	return s.evals.ListDatasetsByAgent(ctx, ownerID, agentID)
}

func (s *Service) CreateEvalCase(ctx context.Context, ownerID, datasetID int64, req CreateEvalCaseRequest) (*agent.EvalCase, error) {
	if s.evals == nil {
		return nil, fmt.Errorf("%w: eval repository is not configured", agenterrors.ErrInvalidInput)
	}
	dataset, err := s.evals.FindDatasetByID(ctx, ownerID, datasetID)
	if err != nil {
		return nil, mapNotFound(err)
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, fmt.Errorf("%w: eval case name is required", agenterrors.ErrInvalidInput)
	}
	inputJSON, err := normalizeRawJSONObject(req.InputJSON, "input_json")
	if err != nil {
		return nil, err
	}
	expectedJSON, err := normalizeOptionalRawJSON(req.ExpectedJSON, "expected_json")
	if err != nil {
		return nil, err
	}
	tagsJSON, err := normalizeOptionalRawJSON(req.TagsJSON, "tags_json")
	if err != nil {
		return nil, err
	}
	requiredToolsJSON, err := normalizeOptionalRawJSON(req.RequiredToolsJSON, "required_tools_json")
	if err != nil {
		return nil, err
	}
	item := &agent.EvalCase{
		OwnerID:           ownerID,
		DatasetID:         dataset.ID,
		Name:              name,
		InputJSON:         inputJSON,
		ExpectedJSON:      expectedJSON,
		TagsJSON:          tagsJSON,
		RequiredToolsJSON: requiredToolsJSON,
	}
	if err := s.evals.CreateCase(ctx, item); err != nil {
		return nil, err
	}
	return item, nil
}

func (s *Service) ListEvalCases(ctx context.Context, ownerID, datasetID int64) ([]agent.EvalCase, error) {
	if s.evals == nil {
		return nil, fmt.Errorf("%w: eval repository is not configured", agenterrors.ErrInvalidInput)
	}
	if _, err := s.evals.FindDatasetByID(ctx, ownerID, datasetID); err != nil {
		return nil, mapNotFound(err)
	}
	return s.evals.ListCasesByDataset(ctx, ownerID, datasetID)
}

func (s *Service) RunEvalDataset(ctx context.Context, ownerID, datasetID int64, req RunEvalDatasetRequest) (*agent.EvalRun, []agent.EvalResult, error) {
	if s.evals == nil {
		return nil, nil, fmt.Errorf("%w: eval repository is not configured", agenterrors.ErrInvalidInput)
	}
	dataset, err := s.evals.FindDatasetByID(ctx, ownerID, datasetID)
	if err != nil {
		return nil, nil, mapNotFound(err)
	}
	cases, err := s.evals.ListCasesByDataset(ctx, ownerID, datasetID)
	if err != nil {
		return nil, nil, err
	}
	started := time.Now().UTC()
	evalRun := &agent.EvalRun{
		OwnerID:       ownerID,
		AgentID:       dataset.AgentID,
		DatasetID:     dataset.ID,
		FlowVersionID: req.FlowVersionID,
		Status:        agent.EvalRunStatusRunning,
		TotalCases:    len(cases),
		StartedAt:     started,
	}
	if err := s.evals.CreateEvalRun(ctx, evalRun); err != nil {
		return nil, nil, err
	}
	results := make([]agent.EvalResult, 0, len(cases))
	for _, evalCase := range cases {
		caseStarted := time.Now().UTC()
		input := map[string]any{}
		if err := json.Unmarshal(evalCase.InputJSON, &input); err != nil {
			result := failedEvalResult(ownerID, evalRun.ID, evalCase.ID, nil, nil, "invalid input_json: "+err.Error(), int(time.Since(caseStarted).Milliseconds()))
			_ = s.evals.CreateEvalResult(ctx, result)
			results = append(results, *result)
			continue
		}
		agentRun, output, runErr := s.RunAgent(ctx, ownerID, dataset.AgentID, RunAgentRequest{FlowVersionID: req.FlowVersionID, Input: input})
		var agentRunID *int64
		if agentRun != nil {
			agentRunID = &agentRun.ID
		}
		outputJSON, _ := json.Marshal(output)
		if runErr != nil {
			result := failedEvalResult(ownerID, evalRun.ID, evalCase.ID, agentRunID, outputJSON, runErr.Error(), int(time.Since(caseStarted).Milliseconds()))
			if err := s.evals.CreateEvalResult(ctx, result); err != nil {
				return evalRun, results, err
			}
			results = append(results, *result)
			continue
		}
		score, reason := scoreEvalOutput(output, evalCase.ExpectedJSON, evalCase.RequiredToolsJSON)
		status := "failed"
		if score >= 1 {
			status = "passed"
		}
		metricsJSON, _ := json.Marshal(map[string]any{"score": score, "reason": reason})
		result := &agent.EvalResult{
			OwnerID:     ownerID,
			EvalRunID:   evalRun.ID,
			EvalCaseID:  evalCase.ID,
			AgentRunID:  agentRunID,
			Status:      status,
			Score:       score,
			Reason:      reason,
			OutputJSON:  outputJSON,
			MetricsJSON: metricsJSON,
			LatencyMS:   int(time.Since(caseStarted).Milliseconds()),
			CreatedAt:   time.Now().UTC(),
		}
		if err := s.evals.CreateEvalResult(ctx, result); err != nil {
			return evalRun, results, err
		}
		results = append(results, *result)
	}
	passed := 0
	for _, result := range results {
		if result.Status == "passed" {
			passed++
		}
	}
	finished := time.Now().UTC()
	evalRun.PassedCases = passed
	evalRun.FailedCases = len(results) - passed
	if len(results) > 0 {
		evalRun.SuccessRate = float64(passed) / float64(len(results))
	}
	evalRun.Status = agent.EvalRunStatusCompleted
	evalRun.FinishedAt = &finished
	evalRun.SummaryJSON, _ = json.Marshal(map[string]any{
		"total_cases":  evalRun.TotalCases,
		"passed_cases": evalRun.PassedCases,
		"failed_cases": evalRun.FailedCases,
		"success_rate": evalRun.SuccessRate,
	})
	if err := s.evals.UpdateEvalRun(ctx, evalRun); err != nil {
		return evalRun, results, err
	}
	return evalRun, results, nil
}

func (s *Service) ListEvalRuns(ctx context.Context, ownerID, datasetID int64) ([]agent.EvalRun, error) {
	if s.evals == nil {
		return nil, fmt.Errorf("%w: eval repository is not configured", agenterrors.ErrInvalidInput)
	}
	if _, err := s.evals.FindDatasetByID(ctx, ownerID, datasetID); err != nil {
		return nil, mapNotFound(err)
	}
	return s.evals.ListEvalRunsByDataset(ctx, ownerID, datasetID)
}

func (s *Service) ListEvalResults(ctx context.Context, ownerID, evalRunID int64) ([]agent.EvalResult, error) {
	if s.evals == nil {
		return nil, fmt.Errorf("%w: eval repository is not configured", agenterrors.ErrInvalidInput)
	}
	if _, err := s.evals.FindEvalRunByID(ctx, ownerID, evalRunID); err != nil {
		return nil, mapNotFound(err)
	}
	return s.evals.ListEvalResultsByRun(ctx, ownerID, evalRunID)
}

func (s *Service) CreateTeam(ctx context.Context, ownerID int64, req CreateTeamRequest) (*agent.Team, error) {
	if s.teams == nil {
		return nil, fmt.Errorf("%w: team repository is not configured", agenterrors.ErrInvalidInput)
	}
	name := strings.TrimSpace(req.Name)
	if ownerID <= 0 || name == "" || req.SupervisorAgentID <= 0 {
		return nil, agenterrors.ErrInvalidInput
	}
	if _, err := s.GetAgent(ctx, ownerID, req.SupervisorAgentID); err != nil {
		return nil, err
	}
	strategy := strings.TrimSpace(req.HandoffStrategy)
	if strategy == "" {
		strategy = "supervisor"
	}
	if strategy != "supervisor" && strategy != "handoff" {
		return nil, fmt.Errorf("%w: handoff_strategy must be supervisor or handoff", agenterrors.ErrInvalidInput)
	}
	maxDepth := req.MaxDepth
	if maxDepth <= 0 {
		maxDepth = 3
	}
	if maxDepth > 5 {
		return nil, fmt.Errorf("%w: max_depth must be <= 5", agenterrors.ErrInvalidInput)
	}
	item := &agent.Team{
		OwnerID:           ownerID,
		Name:              name,
		SupervisorAgentID: req.SupervisorAgentID,
		HandoffStrategy:   strategy,
		MaxDepth:          maxDepth,
	}
	if err := s.teams.CreateTeam(ctx, item); err != nil {
		return nil, err
	}
	return item, nil
}

func (s *Service) ListTeams(ctx context.Context, ownerID int64) ([]agent.Team, error) {
	if s.teams == nil {
		return nil, fmt.Errorf("%w: team repository is not configured", agenterrors.ErrInvalidInput)
	}
	return s.teams.ListTeams(ctx, ownerID)
}

func (s *Service) AddTeamMember(ctx context.Context, ownerID, teamID int64, req AddTeamMemberRequest) (*agent.TeamMember, error) {
	if s.teams == nil {
		return nil, fmt.Errorf("%w: team repository is not configured", agenterrors.ErrInvalidInput)
	}
	if _, err := s.teams.FindTeamByID(ctx, ownerID, teamID); err != nil {
		return nil, mapNotFound(err)
	}
	if _, err := s.GetAgent(ctx, ownerID, req.AgentID); err != nil {
		return nil, err
	}
	item := &agent.TeamMember{
		OwnerID: ownerID,
		TeamID:  teamID,
		AgentID: req.AgentID,
		Role:    strings.TrimSpace(req.Role),
	}
	if err := s.teams.AddMember(ctx, item); err != nil {
		return nil, err
	}
	return item, nil
}

func (s *Service) ListTeamMembers(ctx context.Context, ownerID, teamID int64) ([]agent.TeamMember, error) {
	if s.teams == nil {
		return nil, fmt.Errorf("%w: team repository is not configured", agenterrors.ErrInvalidInput)
	}
	if _, err := s.teams.FindTeamByID(ctx, ownerID, teamID); err != nil {
		return nil, mapNotFound(err)
	}
	return s.teams.ListMembers(ctx, ownerID, teamID)
}

func (s *Service) RemoveTeamMember(ctx context.Context, ownerID, teamID, agentID int64) error {
	if s.teams == nil {
		return fmt.Errorf("%w: team repository is not configured", agenterrors.ErrInvalidInput)
	}
	return s.teams.RemoveMember(ctx, ownerID, teamID, agentID)
}

func (s *Service) DeleteTeam(ctx context.Context, ownerID, teamID int64) error {
	if s.teams == nil {
		return fmt.Errorf("%w: team repository is not configured", agenterrors.ErrInvalidInput)
	}
	return s.teams.DeleteTeam(ctx, ownerID, teamID)
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

func (s *Service) RunAgent(
	ctx context.Context,
	ownerID, agentID int64,
	req RunAgentRequest,
) (*agent.Run, engine.NodeOutput, error) {
	item, output, err := s.run(ctx, ownerID, agentID, req, nil, runOptions{})
	return item, output, err
}

func (s *Service) StreamRunAgent(
	ctx context.Context,
	ownerID, agentID int64,
	req RunAgentRequest,
	emit func(runtimeevent.Event) error,
) (*agent.Run, engine.NodeOutput, error) {
	return s.run(ctx, ownerID, agentID, req, emit, runOptions{})
}

func (s *Service) CallAgent(ctx context.Context, req toolruntime.AgentCallRequest) (*toolruntime.AgentCallResult, error) {
	maxDepth := req.MaxDepth
	if maxDepth <= 0 {
		maxDepth = 3
	}
	if req.CallDepth >= maxDepth {
		s.recordBlockedAgentCall(ctx, req, "max_agent_call_depth_exceeded", maxDepth)
		return nil, fmt.Errorf("%w: max agent call depth exceeded", agenterrors.ErrForbidden)
	}
	if req.OwnerID <= 0 || req.AgentID <= 0 || req.Input == nil {
		return nil, agenterrors.ErrInvalidInput
	}
	callChain := normalizeCallChain(req.CallChain, req.CallerAgentID)
	if containsAgentID(callChain, req.AgentID) {
		s.recordBlockedAgentCall(ctx, req, "agent_call_cycle_detected", maxDepth)
		return nil, fmt.Errorf("%w: agent call cycle detected in call_chain", agenterrors.ErrForbidden)
	}
	var parentRunID *int64
	if req.ParentRunID > 0 {
		parentRunID = &req.ParentRunID
	}
	run, output, err := s.run(ctx, req.OwnerID, req.AgentID, RunAgentRequest{
		FlowVersionID: req.FlowVersionID,
		Input:         req.Input,
	}, nil, runOptions{
		ParentRunID:  parentRunID,
		CallerNodeID: req.CallerNodeID,
		CallDepth:    req.CallDepth + 1,
		CallChain:    append(callChain, req.AgentID),
	})
	if run == nil {
		return nil, err
	}
	result := &toolruntime.AgentCallResult{
		RunID:         run.ID,
		AgentID:       run.AgentID,
		FlowVersionID: run.FlowVersionID,
		Status:        run.Status,
		Output:        map[string]any(output),
		Error:         run.ErrorMessage,
		LatencyMS:     run.LatencyMS,
	}
	return result, err
}

func (s *Service) recordBlockedAgentCall(ctx context.Context, req toolruntime.AgentCallRequest, reason string, maxDepth int) {
	if s.events == nil || req.OwnerID <= 0 || req.ParentRunID <= 0 {
		return
	}
	payload := map[string]any{
		"blocked_reason":  reason,
		"caller_agent_id": req.CallerAgentID,
		"callee_agent_id": req.AgentID,
		"call_depth":      req.CallDepth,
		"max_depth":       maxDepth,
		"call_chain":      append([]int64(nil), req.CallChain...),
	}
	_ = s.events.Create(ctx, &agent.RunEvent{
		OwnerID:     req.OwnerID,
		RunID:       req.ParentRunID,
		EventType:   runtimeevent.AgentCallFailed,
		NodeID:      req.CallerNodeID,
		NodeType:    "call_agent",
		PayloadJSON: mustMarshalJSON(payload),
		CreatedAt:   time.Now().UTC(),
	})
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

func (s *Service) ListChildRuns(ctx context.Context, ownerID, runID int64) ([]agent.Run, error) {
	if _, err := s.GetRun(ctx, ownerID, runID); err != nil {
		return nil, err
	}
	return s.runs.ListByParent(ctx, ownerID, runID)
}

func (s *Service) ListNodeLogs(ctx context.Context, ownerID, runID int64) ([]agent.NodeLog, error) {
	if _, err := s.GetRun(ctx, ownerID, runID); err != nil {
		return nil, err
	}
	return s.nodeLogs.ListByRun(ctx, ownerID, runID)
}

func (s *Service) ListRunSteps(ctx context.Context, ownerID, runID int64) ([]agent.RunStep, error) {
	if _, err := s.GetRun(ctx, ownerID, runID); err != nil {
		return nil, err
	}
	if s.runSteps == nil {
		return []agent.RunStep{}, nil
	}
	return s.runSteps.ListByRun(ctx, ownerID, runID)
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
		_ = s.runCancels.Cancel(id)
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

func (s *Service) PauseRun(ctx context.Context, ownerID, id int64) (*agent.Run, error) {
	item, err := s.GetRun(ctx, ownerID, id)
	if err != nil {
		return nil, err
	}
	if item.Status != agent.RunStatusRunning {
		return nil, fmt.Errorf("%w: run is not running", agenterrors.ErrInvalidInput)
	}
	_ = s.runCancels.Pause(id)
	now := time.Now().UTC()
	item.Status = agent.RunStatusPaused
	item.ErrorMessage = "paused by user request"
	item.LatencyMS = int(now.Sub(item.StartedAt).Milliseconds())
	if err := s.runs.Update(ctx, item); err != nil {
		return nil, err
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

func (s *Service) run(ctx context.Context, ownerID, agentID int64, req RunAgentRequest, stream func(runtimeevent.Event) error, opts runOptions) (*agent.Run, engine.NodeOutput, error) {
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
	callChain := opts.CallChain
	if len(callChain) == 0 {
		callChain = []int64{agentID}
	}
	callChainJSON, _ := json.Marshal(callChain)
	now := time.Now().UTC()
	run := &agent.Run{
		OwnerID:        ownerID,
		AgentID:        agentID,
		FlowVersionID:  version.ID,
		ConversationID: req.ConversationID,
		ParentRunID:    opts.ParentRunID,
		CallerNodeID:   opts.CallerNodeID,
		CallDepth:      opts.CallDepth,
		CallChainJSON:  callChainJSON,
		Status:         agent.RunStatusRunning,
		InputJSON:      inputJSON,
		StartedAt:      now,
	}
	if err := s.runs.Create(ctx, run); err != nil {
		return nil, nil, err
	}
	execCtx, cancel := context.WithCancel(ctx)
	if s.runCancels == nil {
		s.runCancels = newRunCancelRegistry()
	}
	s.runCancels.Register(run.ID, cancel)
	defer func() {
		cancel()
		s.runCancels.Unregister(run.ID)
	}()
	rc := &engine.RunContext{
		OwnerID:        ownerID,
		AgentID:        agentID,
		FlowVersionID:  version.ID,
		RunID:          run.ID,
		ParentRunID:    opts.ParentRunID,
		CallDepth:      opts.CallDepth,
		CallChain:      append([]int64(nil), callChain...),
		ConversationID: req.ConversationID,
		AgentSteps:     s,
		Input:          req.Input,
		Events: &eventEmitter{
			repo:    s.events,
			ownerID: ownerID,
			runID:   run.ID,
			stream:  stream,
		},
	}
	output, execErr := s.executor.Execute(execCtx, rc, dsl)
	cancelReason := s.runCancels.Reason(run.ID)
	finished := time.Now().UTC()
	run.FinishedAt = &finished
	run.LatencyMS = int(finished.Sub(run.StartedAt).Milliseconds())
	if output != nil {
		run.OutputJSON, _ = json.Marshal(output)
	}
	if cancelReason == runCancelReasonPause {
		run.Status = agent.RunStatusPaused
		run.ErrorMessage = "paused by user request"
	} else if errors.Is(execErr, context.Canceled) || execCtx.Err() == context.Canceled {
		run.Status = agent.RunStatusCancelled
		run.ErrorMessage = context.Canceled.Error()
	} else if execErr != nil {
		run.Status = agent.RunStatusFailed
		run.ErrorMessage = execErr.Error()
	} else if status := runStatusFromOutput(output); status != "" {
		run.Status = status
	} else {
		run.Status = agent.RunStatusSucceeded
	}
	if run.Status == agent.RunStatusWaitingHuman {
		if err := s.persistRunCheckpointArtifacts(ctx, run, output, agent.RunStatusWaitingHuman); err != nil {
			return run, output, err
		}
	}
	if run.Status == agent.RunStatusPaused {
		if err := s.persistRunCheckpointArtifacts(ctx, run, output, agent.RunStatusPaused); err != nil {
			return run, output, err
		}
	}
	if err := s.runs.Update(ctx, run); err != nil {
		return run, output, err
	}
	_ = s.writeNodeLogs(ctx, ownerID, run.ID, dsl, rc)
	return run, output, execErr
}

func (s *Service) ListApprovalRequests(ctx context.Context, ownerID int64, status string) ([]agent.ApprovalRequest, error) {
	if s.approvals == nil {
		return nil, fmt.Errorf("%w: approval repository is not configured", agenterrors.ErrInvalidInput)
	}
	return s.approvals.ListApprovalRequests(ctx, ownerID, strings.TrimSpace(status))
}

func (s *Service) ApproveRequest(ctx context.Context, ownerID, approvalID int64, req DecideApprovalRequest) (*agent.ApprovalRequest, error) {
	return s.decideApproval(ctx, ownerID, approvalID, agent.ApprovalStatusApproved, req.Note)
}

func (s *Service) RejectRequest(ctx context.Context, ownerID, approvalID int64, req DecideApprovalRequest) (*agent.ApprovalRequest, error) {
	return s.decideApproval(ctx, ownerID, approvalID, agent.ApprovalStatusRejected, req.Note)
}

func (s *Service) ResumeRun(ctx context.Context, ownerID, runID int64) (*agent.Run, error) {
	if s.approvals == nil {
		return nil, fmt.Errorf("%w: approval repository is not configured", agenterrors.ErrInvalidInput)
	}
	item, err := s.GetRun(ctx, ownerID, runID)
	if err != nil {
		return nil, err
	}
	if item.Status != agent.RunStatusWaitingHuman && item.Status != agent.RunStatusPaused {
		return nil, fmt.Errorf("%w: run is not waiting for resume", agenterrors.ErrInvalidInput)
	}
	checkpoint, err := s.approvals.FindLatestCheckpointByRun(ctx, ownerID, runID)
	if err != nil {
		return nil, mapNotFound(err)
	}
	approval, err := s.approvals.FindPendingApprovalByRun(ctx, ownerID, runID)
	if err == nil && approval.Status == agent.ApprovalStatusPending {
		return nil, fmt.Errorf("%w: approval request is still pending", agenterrors.ErrInvalidInput)
	}
	if len(checkpoint.MessagesJSON) == 0 {
		return nil, fmt.Errorf("%w: checkpoint messages are missing", agenterrors.ErrInvalidInput)
	}
	item.Status = agent.RunStatusResuming
	if err := s.runs.Update(ctx, item); err != nil {
		return nil, err
	}
	return s.resumeRunFromCheckpoint(ctx, item, checkpoint)
}

func (s *Service) decideApproval(ctx context.Context, ownerID, approvalID int64, status, note string) (*agent.ApprovalRequest, error) {
	if s.approvals == nil {
		return nil, fmt.Errorf("%w: approval repository is not configured", agenterrors.ErrInvalidInput)
	}
	item, err := s.approvals.FindApprovalRequestByID(ctx, ownerID, approvalID)
	if err != nil {
		return nil, mapNotFound(err)
	}
	if item.Status != agent.ApprovalStatusPending {
		return nil, fmt.Errorf("%w: approval request is already decided", agenterrors.ErrInvalidInput)
	}
	now := time.Now().UTC()
	item.Status = status
	item.DecisionNote = strings.TrimSpace(note)
	item.DecidedAt = &now
	if err := s.approvals.UpdateApprovalRequest(ctx, item); err != nil {
		return nil, err
	}
	return item, nil
}

func (s *Service) persistRunCheckpointArtifacts(ctx context.Context, run *agent.Run, output engine.NodeOutput, checkpointStatus string) error {
	if s.approvals == nil || run == nil || output == nil {
		return nil
	}
	approval, ok := output["approval"].(*runtimeagent.Approval)
	if !ok {
		if raw, exists := output["approval"]; exists {
			bytes, _ := json.Marshal(raw)
			var decoded runtimeagent.Approval
			if err := json.Unmarshal(bytes, &decoded); err == nil {
				approval = &decoded
			}
		}
	}
	checkpoint, ok := output["checkpoint"].(*runtimeagent.Checkpoint)
	if !ok {
		if raw, exists := output["checkpoint"]; exists {
			bytes, _ := json.Marshal(raw)
			var decoded runtimeagent.Checkpoint
			if err := json.Unmarshal(bytes, &decoded); err == nil {
				checkpoint = &decoded
			}
		}
	}
	if approval != nil {
		requestJSON, _ := json.Marshal(approval)
		item := &agent.ApprovalRequest{
			OwnerID:     run.OwnerID,
			AgentID:     run.AgentID,
			RunID:       run.ID,
			NodeID:      checkpointNodeID(checkpoint),
			ToolCallID:  approval.ToolCallID,
			ToolName:    approval.ToolName,
			RiskLevel:   approval.RiskLevel,
			Reason:      approval.Reason,
			RequestJSON: requestJSON,
			Status:      agent.ApprovalStatusPending,
		}
		if item.NodeID == "" {
			item.NodeID = "agent"
		}
		if err := s.approvals.CreateApprovalRequest(ctx, item); err != nil {
			return err
		}
	}
	if checkpoint != nil {
		messagesJSON, _ := json.Marshal(checkpoint.Messages)
		stepsJSON, _ := json.Marshal(output["steps"])
		pendingJSON, _ := json.Marshal(checkpoint.PendingToolCall)
		contextJSON, _ := json.Marshal(checkpoint.Context)
		item := &agent.AgentCheckpoint{
			OwnerID:             run.OwnerID,
			AgentID:             run.AgentID,
			RunID:               run.ID,
			NodeID:              checkpointNodeID(checkpoint),
			Status:              checkpointStatus,
			MessagesJSON:        messagesJSON,
			MessagesSummary:     checkpoint.MessagesSummary,
			StepsJSON:           stepsJSON,
			PendingToolCallJSON: pendingJSON,
			ContextJSON:         contextJSON,
			ToolRegistryHash:    stableJSONHash(checkpoint.ToolNames),
			ToolPolicyHash:      stableJSONHash(checkpoint.ToolPolicy),
		}
		if item.NodeID == "" {
			item.NodeID = "agent"
		}
		if err := s.approvals.CreateCheckpoint(ctx, item); err != nil {
			return err
		}
	}
	return nil
}

func checkpointNodeID(checkpoint *runtimeagent.Checkpoint) string {
	if checkpoint == nil || checkpoint.Metadata == nil {
		return ""
	}
	nodeID, _ := checkpoint.Metadata["node_id"].(string)
	return nodeID
}

func (s *Service) resumeRunFromCheckpoint(ctx context.Context, run *agent.Run, stored *agent.AgentCheckpoint) (*agent.Run, error) {
	if run == nil || stored == nil {
		return nil, agenterrors.ErrInvalidInput
	}
	decision, err := s.latestApprovalDecision(ctx, run.OwnerID, run.ID)
	if err != nil {
		return nil, err
	}
	checkpoint, err := decodeRuntimeCheckpoint(stored, decision)
	if err != nil {
		return nil, err
	}
	if checkpoint.PendingToolCall != nil && decision == nil {
		return nil, fmt.Errorf("%w: approval decision is missing", agenterrors.ErrInvalidInput)
	}
	version, err := s.GetFlowVersion(ctx, run.OwnerID, run.FlowVersionID)
	if err != nil {
		return nil, err
	}
	dsl, err := flow.ParseDSL(version.DSLJSON)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid dsl_json", agenterrors.ErrInvalidInput)
	}
	nodeSpec, err := findCheckpointAgentNode(dsl, stored.NodeID)
	if err != nil {
		return nil, err
	}
	input := map[string]any{}
	if len(run.InputJSON) > 0 {
		_ = json.Unmarshal(run.InputJSON, &input)
	}
	callChain := []int64{run.AgentID}
	if len(run.CallChainJSON) > 0 {
		_ = json.Unmarshal(run.CallChainJSON, &callChain)
	}
	node := s.resumeAgentLoopNode()
	rc := &engine.RunContext{
		OwnerID:         run.OwnerID,
		AgentID:         run.AgentID,
		FlowVersionID:   run.FlowVersionID,
		RunID:           run.ID,
		ParentRunID:     run.ParentRunID,
		CallDepth:       run.CallDepth,
		CallChain:       append([]int64(nil), callChain...),
		ConversationID:  run.ConversationID,
		Input:           input,
		NodeInputs:      map[string]engine.NodeInput{},
		NodeOutputs:     map[string]engine.NodeOutput{},
		NodeErrors:      map[string]string{},
		NodeLatencies:   map[string]int{},
		ExecutedNodes:   map[string]bool{},
		CurrentNodeID:   nodeSpec.ID,
		CurrentNodeType: nodeSpec.Type,
		AgentSteps:      s,
		Events: &eventEmitter{
			repo:    s.events,
			ownerID: run.OwnerID,
			runID:   run.ID,
		},
	}
	started := time.Now().UTC()
	execCtx, cancel := context.WithCancel(ctx)
	s.runCancels.Register(run.ID, cancel)
	defer func() {
		cancel()
		s.runCancels.Unregister(run.ID)
	}()
	output, execErr := node.Resume(execCtx, rc, engine.NodeInput(input), nodeSpec.Config, runtimenode.AgentResumeOptions{
		Checkpoint:    checkpoint,
		Approved:      decision != nil && decision.Status == agent.ApprovalStatusApproved,
		RejectionNote: approvalDecisionNote(decision),
	})
	finished := time.Now().UTC()
	run.FinishedAt = &finished
	run.LatencyMS += int(finished.Sub(started).Milliseconds())
	if output != nil {
		run.OutputJSON, _ = json.Marshal(output)
		rc.NodeOutputs[nodeSpec.ID] = output
		rc.ExecutedNodes[nodeSpec.ID] = true
		rc.NodeLatencies[nodeSpec.ID] = int(finished.Sub(started).Milliseconds())
	}
	if errors.Is(execErr, context.Canceled) || execCtx.Err() == context.Canceled {
		run.Status = agent.RunStatusCancelled
		run.ErrorMessage = context.Canceled.Error()
	} else if execErr != nil {
		run.Status = agent.RunStatusFailed
		run.ErrorMessage = execErr.Error()
	} else if status := runStatusFromOutput(output); status != "" {
		run.Status = status
		run.ErrorMessage = ""
	} else {
		run.Status = agent.RunStatusSucceeded
		run.ErrorMessage = ""
	}
	if run.Status == agent.RunStatusWaitingHuman {
		if err := s.persistRunCheckpointArtifacts(ctx, run, output, agent.RunStatusWaitingHuman); err != nil {
			return run, err
		}
	}
	if err := s.runs.Update(ctx, run); err != nil {
		return run, err
	}
	_ = s.writeNodeLogs(ctx, run.OwnerID, run.ID, dsl, rc)
	return run, execErr
}

func (s *Service) resumeAgentLoopNode() runtimenode.AgentLoopNode {
	toolCalling, _ := s.llm.(llm.ToolCallingClient)
	registry := s.toolRegistry
	if registry == nil && s.tools != nil {
		registry = toolruntime.BasicRegistry{Tools: s.tools, Invocations: s.toolInvocations}
	}
	return runtimenode.AgentLoopNode{AgentNode: runtimenode.AgentNode{
		LLM:            toolCalling,
		Providers:      s,
		Tools:          registry,
		ToolPacks:      s.toolPacks,
		Retriever:      s.retriever,
		Memories:       s.memories,
		MemoryLogs:     s.memoryLogs,
		AgentCaller:    s,
		Profiles:       s,
		MessageHistory: s.messages,
	}}
}

func decodeRuntimeCheckpoint(stored *agent.AgentCheckpoint, decision *agent.ApprovalRequest) (*runtimeagent.Checkpoint, error) {
	var messages []llm.ChatMessage
	if err := json.Unmarshal(stored.MessagesJSON, &messages); err != nil {
		return nil, fmt.Errorf("%w: invalid checkpoint messages", agenterrors.ErrInvalidInput)
	}
	var pending *llm.ToolCall
	if len(stored.PendingToolCallJSON) > 0 && string(stored.PendingToolCallJSON) != "null" {
		var call llm.ToolCall
		if err := json.Unmarshal(stored.PendingToolCallJSON, &call); err != nil {
			return nil, fmt.Errorf("%w: invalid checkpoint pending tool call", agenterrors.ErrInvalidInput)
		}
		pending = &call
	}
	var contextTrace runtimeagent.ContextTrace
	if len(stored.ContextJSON) > 0 {
		_ = json.Unmarshal(stored.ContextJSON, &contextTrace)
	}
	return &runtimeagent.Checkpoint{
		Messages:        messages,
		MessagesSummary: stored.MessagesSummary,
		PendingToolCall: pending,
		Context:         contextTrace,
		Metadata: map[string]any{
			"node_id":          stored.NodeID,
			"approval_status":  approvalDecisionStatus(decision),
			"approval_note":    approvalDecisionNote(decision),
			"checkpoint_id":    stored.ID,
			"checkpoint_state": stored.Status,
		},
	}, nil
}

func findCheckpointAgentNode(dsl *flow.DSL, nodeID string) (*flow.Node, error) {
	if dsl == nil {
		return nil, fmt.Errorf("%w: flow version is missing", agenterrors.ErrInvalidInput)
	}
	var fallback *flow.Node
	for i := range dsl.Nodes {
		node := &dsl.Nodes[i]
		if node.Type != "agent_loop" {
			continue
		}
		if fallback == nil {
			fallback = node
		}
		if node.ID == nodeID {
			return node, nil
		}
	}
	if nodeID == "" && fallback != nil {
		return fallback, nil
	}
	return nil, fmt.Errorf("%w: checkpoint node %s is not an agent_loop node", agenterrors.ErrInvalidInput, nodeID)
}

func (s *Service) latestApprovalDecision(ctx context.Context, ownerID, runID int64) (*agent.ApprovalRequest, error) {
	items, err := s.approvals.ListApprovalRequests(ctx, ownerID, "")
	if err != nil {
		return nil, err
	}
	for _, item := range items {
		if item.RunID != runID {
			continue
		}
		if item.Status == agent.ApprovalStatusPending {
			return nil, fmt.Errorf("%w: approval request is still pending", agenterrors.ErrInvalidInput)
		}
		clone := item
		return &clone, nil
	}
	return nil, nil
}

func approvalDecisionStatus(decision *agent.ApprovalRequest) string {
	if decision == nil {
		return ""
	}
	return decision.Status
}

func approvalDecisionNote(decision *agent.ApprovalRequest) string {
	if decision == nil {
		return ""
	}
	return decision.DecisionNote
}

func (s *Service) RecordAgentStep(ctx context.Context, rc *engine.RunContext, step engine.AgentStepRecord) error {
	if s.runSteps == nil || rc == nil {
		return nil
	}
	nodeID := step.NodeID
	if nodeID == "" {
		nodeID = rc.CurrentNodeID
	}
	return s.runSteps.Create(ctx, &agent.RunStep{
		OwnerID:       rc.OwnerID,
		RunID:         rc.RunID,
		NodeID:        nodeID,
		StepIndex:     step.StepIndex,
		StepType:      step.StepType,
		Role:          step.Role,
		Content:       step.Content,
		ToolCallID:    step.ToolCallID,
		ToolName:      step.ToolName,
		ArgumentsJSON: step.ArgumentsJSON,
		OutputJSON:    step.OutputJSON,
		ErrorMessage:  step.ErrorMessage,
		TokenCount:    step.TokenCount,
		LatencyMS:     step.LatencyMS,
		CreatedAt:     time.Now().UTC(),
	})
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
	if s.nodeLogs == nil {
		return nil
	}
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
	if e.repo != nil {
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

func normalizeRawJSONObject(raw json.RawMessage, field string) (json.RawMessage, error) {
	normalized, err := normalizeOptionalRawJSON(raw, field)
	if err != nil {
		return nil, err
	}
	if len(normalized) == 0 {
		return nil, fmt.Errorf("%w: %s is required", agenterrors.ErrInvalidInput, field)
	}
	var object map[string]any
	if err := json.Unmarshal(normalized, &object); err != nil {
		return nil, fmt.Errorf("%w: %s must be a JSON object", agenterrors.ErrInvalidInput, field)
	}
	return normalized, nil
}

func normalizeOptionalRawJSON(raw json.RawMessage, field string) (json.RawMessage, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var value any
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("%w: %s must be valid JSON", agenterrors.ErrInvalidInput, field)
	}
	normalized, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return normalized, nil
}

func failedEvalResult(ownerID, evalRunID, evalCaseID int64, agentRunID *int64, outputJSON json.RawMessage, message string, latencyMS int) *agent.EvalResult {
	metricsJSON, _ := json.Marshal(map[string]any{"score": 0, "reason": message})
	return &agent.EvalResult{
		OwnerID:      ownerID,
		EvalRunID:    evalRunID,
		EvalCaseID:   evalCaseID,
		AgentRunID:   agentRunID,
		Status:       "failed",
		Score:        0,
		Reason:       message,
		OutputJSON:   outputJSON,
		MetricsJSON:  metricsJSON,
		ErrorMessage: message,
		LatencyMS:    latencyMS,
		CreatedAt:    time.Now().UTC(),
	}
}

func scoreEvalOutput(output engine.NodeOutput, expectedJSON, requiredToolsJSON json.RawMessage) (float64, string) {
	if output == nil {
		output = engine.NodeOutput{}
	}
	if len(requiredToolsJSON) > 0 {
		var required []string
		if err := json.Unmarshal(requiredToolsJSON, &required); err == nil && len(required) > 0 {
			called := extractToolNames(output)
			for _, name := range required {
				if strings.TrimSpace(name) != "" && !called[strings.TrimSpace(name)] {
					return 0, "required tool was not called: " + strings.TrimSpace(name)
				}
			}
		}
	}
	if len(expectedJSON) == 0 {
		return 1, "no expected_json configured"
	}
	var expected map[string]any
	if err := json.Unmarshal(expectedJSON, &expected); err != nil {
		return 0, "expected_json is invalid: " + err.Error()
	}
	if status, ok := expected["status"].(string); ok && status != "" {
		actualStatus, _ := output["status"].(string)
		if actualStatus != status {
			return 0, fmt.Sprintf("status mismatch: expected %s got %s", status, actualStatus)
		}
	}
	if contains, exists := expected["contains"]; exists {
		text := strings.ToLower(outputText(output))
		for _, expectedText := range expectedContainsList(contains) {
			if !strings.Contains(text, strings.ToLower(expectedText)) {
				return 0, "output does not contain expected text: " + expectedText
			}
		}
	}
	if equals, ok := expected["equals"]; ok {
		actual := output["final_answer"]
		if actual == nil {
			actual = output["answer"]
		}
		expectedBytes, _ := json.Marshal(equals)
		actualBytes, _ := json.Marshal(actual)
		if string(expectedBytes) != string(actualBytes) {
			return 0, "final answer does not equal expected value"
		}
	}
	return 1, "matched expected_json"
}

func expectedContainsList(value any) []string {
	switch typed := value.(type) {
	case string:
		if strings.TrimSpace(typed) == "" {
			return nil
		}
		return []string{strings.TrimSpace(typed)}
	case []any:
		items := make([]string, 0, len(typed))
		for _, item := range typed {
			text, ok := item.(string)
			if ok && strings.TrimSpace(text) != "" {
				items = append(items, strings.TrimSpace(text))
			}
		}
		return items
	default:
		return nil
	}
}

func outputText(output engine.NodeOutput) string {
	for _, key := range []string{"final_answer", "answer", "text", "content"} {
		if value, ok := output[key].(string); ok && strings.TrimSpace(value) != "" {
			return value
		}
	}
	bytes, _ := json.Marshal(output)
	return string(bytes)
}

func stableJSONHash(value any) string {
	bytes, _ := json.Marshal(value)
	sum := sha256.Sum256(bytes)
	return hex.EncodeToString(sum[:])
}

func extractToolNames(output engine.NodeOutput) map[string]bool {
	called := map[string]bool{}
	if rawSteps, ok := output["steps"].([]any); ok {
		for _, raw := range rawSteps {
			if step, ok := raw.(map[string]any); ok {
				if name, _ := step["tool_name"].(string); strings.TrimSpace(name) != "" {
					called[strings.TrimSpace(name)] = true
				}
			}
		}
		return called
	}
	stepsJSON, err := json.Marshal(output["steps"])
	if err == nil && string(stepsJSON) != "null" {
		var steps []runtimeagentStepLike
		if err := json.Unmarshal(stepsJSON, &steps); err == nil {
			for _, step := range steps {
				if strings.TrimSpace(step.ToolName) != "" {
					called[strings.TrimSpace(step.ToolName)] = true
				}
			}
		}
	}
	return called
}

type runtimeagentStepLike struct {
	ToolName string `json:"tool_name"`
}

func normalizeCallChain(chain []int64, callerAgentID int64) []int64 {
	normalized := make([]int64, 0, len(chain)+1)
	for _, id := range chain {
		if id > 0 && !containsAgentID(normalized, id) {
			normalized = append(normalized, id)
		}
	}
	if callerAgentID > 0 && !containsAgentID(normalized, callerAgentID) {
		normalized = append(normalized, callerAgentID)
	}
	return normalized
}

func containsAgentID(chain []int64, agentID int64) bool {
	if agentID <= 0 {
		return false
	}
	for _, id := range chain {
		if id == agentID {
			return true
		}
	}
	return false
}

func normalizePositiveIDs(ids []int64) []int64 {
	normalized := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id > 0 && !containsAgentID(normalized, id) {
			normalized = append(normalized, id)
		}
	}
	return normalized
}

func mustMarshalJSON(value any) json.RawMessage {
	data, _ := json.Marshal(value)
	return data
}

func runStatusFromOutput(output engine.NodeOutput) string {
	if output == nil {
		return ""
	}
	stopReason, _ := output["stop_reason"].(string)
	switch stopReason {
	case "waiting_human":
		return agent.RunStatusWaitingHuman
	case "timeout":
		return agent.RunStatusTimeout
	default:
		return ""
	}
}

func defaultAgentProfile(ownerID, agentID int64, name, description string) *agent.Profile {
	role := strings.TrimSpace(name)
	if role == "" {
		role = "Assistant"
	}
	goal := strings.TrimSpace(description)
	if goal == "" {
		goal = "Complete user tasks through the configured workflow and tools."
	}
	return &agent.Profile{
		OwnerID:                  ownerID,
		AgentID:                  agentID,
		Role:                     role,
		Goal:                     goal,
		SystemPrompt:             "You are a helpful, careful AgentCanvas agent. Use available tools when needed and explain final results clearly.",
		MaxIterations:            10,
		MaxExecutionTimeMS:       120000,
		DefaultToolPackIDs:       json.RawMessage("[]"),
		DefaultToolIDs:           json.RawMessage("[]"),
		DefaultMCPServerIDs:      json.RawMessage("[]"),
		DefaultKnowledgeIDs:      json.RawMessage("[]"),
		DefaultCallAgentIDs:      json.RawMessage("[]"),
		DefaultKnowledgeTopK:     5,
		DefaultKnowledgeMode:     string(retrieval.ModeHybrid),
		DefaultMaxAgentCallDepth: 3,
		OutputSchemaJSON:         json.RawMessage("{}"),
	}
}
