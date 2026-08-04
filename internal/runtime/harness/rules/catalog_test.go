package rules

import (
	"fmt"
	"testing"
)

func TestFallbackToolRulesLoadOnlyAfterToolUse(t *testing.T) {
	items, err := RuntimeRules(nil, true)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := SelectOptionalRules(items, LoadContext{Mode: "plan_execute", TokenBudget: 1000})
	after, _ := SelectOptionalRules(items, LoadContext{Mode: "plan_execute", Tags: []string{"tool_used"}, ToolNames: []string{"bash"}, TokenBudget: 1000})
	if containsRule(before, "tool.plan_execute.checkpoints") || !containsRule(after, "tool.plan_execute.checkpoints") {
		t.Fatalf("tool rule activation mismatch: before=%+v after=%+v", before, after)
	}
}

func TestAgentRulesReplaceFallbackOptionalRules(t *testing.T) {
	items, err := RuntimeRules([]Rule{{ID: "tenant.auth", Content: "handle auth", Strength: RuleOptional, Activation: Activation{KeywordsAny: []string{"401"}}}}, false)
	if err != nil {
		t.Fatal(err)
	}
	loaded, _ := SelectOptionalRules(items, LoadContext{Task: "AgentCanvas 401", TokenBudget: 1000})
	if !containsRule(loaded, "tenant.auth") || containsRule(loaded, "scenario.code.change_verification") {
		t.Fatalf("Agent rules must replace fallback optional rules: %+v", loaded)
	}
}

func TestRuntimeRulesAcceptsFiftyCustomRulesWithPlatformRules(t *testing.T) {
	custom := make([]Rule, 50)
	for index := range custom {
		custom[index] = Rule{ID: fmt.Sprintf("tenant.rule%02d", index), Content: "rule", Strength: RuleOptional, Activation: Activation{Always: true}}
	}
	items, err := RuntimeRules(custom, false)
	if err != nil || len(items) != 52 {
		t.Fatalf("expected 50 custom plus 2 platform rules, items=%d err=%v", len(items), err)
	}
}

func TestRuntimeRulesRejectsMoreThanFiftyCustomRules(t *testing.T) {
	custom := make([]Rule, 51)
	for index := range custom {
		custom[index] = Rule{ID: fmt.Sprintf("tenant.rule%02d", index), Content: "rule", Strength: RuleOptional, Activation: Activation{Always: true}}
	}
	if _, err := RuntimeRules(custom, false); err == nil {
		t.Fatal("expected more than fifty custom rules to be rejected")
	}
}

func containsRule(items []Rule, id string) bool {
	for _, item := range items {
		if item.ID == id {
			return true
		}
	}
	return false
}
