package agent

import (
	"encoding/json"
	"strings"

	"agentcanvas/internal/infrastructure/llm"
	"agentcanvas/internal/runtime/harness/rules"
)

const defaultRuleSafetyMarginTokens = 256

// RulePlanner selects only the non-persistent rules required for one model turn.
// L0/L1 stay in the static context assembled by the node.
type RulePlanner struct{}

type RulePlanningState struct {
	Iteration      int
	SystemPrompt   string
	Task           string
	Mode           string
	RiskLevel      string
	Tags           []string
	UsedToolNames  []string
	AvailableTools []llm.ToolDefinition
	BaseMessages   []llm.ChatMessage
	Transcript     []llm.ChatMessage
	MaxInputTokens int
	ContextWindow  int
	ReservedOutput int
	SafetyMargin   int
	MaxRuleTokens  int
	CustomRules    []rules.Rule
}

type RulePlan struct {
	Rules  []rules.Rule
	Trace  rules.Trace
	Budget RuleBudget
}

type RuleBudget struct {
	ContextWindowTokens  int `json:"context_window_tokens,omitempty"`
	ReservedOutputTokens int `json:"reserved_output_tokens,omitempty"`
	SafetyMarginTokens   int `json:"safety_margin_tokens,omitempty"`
	InputBudgetTokens    int `json:"input_budget_tokens,omitempty"`
	FixedTokens          int `json:"fixed_tokens,omitempty"`
	ToolSchemaTokens     int `json:"tool_schema_tokens,omitempty"`
	TranscriptTokens     int `json:"transcript_tokens,omitempty"`
	AvailableRuleTokens  int `json:"available_rule_tokens,omitempty"`
}

func (RulePlanner) Plan(state RulePlanningState) RulePlan {
	budget := calculateRuleBudget(state)
	tags := append([]string(nil), state.Tags...)
	if len(state.UsedToolNames) > 0 {
		tags = append(tags, "tool_used")
	}
	selected, trace := rules.ResolveDynamicWithRules(
		state.SystemPrompt,
		state.Task,
		state.Mode,
		state.RiskLevel,
		state.UsedToolNames,
		tags,
		budget.AvailableRuleTokens,
		state.CustomRules,
		nil,
		rules.AuditPolicy{},
	)
	// Tool guidance is useful only after the tool has become part of this run.
	// It must not consume every initial prompt merely because a tool is available.
	kept := selected[:0]
	for _, rule := range selected {
		if rule.Level == rules.LevelL3Tool && len(state.UsedToolNames) == 0 {
			trace.RemoveLoaded(rule, rules.ReasonDeferredUntilToolUse)
			continue
		}
		kept = append(kept, rule)
	}
	return RulePlan{Rules: kept, Trace: trace, Budget: budget}
}

func calculateRuleBudget(state RulePlanningState) RuleBudget {
	margin := state.SafetyMargin
	if margin <= 0 {
		margin = defaultRuleSafetyMarginTokens
	}
	input := state.MaxInputTokens
	if state.ContextWindow > 0 {
		windowInput := state.ContextWindow - state.ReservedOutput
		if input <= 0 || (windowInput > 0 && windowInput < input) {
			input = windowInput
		}
	}
	if input <= 0 {
		input = defaultMaxInputTokens
	}
	budget := RuleBudget{
		ContextWindowTokens:  state.ContextWindow,
		ReservedOutputTokens: state.ReservedOutput,
		SafetyMarginTokens:   margin,
		InputBudgetTokens:    input,
		FixedTokens:          estimateMessagesTokens(state.BaseMessages),
		TranscriptTokens:     estimateMessagesTokens(state.Transcript),
		ToolSchemaTokens:     estimateToolSchemaTokens(state.AvailableTools),
	}
	budget.AvailableRuleTokens = input - budget.FixedTokens - budget.TranscriptTokens - budget.ToolSchemaTokens - margin
	if state.MaxRuleTokens > 0 && budget.AvailableRuleTokens > state.MaxRuleTokens {
		budget.AvailableRuleTokens = state.MaxRuleTokens
	}
	if budget.AvailableRuleTokens < 0 {
		budget.AvailableRuleTokens = 0
	}
	return budget
}

func transcriptBudget(state RulePlanningState) int {
	state.Transcript = nil
	budget := calculateRuleBudget(state)
	return budget.InputBudgetTokens - budget.FixedTokens - budget.ToolSchemaTokens - budget.SafetyMarginTokens
}

func estimateMessagesTokens(messages []llm.ChatMessage) int {
	total := 0
	for _, message := range messages {
		total += estimateContextTokens(message.Content)
	}
	return total
}

func estimateToolSchemaTokens(tools []llm.ToolDefinition) int {
	total := 0
	for _, tool := range tools {
		data, err := json.Marshal(tool)
		if err == nil {
			total += estimateContextTokens(string(data))
			continue
		}
		total += estimateContextTokens(strings.TrimSpace(tool.Function.Name) + " " + strings.TrimSpace(tool.Function.Description))
	}
	return total
}

func ruleMessages(items []rules.Rule) []llm.ChatMessage {
	messages := make([]llm.ChatMessage, 0, len(items))
	for _, item := range items {
		content := strings.TrimSpace(item.Content)
		if content != "" {
			messages = append(messages, llm.ChatMessage{Role: "system", Content: content})
		}
	}
	return messages
}
