package agent

import (
	"encoding/json"
	"time"

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
)

const (
	StepTypeLLMResponse = "llm_response"
	StepTypePlan        = "plan"
	StepTypeToolCall    = "tool_call"
	StepTypeToolResult  = "tool_result"
	StepTypeApproval    = "approval_required"
	StepTypeReflection  = "reflection"
	StepTypeFinalAnswer = "final_answer"
	StepTypeError       = "error"
)

type RunRequest struct {
	OwnerID                   int64
	WorkflowID                int64
	RunID                     int64
	NodeID                    string
	CallDepth                 int
	WorkflowCallChain         []int64
	ConversationID            *int64
	Provider                  llm.ChatProviderConfig
	Model                     string
	Mode                      string
	Plan                      *Plan
	SystemPrompt              string
	Task                      string
	ReflectionEnabled         bool
	Temperature               *float64
	MaxIterations             int
	MaxToolCalls              int
	MaxExecutionTimeMS        int
	MaxParallelTools          int
	MaxInputChars             int
	MaxInputTokens            int
	ContextWindowTokens       int
	ReservedOutputTokens      int
	ContextSafetyMarginTokens int
	MaxRuleTokens             int
	RuleTags                  []string
	RuleRiskLevel             string
	RuleSetVersion            string
	CustomRules               []rules.Rule
	RuleTrace                 rules.Trace
	ContextBlocks             []ContextBlock
	ToolPolicy                ToolPolicy
	ToolHookChain             hooks.ToolHookChain
	Tools                     []toolruntime.RuntimeTool
	ResumeMessages            []llm.ChatMessage
	ResumeIteration           int
	ResumeToolCalls           int
	ResumeApprovedToolCallIDs []string
}

type RunResult struct {
	FinalAnswer string       `json:"final_answer"`
	StopReason  string       `json:"stop_reason"`
	Iterations  int          `json:"iterations"`
	ToolCalls   int          `json:"tool_calls"`
	Usage       llm.Usage    `json:"usage"`
	Plan        *Plan        `json:"plan,omitempty"`
	Steps       []RunStep    `json:"steps,omitempty"`
	Context     ContextTrace `json:"context_trace,omitempty"`
	HookTrace   []HookTrace  `json:"hook_trace,omitempty"`
	Approval    *Approval    `json:"approval,omitempty"`
	Checkpoint  *Checkpoint  `json:"checkpoint,omitempty"`
	StartedAt   time.Time    `json:"started_at"`
	FinishedAt  time.Time    `json:"finished_at"`
	LatencyMS   int          `json:"latency_ms"`
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
	Messages        []llm.ChatMessage `json:"messages"`
	MessagesSummary string            `json:"messages_summary"`
	PendingToolCall *llm.ToolCall     `json:"pending_tool_call,omitempty"`
	Context         ContextTrace      `json:"context"`
	ToolPolicy      ToolPolicy        `json:"tool_policy"`
	ToolNames       []string          `json:"tool_names"`
	Metadata        map[string]any    `json:"metadata,omitempty"`
	RuleSetVersion  string            `json:"rule_set_version,omitempty"`
	CustomRules     []rules.Rule      `json:"custom_rules,omitempty"`
}

type ContextBlock struct {
	Name    string `json:"name"`
	Role    string `json:"role"`
	Content string `json:"content"`
	Pinned  bool   `json:"pinned"`
}

type ContextTrace struct {
	MaxChars              int                 `json:"max_chars,omitempty"`
	MaxInputTokens        int                 `json:"max_input_tokens,omitempty"`
	UsedChars             int                 `json:"used_chars,omitempty"`
	UsedTokens            int                 `json:"used_tokens,omitempty"`
	EstimatedTokens       int                 `json:"estimated_tokens,omitempty"`
	SavedTokens           int                 `json:"saved_tokens,omitempty"`
	TokenAudit            TokenAudit          `json:"token_audit,omitempty"`
	RuleTrace             rules.Trace         `json:"rule_trace,omitempty"`
	Included              []string            `json:"included,omitempty"`
	Omitted               []string            `json:"omitted,omitempty"`
	Truncated             []string            `json:"truncated,omitempty"`
	Compressed            []string            `json:"compressed,omitempty"`
	Strategy              string              `json:"strategy,omitempty"`
	Blocks                []ContextBlockTrace `json:"blocks,omitempty"`
	RuleRounds            []RuleRoundTrace    `json:"rule_rounds,omitempty"`
	RuleBudget            RuleBudget          `json:"rule_budget,omitempty"`
	RuleSetVersion        string              `json:"rule_set_version,omitempty"`
	CoreOverflow          bool                `json:"core_overflow,omitempty"`
	EstimatedPromptTokens int                 `json:"estimated_prompt_tokens,omitempty"`
	ProviderPromptTokens  int                 `json:"provider_prompt_tokens,omitempty"`
	TokenEstimationError  int                 `json:"token_estimation_error,omitempty"`
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
	System        int `json:"system,omitempty"`
	Profile       int `json:"profile,omitempty"`
	RulesL0       int `json:"rules_l0,omitempty"`
	RulesL1       int `json:"rules_l1,omitempty"`
	RulesL2       int `json:"rules_l2,omitempty"`
	RulesL3       int `json:"rules_l3,omitempty"`
	RulesL4       int `json:"rules_l4,omitempty"`
	ToolSchema    int `json:"tool_schema,omitempty"`
	History       int `json:"history,omitempty"`
	Memory        int `json:"memory,omitempty"`
	WorkingMemory int `json:"working_memory,omitempty"`
	Retrieval     int `json:"retrieval,omitempty"`
	Task          int `json:"task,omitempty"`
	Total         int `json:"total,omitempty"`
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
