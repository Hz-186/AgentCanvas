package agent

import (
	"testing"

	"agentcanvas/internal/infrastructure/llm"
)

func TestRulePlannerReservesWindowBeforeSelectingScenarioRules(t *testing.T) {
	plan := (RulePlanner{}).Plan(RulePlanningState{
		Task:           "fix this code and run focused tests",
		Tags:           []string{"code", "engineering"},
		BaseMessages:   []llm.ChatMessage{{Role: "system", Content: "core"}, {Role: "user", Content: "task"}},
		AvailableTools: []llm.ToolDefinition{{Type: "function", Function: llm.ToolFunctionDefinition{Name: "bash", Description: "run commands", Parameters: []byte(`{"type":"object"}`)}}},
		ContextWindow:  32000,
		ReservedOutput: 4000,
		SafetyMargin:   256,
		MaxRuleTokens:  200,
	})
	if plan.Budget.InputBudgetTokens != 28000 || plan.Budget.ToolSchemaTokens == 0 {
		t.Fatalf("unexpected calculated budget: %+v", plan.Budget)
	}
	if plan.Budget.AvailableRuleTokens != 200 {
		t.Fatalf("expected configured rule cap, got %+v", plan.Budget)
	}
	if !containsRule(plan.Trace.Loaded, "scenario.code.change_verification") {
		t.Fatalf("expected code scenario rule, got %+v", plan.Trace)
	}
	if containsRule(plan.Trace.Loaded, "tool.high_risk.approval") {
		t.Fatalf("tool rule must not load before a tool is used: %+v", plan.Trace)
	}
}

func TestRulePlannerLoadsToolRulesOnlyAfterToolUse(t *testing.T) {
	state := RulePlanningState{
		Task:           "run the deployment check",
		RiskLevel:      "medium",
		BaseMessages:   []llm.ChatMessage{{Role: "system", Content: "core"}, {Role: "user", Content: "task"}},
		MaxInputTokens: 2000,
	}
	before := (RulePlanner{}).Plan(state)
	if containsRule(before.Trace.Loaded, "tool.high_risk.approval") {
		t.Fatalf("tool rule loaded before tool use: %+v", before.Trace)
	}
	state.UsedToolNames = []string{"bash"}
	after := (RulePlanner{}).Plan(state)
	if !containsRule(after.Trace.Loaded, "tool.high_risk.approval") {
		t.Fatalf("expected tool rule after tool use: %+v", after.Trace)
	}
}

func containsRule(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
