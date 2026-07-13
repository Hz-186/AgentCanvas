package rule_backfill_usecase

import (
	"encoding/json"
	"testing"

	"agentcanvas/internal/runtime/harness/rules"
)

func TestDecodeLegacyRulesAndRowsPreserveActivationAndDependencies(t *testing.T) {
	items, err := decodeLegacyRules(json.RawMessage(`{"rules":[{"id":"tenant.release","level":"l2_scenario","content":"check release","activation":{"tag_any":["release"]},"manual_depends_on":["tenant.audit"]},{"id":"tenant.audit","level":"l2_scenario","content":"audit"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := rules.CompileRuleSet(items, rules.CompileOptions{RuleSetID: 9, Version: "2", RejectLegacyPermanentLevels: true})
	if err != nil {
		t.Fatal(err)
	}
	nodes, edges, err := legacyRows(compiled)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 2 || len(edges) != 1 || edges[0].DependsOnRuleID != "tenant.audit" {
		t.Fatalf("unexpected legacy rows: nodes=%+v edges=%+v", nodes, edges)
	}
	if nodes[1].Strength != string(rules.RuleOptional) {
		t.Fatalf("legacy L2 rule must map to optional, got %+v", nodes)
	}
}

func TestDecodeLegacyRulesRejectsInvalidJSON(t *testing.T) {
	if _, err := decodeLegacyRules(json.RawMessage(`{"rules":`)); err == nil {
		t.Fatal("expected invalid legacy policy to fail")
	}
}
