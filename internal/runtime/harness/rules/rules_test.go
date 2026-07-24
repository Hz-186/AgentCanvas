package rules

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRuleRejectsRemovedFields(t *testing.T) {
	for _, raw := range []string{
		`{"id":"tenant.legacy","level":"l2_scenario","content":"legacy"}`,
		`{"id":"tenant.graph","strength":"optional","content":"graph","manual_depends_on":["tenant.base"]}`,
		`{"id":"tenant.trigger","strength":"optional","content":"trigger","triggers":["code"]}`,
	} {
		var rule Rule
		if err := json.Unmarshal([]byte(raw), &rule); err == nil {
			t.Fatalf("expected removed field to be rejected: %s", raw)
		}
	}
}

func TestValidateRulesRequiresOptionalActivation(t *testing.T) {
	if _, err := ValidateRules([]Rule{{ID: "tenant.optional", Content: "optional", Strength: RuleOptional}}); err == nil || !strings.Contains(err.Error(), "activation") {
		t.Fatalf("expected activation validation error, got %v", err)
	}
	if _, err := ValidateRules([]Rule{{ID: "tenant.optional", Content: "optional", Strength: RuleOptional, Activation: Activation{Always: true}}}); err != nil {
		t.Fatalf("always activation must be valid: %v", err)
	}
}

func TestRuleSetHashRejectsTampering(t *testing.T) {
	set, err := NewRuleSet([]Rule{{ID: "tenant.audit", Content: "keep an audit trail", Strength: RuleMandatory}}, 7, "3")
	if err != nil {
		t.Fatal(err)
	}
	set.Rules[0].Content = "tampered"
	if err := VerifyRuleSet(set); err == nil || !strings.Contains(err.Error(), "mismatch") {
		t.Fatalf("expected tampered rule set to fail verification, got %v", err)
	}
}

func TestSelectRulesUsesTwoLevelsAndBudgetPruning(t *testing.T) {
	items := []Rule{
		{ID: "tenant.required", Content: "required", Strength: RuleMandatory},
		{ID: "tenant.low", Content: "low", Strength: RuleOptional, Priority: 1, Activation: Activation{Always: true}},
		{ID: "tenant.z", Content: "z", Strength: RuleOptional, Priority: 10, Activation: Activation{Always: true}},
		{ID: "tenant.a", Content: "a", Strength: RuleOptional, Priority: 10, Activation: Activation{Always: true}},
	}
	mandatory, mandatoryTrace := SelectMandatoryRules(items)
	if len(mandatory) != 1 || mandatory[0].ID != "tenant.required" || mandatoryTrace.MandatoryTokens == 0 {
		t.Fatalf("unexpected mandatory selection: loaded=%+v trace=%+v", mandatory, mandatoryTrace)
	}
	optional, trace := SelectOptionalRules(items, LoadContext{TokenBudget: 3, RuleTokenCosts: map[string]int{"tenant.a": 2, "tenant.z": 2, "tenant.low": 1}})
	if len(optional) != 2 || optional[0].ID != "tenant.a" || optional[1].ID != "tenant.low" {
		t.Fatalf("unexpected optional pruning: loaded=%+v trace=%+v", optional, trace)
	}
	if trace.SkipReasons["tenant.z"] != ReasonTokenBudgetExceeded {
		t.Fatalf("expected budget skip, got %+v", trace)
	}
}

func TestTagAllIsRequiredBeforeAnyCondition(t *testing.T) {
	items := []Rule{{ID: "tenant.tool", Content: "tool", Strength: RuleOptional, Activation: Activation{RiskAny: []string{"high"}, TagAll: []string{"tool_used"}}}}
	before, _ := SelectOptionalRules(items, LoadContext{RiskLevel: "high", TokenBudget: 100})
	after, _ := SelectOptionalRules(items, LoadContext{RiskLevel: "high", Tags: []string{"tool_used"}, TokenBudget: 100})
	if len(before) != 0 || len(after) != 1 {
		t.Fatalf("tag_all must gate any conditions: before=%+v after=%+v", before, after)
	}
}

func TestValidateRulesRejectsMalformedPolicyParams(t *testing.T) {
	_, err := ValidateRules([]Rule{{ID: "tenant.policy", Content: "require approval", Strength: RuleMandatory, PolicyBinding: &PolicyBinding{PolicyKey: PolicyRiskRequireApproval, Params: json.RawMessage(`{"risk_levels":"high"}`)}}})
	if err == nil || !strings.Contains(err.Error(), "risk_levels") {
		t.Fatalf("expected typed policy params validation error, got %v", err)
	}
}

func TestValidateRulesRejectsPlatformIDs(t *testing.T) {
	_, err := ValidateRules([]Rule{{ID: "core.task.completion", Content: "override", Strength: RuleMandatory}})
	if err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Fatalf("expected platform id to be reserved, got %v", err)
	}
}
