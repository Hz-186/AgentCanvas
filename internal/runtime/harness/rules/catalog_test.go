package rules

import "testing"

func TestResolvePersistentWithRulesLoadsOnlyMandatoryRules(t *testing.T) {
	loaded, trace := ResolvePersistentWithRules([]Rule{
		{ID: "tenant.required", Content: "required", Strength: RuleMandatory},
		{ID: "tenant.optional", Content: "optional", Strength: RuleOptional, Activation: Activation{Always: true}},
	})
	if len(loaded) != 3 {
		t.Fatalf("expected two platform and one tenant mandatory rule, got %+v trace=%+v", loaded, trace)
	}
	for _, rule := range loaded {
		if rule.Strength != RuleMandatory {
			t.Fatalf("persistent rule must be mandatory: %+v", rule)
		}
	}
}

func TestDefaultToolRuleActivatesOnlyAfterToolUse(t *testing.T) {
	compiled, err := CompileRuntimeRuleSet(nil)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := SelectOptionalRules(compiled, LoadContext{
		RiskLevel: "high", ToolNames: []string{"bash"}, TokenBudget: 1000,
	}, DefaultOptimizerExpansions)
	if containsRule(before, "tool.high_risk.approval") {
		t.Fatalf("tool rule must not load before actual tool use: %+v", before)
	}
	after, _ := SelectOptionalRules(compiled, LoadContext{
		RiskLevel: "high", ToolNames: []string{"bash"}, Tags: []string{"tool_used"}, TokenBudget: 1000,
	}, DefaultOptimizerExpansions)
	if !containsRule(after, "tool.high_risk.approval") {
		t.Fatalf("tool rule must load after actual tool use: %+v", after)
	}
}

func TestActiveRuleSetReplacesFallbackOptionalRules(t *testing.T) {
	compiled, err := CompileActiveRuleSet([]Rule{{
		ID: "tenant.active", Content: "active", Strength: RuleOptional, Activation: Activation{Always: true},
	}})
	if err != nil {
		t.Fatal(err)
	}
	loaded, _ := SelectOptionalRules(compiled, LoadContext{
		Tags: []string{"rag"}, TokenBudget: 1000,
	}, DefaultOptimizerExpansions)
	if !containsRule(loaded, "tenant.active") || containsRule(loaded, "scenario.rag.citations") {
		t.Fatalf("active RuleSet must replace fallback optional rules: %+v", loaded)
	}
}

func TestCompileOptionalRuleWithoutActivationMustBeDependency(t *testing.T) {
	if _, err := CompileRuleSet([]Rule{{
		ID: "tenant.dead", Content: "dead", Strength: RuleOptional,
	}}, CompileOptions{}); err == nil {
		t.Fatal("expected standalone optional rule without activation to fail")
	}
	compiled, err := CompileRuleSet([]Rule{
		{ID: "tenant.shared", Content: "shared", Strength: RuleOptional},
		{ID: "tenant.root", Content: "root", Strength: RuleOptional, Activation: Activation{Always: true}, ManualDependsOn: []string{"tenant.shared"}},
	}, CompileOptions{})
	if err != nil {
		t.Fatal(err)
	}
	loaded, _ := SelectOptionalRules(compiled, LoadContext{TokenBudget: 1000}, DefaultOptimizerExpansions)
	if !containsRule(loaded, "tenant.shared") || !containsRule(loaded, "tenant.root") {
		t.Fatalf("dependency-only rule must load with its root: %+v", loaded)
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
