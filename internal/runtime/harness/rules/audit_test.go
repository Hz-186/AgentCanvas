package rules

import "testing"

func TestShouldPruneKeepsPinnedRules(t *testing.T) {
	for _, level := range []RuleLevel{LevelL0Safety, LevelL1Core} {
		rule := Rule{ID: string(level), Level: level}
		audit := RuleAudit{Evaluations: 100, Hits: 0, HitRate: 0}
		if ShouldPrune(rule, audit, AuditPolicy{MinEvaluations: 10, MinHitRate: 0.1}) {
			t.Fatalf("pinned rule level %s must never be pruned", level)
		}
	}
}

func TestRegistryLoadWithAuditPrunesLowHitScenarioRules(t *testing.T) {
	registry := NewRegistry(
		Rule{ID: "core", Level: LevelL1Core, Content: "always"},
		Rule{ID: "rag", Level: LevelL2Scenario, Content: "cite chunks", Activation: Activation{TagAny: []string{"rag"}}},
		Rule{ID: "tool", Level: LevelL3Tool, Content: "tool guard", Activation: Activation{ToolAny: []string{"bash"}}},
	)
	audit := NewAuditStore()
	for i := 0; i < 10; i++ {
		audit.Record("rag", false)
		audit.Record("tool", false)
	}
	loaded, trace := registry.LoadWithAudit(LoadContext{Tags: []string{"rag"}, ToolNames: []string{"bash"}}, audit, AuditPolicy{MinEvaluations: 5, MinHitRate: 0.2})
	if len(loaded) != 1 || loaded[0].ID != "core" {
		t.Fatalf("expected only core rule after pruning, got %+v", loaded)
	}
	if trace.SkipReasons["rag"] != ReasonAuditLowHitRate || trace.SkipReasons["tool"] != ReasonAuditLowHitRate {
		t.Fatalf("expected audit prune reasons, got %+v", trace.SkipReasons)
	}
	if trace.PrunedTokensByLevel[string(LevelL2Scenario)] == 0 || trace.PrunedTokensByLevel[string(LevelL3Tool)] == 0 {
		t.Fatalf("expected pruned token accounting, got %+v", trace.PrunedTokensByLevel)
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

func TestRegistryLoadRespectsTokenBudgetButKeepsPinnedRules(t *testing.T) {
	registry := NewRegistry(
		Rule{ID: "safety", Level: LevelL0Safety, TokenBudget: 40, Content: "safety boundary"},
		Rule{ID: "core", Level: LevelL1Core, TokenBudget: 60, Content: "core task"},
		Rule{ID: "rag", Level: LevelL2Scenario, TokenBudget: 20, Content: "cite retrieval", Activation: Activation{TagAny: []string{"rag"}}},
		Rule{ID: "compress", Level: LevelL4Ephemeral, TokenBudget: 20, Content: "compress long context", Activation: Activation{TagAny: []string{"compression"}}},
	)

	loaded, trace := registry.Load(LoadContext{Tags: []string{"rag", "compression"}, TokenBudget: 110})
	if len(loaded) != 2 || loaded[0].ID != "safety" || loaded[1].ID != "core" {
		t.Fatalf("expected only pinned rules under tight budget, got loaded=%+v trace=%+v", loaded, trace)
	}
	if trace.EstimatedUsed != 100 || trace.TokenBudget != 110 {
		t.Fatalf("unexpected budget trace: %+v", trace)
	}
	if trace.SkipReasons["rag"] != ReasonTokenBudgetExceeded || trace.SkipReasons["compress"] != ReasonTokenBudgetExceeded {
		t.Fatalf("expected budget skip reasons, got %+v", trace.SkipReasons)
	}
}

func TestRegistryLoadAllowsTriggeredRulesWithinBudget(t *testing.T) {
	registry := NewRegistry(
		Rule{ID: "core", Level: LevelL1Core, TokenBudget: 10, Content: "core"},
		Rule{ID: "rag", Level: LevelL2Scenario, TokenBudget: 20, Content: "cite retrieval", Activation: Activation{TagAny: []string{"rag"}}},
		Rule{ID: "tool", Level: LevelL3Tool, TokenBudget: 5, Content: "bash guard", Activation: Activation{ToolAny: []string{"bash"}}},
		Rule{ID: "compress", Level: LevelL4Ephemeral, TokenBudget: 4, Content: "compress", Activation: Activation{TagAny: []string{"compression"}}},
	)

	loaded, trace := registry.Load(LoadContext{Tags: []string{"rag", "compression"}, ToolNames: []string{"bash"}, TokenBudget: 40})
	if len(loaded) != 4 {
		t.Fatalf("expected all matched rules within budget, got loaded=%+v trace=%+v", loaded, trace)
	}
	if trace.EstimatedUsed != 39 || len(trace.Skipped) != 0 {
		t.Fatalf("unexpected trace: %+v", trace)
	}
}

func TestRegistryLoadRecordsActivationMissReason(t *testing.T) {
	registry := NewRegistry(
		Rule{ID: "pdf", Level: LevelL2Scenario, Content: "parse pdf", Activation: Activation{TagAny: []string{"pdf"}}},
	)
	loaded, trace := registry.Load(LoadContext{Tags: []string{"rag"}})
	if len(loaded) != 0 {
		t.Fatalf("expected no loaded rules, got %+v", loaded)
	}
	if trace.SkipReasons["pdf"] != ReasonSignalsNotMatched {
		t.Fatalf("expected activation miss reason, got %+v", trace.SkipReasons)
	}
}

func TestResolveForAgentLoadsScenarioAndEphemeralRules(t *testing.T) {
	loaded, trace := ResolveForAgent(
		"system prompt",
		"Please summarize this long context and include citations for retrieved knowledge.",
		"plan_execute",
		"medium",
		[]string{"knowledge_search", "bash"},
		[]string{"retrieval", "knowledge", "compression", "long_context"},
		400,
		nil,
		AuditPolicy{},
	)
	if len(loaded) < 5 {
		t.Fatalf("expected multiple enterprise rules, got %+v", loaded)
	}
	foundRAG := false
	foundCompression := false
	for _, item := range loaded {
		if item.ID == "scenario.rag.citations" {
			foundRAG = true
		}
		if item.ID == "ephemeral.long_context.compaction" {
			foundCompression = true
		}
	}
	if !foundRAG || !foundCompression {
		t.Fatalf("expected scenario and ephemeral rules to be selected, trace=%+v loaded=%+v", trace, loaded)
	}
	if trace.SelectionStrategy == "" || trace.CandidateCount == 0 || trace.RuleScores["scenario.rag.citations"] == 0 {
		t.Fatalf("expected explainable selection trace, got %+v", trace)
	}
}
