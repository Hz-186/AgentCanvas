package rules

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestCompileRuleSetRejectsCycleAndMandatoryDependingOnOptional(t *testing.T) {
	_, err := CompileRuleSet([]Rule{
		{ID: "tenant.a", Content: "a", Strength: RuleMandatory, ManualDependsOn: []string{"tenant.b"}},
		{ID: "tenant.b", Content: "b", Strength: RuleOptional},
	}, CompileOptions{})
	if err == nil || !strings.Contains(err.Error(), "mandatory") {
		t.Fatalf("expected mandatory dependency validation error, got %v", err)
	}

	_, err = CompileRuleSet([]Rule{
		{ID: "tenant.a", Content: "a", Strength: RuleOptional, ManualDependsOn: []string{"tenant.b"}},
		{ID: "tenant.b", Content: "b", Strength: RuleOptional, ManualDependsOn: []string{"tenant.a"}},
	}, CompileOptions{})
	if err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("expected cycle validation error, got %v", err)
	}
}

func TestCompileRuleSetRequiresBindingForSafetyCriticalRule(t *testing.T) {
	_, err := CompileRuleSet([]Rule{{
		ID: "tenant.safety", Content: "never delete production", Strength: RuleMandatory, SafetyCritical: true,
	}}, CompileOptions{})
	if err == nil || !strings.Contains(err.Error(), "policy binding") {
		t.Fatalf("expected missing policy binding error, got %v", err)
	}

	compiled, err := CompileRuleSet([]Rule{{
		ID:             "tenant.safety",
		Content:        "never delete production",
		Strength:       RuleMandatory,
		SafetyCritical: true,
		PolicyBinding: &PolicyBinding{
			PolicyKey: PolicyDangerousArgumentsDeny,
			Params:    json.RawMessage(`{}`),
		},
	}}, CompileOptions{})
	if err != nil || compiled.CompiledHash == "" || compiled.MandatoryTokens == 0 {
		t.Fatalf("expected compiled safety rule, compiled=%+v err=%v", compiled, err)
	}
}

func TestVerifyCompiledHashRejectsSnapshotTampering(t *testing.T) {
	compiled, err := CompileRuleSet([]Rule{{
		ID: "tenant.audit", Content: "keep an audit trail", Strength: RuleMandatory,
	}}, CompileOptions{RuleSetID: 7, Version: "3"})
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyCompiledHash(compiled); err != nil {
		t.Fatalf("fresh snapshot must verify: %v", err)
	}

	compiled.Rules[0].Rule.Content = "tampered"
	if err := VerifyCompiledHash(compiled); err == nil || !strings.Contains(err.Error(), "mismatch") {
		t.Fatalf("expected tampered snapshot to fail verification, got %v", err)
	}
}

func TestSelectOptionalRulesLoadsDependencyClosureAtomicallyAndCountsSharedDependencyOnce(t *testing.T) {
	compiled, err := CompileRuleSet([]Rule{
		{ID: "tenant.shared", Content: "s", Strength: RuleOptional, TokenBudget: 3},
		{ID: "tenant.a", Content: "a", Strength: RuleOptional, TokenBudget: 4, Priority: 10, Activation: Activation{TagAny: []string{"a"}}, ManualDependsOn: []string{"tenant.shared"}},
		{ID: "tenant.b", Content: "b", Strength: RuleOptional, TokenBudget: 4, Priority: 9, Activation: Activation{TagAny: []string{"b"}}, ManualDependsOn: []string{"tenant.shared"}},
	}, CompileOptions{})
	if err != nil {
		t.Fatal(err)
	}

	loaded, trace := SelectOptionalRules(compiled, LoadContext{Tags: []string{"a", "b"}, TokenBudget: 11}, 10000)
	if len(loaded) != 3 || trace.EstimatedUsed != 11 {
		t.Fatalf("expected both roots and one shared dependency, loaded=%+v trace=%+v", loaded, trace)
	}
	if got := trace.DependencyLoadedBy["tenant.shared"]; len(got) != 2 {
		t.Fatalf("expected dependency attribution for both roots, got %+v", trace.DependencyLoadedBy)
	}
	if got := trace.SharedDependencies["tenant.shared"]; len(got) != 2 || trace.BundleMarginalCosts["tenant.a"] == 0 || trace.BundleMarginalCosts["tenant.b"] == 0 {
		t.Fatalf("expected shared dependency and marginal cost trace, got %+v", trace)
	}
}

func TestSelectOptionalRulesSkipsOversizedBundleButKeepsSmallerCandidate(t *testing.T) {
	compiled, err := CompileRuleSet([]Rule{
		{ID: "tenant.large", Content: "large", Strength: RuleOptional, TokenBudget: 100, Priority: 100, Activation: Activation{Always: true}},
		{ID: "tenant.small", Content: "small", Strength: RuleOptional, TokenBudget: 5, Priority: 1, Activation: Activation{Always: true}},
	}, CompileOptions{})
	if err != nil {
		t.Fatal(err)
	}

	loaded, trace := SelectOptionalRules(compiled, LoadContext{TokenBudget: 10}, 10000)
	if len(loaded) != 1 || loaded[0].ID != "tenant.small" {
		t.Fatalf("expected small rule after oversized candidate, loaded=%+v trace=%+v", loaded, trace)
	}
}

func TestCompileRuleSetRejectsMalformedPolicyParams(t *testing.T) {
	_, err := CompileRuleSet([]Rule{{
		ID:       "tenant.policy",
		Content:  "require approval",
		Strength: RuleMandatory,
		PolicyBinding: &PolicyBinding{
			PolicyKey: PolicyRiskRequireApproval,
			Params:    json.RawMessage(`{"risk_levels":"high"}`),
		},
	}}, CompileOptions{})
	if err == nil || !strings.Contains(err.Error(), "risk_levels") {
		t.Fatalf("expected typed policy params validation error, got %v", err)
	}
}

func TestCompiledOptionalSelectionIgnoresLegacyLevelScore(t *testing.T) {
	compiled, err := CompileRuleSet([]Rule{
		{ID: "tenant.a", Content: "a", Strength: RuleOptional, Level: LevelL4Ephemeral, TokenBudget: 1, Activation: Activation{Always: true}},
		{ID: "tenant.b", Content: "b", Strength: RuleOptional, Level: LevelL2Scenario, TokenBudget: 1, Activation: Activation{Always: true}},
	}, CompileOptions{})
	if err != nil {
		t.Fatal(err)
	}
	loaded, _ := SelectOptionalRules(compiled, LoadContext{TokenBudget: 1}, 100)
	if len(loaded) != 1 || loaded[0].ID != "tenant.a" {
		t.Fatalf("legacy level must not influence compiled optional ranking, got %+v", loaded)
	}
}

func TestConservativeRuleCostCannotBeLoweredByLegacyBudget(t *testing.T) {
	compiled, err := CompileRuleSet([]Rule{{
		ID: "tenant.zh", Content: "必须保留审计记录", Strength: RuleMandatory, TokenBudget: 1,
	}}, CompileOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if compiled.Rules[0].TokenCost < len([]rune("必须保留审计记录")) {
		t.Fatalf("legacy token_budget lowered conservative cost: %+v", compiled.Rules[0])
	}
}

func TestCompileRuleSetNormalizesLegacyLevelToExplicitStrength(t *testing.T) {
	compiled, err := CompileRuleSet([]Rule{
		{ID: "tenant.mandatory", Content: "mandatory", Strength: RuleMandatory, Level: LevelL3Tool},
		{ID: "tenant.optional", Content: "optional", Strength: RuleOptional, Level: LevelL0Safety},
	}, CompileOptions{})
	if err != nil {
		t.Fatal(err)
	}
	mandatory, _ := compiled.RuleByID("tenant.mandatory")
	optional, _ := compiled.RuleByID("tenant.optional")
	if mandatory.Rule.Level != LevelL1Core || optional.Rule.Level != LevelL2Scenario {
		t.Fatalf("legacy levels must not contradict explicit strength: mandatory=%s optional=%s", mandatory.Rule.Level, optional.Rule.Level)
	}
}

func TestCompileRuleSetPersistsDependencyProvenanceAndClosureBits(t *testing.T) {
	compiled, err := CompileRuleSet([]Rule{
		{ID: "tenant.base", Content: "base", Strength: RuleOptional},
		{ID: "tenant.llm", Content: "llm", Strength: RuleOptional},
		{ID: "tenant.manual", Content: "manual", Strength: RuleOptional, ManualDependsOn: []string{"tenant.base"}},
	}, CompileOptions{Edges: []DependencyEdge{
		{RuleID: "tenant.llm", DependsOn: "tenant.base", Source: "llm", Decision: "accepted"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	llmRule, ok := compiled.RuleByID("tenant.llm")
	if !ok || llmRule.DependencySources["tenant.base"] != "llm_confirmed" || len(llmRule.DependencyClosureBits) == 0 || llmRule.DependencyClosureBits[0] == 0 {
		t.Fatalf("expected confirmed LLM provenance and compact closure, got %+v", llmRule)
	}
	manualRule, ok := compiled.RuleByID("tenant.manual")
	if !ok || manualRule.DependencySources["tenant.base"] != "manual" {
		t.Fatalf("expected manual provenance, got %+v", manualRule)
	}
}

func FuzzCompileRuleSetDeterministic(f *testing.F) {
	f.Add([]byte{1, 0, 2, 1})
	f.Add([]byte{0, 1, 1, 0})
	f.Fuzz(func(t *testing.T, data []byte) {
		items := make([]Rule, 4)
		for index := range items {
			items[index] = Rule{
				ID: fmt.Sprintf("tenant.rule%d", index), Content: fmt.Sprintf("rule %d", index),
				Strength: RuleOptional, Activation: Activation{Always: true},
			}
		}
		edges := make([]DependencyEdge, 0, len(data)/2)
		for index := 0; index+1 < len(data) && len(edges) < 12; index += 2 {
			ruleIndex := int(data[index] % 4)
			dependencyIndex := int(data[index+1] % 4)
			edges = append(edges, DependencyEdge{
				RuleID: items[ruleIndex].ID, DependsOn: items[dependencyIndex].ID,
				Source: "manual", Decision: "accepted",
			})
		}
		first, firstErr := CompileRuleSet(items, CompileOptions{RuleSetID: 1, Version: "1", Edges: edges})
		second, secondErr := CompileRuleSet(items, CompileOptions{RuleSetID: 1, Version: "1", Edges: edges})
		if (firstErr == nil) != (secondErr == nil) {
			t.Fatalf("same input produced inconsistent errors: first=%v second=%v", firstErr, secondErr)
		}
		if firstErr == nil && first.CompiledHash != second.CompiledHash {
			t.Fatalf("same input produced different hashes: %s != %s", first.CompiledHash, second.CompiledHash)
		}
	})
}

func BenchmarkSelectOptionalRules50Nodes200Edges(b *testing.B) {
	items := make([]Rule, 50)
	for index := range items {
		items[index] = Rule{
			ID: fmt.Sprintf("tenant.rule%02d", index), Content: "x", TokenBudget: 1,
			Strength: RuleOptional, Priority: 50 - index, Activation: Activation{Always: true},
		}
	}
	edges := make([]DependencyEdge, 0, 200)
	for ruleIndex := 10; ruleIndex < 30; ruleIndex++ {
		for offset := 0; offset < 5; offset++ {
			edges = append(edges, DependencyEdge{RuleID: items[ruleIndex].ID, DependsOn: items[(ruleIndex+offset)%10].ID})
		}
	}
	for ruleIndex := 30; ruleIndex < 50; ruleIndex++ {
		for offset := 0; offset < 5; offset++ {
			edges = append(edges, DependencyEdge{RuleID: items[ruleIndex].ID, DependsOn: items[10+(ruleIndex+offset)%20].ID})
		}
	}
	compiled, err := CompileRuleSet(items, CompileOptions{RuleSetID: 1, Version: "1", Edges: edges})
	if err != nil {
		b.Fatal(err)
	}
	ctx := LoadContext{TokenBudget: 25}
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		loaded, _ := SelectOptionalRules(compiled, ctx, DefaultOptimizerExpansions)
		if len(loaded) == 0 {
			b.Fatal("expected a non-empty selection")
		}
	}
}
