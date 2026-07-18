package rules

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

type LegacyRuleLevel string

const (
	LegacyLevelL0Safety    LegacyRuleLevel = "l0_safety"
	LegacyLevelL1Core      LegacyRuleLevel = "l1_core"
	LegacyLevelL2Scenario  LegacyRuleLevel = "l2_scenario"
	LegacyLevelL3Tool      LegacyRuleLevel = "l3_tool"
	LegacyLevelL4Ephemeral LegacyRuleLevel = "l4_ephemeral"
)

// LegacyRuleDTO is used only by graph-removal preflight and legacy policy migration.
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
		PolicyBinding: item.PolicyBinding, Metadata: cloneStringMap(item.Metadata),
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

func DecodeCompiledRuleSet(data json.RawMessage) (*CompiledRuleSet, error) {
	var probe struct {
		SchemaVersion int `json:"schema_version"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return nil, err
	}
	if probe.SchemaVersion != CurrentSnapshotSchemaVersion {
		return nil, fmt.Errorf("unsupported compiled rule set schema version %d", probe.SchemaVersion)
	}
	var compiled CompiledRuleSet
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&compiled); err != nil {
		return nil, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, fmt.Errorf("compiled rule set contains trailing JSON content")
	}
	if err := VerifyCompiledHash(&compiled); err != nil {
		return nil, err
	}
	compiled.Prepare()
	return &compiled, nil
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
