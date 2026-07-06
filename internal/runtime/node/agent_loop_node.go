package node

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"agentcanvas/internal/domain/memory"
	"agentcanvas/internal/domain/retrieval"
	"agentcanvas/internal/domain/tool"
	"agentcanvas/internal/domain/workflow"
	runtimeagent "agentcanvas/internal/runtime/agent"
	"agentcanvas/internal/runtime/engine"
	runtimeevent "agentcanvas/internal/runtime/event"
	"agentcanvas/internal/runtime/sandbox"
	"agentcanvas/internal/runtime/toolruntime"

	"agentcanvas/internal/infrastructure/llm"
	agenterrors "agentcanvas/internal/pkg/errors"
)

type AgentNode struct {
	LLM            llm.ToolCallingClient
	Providers      ProviderConfigLoader
	Tools          toolruntime.Registry
	ToolPacks      tool.PackRepository
	MCPServers     tool.MCPRepository
	Retriever      retrieval.Retriever
	Memories       memory.Repository
	MemoryLogs     memory.WriteLogRepository
	WorkflowCaller toolruntime.WorkflowCaller
	Profiles       AgentProfileLoader
	Sandbox        sandbox.Runner
	MessageHistory MessageHistoryReader
}

type AgentLoopNode struct {
	AgentNode
}

type AgentResumeOptions struct {
	Checkpoint    *runtimeagent.Checkpoint
	Approved      bool
	RejectionNote string
}

type agentRuntimeConfig struct {
	Mode                    string          `json:"mode"`
	ProviderID              int64           `json:"provider_id"`
	Model                   string          `json:"model"`
	SystemPrompt            string          `json:"system_prompt"`
	TaskTemplate            string          `json:"task_template"`
	ToolIDs                 []int64         `json:"tool_ids"`
	KnowledgeIDs            []int64         `json:"knowledge_ids"`
	KnowledgeTopK           int             `json:"knowledge_top_k"`
	KnowledgeMode           string          `json:"knowledge_mode"`
	CallWorkflowIDs         []int64         `json:"call_workflow_ids"`
	MCPServerIDs            []int64         `json:"mcp_server_ids"`
	MaxWorkflowCallDepth    int             `json:"max_workflow_call_depth"`
	CodeExecutionEnabled    bool            `json:"code_execution_enabled"`
	MemoryEnabled           bool            `json:"memory_enabled"`
	MaxIterations           int             `json:"max_iterations"`
	MaxToolCalls            int             `json:"max_tool_calls"`
	MaxExecutionTimeMS      int             `json:"max_execution_time_ms"`
	MaxInputChars           int             `json:"max_input_chars"`
	MaxInputTokens          int             `json:"max_input_tokens"`
	RequireApprovalForRisk  []string        `json:"require_approval_for_risk"`
	MaxToolTimeoutMS        int             `json:"max_tool_timeout_ms"`
	MaxToolOutputBytes      int             `json:"max_tool_output_bytes"`
	AllowedHosts            []string        `json:"allowed_hosts"`
	ToolPolicyJSON          json.RawMessage `json:"tool_policy_json"`
	MemoryPolicyJSON        json.RawMessage `json:"memory_policy_json"`
	ContextPolicyJSON       json.RawMessage `json:"context_policy_json"`
	OutputSchemaJSON        json.RawMessage `json:"output_schema_json"`
	ReflectionEnabled       bool            `json:"reflection_enabled"`
	Temperature             *float64        `json:"temperature"`
	ReturnIntermediateSteps bool            `json:"return_intermediate_steps"`
	OutputMode              string          `json:"output_mode"`
}

type agentNodeConfig struct {
	Mode         string `json:"mode"`
	TaskTemplate string `json:"task_template"`
	Profile      struct {
		Overrides struct {
			SystemPrompt string `json:"system_prompt"`
			DefaultModel string `json:"default_model"`
		} `json:"overrides"`
	} `json:"profile"`
	Model struct {
		ProviderID  int64    `json:"provider_id"`
		Model       string   `json:"model"`
		Temperature *float64 `json:"temperature"`
	} `json:"model"`
	Tools struct {
		ToolIDs              []int64 `json:"tool_ids"`
		KnowledgeIDs         []int64 `json:"knowledge_ids"`
		KnowledgeTopK        int     `json:"knowledge_top_k"`
		KnowledgeMode        string  `json:"knowledge_mode"`
		CallWorkflowIDs      []int64 `json:"call_workflow_ids"`
		MCPServerIDs         []int64 `json:"mcp_server_ids"`
		MaxWorkflowCallDepth int     `json:"max_workflow_call_depth"`
		CodeExecutionEnabled bool    `json:"code_execution_enabled"`
	} `json:"tools"`
	Memory struct {
		Enabled bool            `json:"enabled"`
		Policy  json.RawMessage `json:"policy"`
	} `json:"memory"`
	Limits struct {
		MaxIterations      int `json:"max_iterations"`
		MaxToolCalls       int `json:"max_tool_calls"`
		MaxExecutionTimeMS int `json:"max_execution_time_ms"`
	} `json:"limits"`
	Output struct {
		Mode                    string          `json:"mode"`
		ReturnIntermediateSteps bool            `json:"return_intermediate_steps"`
		Schema                  json.RawMessage `json:"schema"`
	} `json:"output"`
	Context struct {
		MaxInputTokens int             `json:"max_input_tokens"`
		Policy         json.RawMessage `json:"policy"`
	} `json:"context"`
	Planning struct {
		Enabled           bool `json:"enabled"`
		ReflectionEnabled bool `json:"reflection_enabled"`
	} `json:"planning"`
	Policy struct {
		RequireApprovalForRisk []string        `json:"require_approval_for_risk"`
		MaxToolTimeoutMS       int             `json:"max_tool_timeout_ms"`
		MaxToolOutputBytes     int             `json:"max_tool_output_bytes"`
		AllowedHosts           []string        `json:"allowed_hosts"`
		Raw                    json.RawMessage `json:"raw"`
	} `json:"policy"`
}

func (AgentLoopNode) Type() string { return "agent_loop" }

func (AgentLoopNode) Validate(config json.RawMessage) error {
	cfg, err := parseAgentNodeConfig(config)
	if err != nil {
		return err
	}
	return validateAgentRuntimeConfig(cfg, "agent_loop", false)
}

func parseLegacyAgentLoopConfig(config json.RawMessage) (agentRuntimeConfig, error) {
	var cfg agentRuntimeConfig
	if err := json.Unmarshal(config, &cfg); err != nil {
		return cfg, fmt.Errorf("%w: invalid agent_loop config", agenterrors.ErrInvalidInput)
	}
	return cfg, nil
}

func parseAgentNodeConfig(config json.RawMessage) (agentRuntimeConfig, error) {
	var flat agentRuntimeConfig
	if !hasNestedAgentModel(config) {
		if err := json.Unmarshal(config, &flat); err != nil {
			return flat, fmt.Errorf("%w: invalid agent config", agenterrors.ErrInvalidInput)
		}
		return flat, nil
	}
	var nested agentNodeConfig
	if err := json.Unmarshal(config, &nested); err != nil {
		return flat, fmt.Errorf("%w: invalid agent config", agenterrors.ErrInvalidInput)
	}
	cfg := flat
	if strings.TrimSpace(nested.Mode) != "" {
		cfg.Mode = nested.Mode
	}
	if nested.Model.ProviderID > 0 {
		cfg.ProviderID = nested.Model.ProviderID
	}
	if strings.TrimSpace(nested.Model.Model) != "" {
		cfg.Model = nested.Model.Model
	}
	if nested.Model.Temperature != nil {
		cfg.Temperature = nested.Model.Temperature
	}
	if strings.TrimSpace(nested.Profile.Overrides.SystemPrompt) != "" {
		cfg.SystemPrompt = nested.Profile.Overrides.SystemPrompt
	}
	if cfg.Model == "" && strings.TrimSpace(nested.Profile.Overrides.DefaultModel) != "" {
		cfg.Model = nested.Profile.Overrides.DefaultModel
	}
	if strings.TrimSpace(nested.TaskTemplate) != "" {
		cfg.TaskTemplate = nested.TaskTemplate
	}
	if len(nested.Tools.ToolIDs) > 0 {
		cfg.ToolIDs = nested.Tools.ToolIDs
	}
	if len(nested.Tools.KnowledgeIDs) > 0 {
		cfg.KnowledgeIDs = nested.Tools.KnowledgeIDs
	}
	if nested.Tools.KnowledgeTopK > 0 {
		cfg.KnowledgeTopK = nested.Tools.KnowledgeTopK
	}
	if strings.TrimSpace(nested.Tools.KnowledgeMode) != "" {
		cfg.KnowledgeMode = nested.Tools.KnowledgeMode
	}
	if len(nested.Tools.CallWorkflowIDs) > 0 {
		cfg.CallWorkflowIDs = nested.Tools.CallWorkflowIDs
	}
	if len(nested.Tools.MCPServerIDs) > 0 {
		cfg.MCPServerIDs = nested.Tools.MCPServerIDs
	}
	if nested.Tools.MaxWorkflowCallDepth > 0 {
		cfg.MaxWorkflowCallDepth = nested.Tools.MaxWorkflowCallDepth
	}
	if nested.Tools.CodeExecutionEnabled {
		cfg.CodeExecutionEnabled = true
	}
	if nested.Memory.Enabled {
		cfg.MemoryEnabled = true
	}
	if len(nested.Memory.Policy) > 0 {
		cfg.MemoryPolicyJSON = nested.Memory.Policy
	}
	if nested.Limits.MaxIterations > 0 {
		cfg.MaxIterations = nested.Limits.MaxIterations
	}
	if nested.Limits.MaxToolCalls > 0 {
		cfg.MaxToolCalls = nested.Limits.MaxToolCalls
	}
	if nested.Limits.MaxExecutionTimeMS > 0 {
		cfg.MaxExecutionTimeMS = nested.Limits.MaxExecutionTimeMS
	}
	if nested.Context.MaxInputTokens > 0 {
		cfg.MaxInputTokens = nested.Context.MaxInputTokens
		cfg.MaxInputChars = nested.Context.MaxInputTokens * 4
	}
	if len(nested.Context.Policy) > 0 {
		cfg.ContextPolicyJSON = nested.Context.Policy
	}
	if nested.Planning.Enabled && strings.TrimSpace(cfg.Mode) == "" {
		cfg.Mode = "plan_execute"
	}
	if nested.Planning.ReflectionEnabled {
		cfg.ReflectionEnabled = true
		if strings.TrimSpace(cfg.Mode) == "" {
			cfg.Mode = "reflect"
		}
	}
	if len(nested.Policy.RequireApprovalForRisk) > 0 {
		cfg.RequireApprovalForRisk = nested.Policy.RequireApprovalForRisk
	}
	if nested.Policy.MaxToolTimeoutMS > 0 {
		cfg.MaxToolTimeoutMS = nested.Policy.MaxToolTimeoutMS
	}
	if nested.Policy.MaxToolOutputBytes > 0 {
		cfg.MaxToolOutputBytes = nested.Policy.MaxToolOutputBytes
	}
	if len(nested.Policy.AllowedHosts) > 0 {
		cfg.AllowedHosts = nested.Policy.AllowedHosts
	}
	if len(nested.Policy.Raw) > 0 {
		cfg.ToolPolicyJSON = nested.Policy.Raw
	}
	if strings.TrimSpace(nested.Output.Mode) != "" {
		cfg.OutputMode = nested.Output.Mode
	}
	if len(nested.Output.Schema) > 0 {
		cfg.OutputSchemaJSON = nested.Output.Schema
	}
	if nested.Output.ReturnIntermediateSteps {
		cfg.ReturnIntermediateSteps = true
	}
	return cfg, nil
}

func hasNestedAgentModel(config json.RawMessage) bool {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(config, &root); err != nil {
		return false
	}
	model, ok := root["model"]
	if !ok {
		return false
	}
	return bytes.HasPrefix(bytes.TrimSpace(model), []byte("{"))
}

func validateAgentRuntimeConfig(cfg agentRuntimeConfig, nodeType string, requireProvider bool) error {
	if requireProvider && cfg.ProviderID <= 0 {
		return fmt.Errorf("%w: %s provider_id is required", agenterrors.ErrInvalidInput, nodeType)
	}
	if cfg.MaxIterations < 0 || cfg.MaxIterations > 50 {
		return fmt.Errorf("%w: %s max_iterations must be <= 50", agenterrors.ErrInvalidInput, nodeType)
	}
	if cfg.MaxToolCalls < 0 || cfg.MaxToolCalls > 100 {
		return fmt.Errorf("%w: %s max_tool_calls must be <= 100", agenterrors.ErrInvalidInput, nodeType)
	}
	if cfg.MaxExecutionTimeMS < 0 || cfg.MaxExecutionTimeMS > 10*60*1000 {
		return fmt.Errorf("%w: %s max_execution_time_ms must be <= 600000", agenterrors.ErrInvalidInput, nodeType)
	}
	if cfg.OutputMode != "" && cfg.OutputMode != "final_answer" && cfg.OutputMode != "full" {
		return fmt.Errorf("%w: %s output_mode must be final_answer or full", agenterrors.ErrInvalidInput, nodeType)
	}
	if cfg.KnowledgeTopK < 0 || cfg.KnowledgeTopK > 20 {
		return fmt.Errorf("%w: %s knowledge_top_k must be <= 20", agenterrors.ErrInvalidInput, nodeType)
	}
	if cfg.KnowledgeMode != "" && cfg.KnowledgeMode != string(retrieval.ModeKeyword) && cfg.KnowledgeMode != string(retrieval.ModeVector) && cfg.KnowledgeMode != string(retrieval.ModeHybrid) {
		return fmt.Errorf("%w: unsupported %s knowledge_mode", agenterrors.ErrInvalidInput, nodeType)
	}
	if cfg.MaxWorkflowCallDepth < 0 || cfg.MaxWorkflowCallDepth > 5 {
		return fmt.Errorf("%w: %s max_workflow_call_depth must be <= 5", agenterrors.ErrInvalidInput, nodeType)
	}
	if cfg.Mode != "" && cfg.Mode != "react" && cfg.Mode != "plan_execute" && cfg.Mode != "reflect" && cfg.Mode != "supervisor" {
		return fmt.Errorf("%w: %s mode must be react, plan_execute, reflect, or supervisor", agenterrors.ErrInvalidInput, nodeType)
	}
	for _, risk := range cfg.RequireApprovalForRisk {
		normalized := strings.TrimSpace(risk)
		if normalized != "" && normalized != toolruntime.RiskLow && normalized != toolruntime.RiskMedium && normalized != toolruntime.RiskHigh {
			return fmt.Errorf("%w: %s require_approval_for_risk contains unsupported risk level", agenterrors.ErrInvalidInput, nodeType)
		}
	}
	if cfg.MaxToolTimeoutMS < 0 || cfg.MaxToolTimeoutMS > 10*60*1000 {
		return fmt.Errorf("%w: %s max_tool_timeout_ms must be <= 600000", agenterrors.ErrInvalidInput, nodeType)
	}
	if cfg.MaxToolOutputBytes < 0 || cfg.MaxToolOutputBytes > 2*1024*1024 {
		return fmt.Errorf("%w: %s max_tool_output_bytes must be <= 2097152", agenterrors.ErrInvalidInput, nodeType)
	}
	if err := validateAgentMemoryPolicyJSON(cfg.MemoryPolicyJSON, nodeType); err != nil {
		return err
	}
	if err := validateAgentContextPolicyJSON(cfg.ContextPolicyJSON, nodeType); err != nil {
		return err
	}
	if err := validateAgentToolPolicyJSON(cfg.ToolPolicyJSON, nodeType); err != nil {
		return err
	}
	return nil
}

func (n AgentLoopNode) Run(ctx context.Context, rc *engine.RunContext, input engine.NodeInput, config json.RawMessage) (engine.NodeOutput, error) {
	cfg, err := parseAgentNodeConfig(config)
	if err != nil {
		return nil, err
	}
	return n.AgentNode.runAgent(ctx, rc, input, cfg, n.Type(), true, nil)
}

func (n AgentLoopNode) Resume(ctx context.Context, rc *engine.RunContext, input engine.NodeInput, config json.RawMessage, opts AgentResumeOptions) (engine.NodeOutput, error) {
	cfg, err := parseAgentNodeConfig(config)
	if err != nil {
		return nil, err
	}
	return n.AgentNode.runAgent(ctx, rc, input, cfg, n.Type(), true, &opts)
}

func (n AgentNode) runAgent(ctx context.Context, rc *engine.RunContext, input engine.NodeInput, cfg agentRuntimeConfig, nodeType string, useProfileDefaults bool, resume *AgentResumeOptions) (engine.NodeOutput, error) {
	if n.LLM == nil || n.Providers == nil {
		return nil, fmt.Errorf("%s dependencies are not configured", nodeType)
	}
	if useProfileDefaults {
		var err error
		cfg, err = n.applyProfileDefaults(ctx, rc, cfg)
		if err != nil {
			return nil, err
		}
	}
	cfg = applyNodeMemoryPolicy(cfg, cfg.MemoryPolicyJSON)
	cfg = applyNodeContextPolicy(cfg, cfg.ContextPolicyJSON)
	cfg = applyNodeToolPolicy(cfg, cfg.ToolPolicyJSON)
	if err := validateAgentRuntimeConfig(cfg, nodeType, true); err != nil {
		return nil, err
	}
	loaded, err := n.Providers.LoadChatProviderConfig(ctx, rc.OwnerID, cfg.ProviderID, cfg.Model)
	if err != nil {
		return nil, err
	}
	tools, err := n.loadTools(ctx, rc.OwnerID, cfg)
	if err != nil {
		return nil, err
	}
	task := resolveAgentTask(cfg.TaskTemplate, rc, input)
	if task == "" {
		return nil, fmt.Errorf("%w: %s task is required", agenterrors.ErrInvalidInput, nodeType)
	}
	contextBlocks := buildConversationContext(ctx, n, rc, cfg.MaxInputChars)
	systemPrompt := cfg.SystemPrompt
	mode := agentMode(cfg.Mode)
	var plan *runtimeagent.Plan
	if mode == "plan_execute" && task != "" {
		planner := runtimeagent.Planner{
			LLM:        n.LLM,
			MaxSteps:   8,
			ProviderID: loaded.ProviderID,
			ModelName:  loaded.Model,
		}
		generatedPlan, planErr := planner.GeneratePlan(ctx, loaded.Config, loaded.Model, task, cfg.Temperature)
		if planErr == nil && generatedPlan != nil {
			plan = generatedPlan
		}
	}

	emitRuntimeEvent(ctx, rc, runtimeevent.Event{
		Type:     runtimeevent.AgentStarted,
		RunID:    rc.RunID,
		NodeID:   rc.CurrentNodeID,
		NodeType: nodeType,
		Payload: map[string]any{
			"provider_id": loaded.ProviderID,
			"model":       loaded.Model,
			"mode":        agentMode(cfg.Mode),
			"tool_count":  len(tools),
		},
	})
	runner := runtimeagent.Runner{
		LLM:        n.LLM,
		ProviderID: loaded.ProviderID,
		ModelName:  loaded.Model,
		OnStep: func(ctx context.Context, step runtimeagent.RunStep) error {
			if rc.AgentSteps != nil {
				_ = rc.AgentSteps.RecordAgentStep(ctx, rc, agentStepRecord(step, rc.CurrentNodeID))
			}
			emitRuntimeEvent(ctx, rc, runtimeevent.Event{
				Type:     runtimeevent.AgentStep,
				RunID:    rc.RunID,
				NodeID:   rc.CurrentNodeID,
				NodeType: nodeType,
				Payload:  agentStepPayload(step, loaded.ProviderID, loaded.Model),
			})
			return nil
		},
	}
	runRequest := runtimeagent.RunRequest{
		OwnerID:            rc.OwnerID,
		WorkflowID:         rc.WorkflowID,
		RunID:              rc.RunID,
		NodeID:             rc.CurrentNodeID,
		CallDepth:          rc.CallDepth,
		WorkflowCallChain:  append([]int64(nil), rc.WorkflowCallChain...),
		ConversationID:     rc.ConversationID,
		Provider:           loaded.Config,
		Model:              loaded.Model,
		Mode:               mode,
		Plan:               plan,
		SystemPrompt:       systemPrompt,
		Task:               task,
		ReflectionEnabled:  cfg.ReflectionEnabled,
		Temperature:        cfg.Temperature,
		MaxIterations:      cfg.MaxIterations,
		MaxToolCalls:       cfg.MaxToolCalls,
		MaxExecutionTimeMS: cfg.MaxExecutionTimeMS,
		MaxInputChars:      cfg.MaxInputChars,
		MaxInputTokens:     cfg.MaxInputTokens,
		ContextBlocks:      contextBlocks,
		ToolPolicy: runtimeagent.ToolPolicy{
			RequireApprovalForRisk: cfg.RequireApprovalForRisk,
			MaxToolTimeoutMS:       cfg.MaxToolTimeoutMS,
			MaxToolOutputBytes:     cfg.MaxToolOutputBytes,
			AllowedHosts:           append([]string(nil), cfg.AllowedHosts...),
		},
		Tools: tools,
	}
	if resume != nil && resume.Checkpoint != nil {
		if mismatch := checkpointHashMismatch(resume.Checkpoint, tools, runRequest.ToolPolicy); mismatch != "" {
			return pausedForCheckpointMismatch(resume.Checkpoint, mismatch), nil
		}
		resumeRequest, buildErr := runtimeagent.BuildResumeRequest(runtimeagent.ResumeRequest{
			OwnerID:            rc.OwnerID,
			WorkflowID:         rc.WorkflowID,
			RunID:              rc.RunID,
			NodeID:             rc.CurrentNodeID,
			CallDepth:          rc.CallDepth,
			WorkflowCallChain:  append([]int64(nil), rc.WorkflowCallChain...),
			ConversationID:     rc.ConversationID,
			Provider:           loaded.Config,
			Model:              loaded.Model,
			Mode:               mode,
			Plan:               plan,
			SystemPrompt:       systemPrompt,
			Task:               task,
			ReflectionEnabled:  cfg.ReflectionEnabled,
			Temperature:        cfg.Temperature,
			MaxIterations:      cfg.MaxIterations,
			MaxToolCalls:       cfg.MaxToolCalls,
			MaxExecutionTimeMS: cfg.MaxExecutionTimeMS,
			MaxInputChars:      cfg.MaxInputChars,
			MaxInputTokens:     cfg.MaxInputTokens,
			ContextBlocks:      contextBlocks,
			ToolPolicy:         runRequest.ToolPolicy,
			Tools:              tools,
			Checkpoint:         resume.Checkpoint,
			Approved:           resume.Approved,
			RejectionNote:      resume.RejectionNote,
		})
		if buildErr != nil {
			return nil, buildErr
		}
		runRequest = *resumeRequest
	}
	result, err := runner.Run(ctx, runRequest)
	if result != nil {
		eventType := runtimeevent.AgentFinished
		if err != nil {
			eventType = runtimeevent.AgentFailed
		}
		emitRuntimeEvent(ctx, rc, runtimeevent.Event{
			Type:     eventType,
			RunID:    rc.RunID,
			NodeID:   rc.CurrentNodeID,
			NodeType: nodeType,
			Payload: map[string]any{
				"stop_reason":  result.StopReason,
				"iterations":   result.Iterations,
				"tool_calls":   result.ToolCalls,
				"latency_ms":   result.LatencyMS,
				"total_tokens": result.Usage.TotalTokens,
			},
		})
	}
	if err != nil {
		return nil, err
	}
	output := engine.NodeOutput{
		"content":       result.FinalAnswer,
		"final_answer":  result.FinalAnswer,
		"stop_reason":   result.StopReason,
		"iterations":    result.Iterations,
		"tool_calls":    result.ToolCalls,
		"usage":         result.Usage,
		"total_tokens":  result.Usage.TotalTokens,
		"latency_ms":    result.LatencyMS,
		"context_trace": result.Context,
	}
	if len(cfg.OutputSchemaJSON) > 0 && string(bytes.TrimSpace(cfg.OutputSchemaJSON)) != "{}" && string(bytes.TrimSpace(cfg.OutputSchemaJSON)) != "null" {
		var parsed any
		schemaErr := parseAndValidateStructuredOutput(cfg.OutputSchemaJSON, result.FinalAnswer, &parsed)
		if schemaErr != nil {
			schemaErrText := schemaErr.Error()
			repaired, repairErr := n.repairStructuredOutput(ctx, loaded.Config, loaded.Model, cfg.Temperature, task, result.FinalAnswer, cfg.OutputSchemaJSON, schemaErr)
			if repairErr == nil {
				var repairedParsed any
				if repairedErr := parseAndValidateStructuredOutput(cfg.OutputSchemaJSON, repaired, &repairedParsed); repairedErr == nil {
					result.FinalAnswer = repaired
					output["content"] = repaired
					output["final_answer"] = repaired
					parsed = repairedParsed
					schemaErr = nil
					reflectionStep := runtimeagent.RunStep{
						Type:       runtimeagent.StepTypeReflection,
						Content:    "Repaired final_answer to satisfy output_schema_json after validation failed: " + schemaErrText,
						OutputJSON: json.RawMessage(repaired),
						ProviderID: loaded.ProviderID,
						Model:      loaded.Model,
						CreatedAt:  time.Now().UTC(),
					}
					reflectionStep.Index = len(result.Steps) + 1
					result.Steps = append(result.Steps, reflectionStep)
					n.recordAgentStep(ctx, rc, nodeType, reflectionStep, loaded.ProviderID, loaded.Model)
				} else {
					schemaErr = repairedErr
				}
			}
		}
		if schemaErr != nil {
			return nil, fmt.Errorf("%w: agent_loop final_answer does not match output_schema_json: %v", agenterrors.ErrInvalidInput, schemaErr)
		}
		output["structured_output"] = parsed
	}
	if result.Plan != nil {
		output["plan"] = result.Plan
	}
	if result.Approval != nil {
		output["approval"] = result.Approval
	}
	if result.Checkpoint != nil {
		output["checkpoint"] = result.Checkpoint
	}
	if cfg.ReturnIntermediateSteps || cfg.OutputMode == "full" {
		output["steps"] = runtimeagent.CompactSteps(result.Steps, 8192)
	}
	return output, nil
}

func (n AgentNode) applyProfileDefaults(ctx context.Context, rc *engine.RunContext, cfg agentRuntimeConfig) (agentRuntimeConfig, error) {
	if n.Profiles == nil || rc == nil || rc.WorkflowID <= 0 {
		return cfg, nil
	}
	profile, err := n.Profiles.GetWorkflowProfile(ctx, rc.OwnerID, rc.WorkflowID)
	if err != nil {
		return cfg, err
	}
	if cfg.ProviderID <= 0 && profile.DefaultProviderID != nil {
		cfg.ProviderID = *profile.DefaultProviderID
	}
	if strings.TrimSpace(cfg.Model) == "" {
		cfg.Model = strings.TrimSpace(profile.DefaultModel)
	}
	if strings.TrimSpace(cfg.SystemPrompt) == "" {
		cfg.SystemPrompt = profileSystemPrompt(profile)
	}
	if cfg.MaxIterations <= 0 {
		cfg.MaxIterations = profile.MaxIterations
	}
	if cfg.MaxExecutionTimeMS <= 0 {
		cfg.MaxExecutionTimeMS = profile.MaxExecutionTimeMS
	}
	if profile.MemoryEnabled {
		cfg.MemoryEnabled = true
	}
	cfg = applyProfileMemoryPolicy(cfg, profile.MemoryPolicyJSON)
	if profile.AllowCodeExecution {
		cfg.CodeExecutionEnabled = true
	}
	if strings.TrimSpace(cfg.Mode) == "" {
		profileMode := strings.TrimSpace(profile.Mode)
		if profile.PlanningEnabled && (profileMode == "" || profileMode == "react") {
			cfg.Mode = "plan_execute"
		} else if profileMode != "" {
			cfg.Mode = profileMode
		}
	}
	cfg = applyProfileContextPolicy(cfg, profile.ContextPolicyJSON)
	cfg = applyProfileToolPolicy(cfg, profile.ToolPolicyJSON)
	if len(cfg.ToolIDs) == 0 {
		if ids := profile.DefaultToolIDsSlice(); len(ids) > 0 {
			cfg.ToolIDs = ids
		}
	}
	if packIDs := profile.DefaultToolPackIDsSlice(); len(packIDs) > 0 && n.ToolPacks != nil {
		cfg.ToolIDs = mergeInt64IDs(cfg.ToolIDs, n.toolIDsFromPacks(ctx, rc.OwnerID, packIDs))
	}
	if len(cfg.KnowledgeIDs) == 0 {
		if ids := profile.DefaultKnowledgeIDsSlice(); len(ids) > 0 {
			cfg.KnowledgeIDs = ids
		}
	}
	if cfg.KnowledgeTopK <= 0 && profile.DefaultKnowledgeTopK > 0 {
		cfg.KnowledgeTopK = profile.DefaultKnowledgeTopK
	}
	if strings.TrimSpace(cfg.KnowledgeMode) == "" && strings.TrimSpace(profile.DefaultKnowledgeMode) != "" {
		cfg.KnowledgeMode = profile.DefaultKnowledgeMode
	}
	if len(cfg.CallWorkflowIDs) == 0 && profile.AllowDelegation {
		if ids := profile.DefaultCallWorkflowIDsSlice(); len(ids) > 0 {
			cfg.CallWorkflowIDs = ids
		}
	}
	if len(cfg.MCPServerIDs) == 0 {
		if ids := profile.DefaultMCPServerIDsSlice(); len(ids) > 0 {
			cfg.MCPServerIDs = ids
		}
	}
	if cfg.MaxWorkflowCallDepth <= 0 && profile.DefaultMaxWorkflowCallDepth > 0 {
		cfg.MaxWorkflowCallDepth = profile.DefaultMaxWorkflowCallDepth
	}
	if len(cfg.OutputSchemaJSON) == 0 && len(profile.OutputSchemaJSON) > 0 && string(bytes.TrimSpace(profile.OutputSchemaJSON)) != "{}" {
		cfg.OutputSchemaJSON = profile.OutputSchemaJSON
	}
	return cfg, nil
}

type profileContextPolicy struct {
	MaxInputChars  int `json:"max_input_chars"`
	MaxInputTokens int `json:"max_input_tokens"`
}

type profileMemoryPolicy struct {
	Enabled *bool `json:"enabled"`
}

type nodeToolPolicyOverride struct {
	RequireApprovalForRisk *[]string `json:"require_approval_for_risk"`
	MaxToolTimeoutMS       *int      `json:"max_tool_timeout_ms"`
	MaxToolOutputBytes     *int      `json:"max_tool_output_bytes"`
	AllowedHosts           *[]string `json:"allowed_hosts"`
}

func applyProfileMemoryPolicy(cfg agentRuntimeConfig, raw json.RawMessage) agentRuntimeConfig {
	if cfg.MemoryEnabled || len(raw) == 0 || string(bytes.TrimSpace(raw)) == "{}" || string(bytes.TrimSpace(raw)) == "null" {
		return cfg
	}
	var policy profileMemoryPolicy
	if err := json.Unmarshal(raw, &policy); err != nil {
		return cfg
	}
	if policy.Enabled != nil && *policy.Enabled {
		cfg.MemoryEnabled = true
	}
	return cfg
}

func applyProfileContextPolicy(cfg agentRuntimeConfig, raw json.RawMessage) agentRuntimeConfig {
	if (cfg.MaxInputChars > 0 || cfg.MaxInputTokens > 0) || len(raw) == 0 || string(bytes.TrimSpace(raw)) == "{}" || string(bytes.TrimSpace(raw)) == "null" {
		return cfg
	}
	var policy profileContextPolicy
	if err := json.Unmarshal(raw, &policy); err != nil {
		return cfg
	}
	if policy.MaxInputChars > 0 {
		cfg.MaxInputChars = policy.MaxInputChars
	} else if policy.MaxInputTokens > 0 {
		cfg.MaxInputTokens = policy.MaxInputTokens
		cfg.MaxInputChars = policy.MaxInputTokens * 4
	}
	return cfg
}

func applyNodeContextPolicy(cfg agentRuntimeConfig, raw json.RawMessage) agentRuntimeConfig {
	if len(raw) == 0 || string(bytes.TrimSpace(raw)) == "{}" || string(bytes.TrimSpace(raw)) == "null" {
		return cfg
	}
	var policy profileContextPolicy
	if err := json.Unmarshal(raw, &policy); err != nil {
		return cfg
	}
	if policy.MaxInputChars > 0 {
		cfg.MaxInputChars = policy.MaxInputChars
	} else if policy.MaxInputTokens > 0 {
		cfg.MaxInputTokens = policy.MaxInputTokens
		cfg.MaxInputChars = policy.MaxInputTokens * 4
	}
	return cfg
}

func applyProfileToolPolicy(cfg agentRuntimeConfig, raw json.RawMessage) agentRuntimeConfig {
	if len(raw) == 0 || string(bytes.TrimSpace(raw)) == "{}" || string(bytes.TrimSpace(raw)) == "null" {
		return cfg
	}
	var policy runtimeagent.ToolPolicy
	if err := json.Unmarshal(raw, &policy); err != nil {
		return cfg
	}
	if len(cfg.RequireApprovalForRisk) == 0 && len(policy.RequireApprovalForRisk) > 0 {
		cfg.RequireApprovalForRisk = append([]string(nil), policy.RequireApprovalForRisk...)
	}
	if cfg.MaxToolTimeoutMS <= 0 && policy.MaxToolTimeoutMS > 0 {
		cfg.MaxToolTimeoutMS = policy.MaxToolTimeoutMS
	}
	if cfg.MaxToolOutputBytes <= 0 && policy.MaxToolOutputBytes > 0 {
		cfg.MaxToolOutputBytes = policy.MaxToolOutputBytes
	}
	if len(cfg.AllowedHosts) == 0 && len(policy.AllowedHosts) > 0 {
		cfg.AllowedHosts = append([]string(nil), policy.AllowedHosts...)
	}
	return cfg
}

func applyNodeToolPolicy(cfg agentRuntimeConfig, raw json.RawMessage) agentRuntimeConfig {
	if len(raw) == 0 || string(bytes.TrimSpace(raw)) == "{}" || string(bytes.TrimSpace(raw)) == "null" {
		return cfg
	}
	var policy nodeToolPolicyOverride
	if err := json.Unmarshal(raw, &policy); err != nil {
		return cfg
	}
	if policy.RequireApprovalForRisk != nil {
		cfg.RequireApprovalForRisk = append([]string(nil), (*policy.RequireApprovalForRisk)...)
	}
	if policy.MaxToolTimeoutMS != nil {
		cfg.MaxToolTimeoutMS = *policy.MaxToolTimeoutMS
	}
	if policy.MaxToolOutputBytes != nil {
		cfg.MaxToolOutputBytes = *policy.MaxToolOutputBytes
	}
	if policy.AllowedHosts != nil {
		cfg.AllowedHosts = append([]string(nil), (*policy.AllowedHosts)...)
	}
	return cfg
}

func applyNodeMemoryPolicy(cfg agentRuntimeConfig, raw json.RawMessage) agentRuntimeConfig {
	if len(raw) == 0 || string(bytes.TrimSpace(raw)) == "{}" || string(bytes.TrimSpace(raw)) == "null" {
		return cfg
	}
	var policy profileMemoryPolicy
	if err := json.Unmarshal(raw, &policy); err != nil {
		return cfg
	}
	if policy.Enabled != nil {
		cfg.MemoryEnabled = *policy.Enabled
	}
	return cfg
}

func validateAgentToolPolicyJSON(raw json.RawMessage, nodeType string) error {
	if len(raw) == 0 || string(bytes.TrimSpace(raw)) == "{}" || string(bytes.TrimSpace(raw)) == "null" {
		return nil
	}
	var policy nodeToolPolicyOverride
	if err := json.Unmarshal(raw, &policy); err != nil {
		return fmt.Errorf("%w: %s tool_policy_json is invalid", agenterrors.ErrInvalidInput, nodeType)
	}
	if policy.MaxToolTimeoutMS != nil && (*policy.MaxToolTimeoutMS < 0 || *policy.MaxToolTimeoutMS > 10*60*1000) {
		return fmt.Errorf("%w: %s max_tool_timeout_ms must be <= 600000", agenterrors.ErrInvalidInput, nodeType)
	}
	if policy.MaxToolOutputBytes != nil && (*policy.MaxToolOutputBytes < 0 || *policy.MaxToolOutputBytes > 2*1024*1024) {
		return fmt.Errorf("%w: %s max_tool_output_bytes must be <= 2097152", agenterrors.ErrInvalidInput, nodeType)
	}
	if policy.RequireApprovalForRisk != nil {
		for _, risk := range *policy.RequireApprovalForRisk {
			normalized := strings.TrimSpace(risk)
			if normalized != "" && normalized != toolruntime.RiskLow && normalized != toolruntime.RiskMedium && normalized != toolruntime.RiskHigh {
				return fmt.Errorf("%w: %s require_approval_for_risk contains unsupported risk level", agenterrors.ErrInvalidInput, nodeType)
			}
		}
	}
	return nil
}

func validateAgentMemoryPolicyJSON(raw json.RawMessage, nodeType string) error {
	if len(raw) == 0 || string(bytes.TrimSpace(raw)) == "{}" || string(bytes.TrimSpace(raw)) == "null" {
		return nil
	}
	var policy profileMemoryPolicy
	if err := json.Unmarshal(raw, &policy); err != nil {
		return fmt.Errorf("%w: %s memory_policy_json is invalid", agenterrors.ErrInvalidInput, nodeType)
	}
	return nil
}

func validateAgentContextPolicyJSON(raw json.RawMessage, nodeType string) error {
	if len(raw) == 0 || string(bytes.TrimSpace(raw)) == "{}" || string(bytes.TrimSpace(raw)) == "null" {
		return nil
	}
	var policy profileContextPolicy
	if err := json.Unmarshal(raw, &policy); err != nil {
		return fmt.Errorf("%w: %s context_policy_json is invalid", agenterrors.ErrInvalidInput, nodeType)
	}
	if policy.MaxInputChars < 0 || policy.MaxInputTokens < 0 {
		return fmt.Errorf("%w: %s context policy limits must be positive", agenterrors.ErrInvalidInput, nodeType)
	}
	return nil
}

func (n AgentNode) toolIDsFromPacks(ctx context.Context, ownerID int64, packIDs []int64) []int64 {
	ids := make([]int64, 0)
	for _, packID := range packIDs {
		if packID <= 0 {
			continue
		}
		toolIDs, err := n.ToolPacks.ListToolIDs(ctx, ownerID, packID)
		if err != nil {
			continue
		}
		ids = append(ids, toolIDs...)
	}
	return ids
}

func mergeInt64IDs(values ...[]int64) []int64 {
	seen := map[int64]bool{}
	merged := make([]int64, 0)
	for _, list := range values {
		for _, id := range list {
			if id <= 0 || seen[id] {
				continue
			}
			seen[id] = true
			merged = append(merged, id)
		}
	}
	return merged
}

func profileSystemPrompt(profile *workflow.Profile) string {
	if profile == nil {
		return ""
	}
	if prompt := strings.TrimSpace(profile.SystemPrompt); prompt != "" {
		return prompt
	}
	parts := make([]string, 0, 3)
	if role := strings.TrimSpace(profile.Role); role != "" {
		parts = append(parts, "角色："+role)
	}
	if goal := strings.TrimSpace(profile.Goal); goal != "" {
		parts = append(parts, "目标："+goal)
	}
	if backstory := strings.TrimSpace(profile.Backstory); backstory != "" {
		parts = append(parts, "背景："+backstory)
	}
	return strings.Join(parts, "\n")
}

func agentMode(mode string) string {
	mode = strings.TrimSpace(mode)
	if mode == "" {
		return "react"
	}
	return mode
}

func agentStepRecord(step runtimeagent.RunStep, nodeID string) engine.AgentStepRecord {
	return engine.AgentStepRecord{
		NodeID:        nodeID,
		StepIndex:     step.Index,
		StepType:      step.Type,
		Role:          step.Role,
		Content:       step.Content,
		ToolCallID:    step.ToolCallID,
		ToolName:      step.ToolName,
		ArgumentsJSON: step.ArgumentsJSON,
		OutputJSON:    step.OutputJSON,
		Compressed:    step.Compressed,
		ErrorMessage:  step.Error,
		TokenCount:    step.TokenCount,
		LatencyMS:     step.LatencyMS,
		ProviderID:    step.ProviderID,
		Model:         step.Model,
	}
}

func (n AgentNode) loadTools(ctx context.Context, ownerID int64, cfg agentRuntimeConfig) ([]toolruntime.RuntimeTool, error) {
	tools := make([]toolruntime.RuntimeTool, 0, len(cfg.ToolIDs)+2)
	tools = append(tools, toolruntime.HumanApprovalTool{})
	if len(cfg.KnowledgeIDs) > 0 {
		if n.Retriever == nil {
			return nil, fmt.Errorf("agent_loop retriever is not configured")
		}
		tools = append(tools, toolruntime.KnowledgeSearchTool{
			Retriever: n.Retriever,
			KBIDs:     cfg.KnowledgeIDs,
			DefaultK:  cfg.KnowledgeTopK,
			Mode:      retrieval.Mode(cfg.KnowledgeMode),
		})
	}
	if len(cfg.CallWorkflowIDs) > 0 {
		if n.WorkflowCaller == nil {
			return nil, fmt.Errorf("agent_loop workflow caller is not configured")
		}
		tools = append(tools, toolruntime.WorkflowCallTool{
			Caller:             n.WorkflowCaller,
			AllowedWorkflowIDs: cfg.CallWorkflowIDs,
			MaxDepth:           cfg.MaxWorkflowCallDepth,
			ToolName:           "call_agent",
		})
	}
	if len(cfg.MCPServerIDs) > 0 {
		if n.MCPServers == nil {
			return nil, fmt.Errorf("agent_loop mcp server repository is not configured")
		}
		loaded, err := n.loadMCPTools(ctx, ownerID, cfg.MCPServerIDs)
		if err != nil {
			return nil, err
		}
		tools = append(tools, loaded...)
	}
	if cfg.CodeExecutionEnabled {
		if n.Sandbox == nil {
			return nil, fmt.Errorf("agent_loop sandbox runner is not configured")
		}
		tools = append(tools, toolruntime.PythonSandboxTool{Runner: n.Sandbox})
	}
	if cfg.MemoryEnabled {
		if n.Memories == nil {
			return nil, fmt.Errorf("agent_loop memory repository is not configured")
		}
		tools = append(tools,
			toolruntime.MemoryReadTool{Memories: n.Memories},
			toolruntime.MemoryWriteTool{Memories: n.Memories, Logs: n.MemoryLogs},
		)
	}
	if len(cfg.ToolIDs) == 0 {
		return tools, nil
	}
	if n.Tools == nil {
		return nil, fmt.Errorf("agent_loop tool registry is not configured")
	}
	loaded, err := n.Tools.LoadForAgent(ctx, ownerID, cfg.ToolIDs)
	if err != nil {
		return nil, err
	}
	return append(tools, loaded...), nil
}

func (n AgentNode) loadMCPTools(ctx context.Context, ownerID int64, serverIDs []int64) ([]toolruntime.RuntimeTool, error) {
	loaded := make([]toolruntime.RuntimeTool, 0)
	for _, serverID := range serverIDs {
		if serverID <= 0 {
			continue
		}
		server, err := n.MCPServers.FindServerByID(ctx, ownerID, serverID)
		if err != nil {
			return nil, err
		}
		if server.Status != tool.MCPStatusActive {
			continue
		}
		client := mcpClientFromDomainServer(server)
		defs := cachedMCPToolDefs(ctx, n.MCPServers, ownerID, serverID)
		if len(defs) == 0 {
			var err error
			defs, err = client.Discover(ctx)
			if err != nil {
				return nil, err
			}
		}
		for _, def := range defs {
			loaded = append(loaded, toolruntime.NewMCPToolRuntime(def, client))
		}
	}
	return loaded, nil
}

func cachedMCPToolDefs(ctx context.Context, repo tool.MCPRepository, ownerID, serverID int64) []toolruntime.MCPToolDef {
	if repo == nil {
		return nil
	}
	cached, err := repo.ListToolCache(ctx, ownerID, serverID)
	if err != nil || len(cached) == 0 {
		return nil
	}
	defs := make([]toolruntime.MCPToolDef, 0, len(cached))
	for _, item := range cached {
		name := strings.TrimSpace(item.ToolName)
		if name == "" {
			continue
		}
		defs = append(defs, toolruntime.MCPToolDef{
			Name:        name,
			Description: item.Description,
			Parameters:  item.ParametersJSON,
		})
	}
	return defs
}

func resolveAgentTask(template string, rc *engine.RunContext, input engine.NodeInput) string {
	task := strings.TrimSpace(engine.ResolveTemplate(template, rc))
	if task != "" {
		return task
	}
	for _, key := range []string{"prompt", "query", "content", "final_answer"} {
		if value, ok := input[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
		if value, ok := rc.Input[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	if len(input) > 0 {
		data, _ := json.Marshal(input)
		return string(data)
	}
	return ""
}

func agentStepPayload(step runtimeagent.RunStep, providerID int64, model string) map[string]any {
	payload := map[string]any{
		"index":      step.Index,
		"type":       step.Type,
		"latency_ms": step.LatencyMS,
	}
	if step.Role != "" {
		payload["role"] = step.Role
	}
	if step.Content != "" {
		payload["content"] = step.Content
	}
	if step.ToolCallID != "" {
		payload["tool_call_id"] = step.ToolCallID
	}
	if step.ToolName != "" {
		payload["tool_name"] = step.ToolName
	}
	if len(step.ArgumentsJSON) > 0 {
		payload["arguments_json"] = json.RawMessage(step.ArgumentsJSON)
	}
	if len(step.OutputJSON) > 0 {
		payload["output_json"] = json.RawMessage(step.OutputJSON)
	}
	if step.Compressed {
		payload["compressed"] = true
	}
	if step.IsError {
		payload["is_error"] = true
	}
	if step.Error != "" {
		payload["error"] = step.Error
	}
	if step.TokenCount > 0 {
		payload["token_count"] = step.TokenCount
	}
	if providerID > 0 {
		payload["provider_id"] = providerID
	}
	if model != "" {
		payload["model"] = model
	}
	return payload
}

func (n AgentNode) recordAgentStep(ctx context.Context, rc *engine.RunContext, nodeType string, step runtimeagent.RunStep, providerID int64, model string) {
	if rc.AgentSteps != nil {
		_ = rc.AgentSteps.RecordAgentStep(ctx, rc, agentStepRecord(step, rc.CurrentNodeID))
	}
	emitRuntimeEvent(ctx, rc, runtimeevent.Event{
		Type:     runtimeevent.AgentStep,
		RunID:    rc.RunID,
		NodeID:   rc.CurrentNodeID,
		NodeType: nodeType,
		Payload:  agentStepPayload(step, providerID, model),
	})
}

func checkpointHashMismatch(checkpoint *runtimeagent.Checkpoint, tools []toolruntime.RuntimeTool, policy runtimeagent.ToolPolicy) string {
	if checkpoint == nil || checkpoint.Metadata == nil {
		return ""
	}
	storedRegistryHash, _ := checkpoint.Metadata["tool_registry_hash"].(string)
	if storedRegistryHash != "" && storedRegistryHash != stableRuntimeJSONHash(runtimeToolNames(tools)) {
		return "tool registry changed since checkpoint"
	}
	storedPolicyHash, _ := checkpoint.Metadata["tool_policy_hash"].(string)
	if storedPolicyHash != "" && storedPolicyHash != stableRuntimeJSONHash(policy) {
		return "tool policy changed since checkpoint"
	}
	return ""
}

func pausedForCheckpointMismatch(checkpoint *runtimeagent.Checkpoint, reason string) engine.NodeOutput {
	if checkpoint.Metadata == nil {
		checkpoint.Metadata = map[string]any{}
	}
	checkpoint.Metadata["resume_blocked_reason"] = reason
	return engine.NodeOutput{
		"content":       "Agent resume paused: " + reason,
		"final_answer":  "Agent resume paused: " + reason,
		"stop_reason":   runtimeagent.StopReasonPaused,
		"checkpoint":    checkpoint,
		"context_trace": checkpoint.Context,
	}
}

func runtimeToolNames(tools []toolruntime.RuntimeTool) []string {
	names := make([]string, 0, len(tools))
	for _, item := range tools {
		if item == nil {
			continue
		}
		if name := strings.TrimSpace(item.Name()); name != "" {
			names = append(names, name)
		}
	}
	return names
}

func stableRuntimeJSONHash(value any) string {
	bytes, _ := json.Marshal(value)
	sum := sha256.Sum256(bytes)
	return hex.EncodeToString(sum[:])
}

func parseAndValidateStructuredOutput(schema json.RawMessage, content string, parsed *any) error {
	if err := json.Unmarshal([]byte(content), parsed); err != nil {
		return fmt.Errorf("final_answer must be valid JSON for output_schema_json")
	}
	if err := validateSimpleJSONSchema(schema, *parsed); err != nil {
		return err
	}
	return nil
}

func (n AgentNode) repairStructuredOutput(ctx context.Context, provider llm.ChatProviderConfig, model string, temperature *float64, task, finalAnswer string, schema json.RawMessage, validationErr error) (string, error) {
	if n.LLM == nil {
		return "", fmt.Errorf("llm client is not configured")
	}
	prompt := fmt.Sprintf(`Rewrite the agent final answer so it strictly matches the JSON schema.

Task:
%s

Validation error:
%s

JSON schema:
%s

Current final answer:
%s

Return only valid JSON. Do not include markdown fences or explanation.`, task, validationErr.Error(), string(schema), finalAnswer)
	resp, err := n.LLM.ChatWithTools(ctx, provider, llm.ToolChatRequest{
		Model:       model,
		Messages:    []llm.ChatMessage{{Role: "user", Content: prompt}},
		Tools:       nil,
		Temperature: temperature,
	})
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(resp.Message.Content), nil
}

func buildConversationContext(ctx context.Context, n AgentNode, rc *engine.RunContext, maxInputChars int) []runtimeagent.ContextBlock {
	if n.MessageHistory == nil || rc.ConversationID == nil || *rc.ConversationID <= 0 {
		return nil
	}
	msgs, err := n.MessageHistory.ListByConversation(ctx, rc.OwnerID, *rc.ConversationID)
	if err != nil || len(msgs) == 0 {
		return nil
	}
	const maxRecentMessages = 20
	recent := msgs
	if len(msgs) > maxRecentMessages {
		recent = msgs[len(msgs)-maxRecentMessages:]
	}
	blocks := make([]runtimeagent.ContextBlock, 0, len(recent))
	for _, m := range recent {
		blocks = append(blocks, runtimeagent.ContextBlock{
			Name:    "conversation",
			Role:    m.Role,
			Content: m.Content,
			Pinned:  false,
		})
	}
	return blocks
}
