package agent

import (
	"encoding/json"
	"time"

	"agentcanvas/internal/domain/reflection"
	"agentcanvas/internal/infrastructure/llm"
	"agentcanvas/internal/runtime/harness/hooks"
	"agentcanvas/internal/runtime/harness/rules"
	"agentcanvas/internal/runtime/toolruntime"
)

const (
	StopReasonFinalAnswer      = "final_answer"
	StopReasonMaxIterations    = "max_iterations_exceeded"
	StopReasonMaxToolCalls     = "max_tool_calls_exceeded"
	StopReasonTimeout          = "timeout"
	StopReasonCancelled        = "cancelled"
	StopReasonPaused           = "paused"
	StopReasonWaitingHuman     = "waiting_human"
	StopReasonLLMError         = "llm_error"
	StopReasonToolNameNotFound = "tool_name_not_found"
	StopReasonPlanCompleted    = "plan_completed"
	StopReasonReflectionFailed = "reflection_failed"
	StopReasonContextOverflow  = "context_overflow"
	StopReasonClarification    = "clarification_required"
)

const (
	StepTypeLLMResponse      = "llm_response"
	StepTypePlan             = "plan"
	StepTypeToolCall         = "tool_call"
	StepTypeToolResult       = "tool_result"
	StepTypeApproval         = "approval_required"
	StepTypeReflectionRecall = "reflection_recall"
	StepTypeReflection       = "reflection"
	StepTypePlanRevision     = "plan_revision"
	StepTypeFinalAnswer      = "final_answer"
	StepTypeError            = "error"
)

type RunRequest struct {
	OwnerID                         int64
	WorkflowID                      int64
	AgentID                         int64
	AgentReleaseID                  int64
	RunID                           int64
	NodeID                          string
	CallDepth                       int
	WorkflowCallChain               []int64
	ConversationID                  *int64
	Provider                        llm.ChatProviderConfig
	Model                           string
	Mode                            string
	Plan                            *Plan
	SystemPrompt                    string
	Task                            string
	ReflectionEnabled               bool
	ReflectionPolicy                reflection.Policy
	RecalledReflectionIDs           []int64
	Temperature                     *float64
	MaxIterations                   int
	MaxToolCalls                    int
	MaxExecutionTimeMS              int
	MaxParallelTools                int
	MaxInputChars                   int
	MaxInputTokens                  int
	ContextWindowTokens             int
	ReservedOutputTokens            int
	ContextSafetyMarginTokens       int
	ModelAutoCompactTokenLimit      int
	ModelAutoCompactTokenLimitScope string
	CompactPrompt                   string
	MaxRuleTokens                   int
	RuleTags                        []string
	RuleRiskLevel                   string
	RuleSetVersion                  string
	RuleSetID                       int64
	CompiledRuleHash                string
	CompiledRules                   *rules.CompiledRuleSet
	CustomRules                     []rules.Rule
	RuleTrace                       rules.Trace
	RuleSemanticScores              map[string]float64
	ContextBlocks                   []ContextBlock
	ToolPolicy                      ToolPolicy
	ToolHookChain                   hooks.ToolHookChain
	Tools                           []toolruntime.RuntimeTool
	ResumeMessages                  []llm.ChatMessage
	ResumeIteration                 int
	ResumeToolCalls                 int
	ResumeApprovedToolCallIDs       []string
}

type RunResult struct {
	FinalAnswer string          `json:"final_answer"`
	StopReason  string          `json:"stop_reason"`
	Iterations  int             `json:"iterations"`
	ToolCalls   int             `json:"tool_calls"`
	Usage       llm.Usage       `json:"usage"`
	Plan        *Plan           `json:"plan,omitempty"`
	Steps       []RunStep       `json:"steps,omitempty"`
	Context     ContextTrace    `json:"context_trace,omitempty"`
	HookTrace   []HookTrace     `json:"hook_trace,omitempty"`
	Approval    *Approval       `json:"approval,omitempty"`
	Checkpoint  *Checkpoint     `json:"checkpoint,omitempty"`
	Reflection  ReflectionTrace `json:"reflection_trace,omitempty"`
	StartedAt   time.Time       `json:"started_at"`
	FinishedAt  time.Time       `json:"finished_at"`
	LatencyMS   int             `json:"latency_ms"`
}

type InlineReflection struct {
	Action            string   `json:"action"`
	TriggerType       string   `json:"trigger_type"`
	RootCauseCategory string   `json:"root_cause_category"`
	RootCause         string   `json:"root_cause"`
	CorrectiveAction  string   `json:"corrective_action"`
	Lesson            string   `json:"lesson"`
	Applicability     string   `json:"applicability"`
	EvidenceSteps     []int    `json:"evidence_step_indexes,omitempty"`
	Severity          float64  `json:"severity"`
	Generalizability  float64  `json:"generalizability"`
	Confidence        float64  `json:"confidence"`
	Tags              []string `json:"tags,omitempty"`
}

type ReflectionTrace struct {
	RecalledIDs         []int64            `json:"recalled_ids,omitempty"`
	Inline              []InlineReflection `json:"inline,omitempty"`
	TriggerFingerprints []string           `json:"trigger_fingerprints,omitempty"`
	Errors              []string           `json:"errors,omitempty"`
	Usage               llm.Usage          `json:"usage,omitempty"`
}

type RunStep struct {
	Index         int             `json:"index"`
	Type          string          `json:"type"`
	Role          string          `json:"role,omitempty"`
	Content       string          `json:"content,omitempty"`
	ToolCallID    string          `json:"tool_call_id,omitempty"`
	ToolName      string          `json:"tool_name,omitempty"`
	ArgumentsJSON json.RawMessage `json:"arguments_json,omitempty"`
	OutputJSON    json.RawMessage `json:"output_json,omitempty"`
	Compressed    bool            `json:"compressed,omitempty"`
	IsError       bool            `json:"is_error,omitempty"`
	Error         string          `json:"error,omitempty"`
	LatencyMS     int             `json:"latency_ms,omitempty"`
	ProviderID    int64           `json:"provider_id,omitempty"`
	Model         string          `json:"model,omitempty"`
	TokenCount    int             `json:"token_count,omitempty"`
	CreatedAt     time.Time       `json:"created_at"`
}

type ToolPolicy = hooks.ToolPolicy

type Approval struct {
	ToolCallID string                   `json:"tool_call_id"`
	ToolName   string                   `json:"tool_name"`
	RiskLevel  string                   `json:"risk_level"`
	Reason     string                   `json:"reason"`
	Metadata   toolruntime.ToolMetadata `json:"metadata"`
}

type Checkpoint struct {
	Messages              []llm.ChatMessage      `json:"messages"`
	MessagesSummary       string                 `json:"messages_summary"`
	PendingToolCall       *llm.ToolCall          `json:"pending_tool_call,omitempty"`
	Context               ContextTrace           `json:"context"`
	ToolPolicy            ToolPolicy             `json:"tool_policy"`
	ToolNames             []string               `json:"tool_names"`
	Metadata              map[string]any         `json:"metadata,omitempty"`
	Plan                  *Plan                  `json:"plan,omitempty"`
	ReflectionPolicy      reflection.Policy      `json:"reflection_policy,omitempty"`
	RecalledReflectionIDs []int64                `json:"recalled_reflection_ids,omitempty"`
	RuleSetVersion        string                 `json:"rule_set_version,omitempty"`
	RuleSetID             int64                  `json:"rule_set_id,omitempty"`
	CompiledHash          string                 `json:"compiled_hash,omitempty"`
	CompiledRules         *rules.CompiledRuleSet `json:"compiled_rules,omitempty"`
	CustomRules           []rules.Rule           `json:"custom_rules,omitempty"`
}

type ContextBlock struct {
	Name    string `json:"name"`
	Role    string `json:"role"`
	Content string `json:"content"`
	Pinned  bool   `json:"pinned"`
}

type ContextTrace struct {
	MaxChars               int                 `json:"max_chars,omitempty"`
	MaxInputTokens         int                 `json:"max_input_tokens,omitempty"`
	UsedChars              int                 `json:"used_chars,omitempty"`
	UsedTokens             int                 `json:"used_tokens,omitempty"`
	EstimatedTokens        int                 `json:"estimated_tokens,omitempty"`
	SavedTokens            int                 `json:"saved_tokens,omitempty"`
	TokenAudit             TokenAudit          `json:"token_audit,omitempty"`
	RuleTrace              rules.Trace         `json:"rule_trace,omitempty"`
	Included               []string            `json:"included,omitempty"`
	Omitted                []string            `json:"omitted,omitempty"`
	Truncated              []string            `json:"truncated,omitempty"`
	Compressed             []string            `json:"compressed,omitempty"`
	Strategy               string              `json:"strategy,omitempty"`
	Blocks                 []ContextBlockTrace `json:"blocks,omitempty"`
	RuleRounds             []RuleRoundTrace    `json:"rule_rounds,omitempty"`
	RuleBudget             RuleBudget          `json:"rule_budget,omitempty"`
	RuleSetVersion         string              `json:"rule_set_version,omitempty"`
	RuleSetID              int64               `json:"rule_set_id,omitempty"`
	CompiledHash           string              `json:"compiled_hash,omitempty"`
	CoreOverflow           bool                `json:"core_overflow,omitempty"`
	MandatoryTokens        int                 `json:"mandatory_tokens,omitempty"`
	MandatoryBudgetTokens  int                 `json:"mandatory_budget_tokens,omitempty"`
	MandatoryDeficitTokens int                 `json:"mandatory_deficit_tokens,omitempty"`
	EstimatedPromptTokens  int                 `json:"estimated_prompt_tokens,omitempty"`
	ProviderPromptTokens   int                 `json:"provider_prompt_tokens,omitempty"`
	TokenEstimationError   int                 `json:"token_estimation_error,omitempty"`
	TokenCounterMethod     string              `json:"token_counter_method,omitempty"`
	TokenCounterError      string              `json:"token_counter_error,omitempty"`
	AutoCompactTokenLimit  int                 `json:"auto_compact_token_limit,omitempty"`
	AutoCompactLimitScope  string              `json:"auto_compact_token_limit_scope,omitempty"`
	Compactions            []CompactionTrace   `json:"compactions,omitempty"`
}

type CompactionTrace struct {
	Trigger      string `json:"trigger"`
	Scope        string `json:"scope"`
	Status       string `json:"status"`
	BeforeTokens int    `json:"before_tokens"`
	AfterTokens  int    `json:"after_tokens"`
	SavedTokens  int    `json:"saved_tokens"`
	Threshold    int    `json:"threshold"`
	ModelCalled  bool   `json:"model_called"`
	Error        string `json:"error,omitempty"`
	Summary      string `json:"-"`
}

type RuleRoundTrace struct {
	Iteration             int         `json:"iteration"`
	Loaded                []string    `json:"loaded,omitempty"`
	Removed               []string    `json:"removed,omitempty"`
	Trace                 rules.Trace `json:"trace"`
	Budget                RuleBudget  `json:"budget"`
	EstimatedPromptTokens int         `json:"estimated_prompt_tokens,omitempty"`
	ProviderPromptTokens  int         `json:"provider_prompt_tokens,omitempty"`
}

type TokenAudit struct {
	System           int `json:"system,omitempty"`
	Profile          int `json:"profile,omitempty"`
	RulesMandatory   int `json:"rules_mandatory,omitempty"`
	RulesOptional    int `json:"rules_optional,omitempty"`
	ToolSchema       int `json:"tool_schema,omitempty"`
	History          int `json:"history,omitempty"`
	Memory           int `json:"memory,omitempty"`
	ReflectionMemory int `json:"reflection_memory,omitempty"`
	WorkingMemory    int `json:"working_memory,omitempty"`
	Retrieval        int `json:"retrieval,omitempty"`
	Task             int `json:"task,omitempty"`
	Total            int `json:"total,omitempty"`
}

type ContextBlockTrace struct {
	Name            string `json:"name"`
	Role            string `json:"role"`
	Pinned          bool   `json:"pinned"`
	OriginalChars   int    `json:"original_chars"`
	IncludedChars   int    `json:"included_chars"`
	EstimatedTokens int    `json:"estimated_tokens"`
	SavedTokens     int    `json:"saved_tokens,omitempty"`
	Status          string `json:"status"`
}

type HookTrace struct {
	Hook     string         `json:"hook"`
	Action   string         `json:"action"`
	Reason   string         `json:"reason,omitempty"`
	ToolName string         `json:"tool_name,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
}
