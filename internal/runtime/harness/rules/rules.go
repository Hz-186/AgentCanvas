package rules

import (
	"encoding/json"
	"fmt"
	"strings"
)

type RuleStrength string

const (
	RuleMandatory RuleStrength = "mandatory"
	RuleOptional  RuleStrength = "optional"
)

const (
	ReasonSignalsNotMatched   = "activation_not_matched"
	ReasonTokenBudgetExceeded = "token_budget_exceeded"
)

type Rule struct {
	ID             string         `json:"id"`
	Name           string         `json:"name,omitempty"`
	Strength       RuleStrength   `json:"strength"`
	Content        string         `json:"content"`
	Activation     Activation     `json:"activation,omitempty"`
	Priority       int            `json:"priority,omitempty"`
	SafetyCritical bool           `json:"safety_critical,omitempty"`
	PolicyBinding  *PolicyBinding `json:"policy_binding,omitempty"`
}

func (r *Rule) UnmarshalJSON(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	for _, removed := range []string{"level", "manual_depends_on", "triggers", "token_budget", "metadata"} {
		if _, exists := fields[removed]; exists {
			return fmt.Errorf("rule field %q is no longer supported", removed)
		}
	}
	type ruleAlias Rule
	var decoded ruleAlias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*r = Rule(decoded)
	return nil
}

type Activation struct {
	ModeAny     []string `json:"mode_any,omitempty"`
	RiskAny     []string `json:"risk_any,omitempty"`
	ToolAny     []string `json:"tool_any,omitempty"`
	TagAny      []string `json:"tag_any,omitempty"`
	TagAll      []string `json:"tag_all,omitempty"`
	KeywordsAny []string `json:"keywords_any,omitempty"`
	Always      bool     `json:"always,omitempty"`
}

type LoadContext struct {
	Mode           string
	ToolNames      []string
	RiskLevel      string
	Tags           []string
	Task           string
	Conversation   string
	TokenBudget    int
	RuleTokenCosts map[string]int
}

type Trace struct {
	Loaded          []string          `json:"loaded,omitempty"`
	Skipped         []string          `json:"skipped,omitempty"`
	SkipReasons     map[string]string `json:"skip_reasons,omitempty"`
	EstimatedUsed   int               `json:"estimated_used,omitempty"`
	TokenBudget     int               `json:"token_budget,omitempty"`
	MandatoryTokens int               `json:"mandatory_tokens,omitempty"`
	OptionalBudget  int               `json:"optional_budget,omitempty"`
	RuleHash        string            `json:"rule_hash,omitempty"`
}

type PolicyBinding struct {
	PolicyKey string          `json:"policy_key"`
	Params    json.RawMessage `json:"params,omitempty"`
}

func ruleCost(rule Rule) int {
	cost := len([]rune(strings.TrimSpace(rule.Content))) / 4
	if cost < 1 {
		return 1
	}
	return cost
}

func matches(rule Rule, ctx LoadContext) bool {
	if rule.Strength == RuleMandatory {
		return true
	}
	activation := rule.Activation
	if len(activation.TagAll) > 0 && !containsAllSet(ctx.Tags, activation.TagAll) {
		return false
	}
	if activation.Always {
		return true
	}
	return containsAny(ctx.Mode, activation.ModeAny) ||
		containsAny(ctx.RiskLevel, activation.RiskAny) ||
		matchesSet(ctx.ToolNames, activation.ToolAny) ||
		matchesSet(ctx.Tags, activation.TagAny) ||
		matchesKeyword(ctx, activation.KeywordsAny)
}

func hasActivation(activation Activation) bool {
	return activation.Always || len(activation.ModeAny) > 0 || len(activation.RiskAny) > 0 ||
		len(activation.ToolAny) > 0 || len(activation.TagAny) > 0 ||
		len(activation.TagAll) > 0 || len(activation.KeywordsAny) > 0
}

func containsAny(value string, candidates []string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return false
	}
	for _, candidate := range candidates {
		if value == strings.ToLower(strings.TrimSpace(candidate)) {
			return true
		}
	}
	return false
}

func matchesSet(values, candidates []string) bool {
	set := normalizedSet(values)
	for _, candidate := range candidates {
		if set[strings.ToLower(strings.TrimSpace(candidate))] {
			return true
		}
	}
	return false
}

func containsAllSet(values, required []string) bool {
	set := normalizedSet(values)
	for _, item := range required {
		if !set[strings.ToLower(strings.TrimSpace(item))] {
			return false
		}
	}
	return true
}

func matchesKeyword(ctx LoadContext, candidates []string) bool {
	body := strings.ToLower(strings.TrimSpace(ctx.Task + "\n" + ctx.Conversation))
	for _, candidate := range candidates {
		candidate = strings.ToLower(strings.TrimSpace(candidate))
		if candidate != "" && strings.Contains(body, candidate) {
			return true
		}
	}
	return false
}

func normalizedSet(values []string) map[string]bool {
	set := make(map[string]bool, len(values))
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value != "" {
			set[value] = true
		}
	}
	return set
}

func (t *Trace) skip(ruleID, reason string) {
	t.Skipped = append(t.Skipped, ruleID)
	if reason != "" {
		if t.SkipReasons == nil {
			t.SkipReasons = map[string]string{}
		}
		t.SkipReasons[ruleID] = reason
	}
}
