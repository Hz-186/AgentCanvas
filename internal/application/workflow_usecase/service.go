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

	"agentcanvas/internal/domain/conversation"
	"agentcanvas/internal/domain/flow"
	"agentcanvas/internal/domain/memory"
	providerdomain "agentcanvas/internal/domain/provider"
	"agentcanvas/internal/domain/retrieval"
	"agentcanvas/internal/domain/tool"
	"agentcanvas/internal/domain/workflow"
	cryptoinfra "agentcanvas/internal/infrastructure/crypto"
	"agentcanvas/internal/infrastructure/llm"
	runtimeagent "agentcanvas/internal/runtime/agent"
	"agentcanvas/internal/runtime/engine"
	"agentcanvas/internal/runtime/evalharness"
	runtimeevent "agentcanvas/internal/runtime/event"
	runtimenode "agentcanvas/internal/runtime/node"
	"agentcanvas/internal/runtime/toolruntime"

	agenterrors "agentcanvas/internal/pkg/errors"

	"gorm.io/gorm"
)

type Service struct {
	workflows       workflow.Repository
	profiles        workflow.ProfileRepository
	versions        workflow.WorkflowVersionRepository
	runs            workflow.RunRepository
	events          workflow.RunEventRepository
	nodeLogs        workflow.NodeLogRepository
	runSteps        workflow.RunStepRepository
	evals           workflow.EvalDatasetRepository
	approvals       workflow.ApprovalRepository
	teams           workflow.TeamRepository
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
	teams workflow.TeamRepository,
	memories memory.Repository,
	memoryLogs memory.WriteLogRepository,
	tools tool.DefinitionRepository,
	toolPacks tool.PackRepository,
	mcpServers tool.MCPRepository,
	toolInvocations tool.InvocationRepository,
	providers providerdomain.Repository,
	messages conversation.MessageRepository,
	retriever retrieval.Retriever,
	llmClient llm.ChatClient,
	secrets *cryptoinfra.SecretBox,
) *Service {
	s := &Service{
		workflows:       workflows,
		profiles:        profiles,
		versions:        versions,
		runs:            runs,
		events:          events,
		nodeLogs:        nodeLogs,
		runSteps:        runSteps,
		evals:           evals,
		approvals:       approvals,
		teams:           teams,
		memories:        memories,
		memoryLogs:      memoryLogs,
		tools:           tools,
		toolPacks:       toolPacks,
		mcpServers:      mcpServers,
		toolInvocations: toolInvocations,
		providers:       providers,
		messages:        messages,
		retriever:       retriever,
		llm:             llmClient,
		secrets:         secrets,
	}
	s.executor = engine.NewExecutor(
		runtimenode.DefaultNodes(
			runtimenode.Deps{
				Retriever:       retriever,
				LLM:             llmClient,
				Providers:       s,
				Messages:        s,
				MessageHistory:  messages,
				Memories:        memories,
				MemoryWriteLogs: memoryLogs,
				Tools:           tools,
				ToolPacks:       toolPacks,
				MCPServers:      mcpServers,
				ToolInvocations: toolInvocations,
				WorkflowCaller:  s,
				Profiles:        s,
				Teams:           teams,
			},
		),
	)
	s.validator = flow.NewValidator(s.executor)
	s.runCancels = newRunCancelRegistry()
	return s
}

type runOptions struct {
	ParentRunID       *int64 // nil or id
	CallerNodeID      string // "" or like "node_3a7f2b1c"
	CallDepth         int
	WorkflowCallChain []int64
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
	DefaultMCPServerIDs         *[]int64         `json:"default_mcp_server_ids"`
	DefaultKnowledgeIDs         *[]int64         `json:"default_knowledge_ids"`
	DefaultKnowledgeTopK        *int             `json:"default_knowledge_top_k"`
	DefaultKnowledgeMode        *string          `json:"default_knowledge_mode"`
	DefaultCallWorkflowIDs      *[]int64         `json:"default_call_workflow_ids"`
	DefaultMaxWorkflowCallDepth *int             `json:"default_max_workflow_call_depth"`
	OutputSchemaJSON            *json.RawMessage `json:"output_schema_json"`
	ToolPolicyJSON              *json.RawMessage `json:"tool_policy_json"`
	MemoryPolicyJSON            *json.RawMessage `json:"memory_policy_json"`
	ContextPolicyJSON           *json.RawMessage `json:"context_policy_json"`
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
	if req.ContextPolicyJSON != nil {
		contextPolicy, err := normalizeOptionalRawJSON(*req.ContextPolicyJSON, "context_policy_json")
		if err != nil {
			return nil, err
		}
		profile.ContextPolicyJSON = contextPolicy
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
		if mode != "" && mode != "react" && mode != "plan_execute" && mode != "reflect" && mode != "supervisor" {
			return nil, fmt.Errorf("%w: mode must be react, plan_execute, reflect, or supervisor", agenterrors.ErrInvalidInput)
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

func (s *Service) CreateEvalDataset(
	ctx context.Context,
	ownerID, workflowID int64,
	req CreateEvalDatasetRequest,
) (*workflow.EvalDataset, error) {
	if s.evals == nil {
		return nil, fmt.Errorf("%w: eval repository is not configured", agenterrors.ErrInvalidInput)
	}
	if _, err := s.GetWorkflow(ctx, ownerID, workflowID); err != nil {
		return nil, err
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, fmt.Errorf("%w: eval dataset name is required", agenterrors.ErrInvalidInput)
	}
	item := &workflow.EvalDataset{
		OwnerID:     ownerID,
		WorkflowID:  workflowID,
		Name:        name,
		Description: strings.TrimSpace(req.Description),
		Status:      workflow.EvalDatasetStatusActive,
	}
	if err := s.evals.CreateDataset(ctx, item); err != nil {
		return nil, err
	}
	return item, nil
}

func (s *Service) ListEvalDatasets(ctx context.Context, ownerID, workflowID int64) ([]workflow.EvalDataset, error) {
	if s.evals == nil {
		return nil, fmt.Errorf("%w: eval repository is not configured", agenterrors.ErrInvalidInput)
	}
	if _, err := s.GetWorkflow(ctx, ownerID, workflowID); err != nil {
		return nil, err
	}
	return s.evals.ListDatasetsByWorkflow(ctx, ownerID, workflowID)
}

func (s *Service) CreateEvalCase(
	ctx context.Context,
	ownerID, datasetID int64,
	req CreateEvalCaseRequest,
) (*workflow.EvalCase, error) {
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
	item := &workflow.EvalCase{
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

func (s *Service) ListEvalCases(
	ctx context.Context,
	ownerID, datasetID int64,
) ([]workflow.EvalCase, error) {
	if s.evals == nil {
		return nil, fmt.Errorf("%w: eval repository is not configured", agenterrors.ErrInvalidInput)
	}
	if _, err := s.evals.FindDatasetByID(ctx, ownerID, datasetID); err != nil {
		return nil, mapNotFound(err)
	}
	return s.evals.ListCasesByDataset(ctx, ownerID, datasetID)
}

func (s *Service) RunEvalDataset(ctx context.Context, ownerID, datasetID int64, req RunEvalDatasetRequest) (*workflow.EvalRun, []workflow.EvalResult, error) {
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
	evalRun := &workflow.EvalRun{
		OwnerID:       ownerID,
		WorkflowID:    dataset.WorkflowID,
		DatasetID:     dataset.ID,
		FlowVersionID: req.FlowVersionID,
		Status:        workflow.EvalRunStatusRunning,
		TotalCases:    len(cases),
		StartedAt:     started,
	}
	if err := s.evals.CreateEvalRun(ctx, evalRun); err != nil {
		return nil, nil, err
	}
	results := make([]workflow.EvalResult, 0, len(cases))
	for _, evalCase := range cases {
		caseStarted := time.Now().UTC()
		input := map[string]any{}
		if err := json.Unmarshal(evalCase.InputJSON, &input); err != nil {
			result := failedEvalResult(
				ownerID,
				evalRun.ID,
				evalCase.ID,
				nil, nil,
				"invalid input_json: "+err.Error(),
				int(time.Since(caseStarted).Milliseconds()),
			)
			_ = s.evals.CreateEvalResult(ctx, result)
			results = append(results, *result)
			continue
		}
		agentRun, output, runErr := s.RunWorkflow(ctx,
			ownerID,
			dataset.WorkflowID,
			RunWorkflowRequest{
				FlowVersionID: req.FlowVersionID,
				Input:         input,
			},
		)
		var agentRunID *int64
		if agentRun != nil {
			agentRunID = &agentRun.ID
		}
		outputJSON, _ := json.Marshal(output)
		if runErr != nil {
			result := failedEvalResult(
				ownerID,
				evalRun.ID,
				evalCase.ID,
				agentRunID,
				outputJSON,
				runErr.Error(),
				int(time.Since(caseStarted).Milliseconds()),
			)
			if err := s.evals.CreateEvalResult(ctx, result); err != nil {
				return evalRun, results, err
			}
			results = append(results, *result)
			continue
		}
		scoreResult := scoreEvalOutputDetailed(output, evalCase.ExpectedJSON, evalCase.RequiredToolsJSON)
		score, reason := scoreResult.Score, scoreResult.Reason
		status := "failed"
		if score >= 1 {
			status = "passed"
		}
		metricsJSON, _ := json.Marshal(scoreResult.Metrics)
		result := &workflow.EvalResult{
			OwnerID:       ownerID,
			EvalRunID:     evalRun.ID,
			EvalCaseID:    evalCase.ID,
			WorkflowRunID: agentRunID,
			Status:        status,
			Score:         score,
			Reason:        reason,
			OutputJSON:    outputJSON,
			MetricsJSON:   metricsJSON,
			LatencyMS:     int(time.Since(caseStarted).Milliseconds()),
			CreatedAt:     time.Now().UTC(),
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
	evalRun.Status = workflow.EvalRunStatusCompleted
	evalRun.FinishedAt = &finished
	evalRun.SummaryJSON, _ = json.Marshal(map[string]any{
		"total_cases":  evalRun.TotalCases,
		"passed_cases": evalRun.PassedCases,
		"failed_cases": evalRun.FailedCases,
		"success_rate": evalRun.SuccessRate,
		"metrics":      summarizeEvalMetrics(results),
	})
	if err := s.evals.UpdateEvalRun(ctx, evalRun); err != nil {
		return evalRun, results, err
	}
	return evalRun, results, nil
}

func (s *Service) ListEvalRuns(ctx context.Context, ownerID, datasetID int64) ([]workflow.EvalRun, error) {
	if s.evals == nil {
		return nil, fmt.Errorf("%w: eval repository is not configured", agenterrors.ErrInvalidInput)
	}
	if _, err := s.evals.FindDatasetByID(ctx, ownerID, datasetID); err != nil {
		return nil, mapNotFound(err)
	}
	return s.evals.ListEvalRunsByDataset(ctx, ownerID, datasetID)
}

func (s *Service) GetEvalTrend(ctx context.Context, ownerID, datasetID int64) (*EvalTrend, error) {
	if s.evals == nil {
		return nil, fmt.Errorf("%w: eval repository is not configured", agenterrors.ErrInvalidInput)
	}
	dataset, err := s.evals.FindDatasetByID(ctx, ownerID, datasetID)
	if err != nil {
		return nil, mapNotFound(err)
	}
	runs, err := s.evals.ListEvalRunsByDataset(ctx, ownerID, datasetID)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(runs, func(i, j int) bool {
		if runs[i].StartedAt.Equal(runs[j].StartedAt) {
			return runs[i].ID < runs[j].ID
		}
		return runs[i].StartedAt.Before(runs[j].StartedAt)
	})
	points := make([]EvalTrendPoint, 0, len(runs))
	for _, run := range runs {
		points = append(points, EvalTrendPoint{
			EvalRunID:     run.ID,
			FlowVersionID: run.FlowVersionID,
			Status:        run.Status,
			TotalCases:    run.TotalCases,
			PassedCases:   run.PassedCases,
			FailedCases:   run.FailedCases,
			SuccessRate:   run.SuccessRate,
			Metrics:       evalRunSummaryMetrics(run.SummaryJSON),
			StartedAt:     run.StartedAt,
			FinishedAt:    run.FinishedAt,
		})
	}
	trend := &EvalTrend{
		DatasetID:    dataset.ID,
		WorkflowID:   dataset.WorkflowID,
		Points:       points,
		Delta:        map[string]any{},
		TrendSummary: map[string]any{"run_count": len(points)},
	}
	if len(points) == 0 {
		return trend, nil
	}
	latest := points[len(points)-1]
	best := latest
	for _, point := range points {
		if point.SuccessRate > best.SuccessRate || (point.SuccessRate == best.SuccessRate && point.StartedAt.After(best.StartedAt)) {
			best = point
		}
	}
	trend.Latest = &latest
	trend.Best = &best
	trend.Delta = buildEvalTrendDelta(points[0], latest)
	trend.TrendSummary = map[string]any{
		"run_count":              len(points),
		"latest_eval_run_id":     latest.EvalRunID,
		"latest_flow_version_id": latest.FlowVersionID,
		"latest_success_rate":    latest.SuccessRate,
		"best_eval_run_id":       best.EvalRunID,
		"best_flow_version_id":   best.FlowVersionID,
		"best_success_rate":      best.SuccessRate,
		"success_rate_delta":     latest.SuccessRate - points[0].SuccessRate,
	}
	return trend, nil
}

func (s *Service) ListEvalResults(ctx context.Context, ownerID, evalRunID int64) ([]workflow.EvalResult, error) {
	if s.evals == nil {
		return nil, fmt.Errorf("%w: eval repository is not configured", agenterrors.ErrInvalidInput)
	}
	if _, err := s.evals.FindEvalRunByID(ctx, ownerID, evalRunID); err != nil {
		return nil, mapNotFound(err)
	}
	return s.evals.ListEvalResultsByRun(ctx, ownerID, evalRunID)
}

func (s *Service) CreateTeam(ctx context.Context, ownerID int64, req CreateTeamRequest) (*workflow.Team, error) {
	if s.teams == nil {
		return nil, fmt.Errorf("%w: team repository is not configured", agenterrors.ErrInvalidInput)
	}
	name := strings.TrimSpace(req.Name)
	if ownerID <= 0 || name == "" || req.SupervisorWorkflowID <= 0 {
		return nil, agenterrors.ErrInvalidInput
	}
	if _, err := s.GetWorkflow(ctx, ownerID, req.SupervisorWorkflowID); err != nil {
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
	item := &workflow.Team{
		OwnerID:              ownerID,
		Name:                 name,
		SupervisorWorkflowID: req.SupervisorWorkflowID,
		HandoffStrategy:      strategy,
		MaxDepth:             maxDepth,
	}
	if err := s.teams.CreateTeam(ctx, item); err != nil {
		return nil, err
	}
	return item, nil
}

func (s *Service) ListTeams(ctx context.Context, ownerID int64) ([]workflow.Team, error) {
	if s.teams == nil {
		return nil, fmt.Errorf("%w: team repository is not configured", agenterrors.ErrInvalidInput)
	}
	return s.teams.ListTeams(ctx, ownerID)
}

func (s *Service) AddTeamMember(ctx context.Context, ownerID, teamID int64, req AddTeamMemberRequest) (*workflow.TeamMember, error) {
	if s.teams == nil {
		return nil, fmt.Errorf("%w: team repository is not configured", agenterrors.ErrInvalidInput)
	}
	if _, err := s.teams.FindTeamByID(ctx, ownerID, teamID); err != nil {
		return nil, mapNotFound(err)
	}
	if _, err := s.GetWorkflow(ctx, ownerID, req.WorkflowID); err != nil {
		return nil, err
	}
	item := &workflow.TeamMember{
		OwnerID:    ownerID,
		TeamID:     teamID,
		WorkflowID: req.WorkflowID,
		Role:       strings.TrimSpace(req.Role),
	}
	if err := s.teams.AddMember(ctx, item); err != nil {
		return nil, err
	}
	return item, nil
}

func (s *Service) ListTeamMembers(ctx context.Context, ownerID, teamID int64) ([]workflow.TeamMember, error) {
	if s.teams == nil {
		return nil, fmt.Errorf("%w: team repository is not configured", agenterrors.ErrInvalidInput)
	}
	if _, err := s.teams.FindTeamByID(ctx, ownerID, teamID); err != nil {
		return nil, mapNotFound(err)
	}
	return s.teams.ListMembers(ctx, ownerID, teamID)
}

func (s *Service) RemoveTeamMember(ctx context.Context, ownerID, teamID, workflowID int64) error {
	if s.teams == nil {
		return fmt.Errorf("%w: team repository is not configured", agenterrors.ErrInvalidInput)
	}
	return s.teams.RemoveMember(ctx, ownerID, teamID, workflowID)
}

func (s *Service) DeleteTeam(ctx context.Context, ownerID, teamID int64) error {
	if s.teams == nil {
		return fmt.Errorf("%w: team repository is not configured", agenterrors.ErrInvalidInput)
	}
	return s.teams.DeleteTeam(ctx, ownerID, teamID)
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
	if req.CallDepth >= maxDepth {
		s.recordBlockedWorkflowCall(ctx, req, "max_workflow_call_depth_exceeded", maxDepth)
		return nil, fmt.Errorf("%w: max workflow call depth exceeded", agenterrors.ErrForbidden)
	}
	if req.OwnerID <= 0 || req.WorkflowID <= 0 || req.Input == nil {
		return nil, agenterrors.ErrInvalidInput
	}
	callChain := normalizeWorkflowCallChain(req.WorkflowCallChain, req.CallerWorkflowID)
	if containsWorkflowID(callChain, req.WorkflowID) {
		s.recordBlockedWorkflowCall(ctx, req, "workflow_call_cycle_detected", maxDepth)
		return nil, fmt.Errorf("%w: workflow call cycle detected in workflow_call_chain", agenterrors.ErrForbidden)
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
	if item.Status == workflow.RunStatusRunning {
		_ = s.runCancels.Cancel(id)
		now := time.Now().UTC()
		item.Status = workflow.RunStatusCancelled
		item.FinishedAt = &now
		item.LatencyMS = int(now.Sub(item.StartedAt).Milliseconds())
		if err := s.runs.Update(ctx, item); err != nil {
			return nil, err
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
	inputJSON, _ := json.Marshal(req.Input)
	callChain := opts.WorkflowCallChain
	if len(callChain) == 0 {
		callChain = []int64{workflowID}
	}
	callChainJSON, _ := json.Marshal(callChain)
	now := time.Now().UTC()
	run := &workflow.Run{
		OwnerID:        ownerID,
		WorkflowID:     workflowID,
		FlowVersionID:  version.ID,
		ConversationID: req.ConversationID,
		ParentRunID:    opts.ParentRunID,
		CallerNodeID:   opts.CallerNodeID,
		CallDepth:      opts.CallDepth,
		CallChainJSON:  callChainJSON,
		Status:         workflow.RunStatusRunning,
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
		OwnerID:           ownerID,
		WorkflowID:        workflowID,
		FlowVersionID:     version.ID,
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

func (s *Service) ListApprovalRequests(ctx context.Context, ownerID int64, status string) ([]workflow.ApprovalRequest, error) {
	if s.approvals == nil {
		return nil, fmt.Errorf("%w: approval repository is not configured", agenterrors.ErrInvalidInput)
	}
	return s.approvals.ListApprovalRequests(ctx, ownerID, strings.TrimSpace(status))
}

func (s *Service) ApproveRequest(ctx context.Context, ownerID, approvalID int64, req DecideApprovalRequest) (*workflow.ApprovalRequest, error) {
	return s.decideApproval(ctx, ownerID, approvalID, workflow.ApprovalStatusApproved, req.Note)
}

func (s *Service) RejectRequest(ctx context.Context, ownerID, approvalID int64, req DecideApprovalRequest) (*workflow.ApprovalRequest, error) {
	return s.decideApproval(ctx, ownerID, approvalID, workflow.ApprovalStatusRejected, req.Note)
}

func (s *Service) ResumeRun(ctx context.Context, ownerID, runID int64) (*workflow.Run, error) {
	if s.approvals == nil {
		return nil, fmt.Errorf("%w: approval repository is not configured", agenterrors.ErrInvalidInput)
	}
	item, err := s.GetRun(ctx, ownerID, runID)
	if err != nil {
		return nil, err
	}
	if item.Status != workflow.RunStatusWaitingHuman && item.Status != workflow.RunStatusPaused {
		return nil, fmt.Errorf("%w: run is not waiting for resume", agenterrors.ErrInvalidInput)
	}
	checkpoint, err := s.approvals.FindLatestCheckpointByRun(ctx, ownerID, runID)
	if err != nil {
		return nil, mapNotFound(err)
	}
	approval, err := s.approvals.FindPendingApprovalByRun(ctx, ownerID, runID)
	if err == nil && approval.Status == workflow.ApprovalStatusPending {
		return nil, fmt.Errorf("%w: approval request is still pending", agenterrors.ErrInvalidInput)
	}
	if len(checkpoint.MessagesJSON) == 0 {
		return nil, fmt.Errorf("%w: checkpoint messages are missing", agenterrors.ErrInvalidInput)
	}
	item.Status = workflow.RunStatusResuming
	if err := s.runs.Update(ctx, item); err != nil {
		return nil, err
	}
	return s.resumeRunFromCheckpoint(ctx, item, checkpoint)
}

func (s *Service) decideApproval(ctx context.Context, ownerID, approvalID int64, status, note string) (*workflow.ApprovalRequest, error) {
	if s.approvals == nil {
		return nil, fmt.Errorf("%w: approval repository is not configured", agenterrors.ErrInvalidInput)
	}
	item, err := s.approvals.FindApprovalRequestByID(ctx, ownerID, approvalID)
	if err != nil {
		return nil, mapNotFound(err)
	}
	if item.Status != workflow.ApprovalStatusPending {
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

func (s *Service) persistRunCheckpointArtifacts(
	ctx context.Context,
	run *workflow.Run,
	output engine.NodeOutput,
	checkpointStatus string,
) error {
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
		item := &workflow.ApprovalRequest{
			OwnerID:     run.OwnerID,
			WorkflowID:  run.WorkflowID,
			RunID:       run.ID,
			NodeID:      checkpointNodeID(checkpoint),
			ToolCallID:  approval.ToolCallID,
			ToolName:    approval.ToolName,
			RiskLevel:   approval.RiskLevel,
			Reason:      approval.Reason,
			RequestJSON: requestJSON,
			Status:      workflow.ApprovalStatusPending,
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
		contextJSON, _ := json.Marshal(checkpointContextEnvelope{Context: checkpoint.Context, Metadata: checkpoint.Metadata})
		item := &workflow.WorkflowCheckpoint{
			OwnerID:             run.OwnerID,
			WorkflowID:          run.WorkflowID,
			RunID:               run.ID,
			NodeID:              checkpointNodeID(checkpoint),
			Status:              checkpointStatus,
			MessagesJSON:        messagesJSON,
			MessagesSummary:     checkpoint.MessagesSummary,
			StepsJSON:           stepsJSON,
			PendingToolCallJSON: pendingJSON,
			ContextJSON:         contextJSON,
			ToolRegistryHash:    checkpointToolRegistryHash(checkpoint),
			ToolPolicyHash:      checkpointToolPolicyHash(checkpoint),
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

func checkpointToolRegistryHash(checkpoint *runtimeagent.Checkpoint) string {
	if checkpoint == nil {
		return ""
	}
	if len(checkpoint.ToolNames) > 0 {
		return stableJSONHash(checkpoint.ToolNames)
	}
	if checkpoint.Metadata != nil {
		if hash, _ := checkpoint.Metadata["tool_registry_hash"].(string); hash != "" {
			return hash
		}
	}
	return stableJSONHash(checkpoint.ToolNames)
}

func checkpointToolPolicyHash(checkpoint *runtimeagent.Checkpoint) string {
	if checkpoint == nil {
		return ""
	}
	if checkpoint.Metadata != nil {
		if hash, _ := checkpoint.Metadata["tool_policy_hash"].(string); hash != "" && isZeroToolPolicy(checkpoint.ToolPolicy) {
			return hash
		}
	}
	return stableJSONHash(checkpoint.ToolPolicy)
}

func isZeroToolPolicy(policy runtimeagent.ToolPolicy) bool {
	return len(policy.RequireApprovalForRisk) == 0 && policy.MaxToolTimeoutMS == 0 && policy.MaxToolOutputBytes == 0 && len(policy.AllowedHosts) == 0
}

func (s *Service) resumeRunFromCheckpoint(ctx context.Context, run *workflow.Run, stored *workflow.WorkflowCheckpoint) (*workflow.Run, error) {
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
	version, err := s.GetWorkflowVersion(ctx, run.OwnerID, run.FlowVersionID)
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
	callChain := []int64{run.WorkflowID}
	if len(run.CallChainJSON) > 0 {
		_ = json.Unmarshal(run.CallChainJSON, &callChain)
	}
	node := s.resumeAgentLoopNode()
	rc := &engine.RunContext{
		OwnerID:           run.OwnerID,
		WorkflowID:        run.WorkflowID,
		FlowVersionID:     run.FlowVersionID,
		RunID:             run.ID,
		ParentRunID:       run.ParentRunID,
		CallDepth:         run.CallDepth,
		WorkflowCallChain: append([]int64(nil), callChain...),
		ConversationID:    run.ConversationID,
		Input:             input,
		NodeInputs:        map[string]engine.NodeInput{},
		NodeOutputs:       map[string]engine.NodeOutput{},
		NodeErrors:        map[string]string{},
		NodeLatencies:     map[string]int{},
		ExecutedNodes:     map[string]bool{},
		CurrentNodeID:     nodeSpec.ID,
		CurrentNodeType:   nodeSpec.Type,
		AgentSteps:        s,
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
		Approved:      decision != nil && decision.Status == workflow.ApprovalStatusApproved,
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
		run.Status = workflow.RunStatusCancelled
		run.ErrorMessage = context.Canceled.Error()
	} else if execErr != nil {
		run.Status = workflow.RunStatusFailed
		run.ErrorMessage = execErr.Error()
	} else if status := runStatusFromOutput(output); status != "" {
		run.Status = status
		run.ErrorMessage = ""
	} else {
		run.Status = workflow.RunStatusSucceeded
		run.ErrorMessage = ""
	}
	if run.Status == workflow.RunStatusWaitingHuman {
		if err := s.persistRunCheckpointArtifacts(ctx, run, output, workflow.RunStatusWaitingHuman); err != nil {
			return run, err
		}
	}
	if run.Status == workflow.RunStatusPaused {
		if err := s.persistRunCheckpointArtifacts(ctx, run, output, workflow.RunStatusPaused); err != nil {
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
		WorkflowCaller: s,
		Profiles:       s,
		MessageHistory: s.messages,
	}}
}

func decodeRuntimeCheckpoint(stored *workflow.WorkflowCheckpoint, decision *workflow.ApprovalRequest) (*runtimeagent.Checkpoint, error) {
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
	metadata := map[string]any{}
	if len(stored.ContextJSON) > 0 {
		contextTrace, metadata = decodeCheckpointContext(stored.ContextJSON)
	}
	metadata["node_id"] = stored.NodeID
	metadata["approval_status"] = approvalDecisionStatus(decision)
	metadata["approval_note"] = approvalDecisionNote(decision)
	metadata["checkpoint_id"] = stored.ID
	metadata["checkpoint_state"] = stored.Status
	metadata["tool_registry_hash"] = stored.ToolRegistryHash
	metadata["tool_policy_hash"] = stored.ToolPolicyHash
	return &runtimeagent.Checkpoint{
		Messages:        messages,
		MessagesSummary: stored.MessagesSummary,
		PendingToolCall: pending,
		Context:         contextTrace,
		Metadata:        metadata,
	}, nil
}

type checkpointContextEnvelope struct {
	Context  runtimeagent.ContextTrace `json:"context"`
	Metadata map[string]any            `json:"metadata,omitempty"`
}

func decodeCheckpointContext(raw json.RawMessage) (runtimeagent.ContextTrace, map[string]any) {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(raw, &root); err == nil {
		if _, ok := root["context"]; ok {
			var envelope checkpointContextEnvelope
			_ = json.Unmarshal(raw, &envelope)
			if envelope.Metadata == nil {
				envelope.Metadata = map[string]any{}
			}
			return envelope.Context, envelope.Metadata
		}
	}
	var trace runtimeagent.ContextTrace
	_ = json.Unmarshal(raw, &trace)
	return trace, map[string]any{}
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

func (s *Service) latestApprovalDecision(ctx context.Context, ownerID, runID int64) (*workflow.ApprovalRequest, error) {
	items, err := s.approvals.ListApprovalRequests(ctx, ownerID, "")
	if err != nil {
		return nil, err
	}
	for _, item := range items {
		if item.RunID != runID {
			continue
		}
		if item.Status == workflow.ApprovalStatusPending {
			return nil, fmt.Errorf("%w: approval request is still pending", agenterrors.ErrInvalidInput)
		}
		clone := item
		return &clone, nil
	}
	return nil, nil
}

func approvalDecisionStatus(decision *workflow.ApprovalRequest) string {
	if decision == nil {
		return ""
	}
	return decision.Status
}

func approvalDecisionNote(decision *workflow.ApprovalRequest) string {
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
		"max_iteration_exceeded_rate":          averageMetric(results, "max_iteration_exceeded"),
		"max_tool_calls_exceeded_rate":         averageMetric(results, "max_tool_calls_exceeded"),
		"human_approval_waiting_rate":          averageMetric(results, "human_approval_waiting"),
		"reflection_repair_attempted_rate":     averageMetric(results, "reflection_repair_attempted"),
		"failed_cases_with_runtime_error_rate": runtimeErrorRate(results),
	}
	return summary
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
			matched := 0
			for _, name := range required {
				name = strings.TrimSpace(name)
				if name == "" {
					continue
				}
				if requiredToolCalled(called, name) {
					matched++
				} else {
					metrics["tool_call_accuracy"] = float64(matched) / float64(len(required))
					metrics["required_tools"] = required
					metrics["actual_tools"] = sortedToolNames(called)
					return evalScoreResult{
						Score:   0,
						Reason:  "required tool was not called: " + name,
						Metrics: metrics,
					}
				}
			}
			metrics["tool_call_accuracy"] = float64(matched) / float64(len(required))
			metrics["required_tools"] = required
			metrics["actual_tools"] = sortedToolNames(called)
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
		DefaultMCPServerIDs:         json.RawMessage("[]"),
		DefaultKnowledgeIDs:         json.RawMessage("[]"),
		DefaultCallWorkflowIDs:      json.RawMessage("[]"),
		DefaultKnowledgeTopK:        5,
		DefaultKnowledgeMode:        string(retrieval.ModeHybrid),
		DefaultMaxWorkflowCallDepth: 3,
		OutputSchemaJSON:            json.RawMessage("{}"),
		ToolPolicyJSON:              json.RawMessage("{}"),
		MemoryPolicyJSON:            json.RawMessage("{}"),
		ContextPolicyJSON:           json.RawMessage("{}"),
		RiskLevel:                   toolruntime.RiskMedium,
		Mode:                        "react",
	}
}
