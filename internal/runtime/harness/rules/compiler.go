package rules

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

const (
	PolicyDangerousArgumentsDeny = "tool.dangerous_arguments.deny"
	PolicyRiskRequireApproval    = "tool.risk.require_approval"
	PolicyHostAllowlist          = "tool.host.allowlist"
	PolicyExecutionLimits        = "tool.execution_limits"

	DefaultTokenEstimatorVersion = "conservative-rune-v1"
	CurrentSnapshotSchemaVersion = 3
	maxRuleCount                 = 50
	maxRuleContentBytes          = 16 * 1024
)

var ruleIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,127}$`)

type CompileOptions struct {
	RuleSetID             int64
	Version               string
	TokenEstimatorVersion string
}

type BoundPolicyBinding struct {
	RuleID    string          `json:"rule_id"`
	PolicyKey string          `json:"policy_key"`
	Params    json.RawMessage `json:"params,omitempty"`
}

type CompiledRule struct {
	Rule        Rule   `json:"rule"`
	TokenCost   int    `json:"token_cost"`
	ContentHash string `json:"content_hash"`
}

type CompiledRuleSet struct {
	SchemaVersion         int            `json:"schema_version"`
	ID                    int64          `json:"id,omitempty"`
	Version               string         `json:"version,omitempty"`
	CompiledHash          string         `json:"compiled_hash"`
	TokenEstimatorVersion string         `json:"token_estimator_version"`
	MandatoryTokens       int            `json:"mandatory_tokens"`
	Rules                 []CompiledRule `json:"rules"`

	byID map[string]int
}

func (c *CompiledRuleSet) Prepare() {
	if c == nil {
		return
	}
	c.byID = make(map[string]int, len(c.Rules))
	for index := range c.Rules {
		c.byID[c.Rules[index].Rule.ID] = index
	}
}

func (c *CompiledRuleSet) RuleByID(id string) (CompiledRule, bool) {
	if c == nil {
		return CompiledRule{}, false
	}
	if c.byID == nil {
		c.Prepare()
	}
	index, ok := c.byID[id]
	if !ok {
		return CompiledRule{}, false
	}
	return c.Rules[index], true
}

func (c *CompiledRuleSet) PolicyBindings() []BoundPolicyBinding {
	if c == nil {
		return nil
	}
	bindings := make([]BoundPolicyBinding, 0)
	for _, item := range c.Rules {
		if item.Rule.PolicyBinding == nil || item.Rule.Strength != RuleMandatory {
			continue
		}
		bindings = append(bindings, BoundPolicyBinding{
			RuleID: item.Rule.ID, PolicyKey: item.Rule.PolicyBinding.PolicyKey,
			Params: append(json.RawMessage(nil), item.Rule.PolicyBinding.Params...),
		})
	}
	return bindings
}

func RulesFromCompiled(c *CompiledRuleSet) []Rule {
	if c == nil {
		return nil
	}
	items := make([]Rule, 0, len(c.Rules))
	for _, compiled := range c.Rules {
		items = append(items, compiled.Rule)
	}
	return items
}

func PolicyBindingsForRules(items []Rule) []BoundPolicyBinding {
	bindings := make([]BoundPolicyBinding, 0)
	for _, rule := range items {
		if rule.PolicyBinding == nil || rule.Strength != RuleMandatory {
			continue
		}
		bindings = append(bindings, BoundPolicyBinding{
			RuleID: rule.ID, PolicyKey: rule.PolicyBinding.PolicyKey,
			Params: append(json.RawMessage(nil), rule.PolicyBinding.Params...),
		})
	}
	return bindings
}

func CompileRuleSet(items []Rule, opts CompileOptions) (*CompiledRuleSet, error) {
	if len(items) > maxRuleCount {
		return nil, fmt.Errorf("at most %d custom rules are allowed", maxRuleCount)
	}
	if opts.TokenEstimatorVersion == "" {
		opts.TokenEstimatorVersion = DefaultTokenEstimatorVersion
	}
	compiled := &CompiledRuleSet{
		SchemaVersion: CurrentSnapshotSchemaVersion, ID: opts.RuleSetID,
		Version: strings.TrimSpace(opts.Version), TokenEstimatorVersion: opts.TokenEstimatorVersion,
		Rules: make([]CompiledRule, 0, len(items)),
	}
	seen := make(map[string]bool, len(items))
	for _, input := range items {
		rule := input
		rule.ID = strings.TrimSpace(rule.ID)
		rule.Name = strings.TrimSpace(rule.Name)
		rule.Content = strings.TrimSpace(rule.Content)
		if !ruleIDPattern.MatchString(rule.ID) {
			return nil, fmt.Errorf("custom rule id %q is invalid", rule.ID)
		}
		if rule.Content == "" {
			return nil, fmt.Errorf("custom rule %q requires content", rule.ID)
		}
		if len(rule.Content) > maxRuleContentBytes {
			return nil, fmt.Errorf("custom rule %q content exceeds %d bytes", rule.ID, maxRuleContentBytes)
		}
		if seen[rule.ID] {
			return nil, fmt.Errorf("duplicate custom rule id %q", rule.ID)
		}
		seen[rule.ID] = true
		if rule.Strength != RuleMandatory && rule.Strength != RuleOptional {
			return nil, fmt.Errorf("custom rule %q requires strength mandatory or optional", rule.ID)
		}
		if rule.Strength == RuleOptional && len(rule.Triggers) == 0 && !hasPositiveActivation(rule.Activation) {
			return nil, fmt.Errorf("optional rule %q requires a positive activation or legacy trigger", rule.ID)
		}
		if err := validatePolicyBinding(rule); err != nil {
			return nil, err
		}
		cost := conservativeRuleCost(rule)
		if rule.Strength == RuleMandatory {
			compiled.MandatoryTokens += cost
		}
		compiled.Rules = append(compiled.Rules, CompiledRule{Rule: rule, TokenCost: cost, ContentHash: hashText(rule.Content)})
	}
	sort.SliceStable(compiled.Rules, func(i, j int) bool { return compiled.Rules[i].Rule.ID < compiled.Rules[j].Rule.ID })
	if err := RefreshCompiledHash(compiled); err != nil {
		return nil, err
	}
	compiled.Prepare()
	return compiled, nil
}

func RefreshCompiledHash(compiled *CompiledRuleSet) error {
	if compiled == nil {
		return fmt.Errorf("compiled rule set is nil")
	}
	canonical := *compiled
	canonical.CompiledHash = ""
	data, err := json.Marshal(&canonical)
	if err != nil {
		return fmt.Errorf("marshal compiled rule set: %w", err)
	}
	sum := sha256.Sum256(data)
	compiled.CompiledHash = hex.EncodeToString(sum[:])
	return nil
}

func VerifyCompiledHash(compiled *CompiledRuleSet) error {
	if compiled == nil {
		return fmt.Errorf("compiled rule set is nil")
	}
	if compiled.SchemaVersion != CurrentSnapshotSchemaVersion {
		return fmt.Errorf("unsupported compiled rule set schema version %d", compiled.SchemaVersion)
	}
	expected := strings.TrimSpace(compiled.CompiledHash)
	if expected == "" {
		return fmt.Errorf("compiled rule set hash is empty")
	}
	canonical := *compiled
	if err := RefreshCompiledHash(&canonical); err != nil {
		return fmt.Errorf("compute compiled rule set hash for verification: %w", err)
	}
	if canonical.CompiledHash != expected {
		return fmt.Errorf("compiled rule set hash mismatch")
	}
	return nil
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
		var params struct{}
		return decode(&params)
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

func conservativeRuleCost(rule Rule) int {
	count := len([]rune(strings.TrimSpace(rule.Content)))
	if count < 1 {
		count = 1
	}
	if rule.TokenBudget > count {
		return rule.TokenBudget
	}
	return count
}

func hashText(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func stableUnique(values []string) []string {
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func SelectMandatoryRules(compiled *CompiledRuleSet) ([]Rule, Trace) {
	trace := Trace{SelectionStrategy: "mandatory_static:v3"}
	if compiled == nil {
		return nil, trace
	}
	trace.MandatoryTokens = compiled.MandatoryTokens
	trace.RuleSetID = compiled.ID
	trace.RuleSetVersion = compiled.Version
	trace.CompiledHash = compiled.CompiledHash
	loaded := make([]Rule, 0)
	for _, item := range compiled.Rules {
		if item.Rule.Strength != RuleMandatory {
			continue
		}
		loaded = append(loaded, item.Rule)
		trace.Loaded = append(trace.Loaded, item.Rule.ID)
		trace.EstimatedUsed += item.TokenCost
	}
	return loaded, trace
}

func SelectOptionalRules(compiled *CompiledRuleSet, ctx LoadContext) ([]Rule, Trace) {
	trace := Trace{TokenBudget: ctx.TokenBudget, OptionalBudget: ctx.TokenBudget, SelectionStrategy: "deterministic_activation_budget:v1"}
	if compiled == nil {
		return nil, trace
	}
	trace.MandatoryTokens = compiled.MandatoryTokens
	trace.RuleSetID = compiled.ID
	trace.RuleSetVersion = compiled.Version
	trace.CompiledHash = compiled.CompiledHash
	type candidate struct {
		item CompiledRule
		cost int
	}
	candidates := make([]candidate, 0, len(compiled.Rules))
	for _, item := range compiled.Rules {
		if item.Rule.Strength != RuleOptional {
			continue
		}
		decision, matched, reason := evaluateRule(item.Rule, ctx)
		if !matched {
			trace.skip(item.Rule.ID, reason)
			trace.noteReasons(item.Rule.ID, decision.reasons)
			trace.noteSkippedSignals(item.Rule.ID, decision.signals)
			continue
		}
		cost := item.TokenCost
		if actual := ctx.RuleTokenCosts[item.Rule.ID]; actual > 0 {
			cost = actual
		}
		trace.CandidateCount++
		trace.noteScore(item.Rule.ID, decision.score)
		trace.noteReasons(item.Rule.ID, decision.reasons)
		trace.noteSignals(item.Rule.ID, decision.signals)
		candidates = append(candidates, candidate{item: item, cost: cost})
	}
	trace.ConsideredCount = len(candidates)
	sort.SliceStable(candidates, func(i, j int) bool {
		left, right := candidates[i], candidates[j]
		if left.item.Rule.Priority != right.item.Rule.Priority {
			return left.item.Rule.Priority > right.item.Rule.Priority
		}
		if left.cost != right.cost {
			return left.cost < right.cost
		}
		return left.item.Rule.ID < right.item.Rule.ID
	})
	loaded := make([]Rule, 0, len(candidates))
	used := 0
	for _, candidate := range candidates {
		if ctx.TokenBudget <= 0 || used+candidate.cost > ctx.TokenBudget {
			trace.skip(candidate.item.Rule.ID, ReasonTokenBudgetExceeded)
			continue
		}
		loaded = append(loaded, candidate.item.Rule)
		trace.Loaded = append(trace.Loaded, candidate.item.Rule.ID)
		used += candidate.cost
	}
	trace.EstimatedUsed = used
	return loaded, trace
}
