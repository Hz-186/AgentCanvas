package rule_backfill_usecase

import (
	"encoding/json"
	"testing"

	"agentcanvas/internal/runtime/harness/rules"
)

func TestDecodeLegacyRulesAndRowsDropDependencies(t *testing.T) {
	items, ignored, err := decodeLegacyRules(json.RawMessage(`{"rules":[{"id":"tenant.release","level":"l2_scenario","content":"check release","activation":{"tag_any":["release"]},"manual_depends_on":["tenant.audit"]},{"id":"tenant.audit","level":"l2_scenario","content":"audit"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(ignored) != 0 {
		t.Fatalf("unexpected ignored rules: %+v", ignored)
	}
	compiled, err := rules.CompileRuleSet(items, rules.CompileOptions{RuleSetID: 9, Version: "2"})
	if err != nil {
		t.Fatal(err)
	}
	nodes, err := compiledRows(compiled)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 2 {
		t.Fatalf("unexpected graph-free rows: nodes=%+v", nodes)
	}
	if nodes[1].Strength != string(rules.RuleOptional) {
		t.Fatalf("legacy L2 rule must map to optional, got %+v", nodes)
	}
}

func TestStripRuleGraphJSONRejectsDependencyOnlyOptionalRule(t *testing.T) {
	_, _, err := stripRuleGraphJSON(json.RawMessage(`[{"id":"tenant.dependency","strength":"optional","content":"dependency","manual_depends_on":["tenant.root"]}]`))
	if err == nil {
		t.Fatal("expected dependency-only rule to block graph removal")
	}
}

func TestMergeSnapshotTriggersPreservesLegacyTriggerOnlyRule(t *testing.T) {
	items := []rules.Rule{{ID: "tenant.release", Strength: rules.RuleOptional, Content: "check release"}}
	snapshot := json.RawMessage(`{"schema_version":2,"rules":[{"rule":{"id":"tenant.release","strength":"optional","content":"check release","triggers":["release"]},"depends_on":["tenant.audit"]}]}`)
	merged, err := mergeSnapshotTriggers(items, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if len(merged) != 1 || len(merged[0].Triggers) != 1 || merged[0].Triggers[0] != "release" {
		t.Fatalf("legacy trigger was not restored: %+v", merged)
	}
	if _, err := rules.CompileRuleSet(merged, rules.CompileOptions{}); err != nil {
		t.Fatalf("trigger-only optional rule should remain valid: %v", err)
	}
}

func TestDecodeLegacyRulesRejectsInvalidJSON(t *testing.T) {
	if _, _, err := decodeLegacyRules(json.RawMessage(`{"rules":`)); err == nil {
		t.Fatal("expected invalid legacy policy to fail")
	}
}
