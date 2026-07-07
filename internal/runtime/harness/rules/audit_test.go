package rules

import "testing"

func TestShouldPruneKeepsL1CoreRules(t *testing.T) {
	rule := Rule{ID: "core", Level: LevelL1Core}
	audit := RuleAudit{Evaluations: 100, Hits: 0, HitRate: 0}
	if ShouldPrune(rule, audit, AuditPolicy{MinEvaluations: 10, MinHitRate: 0.1}) {
		t.Fatal("L1 core rules must never be pruned")
	}
}

func TestRegistryLoadWithAuditPrunesLowHitScenarioRules(t *testing.T) {
	registry := NewRegistry(
		Rule{ID: "core", Level: LevelL1Core, Content: "always"},
		Rule{ID: "rag", Level: LevelL2Scenario, Content: "cite chunks"},
		Rule{ID: "scratch", Level: LevelL3Ephemeral, Triggers: []string{"rag"}, Content: "temporary"},
	)
	audit := NewAuditStore()
	for i := 0; i < 10; i++ {
		audit.Record("rag", false)
		audit.Record("scratch", false)
	}
	loaded, trace := registry.LoadWithAudit(LoadContext{Tags: []string{"rag"}}, audit, AuditPolicy{MinEvaluations: 5, MinHitRate: 0.2})
	if len(loaded) != 1 || loaded[0].ID != "core" {
		t.Fatalf("expected only core rule after pruning, got %+v", loaded)
	}
	if len(trace.Skipped) != 2 {
		t.Fatalf("expected low-hit L2/L3 rules to be skipped, got %+v", trace)
	}
}

func TestAuditStoreRecordsHitRate(t *testing.T) {
	audit := NewAuditStore()
	audit.Record("rag", true)
	stat := audit.Record("rag", false)
	if stat.Evaluations != 2 || stat.Hits != 1 || stat.HitRate != 0.5 {
		t.Fatalf("unexpected audit stats: %+v", stat)
	}
}

func TestRegistryLoadRespectsTokenBudgetButKeepsL1(t *testing.T) {
	registry := NewRegistry(
		Rule{ID: "core", Level: LevelL1Core, TokenBudget: 100, Content: "core safety"},
		Rule{ID: "rag", Level: LevelL2Scenario, Triggers: []string{"rag"}, TokenBudget: 20, Content: "cite retrieval"},
		Rule{ID: "pdf", Level: LevelL2Scenario, Triggers: []string{"pdf"}, TokenBudget: 20, Content: "parse pdf"},
		Rule{ID: "scratch", Level: LevelL3Ephemeral, Triggers: []string{"rag"}, TokenBudget: 20, Content: "temporary"},
	)

	loaded, trace := registry.Load(LoadContext{Tags: []string{"rag"}, TokenBudget: 115})
	if len(loaded) != 1 || loaded[0].ID != "core" {
		t.Fatalf("expected only L1 core under tight budget, got loaded=%+v trace=%+v", loaded, trace)
	}
	if trace.EstimatedUsed != 100 || trace.TokenBudget != 115 {
		t.Fatalf("unexpected budget trace: %+v", trace)
	}
	if trace.SkipReasons["rag"] != "token_budget_exceeded" || trace.SkipReasons["scratch"] != "token_budget_exceeded" {
		t.Fatalf("expected budget skip reasons, got %+v", trace.SkipReasons)
	}
	if trace.SkipReasons["pdf"] != "trigger_not_matched" {
		t.Fatalf("expected trigger skip reason for pdf, got %+v", trace.SkipReasons)
	}
}

func TestRegistryLoadAllowsTriggeredScenarioRulesWithinBudget(t *testing.T) {
	registry := NewRegistry(
		Rule{ID: "core", Level: LevelL1Core, TokenBudget: 10, Content: "core"},
		Rule{ID: "rag", Level: LevelL2Scenario, Triggers: []string{"rag"}, TokenBudget: 20, Content: "cite retrieval"},
		Rule{ID: "scratch", Level: LevelL3Ephemeral, Triggers: []string{"rag"}, TokenBudget: 5, Content: "temporary"},
	)

	loaded, trace := registry.Load(LoadContext{Tags: []string{"rag"}, TokenBudget: 35})
	if len(loaded) != 3 {
		t.Fatalf("expected all triggered rules within budget, got loaded=%+v trace=%+v", loaded, trace)
	}
	if trace.EstimatedUsed != 35 || len(trace.Skipped) != 0 {
		t.Fatalf("unexpected trace: %+v", trace)
	}
}

func TestRegistryLoadWithAuditRecordsPruneReason(t *testing.T) {
	registry := NewRegistry(Rule{ID: "rag", Level: LevelL2Scenario, Content: "cite chunks"})
	audit := NewAuditStore()
	for i := 0; i < 5; i++ {
		audit.Record("rag", false)
	}

	loaded, trace := registry.LoadWithAudit(LoadContext{}, audit, AuditPolicy{MinEvaluations: 5, MinHitRate: 0.1})
	if len(loaded) != 0 {
		t.Fatalf("expected pruned rule, got %+v", loaded)
	}
	if trace.SkipReasons["rag"] != "audit_low_hit_rate" {
		t.Fatalf("expected audit prune reason, got %+v", trace.SkipReasons)
	}
}
