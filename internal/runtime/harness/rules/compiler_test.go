package rules

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestRuleRejectsLegacyLevelAndRemovedGraphField(t *testing.T) {
	for _, raw := range []string{
		`{"id":"tenant.legacy","level":"l2_scenario","content":"legacy"}`,
		`{"id":"tenant.graph","strength":"optional","content":"graph","manual_depends_on":["tenant.base"]}`,
	} {
		var rule Rule
		if err := json.Unmarshal([]byte(raw), &rule); err == nil {
			t.Fatalf("expected removed field to be rejected: %s", raw)
		}
	}
}

func TestCompileRuleSetRequiresPositiveOptionalActivation(t *testing.T) {
	_, err := CompileRuleSet([]Rule{{ID: "tenant.optional", Content: "optional", Strength: RuleOptional}}, CompileOptions{})
	if err == nil || !strings.Contains(err.Error(), "positive activation") {
		t.Fatalf("expected activation validation error, got %v", err)
	}
	if _, err := CompileRuleSet([]Rule{{ID: "tenant.optional", Content: "optional", Strength: RuleOptional, Activation: Activation{Always: true}}}, CompileOptions{}); err != nil {
		t.Fatalf("always activation must compile: %v", err)
	}
}

func TestCompileRuleSetRequiresBindingForSafetyCriticalRule(t *testing.T) {
	_, err := CompileRuleSet([]Rule{{ID: "tenant.safety", Content: "never delete production", Strength: RuleMandatory, SafetyCritical: true}}, CompileOptions{})
	if err == nil || !strings.Contains(err.Error(), "policy binding") {
		t.Fatalf("expected missing policy binding error, got %v", err)
	}
	compiled, err := CompileRuleSet([]Rule{{
		ID: "tenant.safety", Content: "never delete production", Strength: RuleMandatory, SafetyCritical: true,
		PolicyBinding: &PolicyBinding{PolicyKey: PolicyDangerousArgumentsDeny, Params: json.RawMessage(`{}`)},
	}}, CompileOptions{})
	if err != nil || compiled.SchemaVersion != CurrentSnapshotSchemaVersion || compiled.CompiledHash == "" {
		t.Fatalf("expected graph-free safety snapshot, compiled=%+v err=%v", compiled, err)
	}
}

func TestVerifyCompiledHashRejectsSnapshotTampering(t *testing.T) {
	compiled, err := CompileRuleSet([]Rule{{ID: "tenant.audit", Content: "keep an audit trail", Strength: RuleMandatory}}, CompileOptions{RuleSetID: 7, Version: "3"})
	if err != nil {
		t.Fatal(err)
	}
	compiled.Rules[0].Rule.Content = "tampered"
	if err := VerifyCompiledHash(compiled); err == nil || !strings.Contains(err.Error(), "mismatch") {
		t.Fatalf("expected tampered snapshot to fail verification, got %v", err)
	}
}

func TestSelectOptionalRulesUsesDeterministicPriorityCostAndIDOrder(t *testing.T) {
	compiled, err := CompileRuleSet([]Rule{
		{ID: "tenant.low", Content: "low", Strength: RuleOptional, Priority: 1, Activation: Activation{Always: true}},
		{ID: "tenant.z", Content: "z", Strength: RuleOptional, Priority: 10, Activation: Activation{Always: true}},
		{ID: "tenant.a", Content: "a", Strength: RuleOptional, Priority: 10, Activation: Activation{Always: true}},
	}, CompileOptions{})
	if err != nil {
		t.Fatal(err)
	}
	loaded, trace := SelectOptionalRules(compiled, LoadContext{TokenBudget: 3, RuleTokenCosts: map[string]int{"tenant.a": 2, "tenant.z": 2, "tenant.low": 1}})
	if len(loaded) != 2 || loaded[0].ID != "tenant.a" || loaded[1].ID != "tenant.low" {
		t.Fatalf("unexpected deterministic selection: loaded=%+v trace=%+v", loaded, trace)
	}
	if trace.SelectionStrategy != "deterministic_activation_budget:v1" || trace.SkipReasons["tenant.z"] != ReasonTokenBudgetExceeded {
		t.Fatalf("unexpected trace: %+v", trace)
	}
}

func TestSelectOptionalRulesNeverBypassesHardActivationConditions(t *testing.T) {
	compiled, err := CompileRuleSet([]Rule{{
		ID: "tenant.safe", Content: "safe", Strength: RuleOptional, Activation: Activation{Always: true, ExcludeModes: []string{"review"}},
	}}, CompileOptions{})
	if err != nil {
		t.Fatal(err)
	}
	loaded, trace := SelectOptionalRules(compiled, LoadContext{Mode: "review", TokenBudget: 100})
	if len(loaded) != 0 || trace.SkipReasons["tenant.safe"] != ReasonSignalsNotMatched {
		t.Fatalf("hard exclusion must win: loaded=%+v trace=%+v", loaded, trace)
	}
}

func TestCompileRuleSetRejectsMalformedPolicyParams(t *testing.T) {
	_, err := CompileRuleSet([]Rule{{
		ID: "tenant.policy", Content: "require approval", Strength: RuleMandatory,
		PolicyBinding: &PolicyBinding{PolicyKey: PolicyRiskRequireApproval, Params: json.RawMessage(`{"risk_levels":"high"}`)},
	}}, CompileOptions{})
	if err == nil || !strings.Contains(err.Error(), "risk_levels") {
		t.Fatalf("expected typed policy params validation error, got %v", err)
	}
}

func FuzzCompileRuleSetDeterministic(f *testing.F) {
	f.Add([]byte{1, 2, 3, 4})
	f.Fuzz(func(t *testing.T, data []byte) {
		items := make([]Rule, 4)
		for index := range items {
			priority := 0
			if len(data) > 0 {
				priority = int(data[index%len(data)])
			}
			items[index] = Rule{ID: fmt.Sprintf("tenant.rule%d", index), Content: fmt.Sprintf("rule %d", index), Strength: RuleOptional, Priority: priority, Activation: Activation{Always: true}}
		}
		first, firstErr := CompileRuleSet(items, CompileOptions{RuleSetID: 1, Version: "1"})
		second, secondErr := CompileRuleSet(items, CompileOptions{RuleSetID: 1, Version: "1"})
		if (firstErr == nil) != (secondErr == nil) || (firstErr == nil && first.CompiledHash != second.CompiledHash) {
			t.Fatalf("same input produced inconsistent output: first=%+v/%v second=%+v/%v", first, firstErr, second, secondErr)
		}
	})
}

func BenchmarkSelectOptionalRules50Nodes(b *testing.B) {
	items := make([]Rule, 50)
	for index := range items {
		items[index] = Rule{ID: fmt.Sprintf("tenant.rule%02d", index), Content: "x", Strength: RuleOptional, Priority: 50 - index, Activation: Activation{Always: true}}
	}
	compiled, err := CompileRuleSet(items, CompileOptions{RuleSetID: 1, Version: "1"})
	if err != nil {
		b.Fatal(err)
	}
	ctx := LoadContext{TokenBudget: 25}
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		loaded, _ := SelectOptionalRules(compiled, ctx)
		if len(loaded) != 25 {
			b.Fatalf("expected 25 rules, got %d", len(loaded))
		}
	}
}
