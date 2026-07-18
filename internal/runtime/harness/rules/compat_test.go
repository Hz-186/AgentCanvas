package rules

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestConvertLegacyRulesDropsGraphFields(t *testing.T) {
	items, _, err := ConvertLegacyRules([]LegacyRuleDTO{{
		ID: "tenant.legacy", Level: LegacyLevelL2Scenario, Content: "legacy", ManualDependsOn: []string{"tenant.base"},
	}})
	if err != nil || len(items) != 1 || items[0].Strength != RuleOptional || !items[0].Activation.Always {
		t.Fatalf("unexpected conversion: items=%+v err=%v", items, err)
	}
	data, _ := json.Marshal(items[0])
	if strings.Contains(string(data), "depends") {
		t.Fatalf("converted rule retained graph data: %s", data)
	}
}

func TestDecodeCompiledRuleSetAcceptsOnlyGraphFreeV3(t *testing.T) {
	compiled, err := CompileRuleSet([]Rule{{ID: "tenant.required", Content: "required", Strength: RuleMandatory}}, CompileOptions{RuleSetID: 7, Version: "3"})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(compiled)
	decoded, err := DecodeCompiledRuleSet(raw)
	if err != nil || decoded.SchemaVersion != 3 {
		t.Fatalf("expected v3 snapshot: decoded=%+v err=%v", decoded, err)
	}
	var object map[string]any
	_ = json.Unmarshal(raw, &object)
	object["schema_version"] = float64(2)
	legacyRaw, _ := json.Marshal(object)
	if _, err := DecodeCompiledRuleSet(legacyRaw); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("expected legacy snapshot rejection, got %v", err)
	}
}
