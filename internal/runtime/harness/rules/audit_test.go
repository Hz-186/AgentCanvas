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
