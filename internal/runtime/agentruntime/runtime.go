package agentruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"agentcanvas/internal/domain/audit"
	"agentcanvas/internal/domain/contextresource"
	"agentcanvas/internal/domain/conversation"
	"agentcanvas/internal/domain/memory"
	"agentcanvas/internal/domain/reflection"
	"agentcanvas/internal/domain/retrieval"
	"agentcanvas/internal/domain/skill"
	"agentcanvas/internal/domain/tool"
	runtimeagent "agentcanvas/internal/runtime/agent"
	"agentcanvas/internal/runtime/conversationcontext"
	"agentcanvas/internal/runtime/harness/rules"
	"agentcanvas/internal/runtime/sandbox"
	"agentcanvas/internal/runtime/toolruntime"

	"agentcanvas/internal/infrastructure/llm"
	agenterrors "agentcanvas/internal/pkg/errors"
)

type coreRepositories struct {
	Providers        ProviderConfigLoader
	ToolPacks        tool.PackRepository
	Skills           skill.Repository
	MCPServers       tool.MCPRepository
	Retriever        retrieval.Retriever
	MemoryReader     MemoryBatchReader
	MemoryRetriever  memory.SemanticRetriever
	Memories         memory.Repository
	MemoryLogs       memory.WriteLogRepository
	MemoryRecallLogs memory.RecallLogRepository
	MemoryCandidates memory.CandidateWriter
	MessageHistory   MessageHistoryReader
	Compactions      conversation.CompactionRepository
	SessionSearch    conversation.MessageSearchIndex
	ContextIndex     contextresource.Index
	ToolInvocations  tool.InvocationRepository
}

type coreClients struct {
	LLM          llm.ToolCallingClient
	Embedder     llm.EmbeddingClient
	PythonBridge PythonToolLoader
	Archival     ArchivalIndexFactory
}

type coreTooling struct {
	Tools               toolruntime.Registry
	SubagentDispatcher  toolruntime.SubagentDispatcher
	PythonToolAllowlist []string
}

type coreWorkspace struct {
	Sandbox          sandbox.Runner
	Coordinator      *conversationcontext.Coordinator
	Git              toolruntime.GitOperations
	FileReadMaxChars int
	MaxOutputBytes   int
	WorkspaceTimeout time.Duration
	SkillRoot        string
}

type coreObservability struct {
	Audits      audit.Repository
	Reflections reflection.Advisor
}

type corePolicies struct {
	OnExtractTrigger func(ctx context.Context, ownerID int64, conversationID int64, roundNumber int)
}

type runtimeCore struct {
	coreRepositories
	coreClients
	coreTooling
	coreWorkspace
	coreObservability
	corePolicies
}

type MemoryBatchReader interface {
	GetMany(ctx context.Context, ownerID int64, ids []int64) ([]memory.Memory, error)
}

type queryUnderstandingPlanner interface {
	PlanQuery(ctx context.Context, req retrieval.RetrievalRequest) (retrieval.QueryPlan, error)
}

type AgentResumeOptions struct {
	Checkpoint    *runtimeagent.Checkpoint
	Approved      bool
	RejectionNote string
}

type RuntimeModelConfig struct {
	Mode        string   `json:"mode"`
	ProviderID  int64    `json:"provider_id"`
	Model       string   `json:"model"`
	Temperature *float64 `json:"temperature"`
}

type RuntimePromptConfig struct {
	SystemPrompt            string                      `json:"system_prompt"`
	TaskTemplate            string                      `json:"task_template"`
	CompactPrompt           string                      `json:"compact_prompt"`
	OutputSchemaJSON        json.RawMessage             `json:"output_schema_json"`
	ReturnIntermediateSteps bool                        `json:"return_intermediate_steps"`
	OutputMode              string                      `json:"output_mode"`
	AdditionalContextBlocks []runtimeagent.ContextBlock `json:"-"`
}

type RuntimeToolConfig struct {
	ToolIDs                []int64         `json:"tool_ids"`
	ToolPackIDs            []int64         `json:"tool_pack_ids"`
	MCPServerIDs           []int64         `json:"mcp_server_ids"`
	MaxSubagentDepth       int             `json:"max_subagent_depth"`
	CodeExecutionEnabled   bool            `json:"code_execution_enabled"`
	PythonToolNames        []string        `json:"python_tool_names"`
	MaxParallelSubAgents   int             `json:"max_parallel_sub_agents"`
	AllowSubagents         bool            `json:"allow_subagents"`
	RequireApprovalForRisk []string        `json:"require_approval_for_risk"`
	MaxToolTimeoutMS       int             `json:"max_tool_timeout_ms"`
	MaxToolOutputBytes     int             `json:"max_tool_output_bytes"`
	AllowedHosts           []string        `json:"allowed_hosts"`
	DenyAllHosts           bool            `json:"deny_all_hosts"`
	ToolPolicyJSON         json.RawMessage `json:"tool_policy_json"`
}

type RuntimeResourceRefs struct {
	SkillIDs         []int64 `json:"skill_ids"`
	SkillLoadingMode string  `json:"skill_loading_mode"`
	KnowledgeBaseIDs []int64 `json:"knowledge_base_ids"`
	KnowledgeTopK    int     `json:"knowledge_top_k"`
	KnowledgeMode    string  `json:"knowledge_mode"`
}

type RuntimeMemoryPolicy struct {
	MemoryEnabled        bool            `json:"memory_enabled"`
	MemoryEnabledSet     bool            `json:"-"`
	MemoryPolicyJSON     json.RawMessage `json:"memory_policy_json"`
	MemoryPolicy         memory.Policy   `json:"-"`
	ContextPolicyJSON    json.RawMessage `json:"context_policy_json"`
	ReflectionEnabled    bool            `json:"reflection_enabled"`
	ReflectionPolicyJSON json.RawMessage `json:"reflection_policy_json"`
	RetrievalPolicy      retrievalPolicy `json:"-"`
}

type RuntimeExecutionLimits struct {
	MaxIterations                   int    `json:"max_iterations"`
	MaxToolCalls                    int    `json:"max_tool_calls"`
	MaxExecutionTimeMS              int    `json:"max_execution_time_ms"`
	MaxInputChars                   int    `json:"max_input_chars"`
	MaxInputTokens                  int    `json:"max_input_tokens"`
	ContextWindowTokens             int    `json:"context_window_tokens"`
	ReservedOutputTokens            int    `json:"reserved_output_tokens"`
	ContextSafetyMarginTokens       int    `json:"context_safety_margin_tokens"`
	ModelAutoCompactTokenLimit      int    `json:"model_auto_compact_token_limit"`
	ModelAutoCompactTokenLimitScope string `json:"model_auto_compact_token_limit_scope"`
	CompactionProviderID            int64  `json:"compaction_provider_id"`
	CompactionModel                 string `json:"compaction_model"`
	MaxRuleTokens                   int    `json:"max_rule_tokens"`
}

type RuntimeRules struct {
	RuleHash string       `json:"-"`
	Rules    []rules.Rule `json:"-"`
}

type agentRuntimeConfig struct {
	RuntimeModelConfig
	RuntimePromptConfig
	RuntimeToolConfig
	RuntimeResourceRefs
	RuntimeMemoryPolicy
	RuntimeExecutionLimits
	RuntimeRules
}

func (c *agentRuntimeConfig) UnmarshalJSON(data []byte) error {
	type flat agentRuntimeConfig
	var decoded flat
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*c = agentRuntimeConfig(decoded)
	var nested struct {
		ModelConfig     *RuntimeModelConfig     `json:"model_config"`
		PromptConfig    *RuntimePromptConfig    `json:"prompt_config"`
		ToolConfig      *RuntimeToolConfig      `json:"tool_config"`
		ResourceRefs    *RuntimeResourceRefs    `json:"resource_refs"`
		MemoryPolicy    *RuntimeMemoryPolicy    `json:"memory_policy"`
		ExecutionLimits *RuntimeExecutionLimits `json:"execution_limits"`
		RulesConfig     *RuntimeRules           `json:"rules_config"`
	}
	if err := json.Unmarshal(data, &nested); err != nil {
		return err
	}
	if nested.ModelConfig != nil {
		c.RuntimeModelConfig = *nested.ModelConfig
	}
	if nested.PromptConfig != nil {
		c.RuntimePromptConfig = *nested.PromptConfig
	}
	if nested.ToolConfig != nil {
		c.RuntimeToolConfig = *nested.ToolConfig
	}
	if nested.ResourceRefs != nil {
		c.RuntimeResourceRefs = *nested.ResourceRefs
	}
	if nested.MemoryPolicy != nil {
		c.RuntimeMemoryPolicy = *nested.MemoryPolicy
	}
	if nested.ExecutionLimits != nil {
		c.RuntimeExecutionLimits = *nested.ExecutionLimits
	}
	if nested.RulesConfig != nil {
		c.RuntimeRules = *nested.RulesConfig
	}
	return nil
}

func decodeRuntimeConfig(config json.RawMessage) (agentRuntimeConfig, error) {
	var cfg agentRuntimeConfig
	if err := json.Unmarshal(config, &cfg); err != nil {
		return cfg, fmt.Errorf("%w: invalid agent config", agenterrors.ErrInvalidInput)
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(config, &fields) == nil {
		_, cfg.MemoryEnabledSet = fields["memory_enabled"]
		if !cfg.MemoryEnabledSet {
			var nested map[string]json.RawMessage
			if json.Unmarshal(fields["memory_policy"], &nested) == nil {
				_, cfg.MemoryEnabledSet = nested["memory_enabled"]
			}
		}
	}
	return cfg, nil
}

func validateAgentRuntimeConfig(cfg agentRuntimeConfig, requireProvider bool) error {
	if requireProvider && cfg.ProviderID <= 0 {
		return fmt.Errorf("%w: agent runtime provider_id is required", agenterrors.ErrInvalidInput)
	}
	if cfg.MaxIterations < 0 || cfg.MaxIterations > 50 {
		return fmt.Errorf("%w: agent runtime max_iterations must be <= 50", agenterrors.ErrInvalidInput)
	}
	if cfg.MaxToolCalls < 0 || cfg.MaxToolCalls > 100 {
		return fmt.Errorf("%w: agent runtime max_tool_calls must be <= 100", agenterrors.ErrInvalidInput)
	}
	if cfg.MaxExecutionTimeMS < 0 || cfg.MaxExecutionTimeMS > 10*60*1000 {
		return fmt.Errorf("%w: agent runtime max_execution_time_ms must be <= 600000", agenterrors.ErrInvalidInput)
	}
	if cfg.MaxParallelSubAgents < 0 || cfg.MaxParallelSubAgents > 64 {
		return fmt.Errorf("%w: agent runtime max_parallel_sub_agents must be <= 64", agenterrors.ErrInvalidInput)
	}
	if cfg.OutputMode != "" && cfg.OutputMode != "final_answer" && cfg.OutputMode != "full" {
		return fmt.Errorf("%w: agent runtime output_mode must be final_answer or full", agenterrors.ErrInvalidInput)
	}
	if cfg.KnowledgeTopK < 0 || cfg.KnowledgeTopK > 20 {
		return fmt.Errorf("%w: agent runtime knowledge_top_k must be <= 20", agenterrors.ErrInvalidInput)
	}
	if cfg.KnowledgeMode != "" && cfg.KnowledgeMode != string(retrieval.ModeKeyword) && cfg.KnowledgeMode != string(retrieval.ModeVector) && cfg.KnowledgeMode != string(retrieval.ModeHybrid) {
		return fmt.Errorf("%w: unsupported agent runtime knowledge_mode", agenterrors.ErrInvalidInput)
	}
	if mode := strings.TrimSpace(cfg.RetrievalPolicy.Mode); mode != "" && mode != string(retrieval.ModeKeyword) && mode != string(retrieval.ModeVector) && mode != string(retrieval.ModeHybrid) {
		return fmt.Errorf("%w: unsupported agent runtime context retrieval mode", agenterrors.ErrInvalidInput)
	}
	if cfg.RetrievalPolicy.CandidateK < 0 || cfg.RetrievalPolicy.CandidateK > 200 || cfg.RetrievalPolicy.MaxRewrites < 0 || cfg.RetrievalPolicy.MaxRewrites > 3 || cfg.RetrievalPolicy.MaxSubqueries < 0 || cfg.RetrievalPolicy.MaxSubqueries > 3 {
		return fmt.Errorf("%w: agent runtime context retrieval limits are invalid", agenterrors.ErrInvalidInput)
	}
	if cfg.MaxSubagentDepth < 0 || cfg.MaxSubagentDepth > 5 {
		return fmt.Errorf("%w: agent runtime max_subagent_depth must be <= 5", agenterrors.ErrInvalidInput)
	}
	if len(cfg.PythonToolNames) > 64 {
		return fmt.Errorf("%w: agent runtime python_tool_names must contain at most 64 tools", agenterrors.ErrInvalidInput)
	}
	for _, name := range cfg.PythonToolNames {
		if strings.TrimSpace(name) == "" {
			return fmt.Errorf("%w: agent runtime python_tool_names contains an empty name", agenterrors.ErrInvalidInput)
		}
	}
	if cfg.Mode != "" && cfg.Mode != "react" && cfg.Mode != "plan_execute" {
		return fmt.Errorf("%w: agent runtime mode must be react or plan_execute", agenterrors.ErrInvalidInput)
	}
	for _, risk := range cfg.RequireApprovalForRisk {
		normalized := strings.TrimSpace(risk)
		if normalized != "" && normalized != toolruntime.RiskLow && normalized != toolruntime.RiskMedium && normalized != toolruntime.RiskHigh {
			return fmt.Errorf("%w: agent runtime require_approval_for_risk contains unsupported risk level", agenterrors.ErrInvalidInput)
		}
	}
	if cfg.MaxToolTimeoutMS < 0 || cfg.MaxToolTimeoutMS > 10*60*1000 {
		return fmt.Errorf("%w: agent runtime max_tool_timeout_ms must be <= 600000", agenterrors.ErrInvalidInput)
	}
	if cfg.MaxToolOutputBytes < 0 || cfg.MaxToolOutputBytes > 2*1024*1024 {
		return fmt.Errorf("%w: agent runtime max_tool_output_bytes must be <= 2097152", agenterrors.ErrInvalidInput)
	}
	if err := validateAgentMemoryPolicyJSON(cfg.MemoryPolicyJSON); err != nil {
		return err
	}
	if _, err := effectiveReflectionPolicy(cfg); err != nil {
		return fmt.Errorf("%w: agent runtime reflection_policy_json is invalid: %v", agenterrors.ErrInvalidInput, err)
	}
	if err := validateAgentContextPolicyJSON(cfg.ContextPolicyJSON); err != nil {
		return err
	}
	if err := validateAgentToolPolicyJSON(cfg.ToolPolicyJSON); err != nil {
		return err
	}
	return nil
}

// runAgent prepares and executes a single Agent run.
