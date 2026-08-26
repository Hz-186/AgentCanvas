package agent

import (
	"encoding/json"
	"strings"

	"agentcanvas/internal/infrastructure/llm"
	"agentcanvas/internal/pkg/tokencounter"
	"agentcanvas/internal/runtime/harness/rules"
)

const defaultRuleSafetyMarginTokens = 256

// RulePlanner selects only optional rules required for one model turn.
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
	ProviderType   string
	Model          string
	Rules          []rules.Rule
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
	if state.Rules == nil {
		state.Rules, _ = rules.RuntimeRules(nil, true)
	}
	tags := append([]string(nil), state.Tags...)
	if len(state.UsedToolNames) > 0 {
		tags = append(tags, "tool_used")
	}
	selected, trace := rules.SelectOptionalRules(state.Rules, rules.LoadContext{
		Mode: state.Mode, RiskLevel: state.RiskLevel, ToolNames: state.UsedToolNames,
		Tags: tags, Task: state.Task, Conversation: state.SystemPrompt,
		TokenBudget: budget.AvailableRuleTokens, RuleTokenCosts: modelRuleTokenCosts(state),
	})
	return RulePlan{Rules: selected, Trace: trace, Budget: budget}
}

func modelRuleTokenCosts(state RulePlanningState) map[string]int {
	costs := make(map[string]int, len(state.Rules))
	for _, rule := range state.Rules {
		result := tokencounter.Count(state.ProviderType, state.Model, rule.Content)
		if result.Tokens > 0 {
			costs[rule.ID] = result.Tokens
		}
	}
	return costs
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
