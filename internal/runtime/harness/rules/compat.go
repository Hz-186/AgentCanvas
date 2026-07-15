package rules

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"agentcanvas/internal/observability"
)

type LegacyRuleLevel string

const (
	LegacyLevelL0Safety    LegacyRuleLevel = "l0_safety"
	LegacyLevelL1Core      LegacyRuleLevel = "l1_core"
	LegacyLevelL2Scenario  LegacyRuleLevel = "l2_scenario"
	LegacyLevelL3Tool      LegacyRuleLevel = "l3_tool"
	LegacyLevelL4Ephemeral LegacyRuleLevel = "l4_ephemeral"
)

// LegacyRuleDTO is restricted to migration and historical snapshot decoding.
// Field order intentionally matches the v1 Rule JSON hash representation.
type LegacyRuleDTO struct {
	ID              string            `json:"id"`
	Name            string            `json:"name"`
	Strength        RuleStrength      `json:"strength,omitempty"`
	Level           LegacyRuleLevel   `json:"level,omitempty"`
	Content         string            `json:"content"`
	Triggers        []string          `json:"triggers,omitempty"`
	Activation      Activation        `json:"activation,omitempty"`
	TokenBudget     int               `json:"token_budget,omitempty"`
	Priority        int               `json:"priority,omitempty"`
	SafetyCritical  bool              `json:"safety_critical,omitempty"`
	ManualDependsOn []string          `json:"manual_depends_on,omitempty"`
	PolicyBinding   *PolicyBinding    `json:"policy_binding,omitempty"`
	Metadata        map[string]string `json:"metadata,omitempty"`
}

func ConvertLegacyRules(items []LegacyRuleDTO) ([]Rule, []string, error) {
	referenced := make(map[string]bool)
	for _, item := range items {
		for _, dependency := range item.ManualDependsOn {
			referenced[strings.TrimSpace(dependency)] = true
		}
	}
	converted := make([]Rule, 0, len(items))
	ignored := make([]string, 0)
	for _, item := range items {
		rule, keep, err := convertLegacyRule(item, referenced[item.ID])
		if err != nil {
			return nil, nil, err
		}
		if !keep {
			ignored = append(ignored, item.ID)
			continue
		}
		converted = append(converted, rule)
	}
	return converted, ignored, nil
}

func convertLegacyRule(item LegacyRuleDTO, referenced bool) (Rule, bool, error) {
	if item.Level != "" {
		switch item.Level {
		case LegacyLevelL0Safety, LegacyLevelL1Core, LegacyLevelL2Scenario, LegacyLevelL3Tool, LegacyLevelL4Ephemeral:
		default:
			return Rule{}, false, fmt.Errorf("legacy rule %q has invalid level %q", item.ID, item.Level)
		}
	}
	strength := item.Strength
	if strength == "" {
		switch item.Level {
		case LegacyLevelL0Safety, LegacyLevelL1Core:
			strength = RuleMandatory
		default:
			strength = RuleOptional
		}
	}
	if strength != RuleMandatory && strength != RuleOptional {
		return Rule{}, false, fmt.Errorf("legacy rule %q has invalid strength %q", item.ID, strength)
	}
	activation := item.Activation
	if strength == RuleOptional {
		switch item.Level {
		case LegacyLevelL3Tool:
			activation.TagAll = stableUnique(append(activation.TagAll, "tool_used"))
			if noActivationHints(item.Activation) {
				activation.Always = true
			}
		case LegacyLevelL4Ephemeral:
			if noActivationHints(activation) && !referenced {
				return Rule{}, false, nil
			}
		default:
			if noActivationHints(activation) {
				activation.Always = true
			}
		}
	}
	return Rule{
		ID: item.ID, Name: item.Name, Strength: strength, Content: item.Content,
		Triggers: append([]string(nil), item.Triggers...), Activation: activation,
		TokenBudget: item.TokenBudget, Priority: item.Priority, SafetyCritical: item.SafetyCritical,
		ManualDependsOn: append([]string(nil), item.ManualDependsOn...), PolicyBinding: item.PolicyBinding,
		Metadata: cloneStringMap(item.Metadata),
	}, true, nil
}

func DecodeLegacyPolicyRules(raw json.RawMessage) ([]Rule, []string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil, nil
	}
	var policy struct {
		Rules []LegacyRuleDTO `json:"rules"`
	}
	if err := json.Unmarshal(raw, &policy); err != nil {
		return nil, nil, fmt.Errorf("decode context_policy_json: %w", err)
	}
	return ConvertLegacyRules(policy.Rules)
}

type legacyCompiledRuleV1 struct {
	Rule                  LegacyRuleDTO     `json:"rule"`
	DependsOn             []string          `json:"depends_on,omitempty"`
	DependencySources     map[string]string `json:"dependency_sources,omitempty"`
	DependencyClosure     []string          `json:"dependency_closure,omitempty"`
	DependencyClosureBits []uint64          `json:"dependency_closure_bits,omitempty"`
	TokenCost             int               `json:"token_cost"`
	TopologicalOrder      int               `json:"topological_order"`
	ContentHash           string            `json:"content_hash"`
}

type legacyCompiledRuleSetV1 struct {
	ID                    int64                  `json:"id,omitempty"`
	Version               string                 `json:"version,omitempty"`
	CompiledHash          string                 `json:"compiled_hash"`
	TokenEstimatorVersion string                 `json:"token_estimator_version"`
	MandatoryTokens       int                    `json:"mandatory_tokens"`
	Rules                 []legacyCompiledRuleV1 `json:"rules"`
}

func DecodeCompiledRuleSet(data json.RawMessage) (*CompiledRuleSet, error) {
	var probe struct {
		SchemaVersion int `json:"schema_version"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return nil, err
	}
	if probe.SchemaVersion > CurrentSnapshotSchemaVersion {
		return nil, fmt.Errorf("unsupported compiled rule set schema version %d", probe.SchemaVersion)
	}
	if probe.SchemaVersion == CurrentSnapshotSchemaVersion {
		var compiled CompiledRuleSet
		if err := json.Unmarshal(data, &compiled); err != nil {
			return nil, err
		}
		if err := VerifyCompiledHash(&compiled); err != nil {
			return nil, err
		}
		compiled.Prepare()
		return &compiled, nil
	}
	return decodeLegacyCompiledRuleSet(data)
}

func decodeLegacyCompiledRuleSet(data json.RawMessage) (*CompiledRuleSet, error) {
	var legacy legacyCompiledRuleSetV1
	if err := json.Unmarshal(data, &legacy); err != nil {
		return nil, err
	}
	expected := strings.TrimSpace(legacy.CompiledHash)
	if expected == "" {
		return nil, fmt.Errorf("compiled rule set hash is empty")
	}
	canonical := legacy
	canonical.CompiledHash = ""
	encoded, err := json.Marshal(&canonical)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(encoded)
	if hex.EncodeToString(sum[:]) != expected {
		return nil, fmt.Errorf("compiled rule set hash mismatch")
	}
	referenced := make(map[string]bool)
	for _, item := range legacy.Rules {
		for _, dependency := range item.DependsOn {
			referenced[dependency] = true
		}
	}
	compiled := &CompiledRuleSet{
		SchemaVersion: 1, ID: legacy.ID, Version: legacy.Version, CompiledHash: legacy.CompiledHash,
		TokenEstimatorVersion: legacy.TokenEstimatorVersion, MandatoryTokens: legacy.MandatoryTokens,
		Rules: make([]CompiledRule, 0, len(legacy.Rules)), legacyVerified: true,
		legacyRaw: append(json.RawMessage(nil), data...),
	}
	for _, item := range legacy.Rules {
		rule, keep, convertErr := convertLegacyRule(item.Rule, referenced[item.Rule.ID])
		if convertErr != nil {
			return nil, convertErr
		}
		if !keep {
			continue
		}
		compiled.Rules = append(compiled.Rules, CompiledRule{
			Rule: rule, DependsOn: append([]string(nil), item.DependsOn...),
			DependencySources:     cloneStringMap(item.DependencySources),
			DependencyClosure:     append([]string(nil), item.DependencyClosure...),
			DependencyClosureBits: append([]uint64(nil), item.DependencyClosureBits...),
			TokenCost:             item.TokenCost, TopologicalOrder: item.TopologicalOrder, ContentHash: item.ContentHash,
		})
	}
	compiled.Prepare()
	observability.RuleSystemMetrics.RecordLegacySnapshotLoad()
	return compiled, nil
}

func cloneStringMap(input map[string]string) map[string]string {
	if input == nil {
		return nil
	}
	output := make(map[string]string, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}
