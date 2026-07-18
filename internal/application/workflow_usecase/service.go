package workflow_usecase

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	memoryusecase "agentcanvas/internal/application/memory_usecase"
	"agentcanvas/internal/domain/audit"
	"agentcanvas/internal/domain/contextresource"
	"agentcanvas/internal/domain/conversation"
	"agentcanvas/internal/domain/flow"
	"agentcanvas/internal/domain/memory"
	providerdomain "agentcanvas/internal/domain/provider"
	"agentcanvas/internal/domain/reflection"
	"agentcanvas/internal/domain/retrieval"
	"agentcanvas/internal/domain/skill"
	"agentcanvas/internal/domain/tool"
	"agentcanvas/internal/domain/workflow"
	"agentcanvas/internal/domain/workspace"
	cryptoinfra "agentcanvas/internal/infrastructure/crypto"
	"agentcanvas/internal/infrastructure/llm"
	queueinfra "agentcanvas/internal/infrastructure/queue"
	"agentcanvas/internal/infrastructure/vectorstore"
	runtimeagent "agentcanvas/internal/runtime/agent"
	"agentcanvas/internal/runtime/engine"
	"agentcanvas/internal/runtime/evalharness"
	runtimeevent "agentcanvas/internal/runtime/event"
	"agentcanvas/internal/runtime/harness/rules"
	runtimenode "agentcanvas/internal/runtime/node"
	"agentcanvas/internal/runtime/sandbox"
	"agentcanvas/internal/runtime/toolruntime"

	agenterrors "agentcanvas/internal/pkg/errors"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

type Service struct {
	workflows         workflow.Repository
	profiles          workflow.ProfileRepository
	versions          workflow.WorkflowVersionRepository
	runs              workflow.RunRepository
	events            workflow.RunEventRepository
	nodeLogs          workflow.NodeLogRepository
	runSteps          workflow.RunStepRepository
	evals             workflow.EvalDatasetRepository
	approvals         workflow.ApprovalRepository
	audits            audit.Repository
	teams             workflow.TeamRepository
	memories          memory.Repository
	memoryLogs        memory.WriteLogRepository
	memoryRetriever   memory.SemanticRetriever
	workingMemory     memory.WorkingMemoryRepository
	extractions       *memoryusecase.ExtractionService
	tools             tool.DefinitionRepository
	toolPacks         tool.PackRepository
	skills            skill.Repository
	mcpServers        tool.MCPRepository
	toolRegistry      toolruntime.Registry
	toolInvocations   tool.InvocationRepository
	workspaces        workspace.Repository
	providers         providerdomain.Repository
	conversations     conversation.Repository
	messages          conversation.MessageRepository
	compactions       conversation.CompactionRepository
	retriever         retrieval.Retriever
	llm               llm.ChatClient
	embedder          llm.EmbeddingClient
	archivalVecStore  vectorstore.Store
	contextIndex      contextresource.Index
	reflections       reflection.Advisor
	secrets           *cryptoinfra.SecretBox
	executor          *engine.Executor
	agentRuntime      runtimenode.AgentRuntime
	agentRunResumer   workflow.IndependentRunResumer
	agentRunCanceller workflow.IndependentRunCanceller
	validator         *flow.Validator
	runCancels        *runCancelRegistry
	dreamQueue        queueinfra.JobQueue
	ruleSets          workflow.RuleSetRepository
	ruleCompileQueue  queueinfra.JobQueue
	redisClient       *redis.Client
	dreamCfg          memoryusecase.DreamConfig
}

// RunTrace is a complete tracing snapshot of a workflow execution (Run).
type RunTrace struct {
	Run             *workflow.Run       `json:"run"`
	Events          []workflow.RunEvent `json:"events"`
	NodeLogs        []workflow.NodeLog  `json:"node_logs"`
	Steps           []workflow.RunStep  `json:"steps"`
	ChildRuns       []workflow.Run      `json:"child_runs"`
	MemoryWriteLogs []memory.WriteLog   `json:"memory_write_logs"`
	ToolInvocations []tool.Invocation   `json:"tool_invocations"`
	ReplaySummary   map[string]any      `json:"replay_summary"`
}

type EvalTrendPoint struct {
	EvalRunID     int64          `json:"eval_run_id"`
	FlowVersionID int64          `json:"flow_version_id"`
	Status        string         `json:"status"`
	TotalCases    int            `json:"total_cases"`
	PassedCases   int            `json:"passed_cases"`
	FailedCases   int            `json:"failed_cases"`
	SuccessRate   float64        `json:"success_rate"`
	Metrics       map[string]any `json:"metrics"`
	StartedAt     time.Time      `json:"started_at"`
	FinishedAt    *time.Time     `json:"finished_at"`
}

// EvalTrend represents a historical trend overview of multiple evaluations
// of a workflow within a given dataset, including all data points and the
// calculated optimal/latest/change values.
type EvalTrend struct {
	DatasetID    int64            `json:"dataset_id"`
	WorkflowID   int64            `json:"workflow_id"`
	Points       []EvalTrendPoint `json:"points"` // [{eval_run_id:98,...}, {eval_run_id:100,...}, {eval_run_id:102,...}]
	Latest       *EvalTrendPoint  `json:"latest,omitempty"`
	Best         *EvalTrendPoint  `json:"best,omitempty"`
	Delta        map[string]any   `json:"delta"`         // like {"success_rate": 0.12, "passed_cases": 4, "avg_latency_ms": -320.5}
	TrendSummary map[string]any   `json:"trend_summary"` // {"run_count": 3, "latest_eval_run_id": 102, "best_eval_run_id": 100, "success_rate_delta": 0.08}
}

func NewService(
	workflows workflow.Repository,
	profiles workflow.ProfileRepository,
	versions workflow.WorkflowVersionRepository,
	runs workflow.RunRepository,
	events workflow.RunEventRepository,
	nodeLogs workflow.NodeLogRepository,
	runSteps workflow.RunStepRepository,
	evals workflow.EvalDatasetRepository,
	approvals workflow.ApprovalRepository,
	audits audit.Repository,
	teams workflow.TeamRepository,
	memories memory.Repository,
	memoryLogs memory.WriteLogRepository,
	memoryRetriever memory.SemanticRetriever,
	workingMemory memory.WorkingMemoryRepository,
	extractionJobs memory.ExtractionJobRepository,
	mergeLogs memory.MergeLogRepository,
	tools tool.DefinitionRepository,
	toolPacks tool.PackRepository,
	skills skill.Repository,
	mcpServers tool.MCPRepository,
	toolInvocations tool.InvocationRepository,
	providers providerdomain.Repository,
	conversations conversation.Repository,
	messages conversation.MessageRepository,
	retriever retrieval.Retriever,
	llmClient llm.ChatClient,
	embedder llm.EmbeddingClient,
	archivalVecStore vectorstore.Store,
	contextIndex contextresource.Index,
	compactions conversation.CompactionRepository,
	secrets *cryptoinfra.SecretBox,
	reflectionAdvisor reflection.Advisor,
	workspaceRepositories ...workspace.Repository,
) (*Service, error) {
	toolCalling, ok := llmClient.(llm.ToolCallingClient)
	if !ok {
		return nil, runtimeagent.ErrNoToolCallingClient
	}
	var registry toolruntime.Registry
	if tools != nil {
		registry = toolruntime.BasicRegistry{Tools: tools, Invocations: toolInvocations}
	}
	var workspaceRepository workspace.Repository
	if len(workspaceRepositories) > 0 {
		workspaceRepository = workspaceRepositories[0]
	}
	s := &Service{
		workflows:        workflows,
		profiles:         profiles,
		versions:         versions,
		runs:             runs,
		events:           events,
		nodeLogs:         nodeLogs,
		runSteps:         runSteps,
		evals:            evals,
		approvals:        approvals,
		audits:           audits,
		teams:            teams,
		memories:         memories,
		memoryLogs:       memoryLogs,
		memoryRetriever:  memoryRetriever,
		workingMemory:    workingMemory,
		tools:            tools,
		toolPacks:        toolPacks,
		skills:           skills,
		mcpServers:       mcpServers,
		toolRegistry:     registry,
		toolInvocations:  toolInvocations,
		workspaces:       workspaceRepository,
		providers:        providers,
		conversations:    conversations,
		messages:         messages,
		compactions:      compactions,
		retriever:        retriever,
		llm:              llmClient,
		embedder:         embedder,
		archivalVecStore: archivalVecStore,
		contextIndex:     contextIndex,
		reflections:      reflectionAdvisor,
		secrets:          secrets,
	}
	if extractionJobs != nil && mergeLogs != nil {
		s.extractions = memoryusecase.NewExtractionService(memories, extractionJobs, mergeLogs)
	}
	nodeDeps := runtimenode.Deps{
		Retriever:               retriever,
		LLM:                     llmClient,
		Embedder:                embedder,
		Providers:               s,
		Messages:                s,
		MessageHistory:          messages,
		Compactions:             compactions,
		Memories:                memories,
		MemoryWriteLogs:         memoryLogs,
		MemoryRetriever:         memoryRetriever,
		WorkingMemory:           workingMemory,
		MemoryExtractionTrigger: s.triggerMemoryExtraction,
		Tools:                   tools,
		ToolPacks:               toolPacks,
		Skills:                  skills,
		MCPServers:              mcpServers,
		ToolInvocations:         toolInvocations,
		ToolCalling:             toolCalling,
		ToolRegistry:            registry,
		WorkflowCaller:          s,
		InlineAgentCaller:       s,
		Profiles:                s,
		RuleSets:                s,
		Reflections:             reflectionAdvisor,
		Audits:                  audits,
		Teams:                   teams,
		Workspaces:              workspaceRepository,
		Sandbox:                 sandbox.NewDockerRunner(),
		ArchivalVecStore:        archivalVecStore,
		ContextIndex:            contextIndex,
	}
	sharedAgentRuntime, err := runtimenode.NewSharedAgentRuntime(nodeDeps)
	if err != nil {
		return nil, err
	}
	nodeDeps.SharedAgentRuntime = sharedAgentRuntime
	nodes, err := runtimenode.DefaultNodes(nodeDeps)
	if err != nil {
		return nil, err
	}
	s.executor = engine.NewExecutor(nodes)
	s.agentRuntime = sharedAgentRuntime
	s.validator = flow.NewValidator(s.executor)
	s.runCancels = newRunCancelRegistry()
	return s, nil
}

func (s *Service) AgentRuntime() runtimenode.AgentRuntime { return s.agentRuntime }

func (s *Service) ConfigureAgentCaller(caller toolruntime.AgentCaller) {
	if runtime, ok := s.agentRuntime.(*runtimenode.SharedAgentRuntime); ok {
		runtime.ConfigureAgentCaller(caller)
	}
}

func (s *Service) ConfigureSessionSearch(index conversation.MessageSearchIndex) {
	if runtime, ok := s.agentRuntime.(*runtimenode.SharedAgentRuntime); ok {
		runtime.ConfigureSessionSearch(index)
	}
}

func (s *Service) ConfigureIndependentRunResumer(resumer workflow.IndependentRunResumer) {
	s.agentRunResumer = resumer
}

func (s *Service) ConfigureIndependentRunCanceller(canceller workflow.IndependentRunCanceller) {
	s.agentRunCanceller = canceller
}

func (s *Service) triggerMemoryExtraction(ctx context.Context, ownerID int64, conversationID int64) {
	if ownerID <= 0 || conversationID <= 0 {
		return
	}
	s.publishDream(ctx, ownerID, conversationID)
}

func (s *Service) ConfigureDream(queue queueinfra.JobQueue, redisClient *redis.Client, dreamCfg memoryusecase.DreamConfig) {
	s.dreamQueue = queue
	s.redisClient = redisClient
	s.dreamCfg = dreamCfg
}

func (s *Service) ConfigureRuleSets(repository workflow.RuleSetRepository, jobQueue queueinfra.JobQueue) {
	s.ruleSets = repository
	s.ruleCompileQueue = jobQueue
}

func (s *Service) publishDream(ctx context.Context, ownerID, conversationID int64) {
	if s.dreamQueue == nil || !s.dreamCfg.Enabled || ownerID <= 0 || conversationID <= 0 {
		return
	}
	if s.redisClient != nil {
		key := fmt.Sprintf("dream:pending:%d", conversationID)
		locked, err := s.redisClient.SetNX(ctx, key, 1, time.Minute).Result()
		if err != nil || !locked {
			return
		}
	}
	_ = s.dreamQueue.Publish(ctx, queueinfra.Job{ID: fmt.Sprintf("dream-%d-%d-%d", ownerID, conversationID, time.Now().UnixNano()), Type: memoryusecase.DreamJobType, Payload: map[string]any{"owner_id": ownerID, "conversation_id": conversationID}})
}

type runOptions struct {
	ParentRunID       *int64 // nil or id
	CallerNodeID      string // "" or like "node_3a7f2b1c"
	CallDepth         int
	WorkflowCallChain []int64
	RunKind           string
	Lifecycle         bool
	AgentID           int64
	AgentReleaseID    int64
}

type CreateWorkflowRequest struct {
	Name        string `json:"name" binding:"required"`
	Description string `json:"description"`
	AvatarURL   string `json:"avatar_url"`
}

type UpdateWorkflowRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	AvatarURL   string `json:"avatar_url"`
	Status      *int   `json:"status"`
}

type UpdateWorkflowProfileRequest struct {
	Role                        *string          `json:"role"`
	Goal                        *string          `json:"goal"`
	Backstory                   *string          `json:"backstory"`
	SystemPrompt                *string          `json:"system_prompt"`
	DefaultProviderID           *int64           `json:"default_provider_id"`
	DefaultModel                *string          `json:"default_model"`
	MaxIterations               *int             `json:"max_iterations"`
	MaxExecutionTimeMS          *int             `json:"max_execution_time_ms"`
	MemoryEnabled               *bool            `json:"memory_enabled"`
	PlanningEnabled             *bool            `json:"planning_enabled"`
	AllowDelegation             *bool            `json:"allow_delegation"`
	AllowCodeExecution          *bool            `json:"allow_code_execution"`
	DefaultToolPackIDs          *[]int64         `json:"default_tool_pack_ids"`
	DefaultToolIDs              *[]int64         `json:"default_tool_ids"`
	DefaultSkillIDs             *[]int64         `json:"default_skill_ids"`
	DefaultMCPServerIDs         *[]int64         `json:"default_mcp_server_ids"`
	DefaultKnowledgeIDs         *[]int64         `json:"default_knowledge_ids"`
	DefaultKnowledgeTopK        *int             `json:"default_knowledge_top_k"`
	DefaultKnowledgeMode        *string          `json:"default_knowledge_mode"`
	DefaultCallWorkflowIDs      *[]int64         `json:"default_call_workflow_ids"`
	DefaultMaxWorkflowCallDepth *int             `json:"default_max_workflow_call_depth"`
	OutputSchemaJSON            *json.RawMessage `json:"output_schema_json"`
	ToolPolicyJSON              *json.RawMessage `json:"tool_policy_json"`
	MemoryPolicyJSON            *json.RawMessage `json:"memory_policy_json"`
	ReflectionPolicyJSON        *json.RawMessage `json:"reflection_policy_json"`
	ContextPolicyJSON           *json.RawMessage `json:"context_policy_json"`
	RuleCompilerProviderID      *int64           `json:"rule_compiler_provider_id"`
	RuleCompilerModel           *string          `json:"rule_compiler_model"`
	RiskLevel                   *string          `json:"risk_level"`
	Mode                        *string          `json:"mode"`
}

type CreateWorkflowVersionRequest struct {
	DSLJSON     json.RawMessage `json:"dsl_json" binding:"required"`
	Description string          `json:"description"`
}

type RunWorkflowRequest struct {
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
	Name                 string `json:"name" binding:"required"`
	SupervisorWorkflowID int64  `json:"supervisor_workflow_id" binding:"required"`
	HandoffStrategy      string `json:"handoff_strategy"`
	MaxDepth             int    `json:"max_depth"`
}

type AddTeamMemberRequest struct {
	WorkflowID int64  `json:"workflow_id" binding:"required"`
	Role       string `json:"role"`
}

type DecideApprovalRequest struct {
	Note string `json:"note"`
}

func (s *Service) CreateWorkflow(ctx context.Context, ownerID int64, req CreateWorkflowRequest) (*workflow.Workflow, error) {
	name := strings.TrimSpace(req.Name)
	if ownerID <= 0 || name == "" {
		return nil, agenterrors.ErrInvalidInput
	}
	item := &workflow.Workflow{
		OwnerID:     ownerID,
		Name:        name,
		Description: strings.TrimSpace(req.Description),
		AvatarURL:   strings.TrimSpace(req.AvatarURL),
		Status:      workflow.StatusActive,
	}
	if err := s.workflows.Create(ctx, item); err != nil {
		return nil, err
	}
	if s.profiles != nil {
		_ = s.profiles.Create(ctx, defaultWorkflowProfile(ownerID, item.ID, item.Name, item.Description))
	}
	return item, nil
}

func (s *Service) ListWorkflows(ctx context.Context, ownerID int64) ([]workflow.Workflow, error) {
	return s.workflows.ListByOwner(ctx, ownerID)
}

func (s *Service) GetWorkflow(ctx context.Context, ownerID, id int64) (*workflow.Workflow, error) {
	item, err := s.workflows.FindByID(ctx, ownerID, id)
	return item, mapNotFound(err)
}

func (s *Service) UpdateWorkflow(ctx context.Context, ownerID, id int64, req UpdateWorkflowRequest) (*workflow.Workflow, error) {
	item, err := s.GetWorkflow(ctx, ownerID, id)
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
	if err := s.workflows.Update(ctx, item); err != nil {
		return nil, err
	}
	return item, nil
}

func (s *Service) GetWorkflowProfile(ctx context.Context, ownerID, workflowID int64) (*workflow.Profile, error) {
	item, err := s.GetWorkflow(ctx, ownerID, workflowID)
	if err != nil {
		return nil, err
	}
	if s.profiles == nil {
		return defaultWorkflowProfile(ownerID, workflowID, item.Name, item.Description), nil
	}
	profile, err := s.profiles.FindByWorkflow(ctx, ownerID, workflowID)
	if err == nil {
		return profile, nil
	}
	if err != gorm.ErrRecordNotFound {
		return nil, err
	}
	profile = defaultWorkflowProfile(ownerID, workflowID, item.Name, item.Description)
	if err := s.profiles.Create(ctx, profile); err != nil {
		return nil, err
	}
	return profile, nil
}

func (s *Service) UpdateWorkflowProfile(ctx context.Context, ownerID, workflowID int64, req UpdateWorkflowProfileRequest) (*workflow.Profile, error) {
	profile, err := s.GetWorkflowProfile(ctx, ownerID, workflowID)
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
	if req.DefaultSkillIDs != nil {
		profile.DefaultSkillIDs = mustMarshalJSON(normalizePositiveIDs(*req.DefaultSkillIDs))
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
	if req.DefaultCallWorkflowIDs != nil {
		profile.DefaultCallWorkflowIDs = mustMarshalJSON(normalizePositiveIDs(*req.DefaultCallWorkflowIDs))
	}
	if req.DefaultMaxWorkflowCallDepth != nil {
		if *req.DefaultMaxWorkflowCallDepth < 0 || *req.DefaultMaxWorkflowCallDepth > 5 {
			return nil, fmt.Errorf("%w: default_max_workflow_call_depth must be 0..5", agenterrors.ErrInvalidInput)
		}
		profile.DefaultMaxWorkflowCallDepth = *req.DefaultMaxWorkflowCallDepth
	}
	if req.OutputSchemaJSON != nil {
		outputSchema, err := normalizeOptionalRawJSON(*req.OutputSchemaJSON, "output_schema_json")
		if err != nil {
			return nil, err
		}
		profile.OutputSchemaJSON = outputSchema
	}
	if req.ToolPolicyJSON != nil {
		toolPolicy, err := normalizeOptionalRawJSON(*req.ToolPolicyJSON, "tool_policy_json")
		if err != nil {
			return nil, err
		}
		if len(toolPolicy) > 0 && string(toolPolicy) != "{}" {
			var policy runtimeagent.ToolPolicy
			if err := json.Unmarshal(toolPolicy, &policy); err != nil {
				return nil, fmt.Errorf("%w: tool_policy_json is invalid", agenterrors.ErrInvalidInput)
			}
		}
		profile.ToolPolicyJSON = toolPolicy
	}
	if req.MemoryPolicyJSON != nil {
		memoryPolicy, err := normalizeOptionalRawJSON(*req.MemoryPolicyJSON, "memory_policy_json")
		if err != nil {
			return nil, err
		}
		profile.MemoryPolicyJSON = memoryPolicy
	}
	if req.ReflectionPolicyJSON != nil {
		reflectionPolicy, err := normalizeOptionalRawJSON(*req.ReflectionPolicyJSON, "reflection_policy_json")
		if err != nil {
			return nil, err
		}
		if string(reflectionPolicy) != "{}" {
			policy := reflection.DefaultPolicy()
			if err := json.Unmarshal(reflectionPolicy, &policy); err != nil {
				return nil, fmt.Errorf("%w: reflection_policy_json is invalid", agenterrors.ErrInvalidInput)
			}
			if err := policy.Validate(); err != nil {
				return nil, fmt.Errorf("%w: reflection_policy_json is invalid: %v", agenterrors.ErrInvalidInput, err)
			}
		}
		profile.ReflectionPolicyJSON = reflectionPolicy
	}
	if req.ContextPolicyJSON != nil {
		contextPolicy, err := normalizeOptionalRawJSON(*req.ContextPolicyJSON, "context_policy_json")
		if err != nil {
			return nil, err
		}
		if err := validateContextPolicyRules(contextPolicy); err != nil {
			return nil, err
		}
		if profile.ActiveRuleSetID != nil && contextPolicyRuleCount(contextPolicy) > 0 {
			return nil, fmt.Errorf("%w: legacy context_policy_json.rules cannot be updated after a versioned rule set is active", workflow.ErrRuleSetConflict)
		}
		profile.ContextPolicyJSON = contextPolicy
	}
	if req.RuleCompilerProviderID != nil {
		if *req.RuleCompilerProviderID > 0 {
			profile.RuleCompilerProviderID = req.RuleCompilerProviderID
		} else {
			profile.RuleCompilerProviderID = nil
		}
	}
	if req.RuleCompilerModel != nil {
		profile.RuleCompilerModel = strings.TrimSpace(*req.RuleCompilerModel)
	}
	if req.RiskLevel != nil {
		risk := strings.TrimSpace(*req.RiskLevel)
		if risk != "" && risk != toolruntime.RiskLow && risk != toolruntime.RiskMedium && risk != toolruntime.RiskHigh {
			return nil, fmt.Errorf("%w: risk_level must be low, medium, or high", agenterrors.ErrInvalidInput)
		}
		profile.RiskLevel = risk
	}
	if req.Mode != nil {
		mode := strings.TrimSpace(*req.Mode)
		if mode != "" && mode != "react" && mode != "plan_execute" {
			return nil, fmt.Errorf("%w: mode must be react or plan_execute", agenterrors.ErrInvalidInput)
		}
		profile.Mode = mode
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

func (s *Service) DeleteWorkflow(ctx context.Context, ownerID, id int64) error {
	if _, err := s.GetWorkflow(ctx, ownerID, id); err != nil {
		return err
	}
	return s.workflows.SoftDelete(ctx, ownerID, id)
}

func (s *Service) CreateWorkflowVersion(ctx context.Context, ownerID, workflowID int64, req CreateWorkflowVersionRequest) (*workflow.WorkflowVersion, error) {
	if _, err := s.GetWorkflow(ctx, ownerID, workflowID); err != nil {
		return nil, err
	}
	dsl, err := flow.ParseDSL(req.DSLJSON)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid dsl_json", agenterrors.ErrInvalidInput)
	}
	if err := s.validator.Validate(dsl); err != nil {
		return nil, err
	}
	if existing, err := s.findEquivalentWorkflowVersion(ctx, ownerID, workflowID, dsl); err != nil {
		return nil, err
	} else if existing != nil {
		return existing, nil
	}
	versionNo, err := s.versions.NextVersionNo(ctx, ownerID, workflowID)
	if err != nil {
		return nil, err
	}
	item := &workflow.WorkflowVersion{
		OwnerID:     ownerID,
		WorkflowID:  workflowID,
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

func (s *Service) findEquivalentWorkflowVersion(
	ctx context.Context,
	ownerID, workflowID int64,
	dsl *flow.DSL,
) (*workflow.WorkflowVersion, error) {
	candidates := make([]*workflow.WorkflowVersion, 0, 2)
	latest, err := s.versions.FindLatestByWorkflow(ctx, ownerID, workflowID)
	if err != nil && err != gorm.ErrRecordNotFound {
		return nil, err
	}
	if latest != nil {
		candidates = append(candidates, latest)
	}
	current, err := s.versions.FindCurrentByWorkflow(ctx, ownerID, workflowID)
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

func (s *Service) ListWorkflowVersions(ctx context.Context, ownerID, workflowID int64) ([]workflow.WorkflowVersion, error) {
	if _, err := s.GetWorkflow(ctx, ownerID, workflowID); err != nil {
		return nil, err
	}
	return s.versions.ListByWorkflow(ctx, ownerID, workflowID)
}

func (s *Service) GetWorkflowVersion(ctx context.Context, ownerID, id int64) (*workflow.WorkflowVersion, error) {
	item, err := s.versions.FindByID(ctx, ownerID, id)
	return item, mapNotFound(err)
}

func (s *Service) ValidateWorkflowVersion(ctx context.Context, ownerID, id int64) error {
	item, err := s.GetWorkflowVersion(ctx, ownerID, id)
	if err != nil {
		return err
	}
	dsl, err := flow.ParseDSL(item.DSLJSON)
	if err != nil {
		return fmt.Errorf("%w: invalid dsl_json", agenterrors.ErrInvalidInput)
	}
	return s.validator.Validate(dsl)
}

func (s *Service) PublishWorkflowVersion(ctx context.Context, ownerID, id int64) (*workflow.WorkflowVersion, error) {
	item, err := s.GetWorkflowVersion(ctx, ownerID, id)
	if err != nil {
		return nil, err
	}
	if err := s.ValidateWorkflowVersion(ctx, ownerID, id); err != nil {
		return nil, err
	}
	if err := s.versions.Publish(ctx, ownerID, item.WorkflowID, id); err != nil {
		return nil, err
	}
	return s.GetWorkflowVersion(ctx, ownerID, id)
}

func (s *Service) RunWorkflow(
	ctx context.Context,
	ownerID, workflowID int64,
	req RunWorkflowRequest,
) (*workflow.Run, engine.NodeOutput, error) {
	item, output, err := s.run(ctx, ownerID, workflowID, req, nil, runOptions{})
	return item, output, err
}

func (s *Service) StreamRunWorkflow(
	ctx context.Context,
	ownerID, workflowID int64,
	req RunWorkflowRequest,
	emit func(runtimeevent.Event) error,
) (*workflow.Run, engine.NodeOutput, error) {
	return s.run(ctx, ownerID, workflowID, req, emit, runOptions{})
}

func (s *Service) CallWorkflow(ctx context.Context, req toolruntime.WorkflowCallRequest) (*toolruntime.WorkflowCallResult, error) {
	maxDepth := req.MaxDepth
	if maxDepth <= 0 {
		maxDepth = 3
	}
	callChain := normalizeWorkflowCallChain(req.WorkflowCallChain, req.CallerWorkflowID)
	if req.CallDepth >= maxDepth {
		s.recordBlockedWorkflowCall(ctx, req, "max_workflow_call_depth_exceeded", maxDepth)
		return nil, fmt.Errorf("%w: max workflow call depth exceeded", agenterrors.ErrForbidden)
	}
	if req.OwnerID <= 0 || req.WorkflowID <= 0 || req.Input == nil {
		return nil, agenterrors.ErrInvalidInput
	}
	if err := runtimeagent.CheckCallChain(callChain, req.WorkflowID, 0, req.CallDepth); err != nil {
		s.recordBlockedWorkflowCall(ctx, req, "workflow_call_cycle_detected", maxDepth)
		return nil, fmt.Errorf("%w: %v", agenterrors.ErrForbidden, err)
	}
	var parentRunID *int64
	if req.ParentRunID > 0 {
		parentRunID = &req.ParentRunID
	}
	run, output, err := s.run(ctx, req.OwnerID, req.WorkflowID, RunWorkflowRequest{
		FlowVersionID: req.FlowVersionID,
		Input:         req.Input,
	}, nil, runOptions{
		ParentRunID:       parentRunID,
		CallerNodeID:      req.CallerNodeID,
		CallDepth:         req.CallDepth + 1,
		WorkflowCallChain: append(callChain, req.WorkflowID),
		RunKind:           req.RunKind,
		Lifecycle:         req.Lifecycle,
		AgentID:           req.CallerAgentID,
		AgentReleaseID:    req.AgentReleaseID,
	})
	if run == nil {
		return nil, err
	}
	result := &toolruntime.WorkflowCallResult{
		RunID:         run.ID,
		WorkflowID:    run.WorkflowID,
		FlowVersionID: run.FlowVersionID,
		Status:        run.Status,
		Output:        map[string]any(output),
		Error:         run.ErrorMessage,
		LatencyMS:     run.LatencyMS,
	}
	return result, err
}

func (s *Service) CallInlineAgent(ctx context.Context, req toolruntime.InlineAgentCallRequest) (*toolruntime.InlineAgentCallResult, error) {
	maxDepth := req.MaxDepth
	if maxDepth <= 0 {
		maxDepth = 2
	}
	if req.OwnerID <= 0 || req.ParentRunID <= 0 || (req.CallerWorkflowID <= 0 && req.CallerAgentID <= 0) || req.CallDepth >= maxDepth {
		return nil, fmt.Errorf("%w: inline agent call is not allowed", agenterrors.ErrForbidden)
	}
	parent, err := s.GetRun(ctx, req.OwnerID, req.ParentRunID)
	if err != nil {
		return nil, err
	}
	parentMatches := parent.CallDepth == req.CallDepth
	if req.CallerWorkflowID > 0 {
		parentMatches = parentMatches && parent.WorkflowID == req.CallerWorkflowID
	} else {
		parentMatches = parentMatches && parent.AgentID != nil && *parent.AgentID == req.CallerAgentID
	}
	if !parentMatches {
		return nil, fmt.Errorf("%w: inline agent parent run context does not match", agenterrors.ErrForbidden)
	}
	var pinnedRules *rules.CompiledRuleSet
	if parent.RuleSetID != nil {
		pinnedRules, err = s.loadPinnedRuleSet(ctx, req.OwnerID, req.CallerWorkflowID, *parent.RuleSetID)
		if err != nil {
			return nil, err
		}
	}
	definitionJSON, err := json.Marshal(req.Definition)
	if err != nil {
		return nil, err
	}
	definitionHash := sha256.Sum256(definitionJSON)
	dsl := inlineAgentDSL(req.Definition, req.CallDepth+1 < maxDepth)
	if err := s.validator.Validate(dsl); err != nil {
		return nil, err
	}
	input := map[string]any{"query": req.Definition.Task}
	inputJSON, _ := json.Marshal(input)
	callChain := normalizeWorkflowCallChain(req.WorkflowCallChain, req.CallerWorkflowID)
	callChainJSON, _ := json.Marshal(callChain)
	now := time.Now().UTC()
	run := &workflow.Run{OwnerID: req.OwnerID, WorkflowID: req.CallerWorkflowID, FlowVersionID: parent.FlowVersionID, AgentID: parent.AgentID, AgentReleaseID: parent.AgentReleaseID, RuleSetID: parent.RuleSetID, RuleSetVersion: parent.RuleSetVersion, CompiledRuleHash: parent.CompiledRuleHash, ConversationID: req.ConversationID, ParentRunID: &req.ParentRunID, CallerNodeID: req.CallerNodeID, CallDepth: req.CallDepth + 1, CallChainJSON: callChainJSON, RunKind: workflow.RunKindInlineAgent, DefinitionJSON: definitionJSON, DefinitionHash: hex.EncodeToString(definitionHash[:]), Status: workflow.RunStatusRunning, InputJSON: inputJSON, StartedAt: now}
	if err := s.runs.Create(ctx, run); err != nil {
		return nil, err
	}
	execCtx, cancel := context.WithCancel(ctx)
	s.runCancels.Register(run.ID, cancel)
	defer func() { cancel(); s.runCancels.Unregister(run.ID) }()
	rc := &engine.RunContext{OwnerID: req.OwnerID, WorkflowID: req.CallerWorkflowID, FlowVersionID: parent.FlowVersionID, AgentID: req.CallerAgentID, AgentReleaseID: ruleSetIDValue(parent.AgentReleaseID), RuleSetID: ruleSetIDValue(parent.RuleSetID), RuleSetVersion: parent.RuleSetVersion, CompiledRuleHash: parent.CompiledRuleHash, CompiledRules: pinnedRules, RunID: run.ID, ParentRunID: &req.ParentRunID, CallDepth: req.CallDepth + 1, WorkflowCallChain: callChain, ConversationID: req.ConversationID, AgentSteps: s, Input: input, Events: &eventEmitter{repo: s.events, ownerID: req.OwnerID, runID: run.ID}}
	output, execErr := s.executor.Execute(execCtx, rc, dsl)
	finished := time.Now().UTC()
	run.FinishedAt = &finished
	run.LatencyMS = int(finished.Sub(run.StartedAt).Milliseconds())
	if output != nil {
		run.OutputJSON, _ = json.Marshal(output)
	}
	if errors.Is(execErr, context.Canceled) || execCtx.Err() == context.Canceled {
		run.Status, run.ErrorMessage = workflow.RunStatusCancelled, context.Canceled.Error()
	} else if execErr != nil {
		run.Status, run.ErrorMessage = workflow.RunStatusFailed, execErr.Error()
	} else if status := runStatusFromOutput(output); status != "" {
		run.Status = status
	} else {
		run.Status = workflow.RunStatusSucceeded
	}
	if run.Status == workflow.RunStatusWaitingHuman || run.Status == workflow.RunStatusPaused {
		if err := s.persistRunCheckpointArtifacts(ctx, run, output, run.Status); err != nil {
			return nil, err
		}
	}
	if err := s.runs.Update(ctx, run); err != nil {
		return nil, err
	}
	_ = s.writeNodeLogs(ctx, req.OwnerID, run.ID, dsl, rc)
	return &toolruntime.InlineAgentCallResult{RunID: run.ID, Status: run.Status, Output: map[string]any(output), Error: run.ErrorMessage, LatencyMS: run.LatencyMS}, execErr
}

func inlineAgentDSL(definition toolruntime.InlineAgentDefinition, allowChildren bool) *flow.DSL {
	agentConfig, _ := json.Marshal(map[string]any{
		"mode": definition.Mode, "provider_id": definition.ProviderID, "model": definition.Model,
		"system_prompt": definition.SystemPrompt, "task_template": definition.Task,
		"tool_ids": definition.ToolIDs, "skill_ids": definition.SkillIDs, "knowledge_ids": definition.KnowledgeIDs,
		"mcp_server_ids": definition.MCPServerIDs, "max_iterations": definition.MaxIterations,
		"max_tool_calls": definition.MaxToolCalls, "max_execution_time_ms": definition.MaxExecutionTimeMS,
		"max_parallel_sub_agents": definition.MaxParallelChildren, "allow_inline_agents": allowChildren,
		"max_workflow_call_depth":   definition.MaxDepth,
		"require_approval_for_risk": definition.RequireApprovalForRisk, "max_tool_timeout_ms": definition.MaxToolTimeoutMS,
		"max_tool_output_bytes": definition.MaxToolOutputBytes, "allowed_hosts": definition.AllowedHosts,
		"code_execution_enabled":   definition.CodeExecutionEnabled,
		"disable_profile_defaults": true,
	})
	return &flow.DSL{SchemaVersion: flow.SchemaVersionV1, FlowID: "inline-agent", Nodes: []flow.Node{
		{ID: "begin", Type: "begin", Name: "Begin", Config: json.RawMessage(`{}`)},
		{ID: "agent", Type: "agent_loop", Name: definition.Name, Config: agentConfig},
	}, Edges: []flow.Edge{{From: "begin", To: "agent"}}}
}

func (s *Service) recordBlockedWorkflowCall(ctx context.Context, req toolruntime.WorkflowCallRequest, reason string, maxDepth int) {
	if s.events == nil || req.OwnerID <= 0 || req.ParentRunID <= 0 {
		return
	}
	payload := map[string]any{
		"blocked_reason":      reason,
		"caller_workflow_id":  req.CallerWorkflowID,
		"callee_workflow_id":  req.WorkflowID,
		"call_depth":          req.CallDepth,
		"max_depth":           maxDepth,
		"workflow_call_chain": append([]int64(nil), req.WorkflowCallChain...),
	}
	_ = s.events.Create(ctx, &workflow.RunEvent{
		OwnerID:     req.OwnerID,
		RunID:       req.ParentRunID,
		EventType:   runtimeevent.WorkflowCallFailed,
		NodeID:      req.CallerNodeID,
		NodeType:    "call_workflow",
		PayloadJSON: mustMarshalJSON(payload),
		CreatedAt:   time.Now().UTC(),
	})
}

func (s *Service) GetRun(ctx context.Context, ownerID, id int64) (*workflow.Run, error) {
	item, err := s.runs.FindByID(ctx, ownerID, id)
	return item, mapNotFound(err)
}

func (s *Service) ListRunEvents(ctx context.Context, ownerID, runID int64) ([]workflow.RunEvent, error) {
	if _, err := s.GetRun(ctx, ownerID, runID); err != nil {
		return nil, err
	}
	return s.events.ListByRun(ctx, ownerID, runID)
}

func (s *Service) ListChildRuns(ctx context.Context, ownerID, runID int64) ([]workflow.Run, error) {
	if _, err := s.GetRun(ctx, ownerID, runID); err != nil {
		return nil, err
	}
	return s.runs.ListByParent(ctx, ownerID, runID)
}

func (s *Service) ListNodeLogs(ctx context.Context, ownerID, runID int64) ([]workflow.NodeLog, error) {
	if _, err := s.GetRun(ctx, ownerID, runID); err != nil {
		return nil, err
	}
	return s.nodeLogs.ListByRun(ctx, ownerID, runID)
}

func (s *Service) ListRunSteps(ctx context.Context, ownerID, runID int64) ([]workflow.RunStep, error) {
	if _, err := s.GetRun(ctx, ownerID, runID); err != nil {
		return nil, err
	}
	if s.runSteps == nil {
		return []workflow.RunStep{}, nil
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

func (s *Service) GetRunTrace(ctx context.Context, ownerID, runID int64) (*RunTrace, error) {
	run, err := s.GetRun(ctx, ownerID, runID)
	if err != nil {
		return nil, err
	}
	trace := &RunTrace{
		Run:             run,
		Events:          []workflow.RunEvent{},
		NodeLogs:        []workflow.NodeLog{},
		Steps:           []workflow.RunStep{},
		ChildRuns:       []workflow.Run{},
		MemoryWriteLogs: []memory.WriteLog{},
		ToolInvocations: []tool.Invocation{},
		ReplaySummary:   map[string]any{},
	}
	if s.events != nil {
		trace.Events, err = s.events.ListByRun(ctx, ownerID, runID)
		if err != nil {
			return nil, err
		}
	}
	if s.nodeLogs != nil {
		trace.NodeLogs, err = s.nodeLogs.ListByRun(ctx, ownerID, runID)
		if err != nil {
			return nil, err
		}
	}
	if s.runSteps != nil {
		trace.Steps, err = s.runSteps.ListByRun(ctx, ownerID, runID)
		if err != nil {
			return nil, err
		}
	}
	if s.runs != nil {
		trace.ChildRuns, err = s.runs.ListByParent(ctx, ownerID, runID)
		if err != nil {
			return nil, err
		}
	}
	if s.memoryLogs != nil {
		trace.MemoryWriteLogs, err = s.memoryLogs.ListByRun(ctx, ownerID, runID)
		if err != nil {
			return nil, err
		}
	}
	if s.toolInvocations != nil {
		trace.ToolInvocations, err = s.toolInvocations.ListByRun(ctx, ownerID, runID)
		if err != nil {
			return nil, err
		}
	}
	trace.ReplaySummary = buildRunTraceReplaySummary(trace)
	return trace, nil
}

func buildRunTraceReplaySummary(trace *RunTrace) map[string]any {
	if trace == nil {
		return map[string]any{}
	}
	compressedSteps := 0
	reflectionSteps := 0
	toolSteps := 0
	for _, step := range trace.Steps {
		if step.Compressed {
			compressedSteps++
		}
		if step.StepType == runtimeagent.StepTypeReflection {
			reflectionSteps++
		}
		if step.StepType == runtimeagent.StepTypeToolCall {
			toolSteps++
		}
	}
	return map[string]any{
		"event_count":           len(trace.Events),
		"node_log_count":        len(trace.NodeLogs),
		"step_count":            len(trace.Steps),
		"compressed_step_count": compressedSteps,
		"reflection_step_count": reflectionSteps,
		"tool_call_step_count":  toolSteps,
		"child_run_count":       len(trace.ChildRuns),
		"memory_write_count":    len(trace.MemoryWriteLogs),
		"tool_invocation_count": len(trace.ToolInvocations),
		"status":                trace.Run.Status,
		"latency_ms":            trace.Run.LatencyMS,
		"total_tokens":          trace.Run.TotalTokens,
		"has_error":             strings.TrimSpace(trace.Run.ErrorMessage) != "",
	}
}

func (s *Service) CancelRun(ctx context.Context, ownerID, id int64) (*workflow.Run, error) {
	item, err := s.GetRun(ctx, ownerID, id)
	if err != nil {
		return nil, err
	}
	if item.Status == workflow.RunStatusRunning || item.Status == workflow.RunStatusQueued {
		wasRunning := item.Status == workflow.RunStatusRunning
		now := time.Now().UTC()
		item.Status = workflow.RunStatusCancelled
		item.FinishedAt = &now
		item.LatencyMS = int(now.Sub(item.StartedAt).Milliseconds())
		if err := s.runs.Update(ctx, item); err != nil {
			return nil, err
		}
		if wasRunning && item.RunKind == workflow.RunKindAgent && s.agentRunCanceller != nil {
			s.agentRunCanceller.CancelIndependentRun(id)
		} else if wasRunning {
			_ = s.runCancels.Cancel(id)
		}
	}
	return item, nil
}

func (s *Service) PauseRun(ctx context.Context, ownerID, id int64) (*workflow.Run, error) {
	item, err := s.GetRun(ctx, ownerID, id)
	if err != nil {
		return nil, err
	}
	if item.Status != workflow.RunStatusRunning {
		return nil, fmt.Errorf("%w: run is not running", agenterrors.ErrInvalidInput)
	}
	if item.RunKind == workflow.RunKindAgent {
		return nil, fmt.Errorf("%w: independent Agent Runs pause only at approval checkpoints; use cancel for an active turn", agenterrors.ErrInvalidInput)
	}
	_ = s.runCancels.Pause(id)
	now := time.Now().UTC()
	item.Status = workflow.RunStatusPaused
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
		EmbeddingConfig: llm.EmbeddingProviderConfig{
			ProviderType: provider.ProviderType,
			BaseURL:      provider.BaseURL,
			APIKey:       apiKey,
		},
		EmbeddingModel: strings.TrimSpace(provider.DefaultEmbeddingModel),
	}, nil
}

func (s *Service) WriteAssistantMessage(
	ctx context.Context,
	ownerID int64,
	conversationID *int64,
	runID int64,
	content string,
	tokenCount int,
) (int64, error) {
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

func (s *Service) run(
	ctx context.Context,
	ownerID, workflowID int64,
	req RunWorkflowRequest,
	stream func(runtimeevent.Event) error,
	opts runOptions,
) (*workflow.Run, engine.NodeOutput, error) {
	if req.Input == nil {
		return nil, nil, agenterrors.ErrInvalidInput
	}
	if _, err := s.GetWorkflow(ctx, ownerID, workflowID); err != nil {
		return nil, nil, err
	}
	version, err := s.loadRunWorkflowVersion(ctx, ownerID, workflowID, req.FlowVersionID)
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
	if opts.Lifecycle {
		if err := validateLifecycleDSL(dsl); err != nil {
			return nil, nil, err
		}
	}
	inputJSON, _ := json.Marshal(req.Input)
	callChain := opts.WorkflowCallChain
	if len(callChain) == 0 {
		callChain = []int64{workflowID}
	}
	callChainJSON, _ := json.Marshal(callChain)
	now := time.Now().UTC()
	var activeRuleSetID *int64
	var activeRuleSet *rules.CompiledRuleSet
	var activeRuleSetVersion, activeCompiledHash string
	if active, activeErr := s.LoadActiveRuleSet(ctx, ownerID, workflowID); activeErr != nil {
		return nil, nil, activeErr
	} else if active != nil {
		activeRuleSet = active
		id := active.ID
		activeRuleSetID = &id
		activeRuleSetVersion = active.Version
		activeCompiledHash = active.CompiledHash
	}
	runKind := opts.RunKind
	if runKind == "" {
		runKind = workflow.RunKindWorkflow
	}
	var agentID, agentReleaseID *int64
	if opts.AgentID > 0 {
		agentID = &opts.AgentID
	}
	if opts.AgentReleaseID > 0 {
		agentReleaseID = &opts.AgentReleaseID
	}
	run := &workflow.Run{
		OwnerID:          ownerID,
		WorkflowID:       workflowID,
		FlowVersionID:    version.ID,
		RuleSetID:        activeRuleSetID,
		RuleSetVersion:   activeRuleSetVersion,
		CompiledRuleHash: activeCompiledHash,
		RunKind:          runKind,
		AgentID:          agentID,
		AgentReleaseID:   agentReleaseID,
		ConversationID:   req.ConversationID,
		ParentRunID:      opts.ParentRunID,
		CallerNodeID:     opts.CallerNodeID,
		CallDepth:        opts.CallDepth,
		CallChainJSON:    callChainJSON,
		Status:           workflow.RunStatusRunning,
		InputJSON:        inputJSON,
		StartedAt:        now,
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
		OwnerID:           ownerID,
		WorkflowID:        workflowID,
		FlowVersionID:     version.ID,
		AgentID:           opts.AgentID,
		AgentReleaseID:    opts.AgentReleaseID,
		RuleSetID:         ruleSetIDValue(activeRuleSetID),
		RuleSetVersion:    activeRuleSetVersion,
		CompiledRuleHash:  activeCompiledHash,
		CompiledRules:     activeRuleSet,
		RunID:             run.ID,
		ParentRunID:       opts.ParentRunID,
		CallDepth:         opts.CallDepth,
		WorkflowCallChain: append([]int64(nil), callChain...),
		ConversationID:    req.ConversationID,
		AgentSteps:        s,
		Input:             req.Input,
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
		run.Status = workflow.RunStatusPaused
		run.ErrorMessage = "paused by user request"
	} else if errors.Is(execErr, context.Canceled) || execCtx.Err() == context.Canceled {
		run.Status = workflow.RunStatusCancelled
		run.ErrorMessage = context.Canceled.Error()
	} else if execErr != nil {
		run.Status = workflow.RunStatusFailed
		run.ErrorMessage = execErr.Error()
	} else if status := runStatusFromOutput(output); status != "" {
		run.Status = status
	} else {
		run.Status = workflow.RunStatusSucceeded
	}
	if run.Status == workflow.RunStatusWaitingHuman {
		if err := s.persistRunCheckpointArtifacts(ctx, run, output, workflow.RunStatusWaitingHuman); err != nil {
			return run, output, err
		}
	}
	if run.Status == workflow.RunStatusPaused {
		if err := s.persistRunCheckpointArtifacts(ctx, run, output, workflow.RunStatusPaused); err != nil {
			return run, output, err
		}
	}
	if err := s.runs.Update(ctx, run); err != nil {
		return run, output, err
	}
	_ = s.writeNodeLogs(ctx, ownerID, run.ID, dsl, rc)
	return run, output, execErr
}

func (s *Service) RecordAgentStep(ctx context.Context, rc *engine.RunContext, step engine.AgentStepRecord) error {
	if s.runSteps == nil || rc == nil {
		return nil
	}
	nodeID := step.NodeID
	if nodeID == "" {
		nodeID = rc.CurrentNodeID
	}
	return s.runSteps.Create(ctx, &workflow.RunStep{
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
		Compressed:    step.Compressed,
		ErrorMessage:  step.ErrorMessage,
		TokenCount:    step.TokenCount,
		LatencyMS:     step.LatencyMS,
		CreatedAt:     time.Now().UTC(),
	})
}

func (s *Service) loadRunWorkflowVersion(ctx context.Context, ownerID, workflowID, versionID int64) (*workflow.WorkflowVersion, error) {
	if versionID > 0 {
		version, err := s.GetWorkflowVersion(ctx, ownerID, versionID)
		if err != nil {
			return nil, err
		}
		if version.WorkflowID != workflowID {
			return nil, agenterrors.ErrNotFound
		}
		return version, nil
	}
	version, err := s.versions.FindCurrentByWorkflow(ctx, ownerID, workflowID)
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
		log := &workflow.NodeLog{
			OwnerID:    ownerID,
			RunID:      runID,
			NodeID:     spec.ID,
			NodeType:   spec.Type,
			Status:     workflow.NodeLogStatusSucceeded,
			InputJSON:  inputJSON,
			OutputJSON: outputJSON,
			LatencyMS:  rc.NodeLatencies[spec.ID],
			StartedAt:  now,
			FinishedAt: &now,
			CreatedAt:  now,
		}
		if _, ok := rc.NodeOutputs[spec.ID]; !ok {
			log.Status = workflow.NodeLogStatusFailed
			log.ErrorMessage = rc.NodeErrors[spec.ID]
		}
		if err := s.nodeLogs.Create(ctx, log); err != nil {
			return err
		}
	}
	return nil
}

type eventEmitter struct {
	repo    workflow.RunEventRepository
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
		if err := e.repo.Create(ctx, &workflow.RunEvent{
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

func validateContextPolicyRules(raw json.RawMessage) error {
	if len(raw) == 0 || string(raw) == "{}" {
		return nil
	}
	if contextPolicyRuleCount(raw) > 0 {
		return fmt.Errorf("%w: context_policy_json.rules is read-only legacy data; use a versioned rule set", agenterrors.ErrInvalidInput)
	}
	return nil
}

func contextPolicyRuleCount(raw json.RawMessage) int {
	var policy struct {
		Rules []json.RawMessage `json:"rules"`
	}
	if json.Unmarshal(raw, &policy) != nil {
		return 0
	}
	return len(policy.Rules)
}

func failedEvalResult(
	ownerID, evalRunID, evalCaseID int64,
	agentRunID *int64,
	outputJSON json.RawMessage,
	message string,
	latencyMS int,
) *workflow.EvalResult {
	metricsJSON, _ := json.Marshal(map[string]any{"score": 0, "reason": message})
	return &workflow.EvalResult{
		OwnerID:       ownerID,
		EvalRunID:     evalRunID,
		EvalCaseID:    evalCaseID,
		WorkflowRunID: agentRunID,
		Status:        "failed",
		Score:         0,
		Reason:        message,
		OutputJSON:    outputJSON,
		MetricsJSON:   metricsJSON,
		ErrorMessage:  message,
		LatencyMS:     latencyMS,
		CreatedAt:     time.Now().UTC(),
	}
}

func summarizeEvalMetrics(results []workflow.EvalResult) map[string]any {
	summary := map[string]any{
		"avg_score":                            averageEvalResultScore(results),
		"avg_latency_ms":                       averageEvalResultLatency(results),
		"avg_tool_call_accuracy":               averageMetric(results, "tool_call_accuracy"),
		"avg_schema_compliance":                averageMetric(results, "schema_compliance"),
		"avg_json_schema_compliance":           averageMetric(results, "json_schema_compliance"),
		"avg_reference_hit_rate":               averageMetric(results, "reference_hit_rate"),
		"avg_retrieval_hit_rate":               averageMetric(results, "retrieval_hit_rate"),
		"avg_mrr":                              averageMetric(results, "mrr"),
		"avg_ndcg":                             averageMetric(results, "ndcg"),
		"avg_citation_rate":                    averageMetric(results, "citation_rate"),
		"avg_candidate_size":                   averageMetric(results, "candidate_size"),
		"avg_total_tokens":                     averageMetric(results, "total_tokens"),
		"avg_token_saved":                      averageMetric(results, "token_saved"),
		"avg_rules_token_cost":                 averageMetric(results, "rules_token_cost"),
		"avg_rules_dynamic_budget":             averageMetric(results, "rules_dynamic_budget"),
		"avg_provider_prompt_tokens":           averageMetric(results, "provider_prompt_tokens"),
		"avg_token_estimation_error":           averageMetric(results, "token_estimation_error"),
		"avg_rule_round_count":                 averageMetric(results, "rule_round_count"),
		"max_iteration_exceeded_rate":          averageMetric(results, "max_iteration_exceeded"),
		"max_tool_calls_exceeded_rate":         averageMetric(results, "max_tool_calls_exceeded"),
		"human_approval_waiting_rate":          averageMetric(results, "human_approval_waiting"),
		"reflection_repair_attempted_rate":     averageMetric(results, "reflection_repair_attempted"),
		"failed_cases_with_runtime_error_rate": runtimeErrorRate(results),
	}
	return summary
}

func summarizeRuleSetVersions(results []workflow.EvalResult) map[string]any {
	type aggregate struct {
		cases        int
		passed       int
		score        float64
		rulesCost    float64
		promptTokens float64
		tokensSaved  float64
	}
	aggregates := map[string]*aggregate{}
	for _, result := range results {
		var metrics map[string]any
		if len(result.MetricsJSON) == 0 || json.Unmarshal(result.MetricsJSON, &metrics) != nil {
			continue
		}
		version, _ := metrics["rule_set_version"].(string)
		if version == "" {
			version = "builtin"
		}
		item := aggregates[version]
		if item == nil {
			item = &aggregate{}
			aggregates[version] = item
		}
		item.cases++
		if result.Status == "passed" || result.Score >= 1 {
			item.passed++
		}
		item.score += result.Score
		item.rulesCost += metricValue(metrics, "rules_token_cost")
		item.promptTokens += metricValue(metrics, "provider_prompt_tokens")
		item.tokensSaved += metricValue(metrics, "token_saved")
	}
	summary := make(map[string]any, len(aggregates))
	for version, item := range aggregates {
		if item.cases == 0 {
			continue
		}
		count := float64(item.cases)
		summary[version] = map[string]any{
			"cases":                      item.cases,
			"pass_rate":                  float64(item.passed) / count,
			"avg_score":                  item.score / count,
			"avg_rules_token_cost":       item.rulesCost / count,
			"avg_provider_prompt_tokens": item.promptTokens / count,
			"avg_token_saved":            item.tokensSaved / count,
		}
	}
	return summary
}

func metricValue(metrics map[string]any, key string) float64 {
	value, _ := evalMetricFloat(metrics[key])
	return value
}

func evalRunSummaryMetrics(raw json.RawMessage) map[string]any {
	if len(raw) == 0 {
		return map[string]any{}
	}
	var summary map[string]any
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	if err := decoder.Decode(&summary); err != nil {
		return map[string]any{}
	}
	metrics, _ := summary["metrics"].(map[string]any)
	if metrics == nil {
		return map[string]any{}
	}
	return metrics
}

func buildEvalTrendDelta(first, latest EvalTrendPoint) map[string]any {
	delta := map[string]any{
		"success_rate": latest.SuccessRate - first.SuccessRate,
		"passed_cases": latest.PassedCases - first.PassedCases,
		"failed_cases": latest.FailedCases - first.FailedCases,
	}
	for _, key := range []string{
		"avg_score",
		"avg_latency_ms",
		"avg_tool_call_accuracy",
		"avg_schema_compliance",
		"avg_json_schema_compliance",
		"avg_reference_hit_rate",
		"avg_retrieval_hit_rate",
		"avg_mrr",
		"avg_ndcg",
		"avg_citation_rate",
		"avg_candidate_size",
		"avg_total_tokens",
		"avg_token_saved",
		"avg_rules_token_cost",
		"avg_rules_dynamic_budget",
		"avg_provider_prompt_tokens",
		"avg_token_estimation_error",
		"avg_rule_round_count",
		"max_iteration_exceeded_rate",
		"max_tool_calls_exceeded_rate",
		"human_approval_waiting_rate",
		"reflection_repair_attempted_rate",
		"failed_cases_with_runtime_error_rate",
	} {
		firstValue, firstOK := evalMetricFloat(first.Metrics[key])
		latestValue, latestOK := evalMetricFloat(latest.Metrics[key])
		if firstOK || latestOK {
			delta[key] = latestValue - firstValue
		}
	}
	return delta
}

func averageEvalResultScore(results []workflow.EvalResult) float64 {
	if len(results) == 0 {
		return 0
	}
	total := 0.0
	for _, result := range results {
		total += result.Score
	}
	return total / float64(len(results))
}

func averageEvalResultLatency(results []workflow.EvalResult) float64 {
	if len(results) == 0 {
		return 0
	}
	total := 0.0
	for _, result := range results {
		total += float64(result.LatencyMS)
	}
	return total / float64(len(results))
}

func averageMetric(results []workflow.EvalResult, key string) float64 {
	if len(results) == 0 {
		return 0
	}
	total := 0.0
	for _, result := range results {
		var metrics map[string]any
		if len(result.MetricsJSON) == 0 || json.Unmarshal(result.MetricsJSON, &metrics) != nil {
			continue
		}
		if value, ok := evalMetricFloat(metrics[key]); ok {
			total += value
		}
	}
	return total / float64(len(results))
}

func evalMetricFloat(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case int:
		return float64(typed), true
	case json.Number:
		number, err := typed.Float64()
		return number, err == nil
	case bool:
		if typed {
			return 1, true
		}
		return 0, true
	default:
		return 0, false
	}
}

func runtimeErrorRate(results []workflow.EvalResult) float64 {
	if len(results) == 0 {
		return 0
	}
	failed := 0
	for _, result := range results {
		if strings.TrimSpace(result.ErrorMessage) != "" {
			failed++
		}
	}
	return float64(failed) / float64(len(results))
}

func scoreEvalOutput(output engine.NodeOutput, expectedJSON, requiredToolsJSON json.RawMessage) (float64, string) {
	result := scoreEvalOutputDetailed(output, expectedJSON, requiredToolsJSON)
	return result.Score, result.Reason
}

type evalScoreResult struct {
	Score   float64
	Reason  string
	Metrics map[string]any
}

// scoreEvalOutputDetailed evaluates a node execution output against expected constraints,
// including tool usage, schema validation, status matching, content checks,
// reference requirements, and exact answer equality.
//
// It returns a structured evaluation result with a final score, reason, and detailed metrics.
func scoreEvalOutputDetailed(output engine.NodeOutput, expectedJSON, requiredToolsJSON json.RawMessage) evalScoreResult {
	if output == nil {
		output = engine.NodeOutput{}
	}
	metrics := baseEvalMetrics(output)
	if len(requiredToolsJSON) > 0 {
		var required []string
		if err := json.Unmarshal(requiredToolsJSON, &required); err == nil && len(required) > 0 {
			called := extractToolNames(output)
			requiredToolsOK, missingTool := mustHaveAllTools(required, called)
			metrics["tool_call_accuracy"] = evalharness.Coverage(required, toolNamesWithAliases(called))
			metrics["required_tools"] = required
			metrics["actual_tools"] = sortedToolNames(called)
			if !requiredToolsOK {
				return evalScoreResult{
					Score:   0,
					Reason:  "required tool was not called: " + missingTool,
					Metrics: metrics,
				}
			}
		}
	}
	if _, exists := metrics["tool_call_accuracy"]; !exists {
		metrics["tool_call_accuracy"] = 1.0
		metrics["actual_tools"] = sortedToolNames(extractToolNames(output))
	}
	if len(expectedJSON) == 0 {
		metrics["score"] = 1.0
		metrics["reason"] = "no expected_json configured"
		return evalScoreResult{
			Score:   1,
			Reason:  "no expected_json configured",
			Metrics: metrics,
		}
	}
	var expected map[string]any
	if err := json.Unmarshal(expectedJSON, &expected); err != nil {
		metrics["score"] = 0.0
		metrics["reason"] = "expected_json is invalid: " + err.Error()
		return evalScoreResult{
			Score:   0,
			Reason:  "expected_json is invalid: " + err.Error(),
			Metrics: metrics,
		}
	}
	mergeEvalMetrics(metrics, ragEvalMetrics(output, expected))
	if schema, ok := expectedSchema(expected); ok {
		if err := validateEvalSchema(schema, output); err != nil {
			metrics["schema_compliance"] = 0.0
			metrics["json_schema_compliance"] = 0.0
			metrics["score"] = 0.0
			metrics["reason"] = "schema mismatch: " + err.Error()
			return evalScoreResult{Score: 0, Reason: "schema mismatch: " + err.Error(), Metrics: metrics}
		}
		metrics["schema_compliance"] = 1.0
		metrics["json_schema_compliance"] = 1.0
	} else {
		metrics["schema_compliance"] = 1.0
		metrics["json_schema_compliance"] = 1.0
	}
	if status, ok := expected["status"].(string); ok && status != "" {
		actualStatus, _ := output["status"].(string)
		if actualStatus != status {
			metrics["score"] = 0.0
			metrics["reason"] = fmt.Sprintf("status mismatch: expected %s got %s", status, actualStatus)
			return evalScoreResult{
				Score:   0,
				Reason:  fmt.Sprintf("status mismatch: expected %s got %s", status, actualStatus),
				Metrics: metrics,
			}
		}
	}
	if contains, exists := expected["contains"]; exists {
		text := strings.ToLower(outputText(output))
		for _, expectedText := range expectedContainsList(contains) {
			if !strings.Contains(text, strings.ToLower(expectedText)) {
				metrics["score"] = 0.0
				metrics["reason"] = "output does not contain expected text: " + expectedText
				return evalScoreResult{
					Score:   0,
					Reason:  "output does not contain expected text: " + expectedText,
					Metrics: metrics,
				}
			}
		}
	}
	if minRefs := expectedMinReferences(expected); minRefs > 0 {
		refCount := referenceCount(output)
		metrics["reference_count"] = refCount
		metrics["reference_hit_rate"] = clampEvalRatio(float64(refCount) / float64(minRefs))
		if refCount < minRefs {
			metrics["score"] = 0.0
			metrics["reason"] = fmt.Sprintf("references below required minimum: expected at least %d got %d", minRefs, refCount)
			return evalScoreResult{
				Score:   0,
				Reason:  fmt.Sprintf("references below required minimum: expected at least %d got %d", minRefs, refCount),
				Metrics: metrics,
			}
		}
	} else {
		metrics["reference_count"] = referenceCount(output)
		metrics["reference_hit_rate"] = 1.0
	}
	if equals, ok := expected["equals"]; ok {
		actual := output["final_answer"]
		if actual == nil {
			actual = output["answer"]
		}
		expectedBytes, _ := json.Marshal(equals)
		actualBytes, _ := json.Marshal(actual)
		if string(expectedBytes) != string(actualBytes) {
			metrics["score"] = 0.0
			metrics["reason"] = "final answer does not equal expected value"
			return evalScoreResult{
				Score:   0,
				Reason:  "final answer does not equal expected value",
				Metrics: metrics,
			}
		}
	}
	metrics["score"] = 1.0
	metrics["reason"] = "matched expected_json"
	return evalScoreResult{
		Score:   1,
		Reason:  "matched expected_json",
		Metrics: metrics,
	}
}

// gather base merics
func baseEvalMetrics(output engine.NodeOutput) map[string]any {
	stopReason, _ := output["stop_reason"].(string)
	metrics := map[string]any{
		"stop_reason":                 stopReason,
		"max_iteration_exceeded":      stopReason == runtimeagent.StopReasonMaxIterations, // bool
		"human_approval_waiting":      stopReason == runtimeagent.StopReasonWaitingHuman,  // bool
		"latency_ms":                  numberFromOutput(output, "latency_ms"),
		"total_tokens":                numberFromOutput(output, "total_tokens"),
		"token_saved":                 tokenSavedFromOutput(output),
		"json_schema_compliance":      1.0,
		"schema_compliance":           1.0,
		"tool_call_accuracy":          1.0,
		"reference_count":             referenceCount(output),
		"reference_hit_rate":          1.0,
		"max_tool_calls_exceeded":     stopReason == runtimeagent.StopReasonMaxToolCalls,
		"reflection_repair_attempted": hasStepType(output, runtimeagent.StepTypeReflection),
		"rules_token_cost":            nestedMetricFloatPath(output["context_trace"], "rule_trace", "estimated_used"),
		"rules_dynamic_budget":        nestedMetricFloatPath(output["context_trace"], "rule_budget", "available_rule_tokens"),
		"provider_prompt_tokens":      nestedMetricFloat(output["context_trace"], "provider_prompt_tokens"),
		"token_estimation_error":      nestedMetricFloat(output["context_trace"], "token_estimation_error"),
		"rule_round_count":            nestedMetricSliceLength(output["context_trace"], "rule_rounds"),
		"rule_set_version":            nestedMetricString(output["context_trace"], "rule_set_version"),
	}
	return metrics
}

func expectedSchema(expected map[string]any) (map[string]any, bool) {
	for _, key := range []string{"json_schema", "schema"} {
		if raw, ok := expected[key].(map[string]any); ok && len(raw) > 0 {
			return raw, true
		}
	}
	return nil, false
}

func validateEvalSchema(schema map[string]any, output engine.NodeOutput) error {
	value := output["structured_output"]
	if value == nil {
		value = output["final_answer"]
		if text, ok := value.(string); ok {
			var parsed any
			if err := json.Unmarshal([]byte(text), &parsed); err != nil {
				return fmt.Errorf("final_answer is not valid JSON")
			}
			value = parsed
		}
	}
	if value == nil {
		return fmt.Errorf("structured output is missing")
	}
	return validateEvalSchemaValue(schema, value)
}

func validateEvalSchemaValue(schema map[string]any, value any) error {
	if typ, _ := schema["type"].(string); typ != "" {
		if err := validateEvalJSONType(typ, value); err != nil {
			return err
		}
	}
	properties, _ := schema["properties"].(map[string]any)
	required, _ := schema["required"].([]any)
	if len(properties) == 0 && len(required) == 0 {
		return nil
	}
	obj, ok := value.(map[string]any)
	if !ok {
		return fmt.Errorf("expected object")
	}
	for _, item := range required {
		key, _ := item.(string)
		if key == "" {
			continue
		}
		if _, ok := obj[key]; !ok {
			return fmt.Errorf("missing required field %s", key)
		}
	}
	for key, spec := range properties {
		value, exists := obj[key]
		if !exists {
			continue
		}
		specMap, ok := spec.(map[string]any)
		if !ok {
			continue
		}
		typ, _ := specMap["type"].(string)
		if typ == "" {
			continue
		}
		if err := validateEvalJSONType(typ, value); err != nil {
			return fmt.Errorf("field %s: %w", key, err)
		}
	}
	return nil
}

func validateEvalJSONType(typ string, value any) error {
	switch typ {
	case "object":
		if _, ok := value.(map[string]any); !ok {
			return fmt.Errorf("expected object")
		}
	case "array":
		if _, ok := value.([]any); !ok {
			return fmt.Errorf("expected array")
		}
	case "string":
		if _, ok := value.(string); !ok {
			return fmt.Errorf("expected string")
		}
	case "number":
		switch value.(type) {
		case float64, int, int64, json.Number:
		default:
			return fmt.Errorf("expected number")
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("expected boolean")
		}
	}
	return nil
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

func expectedMinReferences(expected map[string]any) int {
	if value, ok := expected["min_references"]; ok {
		number, numberOK := evalMetricFloat(value)
		if numberOK && number > 0 {
			return int(number)
		}
	}
	switch value := expected["require_references"].(type) {
	case bool:
		if value {
			return 1
		}
	case string:
		if strings.EqualFold(strings.TrimSpace(value), "true") {
			return 1
		}
	}
	return 0
}

func referenceCount(output engine.NodeOutput) int {
	if output == nil {
		return 0
	}
	bytes, err := json.Marshal(output)
	if err != nil {
		return 0
	}
	var decoded map[string]any
	if err := json.Unmarshal(bytes, &decoded); err != nil {
		return 0
	}
	total := 0
	seen := map[string]bool{}
	for _, key := range []string{"references", "citations", "results"} {
		total += referenceCountFromValue(decoded[key], seen)
	}
	return total
}

func referenceCountFromValue(value any, seen map[string]bool) int {
	switch typed := value.(type) {
	case []any:
		total := 0
		for _, item := range typed {
			total += referenceCountFromValue(item, seen)
		}
		return total
	case map[string]any:
		if !referenceLike(typed) {
			return 0
		}
		key := referenceIdentity(typed)
		if seen[key] {
			return 0
		}
		seen[key] = true
		return 1
	default:
		return 0
	}
}

func referenceLike(item map[string]any) bool {
	for _, key := range []string{"chunk_id", "document_id", "kb_id", "quote_text", "document_name", "source", "content"} {
		if _, ok := item[key]; ok {
			return true
		}
	}
	return false
}

func referenceIdentity(item map[string]any) string {
	parts := make([]string, 0, 4)
	for _, key := range []string{"kb_id", "document_id", "chunk_id", "ref_index"} {
		if value, ok := item[key]; ok {
			parts = append(parts, fmt.Sprintf("%s=%v", key, value))
		}
	}
	if len(parts) > 0 {
		return strings.Join(parts, "|")
	}
	bytes, _ := json.Marshal(item)
	return string(bytes)
}

func clampEvalRatio(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
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

func numberFromOutput(output engine.NodeOutput, key string) int {
	switch value := output[key].(type) {
	case int:
		return value
	case int64:
		return int(value)
	case float64:
		return int(value)
	case json.Number:
		i, _ := value.Int64()
		return int(i)
	default:
		return 0
	}
}

func tokenSavedFromOutput(output engine.NodeOutput) int {
	if output == nil {
		return 0
	}
	for _, key := range []string{"token_saved", "saved_tokens"} {
		if value, ok := evalMetricFloat(output[key]); ok {
			return int(value)
		}
	}
	return int(nestedMetricFloat(output["context_trace"], "saved_tokens"))
}

func ragEvalMetrics(output engine.NodeOutput, expected map[string]any) map[string]any {
	expectedDocs := expectedStringList(expected["expected_doc_ids"], expected["expected_docs"], expected["required_doc_ids"])
	requiredCitations := expectedStringList(expected["required_citations"], expected["expected_citations"])
	if len(expectedDocs) == 0 && len(requiredCitations) == 0 {
		return nil
	}
	hits := retrievalHitsFromOutput(output)
	actualCitations := citationsFromOutput(output)
	metrics := evalharness.ScoreRAG(evalharness.RAGCase{ExpectedDocIDs: expectedDocs, RequiredCitations: requiredCitations}, hits, actualCitations)
	return map[string]any{
		"retrieval_hit_rate": metrics.HitRate,
		"mrr":                metrics.MRR,
		"ndcg":               metrics.NDCG,
		"citation_rate":      metrics.CitationRate,
		"candidate_size":     metrics.CandidateSize,
	}
}

func mergeEvalMetrics(dst map[string]any, src map[string]any) {
	for key, value := range src {
		dst[key] = value
	}
}

func expectedStringList(values ...any) []string {
	for _, value := range values {
		items := stringList(value)
		if len(items) > 0 {
			return items
		}
	}
	return nil
}

func stringList(value any) []string {
	switch typed := value.(type) {
	case string:
		if strings.TrimSpace(typed) == "" {
			return nil
		}
		return []string{strings.TrimSpace(typed)}
	case []string:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if strings.TrimSpace(item) != "" {
				out = append(out, strings.TrimSpace(item))
			}
		}
		return out
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if text := strings.TrimSpace(fmt.Sprint(item)); text != "" && text != "<nil>" {
				out = append(out, text)
			}
		}
		return out
	default:
		return nil
	}
}

func retrievalHitsFromOutput(output engine.NodeOutput) []evalharness.RetrievalHit {
	items := referenceMaps(output)
	hits := make([]evalharness.RetrievalHit, 0, len(items))
	for i, item := range items {
		docID := firstStringField(item, "doc_id", "document_id", "kb_id", "source")
		if docID == "" {
			continue
		}
		score := nestedMetricFloat(item, "score")
		if score == 0 {
			score = 1 / float64(i+1)
		}
		hits = append(hits, evalharness.RetrievalHit{DocID: docID, Score: score})
	}
	return hits
}

func citationsFromOutput(output engine.NodeOutput) []string {
	items := referenceMaps(output)
	seen := map[string]bool{}
	out := make([]string, 0, len(items))
	for _, item := range items {
		for _, value := range []string{firstStringField(item, "citation_id", "chunk_id", "document_id", "doc_id", "source")} {
			if value != "" && !seen[value] {
				seen[value] = true
				out = append(out, value)
			}
		}
	}
	return out
}

func referenceMaps(output engine.NodeOutput) []map[string]any {
	if output == nil {
		return nil
	}
	items := make([]map[string]any, 0)
	for _, key := range []string{"results", "references", "citations"} {
		items = append(items, mapsFromValue(output[key])...)
	}
	return items
}

func mapsFromValue(value any) []map[string]any {
	switch typed := value.(type) {
	case []map[string]any:
		return typed
	case []any:
		out := make([]map[string]any, 0, len(typed))
		for _, item := range typed {
			out = append(out, mapsFromValue(item)...)
		}
		return out
	case map[string]any:
		return []map[string]any{typed}
	default:
		bytes, err := json.Marshal(value)
		if err != nil || string(bytes) == "null" {
			return nil
		}
		var decoded any
		if err := json.Unmarshal(bytes, &decoded); err != nil {
			return nil
		}
		switch decoded.(type) {
		case []any, map[string]any:
			return mapsFromValue(decoded)
		default:
			return nil
		}
	}
}

func firstStringField(item map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := item[key]; ok {
			text := strings.TrimSpace(fmt.Sprint(value))
			if text != "" && text != "<nil>" {
				return text
			}
		}
	}
	return ""
}

func nestedMetricFloat(value any, key string) float64 {
	if value == nil {
		return 0
	}
	if item, ok := value.(map[string]any); ok {
		if number, numberOK := evalMetricFloat(item[key]); numberOK {
			return number
		}
	}
	bytes, err := json.Marshal(value)
	if err != nil || string(bytes) == "null" {
		return 0
	}
	var decoded map[string]any
	if err := json.Unmarshal(bytes, &decoded); err != nil {
		return 0
	}
	if number, ok := evalMetricFloat(decoded[key]); ok {
		return number
	}
	return 0
}

func nestedMetricFloatPath(value any, keys ...string) float64 {
	current := nestedMetricObject(value)
	for index, key := range keys {
		value, ok := current[key]
		if !ok {
			return 0
		}
		if index == len(keys)-1 {
			number, _ := evalMetricFloat(value)
			return number
		}
		current = nestedMetricObject(value)
	}
	return 0
}

func nestedMetricSliceLength(value any, key string) int {
	items, ok := nestedMetricObject(value)[key].([]any)
	if ok {
		return len(items)
	}
	bytes, _ := json.Marshal(value)
	var decoded map[string]any
	if json.Unmarshal(bytes, &decoded) != nil {
		return 0
	}
	items, _ = decoded[key].([]any)
	return len(items)
}

func nestedMetricString(value any, key string) string {
	if text, ok := nestedMetricObject(value)[key].(string); ok {
		return text
	}
	bytes, _ := json.Marshal(value)
	var decoded map[string]any
	if json.Unmarshal(bytes, &decoded) != nil {
		return ""
	}
	text, _ := decoded[key].(string)
	return text
}

func nestedMetricObject(value any) map[string]any {
	if item, ok := value.(map[string]any); ok {
		return item
	}
	bytes, err := json.Marshal(value)
	if err != nil || string(bytes) == "null" {
		return map[string]any{}
	}
	var decoded map[string]any
	if json.Unmarshal(bytes, &decoded) != nil {
		return map[string]any{}
	}
	return decoded
}

func stableJSONHash(value any) string {
	bytes, _ := json.Marshal(value)
	sum := sha256.Sum256(bytes)
	return hex.EncodeToString(sum[:])
}

func sortedToolNames(called map[string]bool) []string {
	names := make([]string, 0, len(called))
	for name := range called {
		if strings.TrimSpace(name) != "" {
			names = append(names, strings.TrimSpace(name))
		}
	}
	sort.Strings(names)
	return names
}

func mustHaveAllTools(required []string, called map[string]bool) (bool, string) {
	for _, name := range required {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if !requiredToolCalled(called, name) {
			return false, name
		}
	}
	return true, ""
}

func requiredToolCalled(called map[string]bool, required string) bool {
	required = strings.TrimSpace(required)
	if required == "" {
		return false
	}
	if called[required] {
		return true
	}
	for _, alias := range toolNameAliases(required) {
		if called[alias] {
			return true
		}
	}
	return false
}

func toolNamesWithAliases(called map[string]bool) []string {
	names := sortedToolNames(called)
	for _, name := range names {
		for _, alias := range toolNameAliases(name) {
			if strings.TrimSpace(alias) != "" && !called[alias] {
				names = append(names, strings.TrimSpace(alias))
			}
		}
	}
	return names
}

func toolNameAliases(name string) []string {
	switch strings.TrimSpace(name) {
	case "call_agent":
		return []string{"call_workflow"}
	case "call_workflow":
		return []string{"call_agent"}
	default:
		return nil
	}
}

func hasStepType(output engine.NodeOutput, stepType string) bool {
	if stepType == "" {
		return false
	}
	if rawSteps, ok := output["steps"].([]any); ok {
		for _, raw := range rawSteps {
			if step, ok := raw.(map[string]any); ok {
				if typ, _ := step["type"].(string); typ == stepType {
					return true
				}
			}
		}
	}
	stepsJSON, err := json.Marshal(output["steps"])
	if err != nil || string(stepsJSON) == "null" {
		return false
	}
	var steps []struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(stepsJSON, &steps); err != nil {
		return false
	}
	for _, step := range steps {
		if step.Type == stepType {
			return true
		}
	}
	return false
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

func normalizeWorkflowCallChain(chain []int64, callerAgentID int64) []int64 {
	normalized := make([]int64, 0, len(chain)+1)
	for _, id := range chain {
		if id > 0 && !containsWorkflowID(normalized, id) {
			normalized = append(normalized, id)
		}
	}
	if callerAgentID > 0 && !containsWorkflowID(normalized, callerAgentID) {
		normalized = append(normalized, callerAgentID)
	}
	return normalized
}

func containsWorkflowID(chain []int64, workflowID int64) bool {
	if workflowID <= 0 {
		return false
	}
	for _, id := range chain {
		if id == workflowID {
			return true
		}
	}
	return false
}

func normalizePositiveIDs(ids []int64) []int64 {
	normalized := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id > 0 && !containsWorkflowID(normalized, id) {
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
		return workflow.RunStatusWaitingHuman
	case "timeout":
		return workflow.RunStatusTimeout
	default:
		return ""
	}
}

func defaultWorkflowProfile(ownerID, workflowID int64, name, description string) *workflow.Profile {
	role := strings.TrimSpace(name)
	if role == "" {
		role = "Assistant"
	}
	goal := strings.TrimSpace(description)
	if goal == "" {
		goal = "Complete user tasks through the configured workflow and tools."
	}
	return &workflow.Profile{
		OwnerID:                     ownerID,
		WorkflowID:                  workflowID,
		Role:                        role,
		Goal:                        goal,
		SystemPrompt:                "You are a helpful, careful AgentCanvas workflow. Use available tools when needed and explain final results clearly.",
		MaxIterations:               10,
		MaxExecutionTimeMS:          120000,
		DefaultToolPackIDs:          json.RawMessage("[]"),
		DefaultToolIDs:              json.RawMessage("[]"),
		DefaultSkillIDs:             json.RawMessage("[]"),
		DefaultMCPServerIDs:         json.RawMessage("[]"),
		DefaultKnowledgeIDs:         json.RawMessage("[]"),
		DefaultCallWorkflowIDs:      json.RawMessage("[]"),
		DefaultKnowledgeTopK:        5,
		DefaultKnowledgeMode:        string(retrieval.ModeHybrid),
		DefaultMaxWorkflowCallDepth: 3,
		OutputSchemaJSON:            json.RawMessage("{}"),
		ToolPolicyJSON:              json.RawMessage("{}"),
		MemoryPolicyJSON:            json.RawMessage("{}"),
		ReflectionPolicyJSON:        mustMarshalJSON(reflection.DefaultPolicy()),
		ContextPolicyJSON:           json.RawMessage("{}"),
		RiskLevel:                   toolruntime.RiskMedium,
		Mode:                        "react",
	}
}
