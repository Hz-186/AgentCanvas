package rules

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"
)

func TestConvertLegacyRulesMapsAllLevels(t *testing.T) {
	items, ignored, err := ConvertLegacyRules([]LegacyRuleDTO{
		{ID: "legacy.l0", Level: LegacyLevelL0Safety, Content: "l0"},
		{ID: "legacy.l1", Level: LegacyLevelL1Core, Content: "l1"},
		{ID: "legacy.l2", Level: LegacyLevelL2Scenario, Content: "l2"},
		{ID: "legacy.l3", Level: LegacyLevelL3Tool, Content: "l3"},
		{ID: "legacy.l4.dead", Level: LegacyLevelL4Ephemeral, Content: "dead"},
		{ID: "legacy.l4.live", Level: LegacyLevelL4Ephemeral, Content: "live", Activation: Activation{TagAny: []string{"long_context"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 5 || len(ignored) != 1 || ignored[0] != "legacy.l4.dead" {
		t.Fatalf("unexpected conversion items=%+v ignored=%+v", items, ignored)
	}
	if items[0].Strength != RuleMandatory || items[1].Strength != RuleMandatory {
		t.Fatalf("L0/L1 must be mandatory: %+v", items[:2])
	}
	if items[2].Strength != RuleOptional || !items[2].Activation.Always {
		t.Fatalf("L2 without activation must become always optional: %+v", items[2])
	}
	if items[3].Strength != RuleOptional || !items[3].Activation.Always || !containsString(items[3].Activation.TagAll, "tool_used") {
		t.Fatalf("L3 must require actual tool use: %+v", items[3])
	}
}

func TestDecodeLegacyCompiledRuleSetVerifiesV1HashAndNormalizes(t *testing.T) {
	legacy := legacyCompiledRuleSetV1{
		ID: 7, Version: "3", TokenEstimatorVersion: DefaultTokenEstimatorVersion,
		Rules: []legacyCompiledRuleV1{{
			Rule:      LegacyRuleDTO{ID: "tenant.required", Name: "Required", Level: LegacyLevelL1Core, Content: "required"},
			TokenCost: 8, TopologicalOrder: 0, ContentHash: hashText("required"),
		}},
		MandatoryTokens: 8,
	}
	canonical, err := json.Marshal(&legacy)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(canonical)
	legacy.CompiledHash = hex.EncodeToString(sum[:])
	raw, err := json.Marshal(&legacy)
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := DecodeCompiledRuleSet(raw)
	if err != nil {
		t.Fatal(err)
	}
	if compiled.SchemaVersion != 1 || !compiled.legacyVerified || compiled.Rules[0].Rule.Strength != RuleMandatory {
		t.Fatalf("unexpected normalized legacy snapshot: %+v", compiled)
	}
	if err := VerifyCompiledHash(compiled); err != nil {
		t.Fatalf("verified legacy snapshot must remain valid: %v", err)
	}
	roundTrip, err := json.Marshal(compiled)
	if err != nil || string(roundTrip) != string(raw) {
		t.Fatalf("legacy snapshot must marshal byte-for-byte: err=%v got=%s want=%s", err, roundTrip, raw)
	}

	var tampered map[string]any
	if err := json.Unmarshal(raw, &tampered); err != nil {
		t.Fatal(err)
	}
	tamperedRules := tampered["rules"].([]any)
	tamperedRules[0].(map[string]any)["token_cost"] = float64(9)
	tamperedRaw, _ := json.Marshal(tampered)
	if _, err := DecodeCompiledRuleSet(tamperedRaw); err == nil {
		t.Fatal("tampered v1 snapshot must fail verification")
	}
}

func containsString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}
