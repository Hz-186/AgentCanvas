package rules

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
)

const (
	PolicyDangerousArgumentsDeny = "tool.dangerous_arguments.deny"
	PolicyRiskRequireApproval    = "tool.risk.require_approval"
	PolicyHostAllowlist          = "tool.host.allowlist"
	PolicyExecutionLimits        = "tool.execution_limits"

	maxRuleCount        = 50
	maxRuleContentBytes = 16 * 1024
)

var ruleIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,127}$`)

type BoundPolicyBinding struct {
	RuleID    string          `json:"rule_id"`
	PolicyKey string          `json:"policy_key"`
	Params    json.RawMessage `json:"params,omitempty"`
}

// RuleSet is an immutable copy of the rules as published. It is not a compiled form.
type RuleSet struct {
	ID      int64  `json:"id,omitempty"`
	Version string `json:"version,omitempty"`
	Hash    string `json:"hash"`
	Rules   []Rule `json:"rules"`
}

func NewRuleSet(items []Rule, id int64, version string) (*RuleSet, error) {
	normalized, err := ValidateRules(items)
	if err != nil {
		return nil, err
	}
	set := &RuleSet{ID: id, Version: strings.TrimSpace(version), Rules: normalized}
	set.Hash, err = hashRuleSet(set)
	return set, err
}

func ValidateRules(items []Rule) ([]Rule, error) {
	if len(items) > maxRuleCount {
		return nil, fmt.Errorf("at most %d custom rules are allowed", maxRuleCount)
	}
	reserved := make(map[string]bool)
	for _, rule := range append(DefaultPlatformMandatoryRules(), DefaultFallbackOptionalRules()...) {
		reserved[rule.ID] = true
	}
	for _, rule := range items {
		if reserved[strings.TrimSpace(rule.ID)] {
			return nil, fmt.Errorf("rule id %q is reserved by the platform", strings.TrimSpace(rule.ID))
		}
	}
	return validateRules(items)
}

// ValidateLoadedRules validates an already composed platform and custom rule list.
func ValidateLoadedRules(items []Rule) ([]Rule, error) {
	return validateRules(items)
}

func validateRules(items []Rule) ([]Rule, error) {
	normalized := make([]Rule, 0, len(items))
	seen := make(map[string]bool, len(items))
	for _, input := range items {
		rule := input
		rule.ID = strings.TrimSpace(rule.ID)
		rule.Name = strings.TrimSpace(rule.Name)
		rule.Content = strings.TrimSpace(rule.Content)
		if !ruleIDPattern.MatchString(rule.ID) {
			return nil, fmt.Errorf("rule id %q is invalid", rule.ID)
		}
		if rule.Content == "" {
			return nil, fmt.Errorf("rule %q requires content", rule.ID)
		}
		if len(rule.Content) > maxRuleContentBytes {
			return nil, fmt.Errorf("rule %q content exceeds %d bytes", rule.ID, maxRuleContentBytes)
		}
		if seen[rule.ID] {
			return nil, fmt.Errorf("duplicate rule id %q", rule.ID)
		}
		seen[rule.ID] = true
		if rule.Strength != RuleMandatory && rule.Strength != RuleOptional {
			return nil, fmt.Errorf("rule %q requires strength mandatory or optional", rule.ID)
		}
		if rule.Strength == RuleOptional && !hasActivation(rule.Activation) {
			return nil, fmt.Errorf("optional rule %q requires an activation", rule.ID)
		}
		if err := validatePolicyBinding(rule); err != nil {
			return nil, err
		}
		normalized = append(normalized, rule)
	}
	sort.SliceStable(normalized, func(i, j int) bool { return normalized[i].ID < normalized[j].ID })
	return normalized, nil
}

func VerifyRuleSet(set *RuleSet) error {
	if set == nil {
		return fmt.Errorf("rule set is nil")
	}
	expected := strings.TrimSpace(set.Hash)
	if expected == "" {
		return fmt.Errorf("rule set hash is empty")
	}
	actual, err := hashRuleSet(set)
	if err != nil {
		return err
	}
	if actual != expected {
		return fmt.Errorf("rule set hash mismatch")
	}
	_, err = validateRules(set.Rules)
	return err
}

func DecodeRuleSet(data json.RawMessage) (*RuleSet, error) {
	var set RuleSet
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&set); err != nil {
		return nil, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, fmt.Errorf("rule set contains trailing JSON content")
	}
	if err := VerifyRuleSet(&set); err != nil {
		return nil, err
	}
	return &set, nil
}

func hashRuleSet(set *RuleSet) (string, error) {
	canonical := *set
	canonical.Hash = ""
	data, err := json.Marshal(&canonical)
	if err != nil {
		return "", fmt.Errorf("marshal rule set: %w", err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func PolicyBindingsForRules(items []Rule) []BoundPolicyBinding {
	bindings := make([]BoundPolicyBinding, 0)
	for _, rule := range items {
		if rule.PolicyBinding == nil || rule.Strength != RuleMandatory {
			continue
		}
		bindings = append(bindings, BoundPolicyBinding{RuleID: rule.ID, PolicyKey: rule.PolicyBinding.PolicyKey, Params: append(json.RawMessage(nil), rule.PolicyBinding.Params...)})
	}
	return bindings
}

func validatePolicyBinding(rule Rule) error {
	if rule.PolicyBinding == nil {
		if rule.SafetyCritical {
			return fmt.Errorf("safety-critical rule %q requires a policy binding", rule.ID)
		}
		return nil
	}
	if rule.Strength != RuleMandatory {
		return fmt.Errorf("rule %q with a policy binding must be mandatory", rule.ID)
	}
	policyKey := strings.TrimSpace(rule.PolicyBinding.PolicyKey)
	switch policyKey {
	case PolicyDangerousArgumentsDeny, PolicyRiskRequireApproval, PolicyHostAllowlist, PolicyExecutionLimits:
	default:
		return fmt.Errorf("rule %q uses unknown policy binding %q", rule.ID, rule.PolicyBinding.PolicyKey)
	}
	if len(rule.PolicyBinding.Params) > 0 && string(rule.PolicyBinding.Params) != "null" {
		var object map[string]any
		if err := json.Unmarshal(rule.PolicyBinding.Params, &object); err != nil || object == nil {
			return fmt.Errorf("rule %q policy binding params must be a JSON object", rule.ID)
		}
	}
	if err := validateTypedPolicyParams(policyKey, rule.PolicyBinding.Params); err != nil {
		return fmt.Errorf("rule %q policy binding: %w", rule.ID, err)
	}
	return nil
}

func validateTypedPolicyParams(policyKey string, raw json.RawMessage) error {
	if len(raw) == 0 || string(raw) == "null" {
		raw = json.RawMessage(`{}`)
	}
	decode := func(target any) error {
		decoder := json.NewDecoder(strings.NewReader(string(raw)))
		decoder.DisallowUnknownFields()
		return decoder.Decode(target)
	}
	switch policyKey {
	case PolicyDangerousArgumentsDeny:
		return decode(&struct{}{})
	case PolicyRiskRequireApproval:
		var params struct {
			RiskLevels []string `json:"risk_levels"`
		}
		if err := decode(&params); err != nil {
			return fmt.Errorf("risk_levels must be an array: %w", err)
		}
		if len(stableUnique(params.RiskLevels)) == 0 {
			return fmt.Errorf("risk_levels must not be empty")
		}
	case PolicyHostAllowlist:
		var params struct {
			AllowedHosts []string `json:"allowed_hosts"`
		}
		if err := decode(&params); err != nil {
			return fmt.Errorf("allowed_hosts must be an array: %w", err)
		}
		if len(stableUnique(params.AllowedHosts)) == 0 {
			return fmt.Errorf("allowed_hosts must not be empty")
		}
	case PolicyExecutionLimits:
		var params struct {
			MaxToolTimeoutMS   int `json:"max_tool_timeout_ms"`
			MaxToolOutputBytes int `json:"max_tool_output_bytes"`
		}
		if err := decode(&params); err != nil {
			return err
		}
		if params.MaxToolTimeoutMS < 0 || params.MaxToolTimeoutMS > 600000 {
			return fmt.Errorf("max_tool_timeout_ms must be 0..600000")
		}
		if params.MaxToolOutputBytes < 0 || params.MaxToolOutputBytes > 2*1024*1024 {
			return fmt.Errorf("max_tool_output_bytes must be 0..2097152")
		}
		if params.MaxToolTimeoutMS == 0 && params.MaxToolOutputBytes == 0 {
			return fmt.Errorf("execution limits must set at least one positive limit")
		}
	}
	return nil
}

func stableUnique(values []string) []string {
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" && !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	return out
}
