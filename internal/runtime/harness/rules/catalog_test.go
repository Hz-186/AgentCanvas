package rules

import "testing"

func TestFallbackToolRulesLoadOnlyAfterToolUse(t *testing.T) {
	compiled, err := CompileRuntimeRuleSet(nil)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := SelectOptionalRules(compiled, LoadContext{Mode: "plan_execute", TokenBudget: 1000})
	after, _ := SelectOptionalRules(compiled, LoadContext{Mode: "plan_execute", Tags: []string{"tool_used"}, ToolNames: []string{"bash"}, TokenBudget: 1000})
	if containsRule(before, "tool.plan_execute.checkpoints") || !containsRule(after, "tool.plan_execute.checkpoints") {
		t.Fatalf("tool rule activation mismatch: before=%+v after=%+v", before, after)
	}
}

func TestActiveRuleSetReplacesFallbackOptionalRules(t *testing.T) {
	compiled, err := CompileActiveRuleSet([]Rule{{ID: "tenant.auth", Content: "handle auth", Strength: RuleOptional, Activation: Activation{KeywordsAny: []string{"401"}}}})
	if err != nil {
		t.Fatal(err)
	}
	loaded, _ := SelectOptionalRules(compiled, LoadContext{Task: "AgentCanvas 401", TokenBudget: 1000})
	if !containsRule(loaded, "tenant.auth") || containsRule(loaded, "scenario.code.change_verification") {
		t.Fatalf("active RuleSet must replace fallback optional rules: %+v", loaded)
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
