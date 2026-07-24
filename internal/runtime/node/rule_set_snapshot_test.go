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
	published, err := rules.NewRuleSet([]rules.Rule{{
		ID: "tenant.published", Content: "published", Strength: rules.RuleMandatory,
	}}, activeID, "3")
	if err != nil {
		t.Fatal(err)
	}
	agent := AgentNode{
		Profiles: snapshotProfileLoader{profile: &workflow.Profile{
			OwnerID: 1, WorkflowID: 2, ActiveRuleSetID: &activeID,
			ContextPolicyJSON: json.RawMessage(`{"rules":[{"id":"tenant.legacy","content":"legacy","strength":"optional"}]}`),
		}},
		RuleSets: snapshotRuleLoader{set: published},
	}
	cfg, err := agent.applyProfileDefaults(context.Background(), &engine.RunContext{OwnerID: 1, WorkflowID: 2}, agentRuntimeConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RuleSetID != activeID || cfg.RuleSetVersion != "3" || len(cfg.Rules) != 1 || cfg.Rules[0].ID != "tenant.published" {
		t.Fatalf("expected published snapshot, got %+v", cfg)
	}
	if len(cfg.Rules) != 1 {
		t.Fatalf("active RuleSet must replace fallback optional rules: %+v", cfg.Rules)
	}
}

func TestApplyProfileDefaultsKeepsRunPinnedSnapshotAfterActiveVersionChanges(t *testing.T) {
	oldSnapshot, err := rules.NewRuleSet([]rules.Rule{{
		ID: "tenant.old", Content: "old published rule", Strength: rules.RuleMandatory,
	}}, 9, "3")
	if err != nil {
		t.Fatal(err)
	}
	newSnapshot, err := rules.NewRuleSet([]rules.Rule{{
		ID: "tenant.new", Content: "new published rule", Strength: rules.RuleMandatory,
	}}, 10, "4")
	if err != nil {
		t.Fatal(err)
	}
	activeID := int64(10)
	loader := &countingSnapshotLoader{set: newSnapshot}
	agent := AgentNode{
		Profiles: snapshotProfileLoader{profile: &workflow.Profile{OwnerID: 1, WorkflowID: 2, ActiveRuleSetID: &activeID}},
		RuleSets: loader,
	}
	rc := &engine.RunContext{OwnerID: 1, WorkflowID: 2, RuleSetID: 9, RuleSetVersion: "3", RuleSetHash: oldSnapshot.Hash, Rules: oldSnapshot.Rules}
	cfg, err := agent.applyProfileDefaults(context.Background(), rc, agentRuntimeConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if loader.calls != 0 || cfg.RuleSetID != 9 || len(cfg.Rules) != 1 || cfg.Rules[0].ID != "tenant.old" {
		t.Fatalf("run must keep its pinned snapshot, calls=%d cfg=%+v", loader.calls, cfg)
	}
}

type snapshotProfileLoader struct{ profile *workflow.Profile }

func (f snapshotProfileLoader) GetWorkflowProfile(context.Context, int64, int64) (*workflow.Profile, error) {
	return f.profile, nil
}

type snapshotRuleLoader struct{ set *rules.RuleSet }

func (f snapshotRuleLoader) LoadActiveRuleSet(context.Context, int64, int64) (*rules.RuleSet, error) {
	return f.set, nil
}

type countingSnapshotLoader struct {
	set   *rules.RuleSet
	calls int
}

func (f *countingSnapshotLoader) LoadActiveRuleSet(context.Context, int64, int64) (*rules.RuleSet, error) {
	f.calls++
	return f.set, nil
}
