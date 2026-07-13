package node

import (
	"context"
	"encoding/json"
	"testing"

	"agentcanvas/internal/domain/workflow"
	"agentcanvas/internal/runtime/engine"
	"agentcanvas/internal/runtime/harness/rules"
)

func TestApplyProfileDefaultsUsesPublishedSnapshotInsteadOfLegacyRules(t *testing.T) {
	activeID := int64(9)
	published, err := rules.CompileRuleSet([]rules.Rule{{
		ID: "tenant.published", Content: "published", Strength: rules.RuleMandatory,
	}}, rules.CompileOptions{RuleSetID: activeID, Version: "3"})
	if err != nil {
		t.Fatal(err)
	}
	agent := AgentNode{
		Profiles: snapshotProfileLoader{profile: &workflow.Profile{
			OwnerID: 1, WorkflowID: 2, ActiveRuleSetID: &activeID,
			ContextPolicyJSON: json.RawMessage(`{"rules":[{"id":"tenant.legacy","content":"legacy","strength":"optional"}]}`),
		}},
		RuleSets: snapshotRuleLoader{compiled: published},
	}
	cfg, err := agent.applyProfileDefaults(context.Background(), &engine.RunContext{OwnerID: 1, WorkflowID: 2}, agentRuntimeConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RuleSetID != activeID || cfg.RuleSetVersion != "3" || cfg.CompiledRules == nil || len(cfg.CustomRules) != 1 || cfg.CustomRules[0].ID != "tenant.published" {
		t.Fatalf("expected published snapshot, got %+v", cfg)
	}
	if _, ok := cfg.CompiledRules.RuleByID("scenario.code.change_verification"); !ok {
		t.Fatalf("runtime snapshot must retain platform optional rules: %+v", cfg.CompiledRules)
	}
	if err := rules.VerifyCompiledHash(cfg.CompiledRules); err != nil {
		t.Fatalf("composed runtime snapshot hash must verify: %v", err)
	}
}

func TestApplyProfileDefaultsKeepsRunPinnedSnapshotAfterActiveVersionChanges(t *testing.T) {
	oldSnapshot, err := rules.CompileRuleSet([]rules.Rule{{
		ID: "tenant.old", Content: "old published rule", Strength: rules.RuleMandatory,
	}}, rules.CompileOptions{RuleSetID: 9, Version: "3"})
	if err != nil {
		t.Fatal(err)
	}
	newSnapshot, err := rules.CompileRuleSet([]rules.Rule{{
		ID: "tenant.new", Content: "new published rule", Strength: rules.RuleMandatory,
	}}, rules.CompileOptions{RuleSetID: 10, Version: "4"})
	if err != nil {
		t.Fatal(err)
	}
	activeID := int64(10)
	loader := &countingSnapshotLoader{compiled: newSnapshot}
	agent := AgentNode{
		Profiles: snapshotProfileLoader{profile: &workflow.Profile{OwnerID: 1, WorkflowID: 2, ActiveRuleSetID: &activeID}},
		RuleSets: loader,
	}
	rc := &engine.RunContext{OwnerID: 1, WorkflowID: 2, RuleSetID: 9, RuleSetVersion: "3", CompiledRuleHash: oldSnapshot.CompiledHash, CompiledRules: oldSnapshot}
	cfg, err := agent.applyProfileDefaults(context.Background(), rc, agentRuntimeConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if loader.calls != 0 || cfg.RuleSetID != 9 || len(cfg.CustomRules) != 1 || cfg.CustomRules[0].ID != "tenant.old" {
		t.Fatalf("run must keep its pinned snapshot, calls=%d cfg=%+v", loader.calls, cfg)
	}
}

type snapshotProfileLoader struct{ profile *workflow.Profile }

func (f snapshotProfileLoader) GetWorkflowProfile(context.Context, int64, int64) (*workflow.Profile, error) {
	return f.profile, nil
}

type snapshotRuleLoader struct{ compiled *rules.CompiledRuleSet }

func (f snapshotRuleLoader) LoadActiveRuleSet(context.Context, int64, int64) (*rules.CompiledRuleSet, error) {
	return f.compiled, nil
}

type countingSnapshotLoader struct {
	compiled *rules.CompiledRuleSet
	calls    int
}

func (f *countingSnapshotLoader) LoadActiveRuleSet(context.Context, int64, int64) (*rules.CompiledRuleSet, error) {
	f.calls++
	return f.compiled, nil
}
